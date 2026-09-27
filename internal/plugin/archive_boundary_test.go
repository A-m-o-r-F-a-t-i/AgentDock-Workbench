package plugin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func securityZIP(t *testing.T, name string, declared uint64) string {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "fixture.zip"))
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	if declared != 0 {
		_, err = writer.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, UncompressedSize64: declared})
	} else {
		var out interface{ Write([]byte) (int, error) }
		out, err = writer.Create(name)
		if err == nil {
			_, err = out.Write([]byte("fixture"))
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
func TestSecurityZIPRejectsUnsignedSizeOverflow(t *testing.T) {
	archive := securityZIP(t, "large.txt", uint64(1)<<63)
	for name, extract := range map[string]func(string, string) error{"legacy": extractZip, "source": extractPluginZip} {
		t.Run(name, func(t *testing.T) {
			if err := extract(archive, t.TempDir()); err == nil {
				t.Fatal("accepted an uncompressed size that overflows int64")
			}
		})
	}
}
func TestSecurityZIPCannotFollowPreexistingExternalDirectory(t *testing.T) {
	archive := securityZIP(t, "linked/file.txt", 0)
	for name, extract := range map[string]func(string, string) error{"legacy": extractZip, "source": extractPluginZip} {
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
				t.Skipf("symlink fixture unavailable: %v", err)
			}
			err := extract(archive, root)
			if err == nil {
				t.Error("extraction accepted an external directory link")
			}
			if _, statErr := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(statErr) {
				t.Error("extraction wrote outside its destination")
			}
		})
	}
}
