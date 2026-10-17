//go:build windows

package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows"
)

func TestTreeWatchMixedShortAndLongPathsDoesNotWatchMembers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "long-plugin-root-for-alias-test")
	member := filepath.Join(root, "member", "skills", "nested")
	if err := os.MkdirAll(member, 0700); err != nil {
		t.Fatal(err)
	}
	pointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	count, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
	if err != nil || count == 0 || count >= uint32(len(buffer)) {
		t.Skipf("short paths unavailable: %v", err)
	}
	short := windows.UTF16ToString(buffer[:count])
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(short, canonical) {
		t.Skip("volume does not expose a short-path alias")
	}
	files := NewTreeFiles(short, nil)
	defer files.Close()
	if files.treeClose == nil {
		t.Fatal("recursive watcher unavailable")
	}
	files.Add(short)
	files.Add(filepath.Join(canonical, "member"))
	files.Add(filepath.Join(canonical, "member", "skills", "nested"))
	files.mu.Lock()
	countDirs := len(files.dirs)
	files.mu.Unlock()
	if countDirs != 0 {
		t.Fatalf("opened %d nested handles for the same tree", countDirs)
	}
	before := files.Revision()
	if err := os.Rename(filepath.Join(canonical, "member"), filepath.Join(canonical, "replaced")); err != nil {
		t.Fatal("watch prevented replacement", err)
	}
	await(t, func() bool { return files.Revision() != before })
}

func TestScopedWatchWindowsPathAliases(t *testing.T) {
	home := filepath.Join(t.TempDir(), "common-skills-long-home-directory")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"case", "short_path"} {
		t.Run(kind, func(t *testing.T) {
			alias := strings.ToUpper(canonical)
			if kind == "short_path" {
				pointer, err := windows.UTF16PtrFromString(home)
				if err != nil {
					t.Fatal(err)
				}
				buffer := make([]uint16, 32768)
				count, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
				if err != nil || count == 0 || count >= uint32(len(buffer)) {
					t.Skipf("short paths unavailable: %v", err)
				}
				alias = windows.UTF16ToString(buffer[:count])
				if strings.EqualFold(alias, canonical) {
					t.Skip("volume does not expose a short-path alias")
				}
			}
			selected := filepath.Join(alias, kind, "skills")
			actual := filepath.Join(canonical, kind, "skills")
			files := NewScopedFiles(nil)
			defer files.Close()
			files.SetStableRoot(home)
			files.Add(selected)
			before := files.Revision()
			files.event(fsnotify.Event{Name: strings.ToLower(actual) + "-backup", Op: fsnotify.Write})
			if files.Revision() != before {
				t.Fatal("case/short-path sibling prefix entered selected source")
			}
			if err := os.MkdirAll(actual, 0700); err != nil {
				t.Fatal(err)
			}
			await(t, func() bool { return files.Revision() != before })
			files.Add(selected)
			before = files.Revision()
			if err := os.WriteFile(filepath.Join(actual, "SKILL.md"), []byte("selected"), 0600); err != nil {
				t.Fatal(err)
			}
			await(t, func() bool { return files.Revision() != before })
		})
	}
}

func TestScopedWatchWindowsUnavailableRootDoesNotOpenNestedHandles(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing-home")
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(filepath.Join(home, ".agents", "skills"))
	files.opMu.Lock()
	trees := len(files.scopedTrees)
	files.opMu.Unlock()
	files.mu.Lock()
	nested := len(files.dirs)
	files.mu.Unlock()
	if files.Healthy() || trees != 0 || nested != 0 {
		t.Fatalf("unavailable tree silently fell back to nested handles: healthy=%t trees=%d nested=%d", files.Healthy(), trees, nested)
	}
}

func TestScopedWatchWindowsAllowsExternalAliasTargetReplacement(t *testing.T) {
	home, boundary := t.TempDir(), t.TempDir()
	target := filepath.Join(boundary, "external-agents")
	selected := filepath.Join(home, ".agents", "skills")
	document := filepath.Join(target, "skills", "linked", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(document), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Dir(selected)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(selected)
	files.Add(filepath.Dir(document))
	files.opMu.Lock()
	trees := len(files.scopedTrees)
	files.opMu.Unlock()
	files.mu.Lock()
	nested := len(files.dirs)
	files.mu.Unlock()
	if trees != 2 || nested != 0 || !files.Healthy() {
		t.Fatalf("alias did not reuse disjoint stable roots: trees=%d nested=%d healthy=%t", trees, nested, files.Healthy())
	}
	before := files.Revision()
	if err := os.Rename(target, filepath.Join(boundary, "retired-agents")); err != nil {
		t.Fatalf("watch prevented external alias target replacement: %v", err)
	}
	await(t, func() bool { return files.Revision() != before })
	if err := os.MkdirAll(filepath.Dir(document), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	files.Add(selected)
	files.Add(filepath.Dir(document))
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
}

func TestWatchWindowsWriteBurstAllowsRegistrationAndClose(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped=%t", scoped), func(t *testing.T) {
			testWindowsWatchRegistrationAndClose(t, scoped)
		})
	}
}

func testWindowsWatchRegistrationAndClose(t *testing.T, scoped bool) {
	root, extra := t.TempDir(), t.TempDir()
	paths := make([]string, 32)
	directories := make([]string, 64)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("document-%d", i))
		if err := os.WriteFile(paths[i], []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range directories {
		directories[i] = filepath.Join(extra, fmt.Sprintf("selected-%d", i))
		if err := os.Mkdir(directories[i], 0700); err != nil {
			t.Fatal(err)
		}
	}
	var files *Files
	if scoped {
		files = NewScopedFiles(nil)
		files.SetStableRoot(root)
	} else {
		files = NewFiles(nil)
	}
	files.Add(root)
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { files.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("watcher close blocked after concurrent writes and registration")
		}
	})
	start := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	done := make(chan error, 3)
	closing := make(chan struct{})
	go func() {
		<-start
		for i := range 2048 {
			select {
			case <-stop:
				return
			default:
			}
			if err := os.WriteFile(paths[i%len(paths)], []byte(fmt.Sprintf("write-%d", i)), 0600); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	go func() {
		<-start
		for index, dir := range directories {
			select {
			case <-stop:
				return
			default:
			}
			files.Add(dir)
			if index == len(directories)/4 {
				close(closing)
			}
		}
		done <- nil
	}()
	go func() {
		select {
		case <-closing:
			files.Close()
			done <- nil
		case <-stop:
		}
	}()
	close(start)
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for range 3 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-deadline.C:
			t.Fatal("concurrent writes blocked directory registration")
		}
	}
}
