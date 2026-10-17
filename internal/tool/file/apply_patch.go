package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/uvwt/agentdock/internal/activity"
)

const ToolApplyPatch = "apply_patch"
const maxApplyPatchBytes = 4 << 20
const maxApplyPatchFiles = 128
const maxApplyPatchOverlayBytes = 64 << 20

// Only Patch is public. The existing dispatcher supplies the immutable binding,
// runtime, working directory and approved preflight revision after validation.
type ApplyPatchRequest struct {
	activity.Binding `json:"-"`
	RuntimeOptions
	Patch            string `json:"patch"`
	Workdir          string `json:"workdir,omitempty"`
	ExpectedRevision string `json:"expected_patch_revision,omitempty"`
}

type nativePatchPlan struct {
	request    ApplyPatchRequest
	root       string
	operations []patchOperation
	staged     map[string]stagedPatchFile
	affected   []map[string]any
	revision   string
	release    func()
}

func parseV4APatch(patch string) ([]patchOperation, error) {
	if len(patch) > maxApplyPatchBytes {
		return nil, toolError("PATCH_TOO_LARGE", "patch exceeds the 4 MiB input limit", "validation")
	}
	text := strings.ReplaceAll(patch, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return nil, toolError("PATCH_CONFLICT", "begin/end markers must occupy complete lines", "validation")
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "*** Move to:") && strings.TrimSpace(strings.TrimPrefix(line, "*** Move to:")) == "" {
			return nil, toolErrorDetails("PATCH_CONFLICT", "move target is empty", "validation", map[string]any{"patch_line": index + 1})
		}
	}
	operations, err := parseEnvelopePatch(text)
	if err != nil {
		return nil, err
	}
	if len(operations) == 0 || len(operations) > maxApplyPatchFiles {
		return nil, toolError("PATCH_CONFLICT", "patch must contain between 1 and 128 file operations", "validation")
	}
	for index, operation := range operations {
		if strings.TrimSpace(operation.Path) == "" || strings.Contains(operation.Path, "://") {
			return nil, v4AConflict(index, operation.Path, "patch requires a text-file path")
		}
		if operation.Kind != "update" {
			continue
		}
		if len(operation.Chunks) == 0 {
			return nil, v4AConflict(index, operation.Path, "update requires a nonempty hunk")
		}
		changed := operation.MoveTo != ""
		for hunk, chunk := range operation.Chunks {
			if len(chunk.Lines) == 0 {
				return nil, toolErrorDetails("PATCH_CONFLICT", "empty update hunk", "validation", map[string]any{"operation_index": index + 1, "path": operation.Path, "hunk_index": hunk})
			}
			for _, line := range chunk.Lines {
				if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
					changed = true
				}
			}
		}
		if !changed {
			return nil, v4AConflict(index, operation.Path, "update contains no content change or move")
		}
	}
	return operations, nil
}

func v4AConflict(index int, path, message string) error {
	return toolErrorDetails("PATCH_CONFLICT", message, "validation", map[string]any{"operation_index": index + 1, "path": path})
}

