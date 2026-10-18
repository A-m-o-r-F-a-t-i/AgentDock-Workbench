package plugin

import (
	"context"
	"testing"
	"time"
)

func TestAcquireRuntimePackageDoesNotWaitForGlobalStoreMutationLock(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	packageRoot := writePluginPackage(t, t.TempDir(), "read-lock-demo", "1.0.0", []string{"read-lock-skill"}, nil)
	if _, err := store.Install(packageRoot, false); err != nil {
		t.Fatal(err)
	}
	releaseMutation, err := store.acquireContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	releases := make(chan func(), 1)
	go func() {
		_, release, err := store.AcquireRuntimePackage(ctx, "read-lock-demo")
		releases <- release
		done <- err
	}()
	select {
	case err := <-done:
		releaseMutation()
		if release := <-releases; release != nil {
			release()
		}
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		releaseMutation()
		t.Fatal("read-only runtime package acquisition waited for the global mutation lock")
	}
}
