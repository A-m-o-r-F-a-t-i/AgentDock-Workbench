//go:build !windows

package snapshot

import (
	"os"
	"path/filepath"
)

func watchPathTarget(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func isWatchAlias(_ string, info os.FileInfo) (bool, error) {
	if info == nil {
		return false, os.ErrInvalid
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}
