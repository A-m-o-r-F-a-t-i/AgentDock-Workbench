package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityReaderLeaseCannotWriteOutsideStore(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(store.root, locksDirectory, "demo.readers")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	release, err := store.AcquireRead(context.Background(), "demo")
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("read lease accepted a readers directory outside its store")
	}
}
