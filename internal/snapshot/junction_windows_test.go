//go:build windows

package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// createWatchJunctionForTest creates only a directory junction beneath this
// test's owned TempDir. Paths are child-process environment values, quoted in a
// fixed cmd command; delayed expansion and AutoRun are disabled.
func createWatchJunctionForTest(t *testing.T, owner, link, target string) {
	t.Helper()
	for _, path := range []string{link, target} {
		rel, err := filepath.Rel(owner, path)
		if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsAny(path, "\x00\r\n\"") {
			t.Fatalf("junction path is not an owned temporary child: %q", path)
		}
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("junction destination must be absent: %q, %v", link, err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("junction target must be an existing temporary directory: %q, %v", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			return
		} else if err != nil {
			t.Errorf("inspect temporary junction during cleanup: %v", err)
			return
		}
		if !isWatchDirectoryJunctionForTest(t, link) {
			t.Errorf("temporary junction was replaced by another entry; refusing to remove %q", link)
			return
		}
		// os.Remove removes this reparse-point entry. Never RemoveAll(link),
		// walk through it, or delete its target as part of junction cleanup.
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove temporary junction: %v", err)
		}
	})
	commandPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if !filepath.IsAbs(commandPath) {
		t.Fatal("SystemRoot must identify the native Windows command interpreter")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, commandPath)
	cmd.Args = nil
	cmd.Env = append(os.Environ(), "AGENTDOCK_WATCH_JUNCTION_LINK="+link, "AGENTDOCK_WATCH_JUNCTION_TARGET="+target)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(commandPath) + ` /d /v:off /s /c "mklink /J "%AGENTDOCK_WATCH_JUNCTION_LINK%" "%AGENTDOCK_WATCH_JUNCTION_TARGET%""`}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create temporary directory junction: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	if !isWatchDirectoryJunctionForTest(t, link) {
		t.Fatal("mklink /J did not create an NTFS mount-point reparse entry")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	resolved, resolveErr := filepath.EvalSymlinks(link)
	t.Logf("junction diagnostic: mode=%s EvalSymlinks=%q error=%v", info.Mode(), resolved, resolveErr)
}

func isWatchDirectoryJunctionForTest(t *testing.T, path string) bool {
	t.Helper()
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var data syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pointer, &data)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.FindClose(handle); err != nil {
		t.Fatal(err)
	}
	const mountPointTag = uint32(0xa0000003)
	return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 && data.Reserved0 == mountPointTag
}

