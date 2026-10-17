package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatchSeesSameSizeSameTimestampContentChange(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	files := NewTreeFiles(root, nil)
	defer files.Close()
	files.Add(root)
	files.Add(dir)
	before := files.Revision()
	if err := os.WriteFile(path, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return files.Revision() != before })
	if err := files.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestWatchDoesNotBlockReplacingMemberDirectory(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "member")
	if err := os.MkdirAll(filepath.Join(member, "skills", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	files := NewTreeFiles(root, nil)
	defer files.Close()
	files.Add(root)
	files.Add(member)
	files.Add(filepath.Join(member, "skills", "nested"))
	if err := os.Rename(member, filepath.Join(root, "old")); err != nil {
		t.Fatalf("watch blocked atomic directory replacement: %v", err)
	}
}
func TestWatchClosesWithoutPendingReadLeak(t *testing.T) {
	for range 20 {
		files := NewTreeFiles(t.TempDir(), nil)
		done := make(chan struct{})
		go func() { files.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("pending watch did not cancel")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	files := NewFiles(nil)
	defer files.Close()
	if err := files.Sync(ctx); err != nil && err != context.Canceled {
		t.Fatal(err)
	}
}

func TestScopedWatchFiltersAncestorAndSiblingEvents(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".agents", "skills")
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(root)
	for _, test := range []struct {
		name string
		path string
		op   fsnotify.Op
		want bool
	}{
		{"core_home_write", filepath.Join(home, "home"), fsnotify.Write, false},
		{"user_home_write", home, fsnotify.Write, false},
		{"ancestor_write", filepath.Dir(root), fsnotify.Write, false},
		{"ancestor_created", filepath.Dir(root), fsnotify.Create, true},
		{"ancestor_removed", filepath.Dir(root), fsnotify.Remove, true},
		{"ancestor_replaced", filepath.Dir(root), fsnotify.Rename, true},
		{"sibling_prefix", root + "-backup", fsnotify.Create, false},
		{"selected_document", filepath.Join(root, "one", "SKILL.md"), fsnotify.Write, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := files.Revision()
			files.event(fsnotify.Event{Name: test.path, Op: test.op})
			if changed := files.Revision() != before; changed != test.want {
				t.Fatalf("event changed source = %t, want %t", changed, test.want)
			}
		})
	}
}

func TestScopedWatchBoundsSelectedDirectories(t *testing.T) {
	t.Run("covered_descendants", func(t *testing.T) {
		home := t.TempDir()
		root := filepath.Join(home, ".agents", "skills")
		files := NewScopedFiles(nil)
		defer files.Close()
		files.SetStableRoot(home)
		files.Add(root)
		before := files.Revision()
		for range 3 {
			files.Add(root)
			for index := range maxWatchedDirectories + 1 {
				files.Add(filepath.Join(root, fmt.Sprintf("package-%d", index)))
			}
			files.scopeMu.RLock()
			selected := len(files.scopes)
			files.scopeMu.RUnlock()
			if selected != 1 || !files.Healthy() || files.Revision() != before {
				t.Fatalf("covered children exhausted source scopes: selected=%d healthy=%t revision=%s before=%s", selected, files.Healthy(), files.Revision(), before)
			}
		}
	})
	home := t.TempDir()
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	for index := range maxWatchedDirectories + 1 {
		files.Add(filepath.Join(home, fmt.Sprintf("absent-%d", index)))
	}
	files.scopeMu.RLock()
	selected := len(files.scopes)
	files.scopeMu.RUnlock()
	files.mu.Lock()
	handles := len(files.dirs)
	files.mu.Unlock()
	files.opMu.Lock()
	handles += len(files.scopedTrees)
	files.opMu.Unlock()
	if selected != maxWatchedDirectories || handles != 1 || files.Healthy() {
		t.Fatalf("unbounded or silently incomplete watch: selected=%d handles=%d healthy=%t", selected, handles, files.Healthy())
	}
	before := files.Revision()
	files.Add(filepath.Join(home, "another-absent-directory"))
	if files.Revision() == before {
		t.Fatal("another scope eviction did not invalidate the unhealthy source")
	}
}

func TestScopedWatchUpdatesAliasWhenTargetBecomesCovered(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	external, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".agents", "skills")
	inside := filepath.Join(root, "local")
	if err := os.MkdirAll(inside, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "linked")
	if err := os.Symlink(external, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files := NewScopedFiles(nil)
	defer files.Close()
	files.SetStableRoot(home)
	files.Add(root)
	files.Add(alias)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, alias); err != nil {
		t.Fatal(err)
	}
	before := files.Revision()
	files.Add(alias)
	if files.Revision() == before {
		t.Fatal("observing an alias target change did not invalidate the source")
	}
	files.scopeMu.RLock()
	resolved := files.scopes[alias].resolved
	files.scopeMu.RUnlock()
	if !sameWatchPath(resolved, inside) {
		t.Fatalf("retargeted alias kept its old canonical mapping: got %q, want %q", resolved, inside)
	}
	// This old target was selected only through the alias. Independently
	// registered sources have their own lifetime and are not removed here.
	if files.selectedEvent(fsnotify.Event{Name: filepath.Join(external, "SKILL.md"), Op: fsnotify.Write}) {
		t.Fatal("the alias still selected events from its previous target")
	}
	if !files.selectedEvent(fsnotify.Event{Name: filepath.Join(inside, "SKILL.md"), Op: fsnotify.Write}) {
		t.Fatal("the alias no longer selected its current target")
	}
}
