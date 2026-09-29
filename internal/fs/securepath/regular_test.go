package securepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityRegularReadBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadRegular(root, "ok.txt", 4); err != nil || string(data) != "1234" {
		t.Fatalf("bounded valid read: %q %v", data, err)
	}
	for _, name := range []string{"../ok.txt", filepath.Join(root, "ok.txt"), ".", "missing"} {
		if _, err := ReadRegular(root, name, 10); err == nil {
			t.Errorf("unsafe/nonregular name accepted: %s", name)
		}
	}
	if _, err := ReadRegular(root, "ok.txt", 3); err == nil {
		t.Error("accepted file exceeding bound")
	}
	if err := os.Symlink("ok.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := ReadRegular(root, "alias.txt", 10); err == nil {
		t.Error("accepted linked file")
	}
}
