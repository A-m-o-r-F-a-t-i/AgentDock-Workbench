package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Files watches explicitly selected metadata directories. It never recursively
// scans a repository or parses Skill bodies. Callers add directories while
// doing their one actual metadata build, including the parent of absent files.
type Files struct {
	treeRoot     string
	treeResolved string
	treeClose    func()
	watcher      *fsnotify.Watcher
	filter       func(string) bool
	opMu         sync.Mutex
	mu           sync.Mutex
	dirs         map[string]uint64
	retired      map[string]uint64
	pending      string
	pendingGone  bool
	scopeMu      sync.RWMutex
	scopes       map[string]directoryScope
	stableRoot   string
	scopedTrees  map[string]directoryTree
	sequence     uint64
	revision     atomic.Uint64
	unhealthy    atomic.Bool
	syncRequests chan chan struct{}
	done         chan struct{}
	once         sync.Once
	closed       bool
}

type directoryScope struct {
	resolved string
	used     uint64
}

const maxWatchedDirectories = 2048

func NewFiles(filter func(string) bool) *Files {
	return newFiles(filter, false)
}

// NewScopedFiles restricts notifications to the directories selected by Add.
// When a selected directory is absent, its existing ancestor is only a watch
// location: unrelated writes there do not change the selected source.
func NewScopedFiles(filter func(string) bool) *Files {
	return newFiles(filter, true)
}

func newFiles(filter func(string) bool, scoped bool) *Files {
	f := &Files{filter: filter, dirs: map[string]uint64{}, retired: map[string]uint64{}, syncRequests: make(chan chan struct{}), done: make(chan struct{})}
	if scoped {
		f.scopes = make(map[string]directoryScope)
		if runtime.GOOS == "windows" {
			f.scopedTrees = make(map[string]directoryTree)
		}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		f.unhealthy.Store(true)
		close(f.done)
		return f
	}
	f.watcher = watcher
	go f.readEvents()
	return f
}

// NewTreeFiles uses a single stable-root recursive notification handle on
// Windows. Watching every nested directory would block atomic plugin/Skill
// directory replacement even with FILE_SHARE_DELETE.
func NewTreeFiles(root string, filter func(string) bool) *Files {
	f := NewFiles(filter)
	if closeTree, ok := startTreeWatch(filepath.Clean(root), f.event, func() { f.unhealthy.Store(true); f.revision.Add(1) }); ok {
		f.treeRoot = filepath.Clean(root)
		f.treeResolved, _ = filepath.EvalSymlinks(root)
		f.treeClose = closeTree
	}
	return f
}
func (f *Files) readEvents() {
	defer close(f.done)
	for {
		select {
		case event, ok := <-f.watcher.Events:
			if !ok {
				return
			}
			f.event(event)
		case _, ok := <-f.watcher.Errors:
			if !ok {
				return
			}
			f.unhealthy.Store(true)
			f.revision.Add(1)
		case reply := <-f.syncRequests:
			// Process queued events before acknowledging the caller's read barrier.
			for {
				select {
				case event, ok := <-f.watcher.Events:
					if !ok {
						close(reply)
						return
					}
					f.event(event)
				default:
					close(reply)
					goto drained
				}
			}
		drained:
		}
	}
}
func (f *Files) event(e fsnotify.Event) {
	if (f.filter == nil || f.filter(e.Name)) && f.selectedEvent(e) {
		f.revision.Add(1)
	}
	if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
		f.mu.Lock()
		for dir, watch := range f.dirs {
			if insideWatchRoot(e.Name, dir) {
				delete(f.dirs, dir)
				f.retired[dir] = watch
			}
		}
		if f.pending != "" && insideWatchRoot(e.Name, f.pending) {
			f.pendingGone = true
		}
		f.mu.Unlock()
	}
}