func (svc *Service) prepareNativePatch(ctx context.Context, request ApplyPatchRequest) (*nativePatchPlan, error) {
	operations, err := parseV4APatch(request.Patch)
	if err != nil {
		return nil, err
	}
	root, err := svc.patchWorkdir(request.Workdir)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	seen := map[string]bool{}
	for index := range operations {
		operation := &operations[index]
		for _, value := range []*string{&operation.Path, &operation.MoveTo} {
			if value == &operation.MoveTo && *value == "" {
				continue
			}
			joined, err := patchPathInBase(root.Abs, *value)
			if err != nil {
				return nil, err
			}
			if runtime.GOOS == "windows" && strings.Contains(strings.TrimPrefix(joined, filepath.VolumeName(joined)), ":") {
				return nil, v4AConflict(index, *value, "alternate data streams are not text-file patch targets")
			}
			resolved, err := svc.ws.ResolveForWrite(joined)
			if err != nil {
				return nil, err
			}
			relative, relErr := filepath.Rel(root.Abs, resolved.Abs)
			if request.WorkspaceID != "" && (relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative)) {
				return nil, v4AConflict(index, *value, "patch path escapes the bound workspace")
			}
			for _, segment := range strings.Split(filepath.ToSlash(resolved.Abs), "/") {
				if strings.EqualFold(segment, ".git") {
					return nil, v4AConflict(index, *value, "Git administrative files are protected from text patch operations")
				}
			}
			if info, inspectErr := lstatPatchTarget(resolved.Abs); inspectErr == nil && !info.Mode().IsRegular() {
				return nil, v4AConflict(index, *value, "patch target must be a regular file, not a symlink or special file")
			} else if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
				return nil, inspectErr
			}
			key := patchPathIdentity(resolved.Abs)
			if seen[key] {
				return nil, v4AConflict(index, *value, "patch contains conflicting or duplicate targets")
			}
			seen[key] = true
			*value = resolved.Abs
			paths = append(paths, resolved.Abs)
		}
		if operation.Kind == "add" && runtime.GOOS == "windows" {
			operation.AddContent = strings.ReplaceAll(operation.AddContent, "\n", "\r\n")
		}
	}
	release, err := acquirePatchPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			release()
		}
	}()
	// Check type before opening, including dangling symlinks and special files.
	originalBytes := int64(0)
	for _, target := range paths {
		info, err := lstatPatchTarget(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil && !info.Mode().IsRegular() {
			return nil, toolErrorDetails("NOT_REGULAR_FILE", "patch target must be a regular text file", "validation", map[string]any{"path": target})
		}
		if err == nil {
			originalBytes += info.Size()
			if originalBytes > (maxApplyPatchOverlayBytes-int64(len(request.Patch)))/2 {
				return nil, toolError("PATCH_TOO_LARGE", "patch source snapshots exceed the overlay memory budget", "validation")
			}
		}
	}
	staged, affected, _, err := svc.stageEnvelopePatch(operations, root.Abs)
	if err != nil {
		return nil, err
	}
	size, changed := 0, false
	for path, file := range staged {
		size += len(file.Original)
		if file.Content != nil {
			size += len(*file.Content)
		}
		if size > maxApplyPatchOverlayBytes {
			return nil, toolError("PATCH_TOO_LARGE", "patch preflight exceeds the 64 MiB overlay limit", "validation")
		}
		changed = changed || !file.OriginalExists || file.Content == nil || *file.Content != string(file.Original)
		if relative, err := filepath.Rel(root.Abs, path); err == nil {
			file.Display = filepath.ToSlash(relative)
			staged[path] = file
		}
	}
	if !changed {
		return nil, toolError("PATCH_CONFLICT", "patch produces no actual file change", "validation")
	}
	for index, operation := range operations {
		affected[index]["path"] = staged[operation.Path].Display
		if operation.MoveTo != "" {
			affected[index]["move_to"] = staged[operation.MoveTo].Display
		}
	}
	for index, operation := range operations {
		file := staged[operation.Path]
		if operation.Kind == "update" && operation.MoveTo == "" && file.Content != nil && *file.Content == string(file.Original) {
			return nil, v4AConflict(index, file.Display, "update produces no actual change")
		}
	}
	plan := &nativePatchPlan{request: request, root: root.Abs, operations: operations, staged: staged, affected: affected, release: release}
	plan.revision = nativePatchRevision(plan)
	if request.ExpectedRevision != "" && request.ExpectedRevision != plan.revision {
		return nil, toolError("PATCH_REVISION_CHANGED", "files changed after the permission preview; inspect the new diff before resubmitting", "conflict")
	}
	retained = true
	return plan, nil
}

