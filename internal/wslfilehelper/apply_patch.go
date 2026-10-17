//go:build linux

package wslfilehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/activity"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	"github.com/uvwt/agentdock/internal/workspace"
)

// Reuse the native file service inside the existing Linux helper. The helper's
// protected-path and symlink gates remain stricter than ordinary source reads.
func applyV4APatch(req *Request) (*Response, error) {
	root, err := checkedPath(req.Workdir, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fail("NOT_A_DIRECTORY", "patch workdir must be an existing directory", nil)
	}
	paths, err := toolfile.V4APatchPaths(req.Patch)
	if err != nil {
		return nil, fail("INVALID_ARGUMENT", err.Error(), nil)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path, err = checkedPath(path, true)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			return nil, fail("PROTECTED_WSL_PATH", "patch path escapes workdir", map[string]any{"path": path})
		}
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return nil, fail("NOT_REGULAR_FILE", "patch requires regular text targets", map[string]any{"path": path})
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	ws, err := workspace.New(root)
	if err != nil {
		return nil, err
	}
	service := toolfile.New(ws, nil, nil)
	request := toolfile.ApplyPatchRequest{Binding: activity.Binding{WorkspaceID: req.WorkspaceID}, Patch: req.Patch, Workdir: root, ExpectedRevision: req.ExpectedPatchRevision}
	var result toolfile.Result
	if req.DryRun {
		result, err = service.PreviewApplyPatch(context.Background(), request)
	} else {
		result, err = service.ApplyPatch(context.Background(), request)
	}
	if result == nil && err != nil {
		result = toolfile.Result{"operation_status": "failed", "effect_state": "none", "retry_safe": true, "error_kind": "patch_conflict", "message": err.Error()}
	}
	return &Response{PatchResult: map[string]any(result)}, nil
}
