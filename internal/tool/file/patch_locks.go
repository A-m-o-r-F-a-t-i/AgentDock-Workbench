package file

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/uvwt/agentdock/internal/fs/filelock"
)

func patchPathIdentity(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// Reuse the existing cross-process lock implementation, including its stale
// owner recovery. Locks live in private managed cache, never in the workspace.
func acquirePatchPaths(ctx context.Context, paths []string) (func(), error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "agentdock", "file-locks")
	keys := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		key := patchPathIdentity(path)
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	releases := []func(){}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for index := len(releases) - 1; index >= 0; index-- {
				releases[index]()
			}
		})
	}
	for _, key := range keys {
		hash := sha256.Sum256([]byte(key))
		unlock, err := filelock.Acquire(ctx, filepath.Join(root, fmt.Sprintf("%x.lock", hash)))
		if err != nil {
			release()
			return nil, err
		}
		releases = append(releases, unlock)
	}
	return release, nil
}
