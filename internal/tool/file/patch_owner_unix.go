//go:build !windows

package file

import (
	"fmt"
	"os"
	"syscall"
)

func preserveForwardPatchOwner(temporary string, file stagedPatchFile) error {
	source := file.MoveFrom
	if file.OriginalExists {
		source = file.Abs
	}
	if source == "" {
		return nil
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine patch target ownership: %s", file.Display)
	}
	if err := os.Chown(temporary, int(owner.Uid), int(owner.Gid)); err != nil {
		return fmt.Errorf("preserve patch target ownership: %w", err)
	}
	// chown may clear special mode bits; preserve the existing supported mode.
	return os.Chmod(temporary, patchTargetMode(file))
}
