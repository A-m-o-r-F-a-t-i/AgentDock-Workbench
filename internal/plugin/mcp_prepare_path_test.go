package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSameFilesystemPathResolvesPlatformAliases(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		if !sameFilesystemPath(strings.ToUpper(root), strings.ToLower(root)) {
			t.Fatalf("case-insensitive aliases were treated as different: %q", root)
		}
		return
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if !sameFilesystemPath(filepath.Join(alias, "missing"), filepath.Join(root, "missing")) {
		t.Fatalf("filesystem-resolved aliases were treated as different: %q and %q", alias, root)
	}
}