func nativePatchRevision(plan *nativePatchPlan) string {
	hash := sha256.New()
	paths := make([]string, 0, len(plan.staged))
	for path := range plan.staged {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fmt.Fprintf(hash, "%d:%s", len(plan.request.Patch), plan.request.Patch)
	for _, path := range paths {
		file := plan.staged[path]
		fmt.Fprintf(hash, "%d:%s:%t:%d:%d:", len(path), path, file.OriginalExists, file.Mode, len(file.Original))
		hash.Write(file.Original)
	}
	return "patchrev_" + hex.EncodeToString(hash.Sum(nil))
}

// PreviewApplyPatch is read-only. A later approved application must reproduce
// this revision under the same path locks before it can write any file.
func (svc *Service) PreviewApplyPatch(ctx context.Context, request ApplyPatchRequest) (Result, error) {
	selection, err := selectFileRuntime(request.RuntimeOptions)
	if err != nil {
		return nil, err
	}
	if selection.isWSL() {
		return svc.previewV4APatchWSL(ctx, request, selection)
	}
	plan, err := svc.prepareNativePatch(ctx, request)
	if err != nil {
		return nil, err
	}
	defer plan.release()
	result, err := nativePatchResult(plan.staged, plan.affected)
	if err != nil {
		return nil, err
	}
	result["source_revision"], result["workdir"] = plan.revision, plan.root
	result["dry_run"], result["effect_state"], result["changed"], result["stats_state"] = true, "none", false, "preview"
	return result, nil
}

func (svc *Service) ApplyPatch(ctx context.Context, request ApplyPatchRequest) (Result, error) {
	selection, err := selectFileRuntime(request.RuntimeOptions)
	if err != nil {
		return failedV4APatch("preflight", "none", err, nil)
	}
	if selection.isWSL() {
		return svc.applyV4APatchWSL(ctx, request, selection)
	}
	plan, err := svc.prepareNativePatch(ctx, request)
	if err != nil {
		return failedV4APatch("preflight", "none", err, nil)
	}
	defer plan.release()
	result, err := plan.apply(ctx)
	if result != nil {
		result["workspace_id"], result["resolved_path"], result["workdir"] = request.WorkspaceID, plan.root, plan.root
	}
	return addFileRuntimeResult(result, selection), err
}

func nativePatchResult(staged map[string]stagedPatchFile, changes []map[string]any) (Result, error) {
	files := []editFileStatistics{}
	diff, _, stats, err := stagedDiffPreview(staged, 0, &files)
	if err != nil {
		return nil, err
	}
	return Result{"operation_status": "succeeded", "effect_state": "complete", "changed": stats.FilesChanged > 0, "changed_files": changes, "affected_files": changes, "file_statistics": files, "files_changed": stats.FilesChanged, "insertions": stats.Insertions, "deletions": stats.Deletions, "stats_state": "known", "diff": diff, "diff_truncated": false}, nil
}

func failedV4APatch(phase, effect string, cause error, result Result) (Result, error) {
	if result == nil {
		result = Result{}
	}
	kind, code := "apply_failed", "PATCH_APPLY_FAILED"
	if phase == "preflight" {
		kind, code = "patch_conflict", "PATCH_CONFLICT"
	}
	if effect == "unknown" {
		kind, code = "outcome_unknown", "PATCH_OUTCOME_UNKNOWN"
	}
	result["operation_status"], result["effect_state"], result["error_kind"] = "failed", effect, kind
	result["retry_safe"], result["phase"], result["message"] = effect == "none", phase, cause.Error()
	var original *ToolError
	if errors.As(cause, &original) && len(original.Details) > 0 {
		result["conflicts"] = []map[string]any{original.Details}
	}
	if effect == "none" {
		result["changed"], result["files_changed"], result["insertions"], result["deletions"], result["stats_state"] = false, 0, 0, 0, "known"
	}
	return result, errors.Join(toolErrorDetails(code, cause.Error(), "runtime", map[string]any(result)), cause)
}

// V4APatchPaths exposes only validated selectors to the existing WSL safety gate.
func V4APatchPaths(patch string) ([]string, error) {
	operations, err := parseV4APatch(patch)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, operation := range operations {
		paths = append(paths, operation.Path)
		if operation.MoveTo != "" {
			paths = append(paths, operation.MoveTo)
		}
	}
	return paths, nil
}
