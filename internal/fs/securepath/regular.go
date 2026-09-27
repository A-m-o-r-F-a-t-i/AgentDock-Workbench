package securepath

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrReadLimit allows callers to retain their domain-specific size-limit errors.
var ErrReadLimit = errors.New("regular-file read limit exceeded")

// OpenRegular pins access to root and refuses linked or non-regular entries.
// Path checks and opening use the same directory handle. The opened file must
// still have the identity inspected before opening; replacement is not accepted.
func OpenRegular(root, relative string) (*os.File, os.FileInfo, error) {
	if !filepath.IsLocal(relative) || filepath.Clean(relative) == "." || strings.ContainsRune(relative, 0) {
		return nil, nil, errors.New("expected a local regular-file path")
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer handle.Close()
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	current := ""
	var before os.FileInfo
	for index, part := range parts {
		current = filepath.Join(current, part)
		before, err = handle.Lstat(current)
		if err != nil {
			return nil, nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("symbolic links are not allowed in regular-file paths")
		}
		if index < len(parts)-1 && !before.IsDir() {
			return nil, nil, errors.New("regular-file path has a non-directory ancestor")
		}
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errors.New("expected a regular file")
	}
	file, err := handle.OpenFile(relative, regularReadFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("regular file changed while opening")
	}
	return file, after, nil
}

// ReadRegular reads at most maximum bytes from a root-confined regular file.
func ReadRegular(root, relative string, maximum int64) ([]byte, error) {
	if maximum < 1 || maximum > 1<<30 {
		return nil, errors.New("invalid regular-file read limit")
	}
	file, info, err := OpenRegular(root, relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > maximum {
		return nil, fmt.Errorf("%w: %d bytes", ErrReadLimit, maximum)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("%w: %d bytes", ErrReadLimit, maximum)
	}
	return data, nil
}
