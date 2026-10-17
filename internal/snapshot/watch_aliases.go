package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

// watchOperationMutex follows the same token-lock pattern as resourceMutex.
// A canceled read barrier must not wait for another caller's directory I/O;
// the lock needs neither timer polling nor a background lock-acquiring worker.
type watchOperationMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *watchOperationMutex) initialize() {
	m.once.Do(func() {
		m.token = make(chan struct{}, 1)
		m.token <- struct{}{}
	})
}

func (m *watchOperationMutex) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.initialize()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
	}
	if err := ctx.Err(); err != nil {
		m.Unlock()
		return err
	}
	return nil
}

func (m *watchOperationMutex) Lock() {
	_ = m.LockContext(context.Background())
}

func (m *watchOperationMutex) Unlock() {
	m.initialize()
	select {
	case m.token <- struct{}{}:
	default:
		panic("snapshot: unlock of unlocked watch operation mutex")
	}
}

type watchAliasState struct {
	target  string
	parent  string
	exists  bool
	alias   bool
	mode    os.FileMode
	size    int64
	modTime int64
}

type watchAlias struct {
	state        watchAliasState
	selection    string
	used         uint64
	pendingRearm bool
}

// Kqueue follows symlink vnodes and can silently retain the old target after
// the link is replaced. Windows can change a junction's target in place, before
// a cached Context performs another Add. These platforms revalidate only the
// aliases already found by the actual metadata build. Linux directory events
// cover lexical link replacement, so it needs no additional read barrier.
func needsAliasReadBarrier() bool {
	switch runtime.GOOS {
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly", "windows":
		return true
	default:
		return false
	}
}

// readWatchAlias reads only a registered node and its target metadata. It does
// not enumerate directories or read Skill bodies. In particular, Windows uses
// the short, share-delete-safe target handle, not os.Readlink's reparse handle.
func readWatchAlias(path string) (watchAliasState, error) {
	var state watchAliasState
	parent, err := resolveWatchPath(filepath.Dir(path))
	if err != nil && !os.IsNotExist(err) {
		return state, err
	}
	state.parent = parent
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	state.exists = true
	state.alias, err = isWatchAlias(path, info)
	if err != nil {
		return watchAliasState{}, err
	}
	state.mode = info.Mode().Type()
	if state.alias {
		state.size, state.modTime = info.Size(), info.ModTime().UnixNano()
	}
	state.target, err = watchPathTarget(path)
	if err != nil && !os.IsNotExist(err) {
		return watchAliasState{}, err
	}
	return state, nil
}

// rememberAlias is independent of the source-scope union: an alias currently
// targeting inside a selected root can later escape it without a notification.
// Like directories, this registration has the existing bounded LRU lifetime.
func (f *Files) rememberAlias(path, selection string, state watchAliasState) {
	if f.aliases == nil {
		return
	}
	if previous, exists := f.aliases[path]; exists {
		if previous.state != state {
			// Keep the previous state until Sync retires its registrations.
			// A build which discovers a retarget must not publish as unchanged.
			f.revision.Add(1)
			previous.pendingRearm = true
		}
		if insideWatchRoot(selection, previous.selection) {
			previous.selection = selection
		}
		previous.used = f.sequence
		f.aliases[path] = previous
		return
	}
	if len(f.aliases) >= maxWatchedDirectories {
		victim, oldest := "", ^uint64(0)
		for candidate, entry := range f.aliases {
			if entry.used < oldest {
				victim, oldest = candidate, entry.used
			}
		}
		delete(f.aliases, victim)
		f.unhealthy.Store(true)
		f.revision.Add(1)
	}
	f.aliases[path] = watchAlias{state: state, selection: selection, used: f.sequence}
}

func (f *Files) syncAliases(ctx context.Context) error {
	if f.aliases == nil {
		return ctx.Err()
	}
	if err := f.opMu.LockContext(ctx); err != nil {
		return err
	}
	defer f.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.closed {
		return nil
	}
	paths := make([]string, 0, len(f.aliases))
	for path := range f.aliases {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	type change struct {
		path  string
		entry watchAlias
		state watchAliasState
	}
	changes := make([]change, 0)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := readWatchAlias(path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			f.unhealthy.Store(true)
			return fmt.Errorf("revalidate watch alias %q: %w", path, err)
		}
		entry := f.aliases[path]
		if entry.state != state || entry.pendingRearm {
			changes = append(changes, change{path: path, entry: entry, state: state})
		}
	}
	for _, changed := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if changed.entry.state != changed.state {
			f.revision.Add(1)
		}
		// Cancellation may happen after removal but before the replacement
		// Add. Retain that obligation even if the alias returns to its old
		// target before the next read barrier.
		if entry, exists := f.aliases[changed.path]; exists {
			entry.pendingRearm = true
			f.aliases[changed.path] = entry
		}
		if err := f.rearmAlias(ctx, changed.path, changed.entry, changed.state); err != nil {
			if ctx.Err() == nil {
				f.unhealthy.Store(true)
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// New aliases found while rearming share the same bounded budget.
		// Do not resurrect an evicted entry with an empty selection.
		if entry, exists := f.aliases[changed.path]; exists {
			entry.state = changed.state
			entry.pendingRearm = false
			f.aliases[changed.path] = entry
		}
	}
	return ctx.Err()
}

// rearmAlias runs under opMu. Retiring the containing kqueue directory also
// retires its internally followed symlink vnode; a cached dirs entry must not
// suppress the subsequent Add. Windows scoped trees use canonical stable roots
// and have no lexical member handle, so their existing owner covers new targets.
func (f *Files) rearmAlias(ctx context.Context, path string, previous watchAlias, state watchAliasState) error {
	var directories []string
	f.mu.Lock()
	for dir, used := range f.dirs {
		if (previous.state.parent != "" && sameWatchPath(previous.state.parent, dir)) ||
			insideWatchRoot(path, dir) ||
			(previous.state.target != "" && insideWatchRoot(previous.state.target, dir)) {
			delete(f.dirs, dir)
			f.retired[dir] = used
			directories = append(directories, dir)
			if previous.state.target != "" && state.target != "" && insideWatchRoot(previous.state.target, dir) {
				if relative, err := filepath.Rel(previous.state.target, dir); err == nil {
					directories = append(directories, filepath.Join(state.target, relative))
				}
			}
		}
	}
	f.mu.Unlock()
	if !f.removeRetired() {
		return fmt.Errorf("retire changed watch alias %q: %w", path, os.ErrInvalid)
	}
	selections := []string{previous.selection}
	f.scopeMu.RLock()
	for selected := range f.scopes {
		if insideWatchRoot(path, selected) && selected != previous.selection {
			selections = append(selections, selected)
		}
	}
	f.scopeMu.RUnlock()
	for _, selected := range selections {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.addPathLocked(selected); err != nil {
			return fmt.Errorf("rearm watch alias %q: %w", path, err)
		}
	}
	for _, dir := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := f.addDirectory(dir); err != nil {
			return fmt.Errorf("rearm alias directory %q: %w", dir, err)
		}
	}
	return nil
}
