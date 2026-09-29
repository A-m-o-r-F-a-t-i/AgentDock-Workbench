package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/uvwt/agentdock/internal/fs/securepath"
)

// prepareDirectory owns private state, but not an existing user workspace.
// Mkdir identifies creation atomically; a competing creator does not grant us
// ownership of an existing workspace's permissions.
func prepareDirectory(path string, privateState bool) error {
	created := false
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		err = os.Mkdir(path, 0o700)
		created = err == nil
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Stat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	if privateState || created {
		return securepath.EnsurePrivate(path)
	}
	return nil
}
