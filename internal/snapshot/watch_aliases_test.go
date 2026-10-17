//go:build !windows

package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestScopedAliasReadBarrierTracksSilentRetarget(t *testing.T) {
	for _, document := range []bool{false, true} {
		name := "directory"
		if document {
			name = "document"
		}
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			inside := filepath.Join(root, "inside")
			outside, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(inside, 0700); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{inside, outside} {
				if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("before"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			oldTarget, newTarget := inside, outside
			if document {
				oldTarget, newTarget = filepath.Join(inside, "SKILL.md"), filepath.Join(outside, "SKILL.md")
			}
			link := filepath.Join(root, "selected")
			if err := os.Symlink(oldTarget, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			var notifications atomic.Bool
			files := NewScopedFiles(func(string) bool { return notifications.Load() })
			t.Cleanup(files.Close)
			// Exercise the kqueue/Windows read barrier on Linux without an OS
			// dependency or a second watcher. Real events cannot change source.
			files.aliases = make(map[string]watchAlias)
			files.Add(root)
			files.Add(link)
			if err := files.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(files.scopes) != 1 || files.aliases[link].selection != link {
				t.Fatalf("covered alias was lost: scopes=%#v aliases=%#v", files.scopes, files.aliases)
			}
			before := files.Revision()
			replaceWatchAlias(t, link, newTarget)
			if files.Revision() != before {
				t.Fatal("fixture did not suppress retarget notifications")
			}
			if err := files.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			if files.Revision() == before || !sameWatchPath(files.aliases[link].state.target, newTarget) {
				t.Fatalf("silent retarget was not observed: revision=%s alias=%#v", files.Revision(), files.aliases[link])
			}
			stable := files.Revision()
			for range 3 {
				if err := files.Sync(t.Context()); err != nil || files.Revision() != stable {
					t.Fatalf("unchanged alias advanced source: before=%s after=%s err=%v", stable, files.Revision(), err)
				}
			}
			// A build can see B -> A before the read barrier, followed by
			// A -> B with identical link metadata. The pending subscription
			// change must still restore B's scope and physical registrations.
			lastState := files.aliases[link].state
			replaceWatchAlias(t, link, oldTarget)
			files.Add(link)
			replaceWatchAlias(t, link, newTarget)
			stamp := unix.NsecToTimespec(lastState.modTime)
			if err := unix.UtimesNanoAt(unix.AT_FDCWD, link, []unix.Timespec{stamp, stamp}, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				t.Fatal(err)
			}
			if observed, err := readWatchAlias(link); err != nil || observed != lastState {
				t.Fatalf("ABA fixture did not restore original metadata: %#v want %#v err=%v", observed, lastState, err)
			}
			if err := files.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			if scope := files.scopes[link]; !sameWatchPath(scope.resolved, newTarget) || files.aliases[link].pendingRearm {
				t.Fatalf("ABA read barrier retained the intermediate target: scope=%#v alias=%#v", scope, files.aliases[link])
			}
			stable = files.Revision()
			if err := files.Sync(t.Context()); err != nil || files.Revision() != stable {
				t.Fatalf("repaired ABA alias advanced source again: %v", err)
			}
			files.mu.Lock()
			_, watchesNewTarget := files.dirs[outside]
			files.mu.Unlock()
			if !watchesNewTarget {
				t.Fatal("read barrier did not rearm the new target directory")
			}
			notifications.Store(true)
			doc := filepath.Join(outside, "SKILL.md")
			info, err := os.Stat(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(doc, []byte("after!"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(doc, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for files.Revision() == stable && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if files.Revision() == stable {
				t.Fatal("same-stamp content edit on the new target was not observed")
			}
		})
	}
}

func TestScopedAliasReadBarrierCancellationAndErrors(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "selected")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files := NewScopedFiles(func(string) bool { return false })
	t.Cleanup(files.Close)
	files.aliases = make(map[string]watchAlias)
	files.Add(root)
	files.Add(link)
	if err := files.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := files.Revision()
	replaceWatchAlias(t, link, second)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := files.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read barrier: %v", err)
	}
	if files.Revision() != before || !sameWatchPath(files.aliases[link].state.target, first) {
		t.Fatal("canceled read barrier consumed the pending alias change")
	}
	if err := files.Sync(t.Context()); err != nil || files.Revision() == before {
		t.Fatalf("live read barrier lost canceled change: %v", err)
	}
	last := files.aliases[link].state
	replaceWatchAlias(t, link, link)
	if err := files.Sync(t.Context()); err == nil || os.IsNotExist(err) {
		t.Fatalf("loop was treated as an unchanged or missing target: %v", err)
	}
	if files.Healthy() || files.aliases[link].state != last {
		t.Fatal("unknown target failure overwrote the last proved alias mapping")
	}
}

func replaceWatchAlias(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestScopedAliasReadBarrierCancelsWhileWatchOperationIsActive(t *testing.T) {
	files := NewScopedFiles(nil)
	t.Cleanup(files.Close)
	files.aliases = make(map[string]watchAlias)
	files.opMu.Lock()
	locked := true
	defer func() {
		if locked {
			files.opMu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- files.Sync(ctx) }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked read barrier returned %v", err)
		}
	case <-time.After(time.Second):
		files.opMu.Unlock()
		locked = false
		<-result
		t.Fatal("cancellation waited for an unrelated watch operation to release the lock")
	}
	if files.Revision() != "0" {
		t.Fatalf("canceled lock acquisition changed source: %s", files.Revision())
	}
}
