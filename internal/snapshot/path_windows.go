//go:build windows

package snapshot

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// watchPathTarget resolves an existing watch location through the handle that
// Windows actually opens. Go's default EvalSymlinks does not follow directory
// junctions, even though ordinary filesystem reads through them succeed.
// Missing-ancestor selection belongs to Files, not this platform boundary.
func watchPathTarget(path string) (string, error) {
	name, err := watchLongPath(path)
	if err != nil {
		return "", &os.PathError{Op: "resolve watch target", Path: path, Err: err}
	}
	pointer, err := watchPathPointer(name)
	if err != nil {
		return "", &os.PathError{Op: "resolve watch target", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "resolve watch target", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 256)
	for {
		// Zero requests the normalized DOS name, rather than the spelling
		// originally used to open this handle.
		count, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", &os.PathError{Op: "resolve watch target", Path: path, Err: err}
		}
		if count == 0 {
			return "", &os.PathError{Op: "resolve watch target", Path: path, Err: os.ErrInvalid}
		}
		if count < uint32(len(buffer)) {
			target, err := normalizedWatchTarget(windows.UTF16ToString(buffer[:count]))
			if err != nil {
				return "", &os.PathError{Op: "resolve watch target", Path: path, Err: err}
			}
			return target, nil
		}
		if count >= windows.MAX_LONG_PATH || len(buffer) == windows.MAX_LONG_PATH {
			return "", &os.PathError{Op: "resolve watch target", Path: path, Err: windows.ERROR_FILENAME_EXCED_RANGE}
		}
		// Doubling bounds the number of calls even if a concurrent rename
		// repeatedly lengthens the path between API calls.
		size := max(len(buffer)*2, int(count)+1)
		size = min(size, windows.MAX_LONG_PATH)
		buffer = make([]uint16, size)
	}
}

func isWatchAlias(path string, info os.FileInfo) (bool, error) {
	if info == nil {
		return false, os.ErrInvalid
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	if info.Mode()&os.ModeIrregular == 0 {
		return false, nil
	}
	name, err := watchLongPath(path)
	if err != nil {
		return false, &os.PathError{Op: "inspect watch alias", Path: path, Err: err}
	}
	pointer, err := watchPathPointer(name)
	if err != nil {
		return false, &os.PathError{Op: "inspect watch alias", Path: path, Err: err}
	}
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(pointer, &data)
	if err != nil {
		return false, &os.PathError{Op: "inspect watch alias", Path: path, Err: err}
	}
	defer windows.FindClose(handle)
	// Other reparse types, such as cloud placeholders, are not aliases merely
	// because Lstat marks them irregular. Inspect the actual directory tag.
	return data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 &&
		data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 &&
		data.Reserved0 == windows.IO_REPARSE_TAG_MOUNT_POINT, nil
}

func watchPathPointer(path string) (*uint16, error) {
	units, err := windows.UTF16FromString(path)
	if err != nil {
		return nil, err
	}
	if len(units) > windows.MAX_LONG_PATH {
		return nil, windows.ERROR_FILENAME_EXCED_RANGE
	}
	return &units[0], nil
}

// watchLongPath adds only the extended spelling of an ordinary absolute DOS or
// UNC path. It does not admit device namespaces or resolve relative paths.
func watchLongPath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", os.ErrInvalid
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", os.ErrInvalid
	}
	if len(path) >= 3 && path[1] == ':' && path[2] == '\\' &&
		(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') {
		return "\\\\?\\" + path, nil
	}
	if strings.HasPrefix(path, "\\\\") {
		parts := strings.Split(strings.TrimPrefix(filepath.VolumeName(path), "\\\\"), "\\")
		if len(parts) == 2 && parts[0] != "" && parts[0] != "." && parts[0] != "?" &&
			parts[1] != "" && parts[1] != "." && parts[1] != ".." {
			return "\\\\?\\UNC\\" + path[2:], nil
		}
	}
	return "", os.ErrInvalid
}

func normalizedWatchTarget(target string) (string, error) {
	if !strings.HasPrefix(target, "\\\\?\\") {
		return "", os.ErrInvalid
	}
	native := target[4:]
	if len(native) >= 4 && strings.EqualFold(native[:4], "UNC\\") {
		native = "\\\\" + native[4:]
	}
	// Revalidate the complete native representation. In particular, stripping
	// the prefix must not turn a Volume{GUID} or GLOBALROOT path into a relative
	// path that could accidentally appear covered by another watch.
	if _, err := watchLongPath(native); err != nil {
		return "", err
	}
	return filepath.Clean(native), nil
}
