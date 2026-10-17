package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
)

type directoryTree struct {
	close func()
	used  uint64
}

// SetStableRoot supplies a watch location, not an additional selected source.
// Common's owner supplies UserHome before selecting .agents/skills. On Windows
// one recursive handle there permits atomic replacement of nested directories.
func (f *Files) SetStableRoot(root string) {
	if f == nil || f.watcher == nil || f.scopes == nil {
		return
	}
	root, err := filepath.Abs(root)
	if err != nil {
		f.unhealthy.Store(true)
		return
	}
	root, err = watchPathTarget(root)
	if err != nil {
		f.unhealthy.Store(true)
		return
	}
	f.opMu.Lock()
	defer f.opMu.Unlock()
	if f.closed {
		return
	}
	if f.stableRoot != "" && !sameWatchPath(f.stableRoot, root) {
		f.revision.Add(1)
	}
	f.stableRoot = root
	if f.scopedTrees != nil {
		if err := f.ensureScopedTree(root); err != nil {
			f.unhealthy.Store(true)
		}
		return
	}
	if err := f.addDirectory(root); err != nil {
		f.unhealthy.Store(true)
	}
}

// coverScopedPath runs under opMu. External aliases use their actual target's
// immediate parent so replacing that target does not fight a nested handle.
func (f *Files) coverScopedPath(path string) error {
	resolved, err := resolveWatchPath(path)
	if err != nil {
		return err
	}
	for root, tree := range f.scopedTrees {
		if insideWatchRoot(root, resolved) && (!sameWatchPath(root, resolved) || sameWatchPath(resolved, f.stableRoot)) {
			tree.used = f.sequence
			f.scopedTrees[root] = tree
			return nil
		}
	}
	return f.ensureScopedTree(filepath.Dir(resolved))
}

// ensureScopedTree keeps the recursive roots disjoint and within the existing
// directory budget. It never falls back to nested Windows fsnotify handles:
// those handles prevent the directory replacements this watcher must observe.
func (f *Files) ensureScopedTree(root string) error {
	root, err := watchPathTarget(root)
	if err != nil {
		return err
	}
	if filepath.Dir(root) == root {
		return fmt.Errorf("watch volume root %q: %w", root, os.ErrInvalid)
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("watch root %q is not a directory: %w", root, os.ErrInvalid)
	}
	for existing, tree := range f.scopedTrees {
		if insideWatchRoot(existing, root) {
			tree.used = f.sequence
			f.scopedTrees[existing] = tree
			return nil
		}
	}
	for existing, tree := range f.scopedTrees {
		if insideWatchRoot(root, existing) {
			tree.close()
			delete(f.scopedTrees, existing)
			f.revision.Add(1)
		}
	}
	if len(f.scopedTrees) >= maxWatchedDirectories {
		victim, oldest := "", ^uint64(0)
		for existing, tree := range f.scopedTrees {
			if tree.used < oldest && !insideWatchRoot(existing, f.stableRoot) {
				victim, oldest = existing, tree.used
			}
		}
		if victim == "" {
			return fmt.Errorf("watch tree capacity reached: %w", os.ErrInvalid)
		}
		f.scopedTrees[victim].close()
		delete(f.scopedTrees, victim)
		f.unhealthy.Store(true)
		f.revision.Add(1)
	}
	closeTree, ok := startTreeWatch(root, f.event, func() {
		f.unhealthy.Store(true)
		f.revision.Add(1)
	})
	if !ok {
		return fmt.Errorf("start watch tree %q: %w", root, os.ErrInvalid)
	}
	f.scopedTrees[root] = directoryTree{close: closeTree, used: f.sequence}
	return nil
}
