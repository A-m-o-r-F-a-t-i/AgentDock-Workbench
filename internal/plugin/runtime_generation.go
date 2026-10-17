package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/uvwt/agentdock/internal/fs/securepath"
)

type runtimeGenerationKey struct {
	name       string
	generation string
}

type runtimeGeneration struct {
	root    string
	readers int
}

func (s *Store) CreateRuntimeGeneration(name, generation, source string) (string, func(), error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", nil, err
	}
	if err := validateGeneration(generation); err != nil {
		return "", nil, err
	}
	expected, err := s.generationPath(name, generation)
	if err != nil {
		return "", nil, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", nil, err
	}
	if !sameFilesystemPath(source, expected) {
		return "", nil, errors.New("Plugin runtime source does not match the selected immutable generation")
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	key := runtimeGenerationKey{name: name, generation: generation}
	if current := s.runtimeCopies[key]; current != nil {
		current.readers++
		return current.root, s.runtimeGenerationRelease(key, current), nil
	}
	owner, err := newLifecycleOwner()
	if err != nil {
		return "", nil, err
	}
	root := filepath.Join(s.root, ".runtime", name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	target := filepath.Join(root, "generation-"+generation+"-"+owner)
	if err := copyPackageTree(source, target); err != nil {
		_ = os.RemoveAll(target)
		return "", nil, fmt.Errorf("copy Plugin runtime generation: %w", err)
	}
	current := &runtimeGeneration{root: target, readers: 1}
	if s.runtimeCopies == nil {
		s.runtimeCopies = make(map[runtimeGenerationKey]*runtimeGeneration)
	}
	s.runtimeCopies[key] = current
	return target, s.runtimeGenerationRelease(key, current), nil
}

func (s *Store) runtimeGenerationRelease(key runtimeGenerationKey, current *runtimeGeneration) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.runtimeMu.Lock()
			defer s.runtimeMu.Unlock()
			current.readers--
			if current.readers == 0 {
				delete(s.runtimeCopies, key)
				_ = os.RemoveAll(current.root)
			}
		})
	}
}

func (s *Store) ownsRuntimeGeneration(name, generation, root string) bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	current := s.runtimeCopies[runtimeGenerationKey{name: name, generation: generation}]
	return current != nil && current.readers > 0 && sameFilesystemPath(root, current.root)
}

// Runtime-generated files do not change the installed generation. Files shipped
// in the reviewed package must still be identical before a process is started or
// refreshed; additional caches may exist only in the leased runtime directory.
func validateRuntimePackageFiles(ctx context.Context, source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".agentdock-install.json" {
			return nil
		}
		if err := compareRuntimePackageFile(ctx, source, target, relative); err != nil {
			return fmt.Errorf("Plugin runtime file %q no longer matches its generation: %w", relative, err)
		}
		return nil
	})
}

func compareRuntimePackageFile(ctx context.Context, source, target, relative string) error {
	original, originalInfo, err := securepath.OpenRegular(source, relative)
	if err != nil {
		return err
	}
	defer original.Close()
	runtimeFile, runtimeInfo, err := securepath.OpenRegular(target, relative)
	if err != nil {
		return err
	}
	defer runtimeFile.Close()
	if originalInfo.Size() != runtimeInfo.Size() || originalInfo.Mode().Perm()&0o755 != runtimeInfo.Mode().Perm()&0o755 {
		return errors.New("file size or permissions changed")
	}
	originalBuffer, runtimeBuffer := make([]byte, 32<<10), make([]byte, 32<<10)
	for remaining := originalInfo.Size(); remaining > 0; {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(remaining, int64(len(originalBuffer)))
		if _, err := io.ReadFull(original, originalBuffer[:size]); err != nil {
			return err
		}
		if _, err := io.ReadFull(runtimeFile, runtimeBuffer[:size]); err != nil {
			return err
		}
		if !bytes.Equal(originalBuffer[:size], runtimeBuffer[:size]) {
			return errors.New("file contents changed")
		}
		remaining -= size
	}
	return nil
}
