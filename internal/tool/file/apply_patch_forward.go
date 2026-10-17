package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Forward application never restores or replays an already-mutated target.
// On failure, owned originals remain named in the result for explicit recovery.
func (plan *nativePatchPlan) apply(ctx context.Context) (Result, error) {
	applied := map[string]stagedPatchFile{}
	completed := []map[string]any{}
	created := []string{}
	backups := []map[string]any{}
	cleanup := []string{}
	fail := func(index int, cause error, uncertain bool) (Result, error) {
		verified := map[string]stagedPatchFile{}
		unverified := []string{}
		for path, file := range applied {
			state, err := inspectForwardPatchFile(file)
			matches := file.Content == nil && state == installedPatchMissing || file.Content != nil && state == installedPatchUnchanged
			if err != nil || !matches {
				uncertain = true
				unverified = append(unverified, file.Display)
			} else {
				verified[path] = file
			}
		}
		result, err := nativePatchResult(verified, completed)
		if err != nil {
			result = Result{}
			uncertain = true
		}
		result["completed_changes"], result["retained_originals"], result["created_directories"] = completed, backups, created
		operation := plan.operations[index]
		result["failed_change"] = map[string]any{"operation_index": index + 1, "operation": operation.Kind, "path": plan.staged[operation.Path].Display, "message": cause.Error()}
		effect := "none"
		if len(applied) > 0 || len(created) > 0 {
			effect = "partial"
			result["stats_state"] = "partial"
		}
		if uncertain {
			effect = "unknown"
			result["unverified_files"] = unverified
			result["stats_state"] = "partial"
		}
		if effect != "none" {
			result["recovery"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": operation.Path}}
		}
		return failedV4APatch("apply", effect, cause, result)
	}
	// Revalidate every target before the first write. This also protects files
	// changed by external editors between permission preview and dispatch.
	for index, operation := range plan.operations {
		for _, path := range []string{operation.Path, operation.MoveTo} {
			if path == "" {
				continue
			}
			if err := verifyPatchParent(path); err != nil {
				return fail(index, err, false)
			}
			if err := verifyPatchOriginal(plan.staged[path]); err != nil {
				return fail(index, err, false)
			}
		}
	}
	for index, operation := range plan.operations {
		if err := ctx.Err(); err != nil {
			return fail(index, err, false)
		}
		source := plan.staged[operation.Path]
		destination := source
		if operation.MoveTo != "" {
			destination = plan.staged[operation.MoveTo]
		}
		if err := verifyPatchParent(source.Abs); err != nil {
			return fail(index, err, false)
		}
		if err := verifyPatchOriginal(source); err != nil {
			return fail(index, err, false)
		}
		if operation.MoveTo != "" {
			if err := verifyPatchParent(destination.Abs); err != nil {
				return fail(index, err, false)
			}
			if err := verifyPatchOriginal(destination); err != nil {
				return fail(index, err, false)
			}
		}
		temporary := ""
		if destination.Content != nil {
			directories, err := ensureForwardPatchParent(filepath.Dir(destination.Abs))
			created = append(created, directories...)
			if err != nil {
				return fail(index, err, false)
			}
			if err := verifyPatchParent(destination.Abs); err != nil {
				return fail(index, err, false)
			}
			temporary, err = writePatchTemp(destination)
			if err != nil {
				return fail(index, err, false)
			}
			if err := preserveForwardPatchOwner(temporary, destination); err != nil {
				_ = os.Remove(temporary)
				return fail(index, err, false)
			}
		}
		// A deferred cleanup may remove only our uninstalled temporary file.
		if temporary != "" {
			defer os.Remove(temporary)
		}
		backup := ""
		if source.OriginalExists {
			var err error
			backup, err = reservePatchPath(filepath.Dir(source.Abs), ".agentdock-original-*")
			if err != nil {
				return fail(index, err, false)
			}
			if err := renameNoReplace(source.Abs, backup); err != nil {
				return fail(index, err, true)
			}
			backups = append(backups, map[string]any{"path": source.Display, "backup_path": backup})
			removed := source
			removed.Content = nil
			applied[source.Abs] = removed
			original := source
			original.Abs = backup
			if err := verifyPatchOriginal(original); err != nil {
				return fail(index, err, true)
			}
		}
		if destination.Content != nil {
			if err := renameNoReplace(temporary, destination.Abs); err != nil {
				state, checkErr := inspectForwardPatchFile(destination)
				if checkErr == nil && state == installedPatchUnchanged {
					applied[destination.Abs] = destination
				}
				return fail(index, err, checkErr != nil || state == installedPatchChanged)
			}
			applied[destination.Abs] = destination
		}
		for _, path := range []string{operation.Path, operation.MoveTo} {
			if path == "" {
				continue
			}
			file := plan.staged[path]
			state, err := inspectForwardPatchFile(file)
			matches := file.Content == nil && state == installedPatchMissing || file.Content != nil && state == installedPatchUnchanged
			if err != nil || !matches {
				return fail(index, fmt.Errorf("written path could not be verified: %s", file.Display), true)
			}
		}
		completed = append(completed, plan.affected[index])
		if backup != "" {
			if err := os.Remove(backup); err != nil {
				cleanup = append(cleanup, backup)
			} else {
				backups = backups[:len(backups)-1]
			}
		}
	}
	result, err := nativePatchResult(applied, completed)
	if err != nil {
		return fail(len(plan.operations)-1, err, true)
	}
	result["completed_changes"] = completed
	if len(cleanup) > 0 {
		result["cleanup_pending"], result["retained_originals"] = true, backups
	}
	return result, nil
}

func verifyPatchParent(path string) error {
	parent := filepath.Dir(path)
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			canonical, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return err
			}
			if patchPathIdentity(filepath.Clean(canonical)) != patchPathIdentity(filepath.Clean(parent)) {
				return fmt.Errorf("patch parent changed its resolved identity: %s", parent)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return err
		}
		parent = next
	}
}

func inspectForwardPatchFile(file stagedPatchFile) (installedPatchState, error) {
	if file.Content != nil {
		return inspectInstalledPatchFile(preparedPatchFile{file: file, installed: true}), nil
	}
	_, err := os.Lstat(file.Abs)
	if errors.Is(err, os.ErrNotExist) {
		return installedPatchMissing, nil
	}
	return installedPatchChanged, err
}

func ensureForwardPatchParent(parent string) ([]string, error) {
	missing, created := []string{}, []string{}
	for cursor := parent; ; cursor = filepath.Dir(cursor) {
		info, err := os.Stat(cursor)
		if err == nil {
			if !info.IsDir() {
				return created, fmt.Errorf("patch parent is not a directory: %s", cursor)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(cursor) == cursor {
			return created, err
		}
		missing = append(missing, cursor)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0o755); err != nil {
			return created, err
		}
		created = append(created, missing[index])
	}
	return created, nil
}