func (f *Files) selectedEvent(e fsnotify.Event) bool {
	if f.scopes == nil {
		return true
	}
	f.scopeMu.RLock()
	defer f.scopeMu.RUnlock()
	for path, scope := range f.scopes {
		for _, root := range []string{path, scope.resolved} {
			if root == "" {
				continue
			}
			if insideWatchRoot(root, e.Name) {
				return true
			}
			// An ancestor becoming present, disappearing or being replaced may
			// change a missing root. Its own content/mtime writes may not.
			if e.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 && insideWatchRoot(e.Name, root) {
				return true
			}
		}
	}
	return false
}

func (f *Files) Add(path string) {
	if f == nil || f.watcher == nil {
		return
	}
	path = filepath.Clean(path)
	resolved := ""
	var linkParents, linkTargets []string
	if f.scopes != nil {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			f.unhealthy.Store(true)
			return
		}
		resolved = resolveWatchPath(path)
		for candidate := path; ; candidate = filepath.Dir(candidate) {
			if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
				linkParents = append(linkParents, filepath.Dir(candidate))
				if target, err := filepath.EvalSymlinks(candidate); err == nil {
					linkTargets = append(linkTargets, target)
				} else {
					f.unhealthy.Store(true)
				}
			}
			if filepath.Dir(candidate) == candidate {
				break
			}
		}
	}
	f.opMu.Lock()
	defer f.opMu.Unlock()
	if f.closed {
		return
	}
	f.sequence++
	if f.scopes != nil {
		resolved = f.selectDirectory(path, resolved)
	}
	// Register both names before checking tree coverage. A lexical path below
	// UserHome may point at a Common package outside that tree.
	if f.scopedTrees != nil {
		f.coverScopedPath(path)
		if resolved != "" {
			f.coverScopedPath(resolved)
		}
		for _, target := range linkTargets {
			f.coverScopedPath(target)
		}
		for _, parent := range linkParents {
			f.ensureScopedTree(parent)
		}
		return
	}
	if f.treeRoot != "" {
		if insideWatchRoot(f.treeRoot, path) {
			return
		}
		// Safety checks may canonicalize a Windows 8.3 path. Reuse the
		// existing recursive handle rather than opening a member handle.
		if f.treeResolved != "" {
			if actual, err := filepath.EvalSymlinks(path); err == nil && insideWatchRoot(f.treeResolved, actual) {
				return
			}
		}
	}
	f.addDirectory(path)
	if f.scopes != nil {
		f.addDirectory(filepath.Dir(path))
		for _, parent := range linkParents {
			f.addDirectory(parent)
		}
		if resolved != "" && resolved != path {
			f.addDirectory(resolved)
			f.addDirectory(filepath.Dir(resolved))
		}
		for _, target := range linkTargets {
			f.addDirectory(filepath.Dir(target))
		}
	}
}

// selectDirectory keeps only ranges not already covered by selected sources.
// Lexical and resolved names are checked separately: a package below the Common
// root can still point outside it. Platform coverage must run even on a hit.
func (f *Files) selectDirectory(path, resolved string) string {
	f.scopeMu.Lock()
	defer f.scopeMu.Unlock()
	previous, exists := f.scopes[path]
	if resolved == "" {
		resolved = previous.resolved
	}
	if exists {
		if previous.resolved != "" && resolved != "" && !sameWatchPath(previous.resolved, resolved) {
			// Add has observed the alias change itself; invalidation must not
			// depend on the platform event already having reached the reader.
			f.revision.Add(1)
		}
		previous.resolved, previous.used = resolved, f.sequence
		f.scopes[path] = previous
	}
	pathCovered, targetCovered := false, false
	for selected, scope := range f.scopes {
		coversPath, coversTarget := false, false
		for _, root := range []string{selected, scope.resolved} {
			if root == "" {
				continue
			}
			coversPath = coversPath || insideWatchRoot(root, path)
			coversTarget = coversTarget || (resolved != "" && insideWatchRoot(root, resolved))
		}
		if coversPath || coversTarget {
			scope.used = f.sequence
			f.scopes[selected] = scope
		}
		pathCovered = pathCovered || coversPath
		targetCovered = targetCovered || coversTarget
	}
	if pathCovered && targetCovered {
		return resolved
	}
	if _, exists := f.scopes[path]; !exists && len(f.scopes) >= maxWatchedDirectories {
		victim, oldest := "", ^uint64(0)
		for candidate, scope := range f.scopes {
			if scope.used < oldest {
				victim, oldest = candidate, scope.used
			}
		}
		delete(f.scopes, victim)
		f.unhealthy.Store(true)
		f.revision.Add(1)
	}
	f.scopes[path] = directoryScope{resolved: resolved, used: f.sequence}
	return resolved
}

