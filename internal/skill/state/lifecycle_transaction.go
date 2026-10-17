package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PublishVersion durably publishes a staged ordinary Skill package into the
// Codex-style Skill tree. The staged candidate must live in this Store's
// private temp directory so interrupted installs can be discarded safely.
func (s *Store) PublishVersion(ctx context.Context, skill, version, candidate, expectedDigest string) error {
	if err := validateIdentifier("skill", skill); err != nil {
		return err
	}
	if err := validateIdentifier("version", version); err != nil {
		return err
	}
	if expectedDigest == "" {
		return errors.New("Skill candidate digest is required")
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	candidateParent := filepath.Dir(candidate)
	tempRoot := filepath.Join(s.root, tempDirectory)
	tempRelative, err := filepath.Rel(tempRoot, candidateParent)
	if err != nil || !filepath.IsLocal(tempRelative) || tempRelative == "." {
		return errors.New("Skill candidate must live in a private Store temp directory")
	}
	cleanupPath, err := s.relativeSwapPath(candidateParent)
	if err != nil {
		return err
	}

	release, err := s.AcquireWrite(ctx, skill)
	if err != nil {
		return err
	}
	defer release()
	if err := s.recoverSwapLocked(skill); err != nil {
		return err
	}
	selection, err := s.load(skill)
	if err != nil {
		return err
	}
	destination, err := s.InstalledPath(skill, version)
	if err != nil {
		return err
	}
	if digest, exists, err := directoryDigest(destination); err != nil {
		return err
	} else if exists {
		if digest != expectedDigest {
			return errors.New("version already exists with different content")
		}
		return s.cleanupSwapPath(cleanupPath)
	}
	candidateDigest, exists, err := directoryDigest(candidate)
	if err != nil {
		return err
	}
	if !exists || candidateDigest != expectedDigest {
		return errors.New("staged Skill candidate does not match its expected digest")
	}
	move, err := s.newSwapMove(swapRoleCandidate, candidate, destination, expectedDigest, swapCommitKeep, swapRollbackDiscard)
	if err != nil {
		return err
	}
	transaction := s.newSwapTransaction(skill, "install", selection, selection, []SwapMove{move}, cleanupPath)
	return s.executeSwapLocked(transaction)
}

func (s *Store) activateTransactional(ctx context.Context, skill, version string) error {
	if err := validateIdentifier("skill", skill); err != nil {
		return err
	}
	if err := validateIdentifier("version", version); err != nil {
		return err
	}
	release, err := s.AcquireWrite(ctx, skill)
	if err != nil {
		return err
	}
	defer release()
	if err := s.recoverSwapLocked(skill); err != nil {
		return err
	}

	current, err := s.load(skill)
	if err != nil {
		return err
	}
	if current.ActiveVersion == version {
		activePath, pathErr := s.activePath(skill, current.System)
		if pathErr != nil {
			return pathErr
		}
		if _, exists, digestErr := directoryDigest(activePath); digestErr != nil {
			return digestErr
		} else if !exists {
			return fmt.Errorf("skill %s active version %s is not installed", skill, version)
		}
		return nil
	}

	targetArchive, err := s.archivePath(skill, version)
	if err != nil {
		return err
	}
	targetDigest, exists, err := directoryDigest(targetArchive)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("skill %s version %s is not installed", skill, version)
	}
	targetPath, err := s.activePath(skill, current.System)
	if err != nil {
		return err
	}

	moves := make([]SwapMove, 0, 2)
	if current.ActiveVersion != "" {
		currentPath, pathErr := s.activePath(skill, current.System)
		if pathErr != nil {
			return pathErr
		}
		currentDigest, currentExists, digestErr := directoryDigest(currentPath)
		if digestErr != nil {
			return digestErr
		}
		if !currentExists {
			return fmt.Errorf("skill %s active version %s is not installed", skill, current.ActiveVersion)
		}
		currentArchive, archiveErr := s.archivePath(skill, current.ActiveVersion)
		if archiveErr != nil {
			return archiveErr
		}
		if _, archived, archiveDigestErr := directoryDigest(currentArchive); archiveDigestErr != nil {
			return archiveDigestErr
		} else if archived {
			return fmt.Errorf("skill %s archived version %s already exists", skill, current.ActiveVersion)
		}
		move, moveErr := s.newSwapMove(swapRolePrevious, currentPath, currentArchive, currentDigest, swapCommitKeep, swapRollbackRestore)
		if moveErr != nil {
			return moveErr
		}
		moves = append(moves, move)
	} else if _, occupied, digestErr := directoryDigest(targetPath); digestErr != nil {
		return digestErr
	} else if occupied {
		return fmt.Errorf("skill %s target active directory already exists", skill)
	}
	candidateMove, err := s.newSwapMove(swapRoleCandidate, targetArchive, targetPath, targetDigest, swapCommitKeep, swapRollbackRestore)
	if err != nil {
		return err
	}
	moves = append(moves, candidateMove)

	target := current
	if current.ActiveVersion != "" {
		target.History = prependUnique(target.History, current.ActiveVersion, 20)
	}
	target.ActiveVersion = version
	target.UpdatedAt = time.Now().UTC()
	transaction := s.newSwapTransaction(skill, "activate", current, target, moves, "")
	return s.executeSwapLocked(transaction)
}

// removeEmptyVersionParent removes only the now-empty per-Skill archive
// directory. It never removes another version or the shared .versions root.
func (s *Store) removeEmptyVersionParent(skill string) {
	path := filepath.Join(s.root, versionsDirectory, skill)
	if entries, err := os.ReadDir(path); err == nil && len(entries) == 0 {
		_ = os.Remove(path)
	}
}
