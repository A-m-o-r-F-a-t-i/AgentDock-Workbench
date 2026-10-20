package skilldata

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

const privateDirectoryMode = 0o700

// CleanupResult reports the private runtime-data outcome of a full Skill
// uninstall. Package removal and data cleanup are separate outcomes so callers
// never mistake a cleanup failure for an unexecuted package uninstall.
type CleanupResult struct {
	Path       string
	LegacyPath string
	State      string
	Preserved  bool
	Removed    bool
}

// MigrateLegacyRoot moves private runtime data out of the old data/skills tree.
// Invalid legacy entries are left untouched; valid Skill directories are moved
// atomically within AgentDockHome. Conflicting non-empty directories are
// rejected instead of silently merging unrelated state.
func MigrateLegacyRoot(cfg config.Config) error {
	legacyRoot, err := config.LegacySkillDataRoot(cfg)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(legacyRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect legacy Skill data root: %w", err)
	}

	canonicalRoot, err := config.SkillDataRoot(cfg)
	if err != nil {
		return err
	}
	root, err := openHomeRoot(cfg)
	if err != nil {
		return err
	}
	defer root.Close()

	legacyRelative, err := relativeToHome(cfg.AgentDockHome, legacyRoot)
	if err != nil {
		return err
	}
	canonicalRelative, err := relativeToHome(cfg.AgentDockHome, canonicalRoot)
	if err != nil {
		return err
	}
	if err := requireDirectory(root, cfg.AgentDockHome, "data"); err != nil {
		return err
	}
	if err := requireDirectory(root, cfg.AgentDockHome, legacyRelative); err != nil {
		return err
	}
	if err := ensureDirectory(root, cfg.AgentDockHome, canonicalRelative); err != nil {
		return err
	}

	directory, err := root.Open(legacyRelative)
	if err != nil {
		return fmt.Errorf("open legacy Skill data root: %w", err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return fmt.Errorf("read legacy Skill data root: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close legacy Skill data root: %w", closeErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		name := entry.Name()
		if _, err := config.LegacySkillDataDir(cfg, name); err != nil {
			continue
		}
		if err := migrateOne(root, cfg, name); err != nil {
			return err
		}
	}
	return removeDirectoryIfEmpty(root, legacyRelative)
}

// Ensure returns the canonical private runtime-data path, migrating an existing
// legacy directory before creating the canonical leaf. Every traversed
// component is kept beneath AgentDockHome and rejected if it is a symlink or a
// non-directory entry.
func Ensure(cfg config.Config, skill string) (string, error) {
	dataPath, err := config.SkillDataDir(cfg, skill)
	if err != nil {
		return "", err
	}
	canonicalRoot, err := config.SkillDataRoot(cfg)
	if err != nil {
		return "", err
	}
	root, err := openHomeRoot(cfg)
	if err != nil {
		return "", err
	}
	defer root.Close()

	canonicalRelative, err := relativeToHome(cfg.AgentDockHome, canonicalRoot)
	if err != nil {
		return "", err
	}
	if err := ensureDirectory(root, cfg.AgentDockHome, canonicalRelative); err != nil {
		return "", err
	}
	if err := migrateOne(root, cfg, skill); err != nil {
		return "", err
	}
	dataRelative, err := relativeToHome(cfg.AgentDockHome, dataPath)
	if err != nil {
		return "", err
	}
	if err := ensureDirectory(root, cfg.AgentDockHome, dataRelative); err != nil {
		return "", err
	}
	return dataPath, nil
}

// Cleanup removes empty private data after a full uninstall. Non-empty data is
// preserved by default; purge removes it explicitly. Both canonical and legacy
// locations are considered so old installations do not retain an accidental
// second Skill-looking tree.
func Cleanup(cfg config.Config, skill string, purge bool) (CleanupResult, error) {
	canonicalPath, err := config.SkillDataDir(cfg, skill)
	if err != nil {
		return CleanupResult{}, err
	}
	legacyPath, err := config.LegacySkillDataDir(cfg, skill)
	if err != nil {
		return CleanupResult{}, err
	}
	result := CleanupResult{Path: canonicalPath, State: "absent"}
	root, err := openHomeRoot(cfg)
	if err != nil {
		return result, err
	}
	defer root.Close()

	locations := []struct {
		path   string
		legacy bool
	}{
		{path: canonicalPath},
		{path: legacyPath, legacy: true},
	}
	for _, location := range locations {
		relative, relativeErr := relativeToHome(cfg.AgentDockHome, location.path)
		if relativeErr != nil {
			return result, relativeErr
		}
		parentExists, parentErr := requireDirectoryChainIfExists(root, cfg.AgentDockHome, filepath.Dir(relative))
		if parentErr != nil {
			return result, parentErr
		}
		if !parentExists {
			continue
		}
		info, statErr := root.Lstat(relative)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return result, fmt.Errorf("inspect Skill data directory %s: %w", relative, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return result, fmt.Errorf("Skill data path contains a symlink or non-directory component: %s", relative)
		}
		if location.legacy {
			result.LegacyPath = location.path
		}
		empty, emptyErr := directoryEmpty(root, relative)
		if emptyErr != nil {
			return result, emptyErr
		}
		if purge || empty {
			if removeErr := root.RemoveAll(relative); removeErr != nil {
				return result, fmt.Errorf("remove Skill data directory %s: %w", relative, removeErr)
			}
			result.Removed = true
			continue
		}
		result.Preserved = true
	}

	switch {
	case result.Preserved && result.Removed:
		result.State = "partially_removed"
	case result.Preserved:
		result.State = "preserved"
	case result.Removed && purge:
		result.State = "purged"
	case result.Removed:
		result.State = "removed_empty"
	}
	for _, directory := range []string{filepath.Dir(canonicalPath), filepath.Dir(legacyPath)} {
		relative, relativeErr := relativeToHome(cfg.AgentDockHome, directory)
		if relativeErr == nil {
			_ = removeDirectoryIfEmpty(root, relative)
		}
	}
	return result, nil
}

func migrateOne(root *os.Root, cfg config.Config, skill string) error {
	legacyRoot, err := config.LegacySkillDataRoot(cfg)
	if err != nil {
		return err
	}
	legacyRootRelative, err := relativeToHome(cfg.AgentDockHome, legacyRoot)
	if err != nil {
		return err
	}
	legacyRootInfo, err := root.Lstat(legacyRootRelative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Skill data root: %w", err)
	}
	if legacyRootInfo.Mode()&os.ModeSymlink != 0 || !legacyRootInfo.IsDir() {
		return fmt.Errorf("legacy Skill data root contains a symlink or non-directory component: %s", legacyRootRelative)
	}

	legacyPath, err := config.LegacySkillDataDir(cfg, skill)
	if err != nil {
		return err
	}
	canonicalPath, err := config.SkillDataDir(cfg, skill)
	if err != nil {
		return err
	}
	legacyRelative, err := relativeToHome(cfg.AgentDockHome, legacyPath)
	if err != nil {
		return err
	}
	canonicalRelative, err := relativeToHome(cfg.AgentDockHome, canonicalPath)
	if err != nil {
		return err
	}

	legacyInfo, err := root.Lstat(legacyRelative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Skill data for %s: %w", skill, err)
	}
	if legacyInfo.Mode()&os.ModeSymlink != 0 || !legacyInfo.IsDir() {
		return fmt.Errorf("legacy Skill data path contains a symlink or non-directory component: %s", legacyRelative)
	}
	legacyEmpty, err := directoryEmpty(root, legacyRelative)
	if err != nil {
		return err
	}
	if legacyEmpty {
		return root.Remove(legacyRelative)
	}

	canonicalInfo, err := root.Lstat(canonicalRelative)
	if errors.Is(err, os.ErrNotExist) {
		if renameErr := root.Rename(legacyRelative, canonicalRelative); renameErr != nil {
			return fmt.Errorf("migrate Skill data for %s: %w", skill, renameErr)
		}
		return securepath.EnsurePrivate(canonicalPath)
	}
	if err != nil {
		return fmt.Errorf("inspect canonical Skill data for %s: %w", skill, err)
	}
	if canonicalInfo.Mode()&os.ModeSymlink != 0 || !canonicalInfo.IsDir() {
		return fmt.Errorf("canonical Skill data path contains a symlink or non-directory component: %s", canonicalRelative)
	}

	canonicalEmpty, err := directoryEmpty(root, canonicalRelative)
	if err != nil {
		return err
	}
	switch {
	case canonicalEmpty:
		if err := root.Remove(canonicalRelative); err != nil {
			return fmt.Errorf("remove empty canonical Skill data for %s: %w", skill, err)
		}
		if err := root.Rename(legacyRelative, canonicalRelative); err != nil {
			return fmt.Errorf("migrate Skill data for %s: %w", skill, err)
		}
		return securepath.EnsurePrivate(canonicalPath)
	default:
		return fmt.Errorf("Skill data migration conflict for %s: canonical and legacy directories are both non-empty", skill)
	}
}

func openHomeRoot(cfg config.Config) (*os.Root, error) {
	home := filepath.Clean(cfg.AgentDockHome)
	if home == "." || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("AgentDockHome must be an absolute path")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("open AgentDock home for Skill data: %w", err)
	}
	return root, nil
}

func relativeToHome(home, path string) (string, error) {
	relative, err := filepath.Rel(filepath.Clean(home), filepath.Clean(path))
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return "", fmt.Errorf("Skill data path escapes AgentDockHome: %s", path)
	}
	return relative, nil
}

