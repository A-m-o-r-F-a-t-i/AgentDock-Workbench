package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDirectoryRejectsFileAndFileParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(file, "child")} {
		for _, privateState := range []bool{true, false} {
			if err := prepareDirectory(path, privateState); err == nil {
				t.Fatalf("file accepted as directory: %s", path)
			}
		}
	}
}
