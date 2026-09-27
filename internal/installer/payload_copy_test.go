package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyTreeCopiesContentsAndModes(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(source, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	writePayloadFixture(t, filepath.Join(source, "nested", "file"), "replacement")
	writePayloadFixture(t, filepath.Join(target, "nested", "file"), "long obsolete contents")
	if err := copyTree(source, target, 0644); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, filepath.Join(target, "nested", "file"), "replacement")
	if info, err := os.Stat(filepath.Join(target, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(source, "nested", "file"), 0750); err != nil {
			t.Fatal(err)
		}
		preserved := filepath.Join(root, "preserved")
		if err := copyTree(filepath.Join(source, "nested", "file"), preserved, 0); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(preserved); err != nil || info.Mode().Perm() != 0750 {
			t.Fatalf("source mode not preserved: %v", err)
		}
	}
}

func TestCopyTreeDoesNotTruncateSameFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writePayloadFixture(t, source, "must survive")
	if err := copyTree(source, source, 0755); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, source, "must survive")
	target := filepath.Join(root, "alias")
	if err := os.Link(source, target); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := copyTree(source, target, 0755); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, source, "must survive")
}

func TestCopyTreeRejectsLinks(t *testing.T) {
	for _, destinationLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "destination"}[destinationLink], func(t *testing.T) {
			root := t.TempDir()
			source, target, outside := filepath.Join(root, "source"), filepath.Join(root, "target"), filepath.Join(root, "outside")
			writePayloadFixture(t, outside, "outside untouched")
			link := source
			if destinationLink {
				writePayloadFixture(t, source, "payload")
				link = target
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := copyTree(source, target, 0644); err == nil {
				t.Fatal("payload copy followed a symbolic link")
			}
			requirePayloadFixture(t, outside, "outside untouched")
		})
	}
}

func TestCopyTreeRejectsOverlappingDirectories(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		root := t.TempDir()
		parent, child := filepath.Join(root, "tree"), filepath.Join(root, "tree", "child")
		writePayloadFixture(t, filepath.Join(child, "file"), "payload")
		source, target := parent, child
		if reverse {
			source, target = child, parent
		}
		if err := copyTree(source, target, 0644); err == nil {
			t.Fatal("overlapping directories accepted")
		}
		requirePayloadFixture(t, filepath.Join(child, "file"), "payload")
	}
}

// Both cases copy the same 16 MiB regular file. The old whole-file path is a
// benchmark control only; it is not an alternative production implementation.
func BenchmarkInstallerPayloadCopy(b *testing.B) {
	root := b.TempDir()
	source := filepath.Join(root, "source")
	data := bytes.Repeat([]byte("installer-payload"), (16<<20)/len("installer-payload")+1)[:16<<20]
	if err := os.WriteFile(source, data, 0644); err != nil {
		b.Fatal(err)
	}
	for _, name := range []string{"previous-read-all", "streamed"} {
		b.Run(name, func(b *testing.B) {
			target := filepath.Join(root, name)
			b.SetBytes(16 << 20)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				if name == "previous-read-all" {
					var data []byte
					data, err = os.ReadFile(source)
					if err == nil {
						err = os.WriteFile(target, data, 0644)
					}
				} else {
					err = copyTree(source, target, 0644)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