func ensureDirectory(root *os.Root, home, relative string) error {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(relative, privateDirectoryMode); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create Skill data directory %s: %w", relative, err)
		}
		info, err = root.Lstat(relative)
	}
	if err != nil {
		return fmt.Errorf("inspect Skill data directory %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Skill data path contains a symlink or non-directory component: %s", relative)
	}
	if err := securepath.EnsurePrivate(filepath.Join(home, relative)); err != nil {
		return fmt.Errorf("secure Skill data directory %s: %w", relative, err)
	}
	return nil
}

func requireDirectory(root *os.Root, home, relative string) error {
	info, err := root.Lstat(relative)
	if err != nil {
		return fmt.Errorf("inspect Skill data directory %s: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Skill data path contains a symlink or non-directory component: %s", relative)
	}
	if err := securepath.EnsurePrivate(filepath.Join(home, relative)); err != nil {
		return fmt.Errorf("secure Skill data directory %s: %w", relative, err)
	}
	return nil
}

func requireDirectoryChainIfExists(root *os.Root, home, relative string) (bool, error) {
	relative = filepath.Clean(relative)
	if !filepath.IsLocal(relative) || relative == "." {
		return false, fmt.Errorf("Skill data path escapes AgentDockHome: %s", relative)
	}
	current := ""
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect Skill data directory %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("Skill data path contains a symlink or non-directory component: %s", current)
		}
		if err := securepath.EnsurePrivate(filepath.Join(home, current)); err != nil {
			return false, fmt.Errorf("secure Skill data directory %s: %w", current, err)
		}
	}
	return true, nil
}

func directoryEmpty(root *os.Root, relative string) (bool, error) {
	directory, err := root.Open(relative)
	if err != nil {
		return false, fmt.Errorf("open Skill data directory %s: %w", relative, err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Skill data directory %s: %w", relative, err)
	}
	return len(entries) == 0, nil
}

func removeDirectoryIfEmpty(root *os.Root, relative string) error {
	empty, err := directoryEmpty(root, relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !empty {
		return nil
	}
	if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove empty Skill data directory %s: %w", relative, err)
	}
	return nil
}