func TestScopedWatchWindowsJunctionRetarget(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(owner, "Common home & spaces")
	root := filepath.Join(home, ".agents", "skills")
	external := filepath.Join(owner, "external boundary", "target A")
	inside := filepath.Join(root, "covered target B")
	for _, directory := range []string{external, inside} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "junction alias")
	createWatchJunctionForTest(t, owner, alias, external)
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(root)
	files.Add(alias)
	files.opMu.Lock()
	treeCount := len(files.scopedTrees)
	files.opMu.Unlock()
	files.mu.Lock()
	nestedCount := len(files.dirs)
	files.mu.Unlock()
	if treeCount != 2 || nestedCount != 0 || !files.Healthy() {
		t.Fatalf("junction did not use disjoint stable trees: trees=%d nested=%d healthy=%t", treeCount, nestedCount, files.Healthy())
	}
	files.scopeMu.RLock()
	initial := files.scopes[alias].resolved
	files.scopeMu.RUnlock()
	if !sameWatchPath(initial, external) {
		t.Errorf("junction alias did not map to external A: got=%q want=%q", initial, external)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	createWatchJunctionForTest(t, owner, alias, inside)
	before := files.Revision()
	files.Add(alias)
	if files.Revision() == before {
		t.Error("observing the junction retarget did not invalidate the source")
	}
	files.scopeMu.RLock()
	resolved := files.scopes[alias].resolved
	files.scopeMu.RUnlock()
	if !sameWatchPath(resolved, inside) {
		t.Errorf("junction alias retained the wrong canonical target: got=%q want=%q", resolved, inside)
	}
	// Only the alias registered A in this fixture; independently selected
	// historical sources have their own bounded lifetime and are not removed.
	if files.selectedEvent(fsnotify.Event{Name: filepath.Join(external, "SKILL.md"), Op: fsnotify.Write}) {
		t.Error("the junction alias still selected events from external A")
	}
	if !files.selectedEvent(fsnotify.Event{Name: filepath.Join(inside, "SKILL.md"), Op: fsnotify.Write}) {
		t.Error("the junction alias no longer selected events from current B")
	}
	document := filepath.Join(inside, "SKILL.md")
	if err := os.WriteFile(document, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(document)
	if err != nil {
		t.Fatal(err)
	}
	before = files.Revision()
	if err := os.WriteFile(document, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(document, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return files.Revision() != before })
	if err := os.Remove(alias); err != nil {
		t.Fatalf("remove only the temporary junction: %v", err)
	}
	if data, err := os.ReadFile(document); err != nil || string(data) != "after!" {
		t.Fatalf("junction removal touched target B: %q %v", data, err)
	}
}

func TestScopedWatchWindowsJunctionReadBarrier(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(owner, "Common home & spaces")
	root := filepath.Join(home, ".agents", "skills")
	external := filepath.Join(owner, "external boundary", "target A")
	inside := filepath.Join(root, "covered target B")
	for _, directory := range []string{external, inside} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "junction alias")
	createWatchJunctionForTest(t, owner, alias, external)
	// A cache hit performs Sync without another metadata build or Add. Block
	// platform notifications so only that read barrier can observe the change.
	files := NewScopedFiles(func(string) bool { return false })
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(root)
	files.Add(alias)
	if err := files.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	files.opMu.Lock()
	initial, registered := files.aliases[alias]
	files.opMu.Unlock()
	if !registered || !sameWatchPath(initial.state.target, external) || initial.selection != alias {
		t.Fatalf("initial junction was not registered for read barriers: %#v", initial)
	}
	before := files.Revision()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	createWatchJunctionForTest(t, owner, alias, inside)
	if files.Revision() != before {
		t.Fatal("fixture did not suppress junction retarget notifications")
	}
	// No external Add occurs after the junction changes its target.
	if err := files.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	files.opMu.Lock()
	observed, registered := files.aliases[alias]
	files.opMu.Unlock()
	files.scopeMu.RLock()
	resolved := files.scopes[alias].resolved
	files.scopeMu.RUnlock()
	if files.Revision() == before || !registered || observed.pendingRearm ||
		!sameWatchPath(observed.state.target, inside) || !sameWatchPath(resolved, inside) ||
		!files.Healthy() {
		t.Fatalf("read barrier did not repair the silent junction retarget: before=%s after=%s alias=%#v scope=%q healthy=%t",
			before, files.Revision(), observed, resolved, files.Healthy())
	}
	if files.selectedEvent(fsnotify.Event{Name: filepath.Join(external, "SKILL.md"), Op: fsnotify.Write}) {
		t.Fatal("read barrier retained the junction's old target scope")
	}
	if !files.selectedEvent(fsnotify.Event{Name: filepath.Join(inside, "SKILL.md"), Op: fsnotify.Write}) {
		t.Fatal("read barrier lost the junction's current target scope")
	}
	stable := files.Revision()
	for range 3 {
		if err := files.Sync(t.Context()); err != nil || files.Revision() != stable {
			t.Fatalf("unchanged junction advanced the source at a read barrier: before=%s after=%s error=%v",
				stable, files.Revision(), err)
		}
	}
	if err := os.Remove(alias); err != nil {
		t.Fatalf("remove only the temporary junction: %v", err)
	}
	if info, err := os.Stat(inside); err != nil || !info.IsDir() {
		t.Fatalf("junction removal touched target B: %#v %v", info, err)
	}
}