// addDirectory runs under opMu, which the event reader never takes. Windows
// Add/Remove use the same platform thread that delivers Events, so no platform
// operation may hold the state or scope lock needed by that reader.
func (f *Files) addDirectory(path string) {
	f.mu.Lock()
	hasRetired := len(f.retired) != 0
	f.mu.Unlock()
	if hasRetired && !f.removeRetired() {
		return
	}
	if f.useDirectory(path) {
		return
	}
	for {
		info, err := os.Lstat(path)
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			f.unhealthy.Store(true)
			return
		}
		path = parent
		if f.useDirectory(path) {
			return
		}
	}
	if f.scopes != nil {
		if actual, err := filepath.EvalSymlinks(path); err == nil {
			path = actual
		}
		if f.useDirectory(path) {
			return
		}
	}
	f.mu.Lock()
	if len(f.dirs)+len(f.retired) >= maxWatchedDirectories {
		victim, oldest := "", ^uint64(0)
		for dir, used := range f.dirs {
			if used < oldest {
				victim, oldest = dir, used
			}
		}
		if victim != "" {
			f.retired[victim] = f.dirs[victim]
			delete(f.dirs, victim)
			f.revision.Add(1)
		}
	}
	f.mu.Unlock()
	if !f.removeRetired() {
		return
	}
	f.mu.Lock()
	f.pending, f.pendingGone = path, false
	f.mu.Unlock()
	err := f.watcher.Add(path)
	f.mu.Lock()
	removed := f.pendingGone
	f.pending, f.pendingGone = "", false
	if err == nil {
		if removed {
			f.retired[path] = f.sequence
		} else {
			f.dirs[path] = f.sequence
		}
	}
	f.mu.Unlock()
	if err != nil {
		f.unhealthy.Store(true)
		return
	}
	if removed {
		f.revision.Add(1)
		f.removeRetired()
	}
}

func (f *Files) useDirectory(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.dirs[path]; !ok {
		return false
	}
	f.dirs[path] = f.sequence
	return true
}

// removeRetired reconciles only this Files instance's registrations. Windows
// updates WatchList names after a rename, while Linux/kqueue retain the original
// registered name. Remove uses the current list on either backend; an error
// alone never proves that a handle has gone. Retired entries remain in the same
// budget until every listed registration belongs to an active directory.
func (f *Files) removeRetired() bool {
	f.mu.Lock()
	hasRetired := len(f.retired) != 0
	f.mu.Unlock()
	if !hasRetired {
		return true
	}
	for {
		listed := f.watcher.WatchList()
		f.mu.Lock()
		for dir, used := range f.dirs {
			if !containsWatchPath(listed, dir) {
				delete(f.dirs, dir)
				f.retired[dir] = used
				f.revision.Add(1)
			}
		}
		unowned := make([]string, 0)
		for _, path := range listed {
			if !f.ownsDirectory(path) {
				unowned = append(unowned, path)
			}
		}
		if len(unowned) == 0 {
			clear(f.retired)
			f.mu.Unlock()
			return true
		}
		f.mu.Unlock()
		for _, path := range unowned {
			_ = f.watcher.Remove(path)
		}
		after := f.watcher.WatchList()
		if len(after) >= len(listed) {
			// A concurrent Windows rename can change the listed name without
			// releasing its handle. Do not spin under the Add/Close lock.
			f.unhealthy.Store(true)
			return false
		}
		for _, path := range unowned {
			if containsWatchPath(after, path) {
				f.unhealthy.Store(true)
				return false
			}
		}
		// Each pass removed at least one owned platform registration. Events
		// may retire more during removal; no new Add can run under opMu.
	}
}

