package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type UninstallResult struct {
	RemovedVersions []string
	ActiveVersion   string
}

// Uninstall shares the per-Skill writer with activation and publishes a
// durable swap journal before moving any package directory. Removing one
// inactive version prunes rollback history; removing the whole Skill removes
// active and archived packages while leaving environment and data stores alone.
func (s *Store) Uninstall(ctx context.Context, skill, version string) (UninstallResult, error) {
	if err := validateIdentifier("skill", skill); err != nil {
		return UninstallResult{}, err
	}
	if version != "" {
		if err := validateIdentifier("version", version); err != nil {
			return UninstallResult{}, err
		}
	}

	release, err := s.AcquireWrite(ctx, skill)
	if err != nil {
		return UninstallResult{}, err
	}
	defer release()
	if err := s.recoverSwapLocked(skill); err != nil {
		return UninstallResult{}, err
	}
	selection, err := s.load(skill)
	if err != nil {
		return UninstallResult{}, err
	}
	if version == "" {
		return s.uninstallSkillLocked(skill, selection)
	}
	return s.uninstallVersionLocked(skill, version, selection)
}

func (s *Store) uninstallVersionLocked(skill, version string, selection Selection) (UninstallResult, error) {
	if selection.ActiveVersion == version {
		return UninstallResult{}, fmt.Errorf("cannot uninstall active skill version %s", version)
	}
	packagePath, err := s.archivePath(skill, version)
	if err != nil {
		return UninstallResult{}, err
	}
	digest, exists, err := directoryDigest(packagePath)
	if err != nil {
		return UninstallResult{}, err
	}
	if !exists {
		return UninstallResult{}, fmt.Errorf("skill %s version %s is not installed", skill, version)
	}

	target := selection
	var historyChanged bool
	target.History, historyChanged = removeVersion(target.History, version)
	if historyChanged {
		target.UpdatedAt = time.Now().UTC()
	}
	work, cleanupPath, err := s.transactionWorkPath(skill)
	if err != nil {
		return UninstallResult{}, err
	}
	backup := filepath.Join(work, "version")
	move, err := s.newSwapMove(swapRoleRemoval, packagePath, backup, digest, swapCommitRemove, swapRollbackRestore)
	if err != nil {
		_ = os.RemoveAll(work)
		return UninstallResult{}, err
	}
	transaction := s.newSwapTransaction(skill, "uninstall_version", selection, target, []SwapMove{move}, cleanupPath)
	if err := s.executeSwapLocked(transaction); err != nil {
		return UninstallResult{}, err
	}
	s.removeEmptyVersionParent(skill)
	return UninstallResult{RemovedVersions: []string{version}, ActiveVersion: selection.ActiveVersion}, nil
}

func (s *Store) uninstallSkillLocked(skill string, selection Selection) (UninstallResult, error) {
	versions, err := s.ListVersions(skill)
	if err != nil {
		return UninstallResult{}, err
	}
	if len(versions) == 0 {
		return UninstallResult{}, fmt.Errorf("skill %s is not installed", skill)
	}
	sort.Strings(versions)

	work, cleanupPath, err := s.transactionWorkPath(skill)
	if err != nil {
		return UninstallResult{}, err
	}
	moves := make([]SwapMove, 0, 2)
	if selection.ActiveVersion != "" {
		activePath, pathErr := s.activePath(skill, selection.System)
		if pathErr != nil {
			_ = os.RemoveAll(work)
			return UninstallResult{}, pathErr
		}
		digest, exists, digestErr := directoryDigest(activePath)
		if digestErr != nil {
			_ = os.RemoveAll(work)
			return UninstallResult{}, digestErr
		}
		if !exists {
			_ = os.RemoveAll(work)
			return UninstallResult{}, fmt.Errorf("skill %s active version %s is not installed", skill, selection.ActiveVersion)
		}
		move, moveErr := s.newSwapMove(swapRoleRemoval, activePath, filepath.Join(work, "active"), digest, swapCommitRemove, swapRollbackRestore)
		if moveErr != nil {
			_ = os.RemoveAll(work)
			return UninstallResult{}, moveErr
		}
		moves = append(moves, move)
	}
	archiveRoot := filepath.Join(s.root, versionsDirectory, skill)
	if digest, exists, digestErr := directoryDigest(archiveRoot); digestErr != nil {
		_ = os.RemoveAll(work)
		return UninstallResult{}, digestErr
	} else if exists {
		move, moveErr := s.newSwapMove(swapRoleRemoval, archiveRoot, filepath.Join(work, "versions"), digest, swapCommitRemove, swapRollbackRestore)
		if moveErr != nil {
			_ = os.RemoveAll(work)
			return UninstallResult{}, moveErr
		}
		moves = append(moves, move)
	}
	if len(moves) == 0 {
		_ = os.RemoveAll(work)
		return UninstallResult{}, fmt.Errorf("skill %s has state but no recoverable package directories", skill)
	}
	transaction := s.newSwapTransaction(skill, "uninstall_skill", selection, Selection{}, moves, cleanupPath)
	if err := s.executeSwapLocked(transaction); err != nil {
		return UninstallResult{}, err
	}
	return UninstallResult{RemovedVersions: versions, ActiveVersion: selection.ActiveVersion}, nil
}

func removeVersion(history []string, version string) ([]string, bool) {
	filtered := make([]string, 0, len(history))
	changed := false
	for _, item := range history {
		if item == version {
			changed = true
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, changed
}
