//go:build windows

package plugin

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPromoteGenerationRetriesWindowsSharingViolation(t *testing.T) {
	source := writePluginPackage(t, t.TempDir(), "promotion-windows", "1.0.0", []string{"demo-skill"}, nil)
	expectedDigest, exists, err := packageDigestAt(source)
	if err != nil || !exists {
		t.Fatalf("source digest = %q exists=%v err=%v", expectedDigest, exists, err)
	}
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	destination := filepath.Join(root, "destination")
	if err := copyPackageTree(source, candidate); err != nil {
		t.Fatal(err)
	}

	name, err := windows.UTF16PtrFromString(candidate)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		t.Fatalf("open candidate without delete sharing: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		time.Sleep(600 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(closed)
	}()

	started := time.Now()
	err = promoteGeneration(candidate, destination, expectedDigest, "promotion-windows", promotionTestGeneration)
	<-closed
	if err != nil {
		t.Fatalf("transient sharing violation was not retried: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 400*time.Millisecond {
		t.Fatalf("promotion completed before the blocking handle was released: %s", elapsed)
	}
	digest, exists, err := packageDigestAt(destination)
	if err != nil || !exists || digest != expectedDigest {
		t.Fatalf("promoted digest = %q exists=%v err=%v", digest, exists, err)
	}
}