// ownsDirectory requires mu. Path comparison follows the platform's case and
// separator rules, including Windows case aliases.
func (f *Files) ownsDirectory(path string) bool {
	for dir := range f.dirs {
		if sameWatchPath(dir, path) {
			return true
		}
	}
	return false
}

func containsWatchPath(paths []string, path string) bool {
	for _, candidate := range paths {
		if sameWatchPath(candidate, path) {
			return true
		}
	}
	return false
}

func sameWatchPath(first, second string) bool {
	relative, err := filepath.Rel(first, second)
	return err == nil && relative == "."
}

// resolveWatchPath retains a canonical alias even when the selected leaf does
// not exist yet. No filesystem lookup is needed when processing a later rename
// or deletion notification, when EvalSymlinks on the removed path would fail.
func resolveWatchPath(path string) string {
	for candidate := path; ; candidate = filepath.Dir(candidate) {
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			relative, err := filepath.Rel(candidate, path)
			if err == nil {
				return filepath.Join(resolved, relative)
			}
			return ""
		}
		if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return ""
		}
		if filepath.Dir(candidate) == candidate {
			return ""
		}
	}
}
func (f *Files) Sync(ctx context.Context) error {
	if f == nil || f.watcher == nil {
		return nil
	}
	reply := make(chan struct{})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return nil
	case f.syncRequests <- reply:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return nil
	case <-reply:
		return nil
	}
}

const unhealthyRevalidationInterval = 250 * time.Millisecond

// Revisions separates actual source changes from periodic cache revalidation.
// A failed watcher still causes fresh disk reads, but elapsed time alone must
// never invalidate the before/after consistency check of an in-flight build.
func (f *Files) Revisions() (source, cache string) {
	if f == nil {
		return "none", "none"
	}
	source = strconv.FormatUint(f.revision.Load(), 10)
	cache = source
	if f.unhealthy.Load() {
		source += "-unhealthy"
		cache = source + "-revalidate-" + strconv.FormatInt(time.Now().UnixMilli()/unhealthyRevalidationInterval.Milliseconds(), 10)
	}
	return source, cache
}

// Revision is the stable source identity used by consistency checks.
func (f *Files) Revision() string {
	source, _ := f.Revisions()
	return source
}
func (f *Files) Invalidate() {
	if f != nil {
		f.revision.Add(1)
	}
}
func (f *Files) Healthy() bool { return f != nil && !f.unhealthy.Load() }
func (f *Files) Close() {
	if f == nil {
		return
	}
	f.once.Do(func() {
		f.opMu.Lock()
		f.closed = true
		if f.treeClose != nil {
			f.treeClose()
		}
		for root, tree := range f.scopedTrees {
			tree.close()
			delete(f.scopedTrees, root)
		}
		if f.watcher != nil {
			_ = f.watcher.Close()
		}
		f.opMu.Unlock()
		<-f.done
	})
}

// Stamps uses only metadata for a known bounded set of paths. It is not a walk
// or full content hash. File notifications cover same-size/same-mtime rewrites.
func Stamps(paths ...string) string {
	var out strings.Builder
	for _, path := range paths {
		out.WriteString(path)
		out.WriteByte('=')
		info, err := os.Lstat(path)
		if err != nil {
			out.WriteString("missing:")
			if !os.IsNotExist(err) {
				out.WriteString(err.Error())
			}
		} else {
			fmt.Fprintf(&out, "%d/%d/%d", info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
		out.WriteByte('|')
	}
	return out.String()
}

// MetadataPaths returns deterministic rule candidates up to the filesystem
// root, without following links or changing how the rules loader chooses scope.
func MetadataPaths(workdir, global string) []string {
	paths := []string{global}
	for dir := filepath.Clean(workdir); ; dir = filepath.Dir(dir) {
		paths = append(paths, filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, ".git"))
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	sort.Strings(paths)
	return paths
}

func insideWatchRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
