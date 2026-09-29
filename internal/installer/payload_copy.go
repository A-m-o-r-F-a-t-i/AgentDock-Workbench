package installer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// copyTree copies installer payloads with their requested default mode. Backups
// retain their separate integrity- and native-metadata-preserving copier.
func copyTree(src, dst string, mode os.FileMode) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("payload source is not a regular file or directory: %s", src)
	}
	target, err := os.Lstat(dst)
	if err == nil {
		if !target.IsDir() && !target.Mode().IsRegular() {
			return fmt.Errorf("payload destination is not a regular file or directory: %s", dst)
		}
		// Do not truncate the source through an in-place or hard-link alias.
		if os.SameFile(info, target) {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info.IsDir() {
		source, err := filepath.Abs(src)
		if err != nil {
			return err
		}
		destination, err := filepath.Abs(dst)
		if err != nil {
			return err
		}
		if payloadPathContains(source, destination) || payloadPathContains(destination, source) {
			return errors.New("payload source and destination directories overlap")
		}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyTree(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), mode); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyPayloadFile(src, dst, info, mode)
}

func payloadPathContains(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// Copy only the observed file length instead of allocating a slice as large as
// the self-contained desktop executable. io.CopyN retains platform file-copy
// optimizations and a bounded-buffer fallback. A failed copy is handled by the
// caller's unpublished staging directory or rollback journal.
func copyPayloadFile(src, dst string, before os.FileInfo, mode os.FileMode) (returnErr error) {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, input.Close()) }()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return fmt.Errorf("payload source changed before copying: %s", src)
	}
	if mode == 0 {
		mode = before.Mode()
	}
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, output.Close()) }()
	if _, err := io.CopyN(output, input, before.Size()); err != nil {
		return fmt.Errorf("copy payload %s: %w", src, err)
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("payload source changed during copying: %s", src)
	}
	return nil
}
