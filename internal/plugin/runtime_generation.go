package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/fs/processlock"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

const runtimeGenerationPreparationTimeout = 2 * time.Minute

type runtimeGenerationKey struct {
	name       string
	generation string
}

type runtimeGeneration struct {
	root          string
	packageDigest string
	readers       int
	lock          *processlock.Lock
	lockOnce      sync.Once
}

func (current *runtimeGeneration) releaseLock() {
	if current == nil {
		return
	}
	current.lockOnce.Do(func() {
		if current.lock != nil {
			_ = current.lock.Release()
		}
	})
}

func (s *Store) CreateRuntimeGeneration(name, generation, source string) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeGenerationPreparationTimeout)
	defer cancel()
	return s.createRuntimeGeneration(ctx, name, generation, source)
}

func (s *Store) createRuntimeGeneration(ctx context.Context, name, generation, source string) (string, func(), error) {
	return s.createVerifiedRuntimeGeneration(ctx, name, generation, source, "")
}

func (s *Store) createVerifiedRuntimeGeneration(ctx context.Context, name, generation, source, expectedDigest string) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
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
		if expectedDigest != "" && current.packageDigest != expectedDigest {
			if current.packageDigest != "" {
				return "", nil, newError("PLUGIN_GENERATION_DRIFT", "Plugin runtime cache belongs to different package content", map[string]any{"plugin": name, "generation": generation}, nil)
			}
			if err := verifyRuntimeSourceDigest(source, expectedDigest, name, generation); err != nil {
				return "", nil, err
			}
			current.packageDigest = expectedDigest
		}
		current.readers++
		return current.root, s.runtimeGenerationRelease(key, current), nil
	}
	target, lockPath, err := s.runtimeGenerationPaths(name, generation)
	if err != nil {
		return "", nil, err
	}
	runtimeLock, err := processlock.Acquire(ctx, lockPath)
	if err != nil {
		return "", nil, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = runtimeLock.Release()
		}
	}()
	if err := verifyRuntimeSourceDigest(source, expectedDigest, name, generation); err != nil {
		return "", nil, err
	}
	if err := s.ensureRuntimeGeneration(ctx, name, generation, source, target); err != nil {
		return "", nil, err
	}
	current := &runtimeGeneration{root: target, packageDigest: expectedDigest, readers: 1, lock: runtimeLock}
	if s.runtimeCopies == nil {
		s.runtimeCopies = make(map[runtimeGenerationKey]*runtimeGeneration)
	}
	s.runtimeCopies[key] = current
	retained = true
	return target, s.runtimeGenerationRelease(key, current), nil
}

func verifyRuntimeSourceDigest(source, expectedDigest, name, generation string) error {
	if expectedDigest == "" {
		return nil
	}
	digest, exists, err := packageDigestAt(source)
	if err != nil {
		return err
	}
	if !exists || digest != expectedDigest {
		return newError("PLUGIN_GENERATION_DRIFT", "Plugin generation no longer matches its persisted package digest", map[string]any{"plugin": name, "generation": generation}, nil)
	}
	return nil
}

func (s *Store) runtimeGenerationPaths(name, generation string) (string, string, error) {
	key, err := generationLeaseKey(name, generation)
	if err != nil {
		return "", "", err
	}
	root, err := containedPath(s.root, filepath.Join(s.root, ".runtime", name), true)
	if err != nil {
		return "", "", err
	}
	target, err := containedPath(s.root, filepath.Join(root, "generation-"+generation), true)
	if err != nil {
		return "", "", err
	}
	lockPath, err := containedPath(s.root, filepath.Join(s.root, ".locks", "generations", key, "runtime.lock"), true)
	if err != nil {
		return "", "", err
	}
	return target, lockPath, nil
}

func (s *Store) ensureRuntimeGeneration(ctx context.Context, name, generation, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err := validateRuntimePackageFiles(ctx, source, target); err == nil {
				return nil
			}
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	migrated, err := s.migrateLegacyRuntimeGeneration(ctx, name, generation, source, target)
	if err != nil || migrated {
		return err
	}
	root := filepath.Dir(target)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	if err := copyPackageTree(source, staged); err != nil {
		return fmt.Errorf("copy Plugin runtime generation: %w", err)
	}
	if err := validateRuntimePackageFiles(ctx, source, staged); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrateLegacyRuntimeGeneration(ctx context.Context, name, generation, source, target string) (bool, error) {
	guard, err := s.generationLeaseGuard(ctx, name, generation)
	if err != nil {
		return false, err
	}
	defer guard()
	readers, err := s.generationReaderCountUnlocked(name, generation)
	if err != nil || readers != 1 {
		return false, err
	}
	root := filepath.Dir(target)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	prefix := "generation-" + generation + "-"
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if err := validateRuntimePackageFiles(ctx, source, candidate); err != nil {
			continue
		}
		if err := os.Rename(candidate, target); err != nil {
			continue
		}
		return true, nil
	}
	return false, nil
}

func (s *Store) runtimeGenerationRelease(key runtimeGenerationKey, current *runtimeGeneration) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.runtimeMu.Lock()
			if current.readers > 0 {
				current.readers--
			}
			if current.readers > 0 || s.runtimeGenerationRetained(key) {
				s.runtimeMu.Unlock()
				return
			}
			delete(s.runtimeCopies, key)
			_ = os.RemoveAll(current.root)
			current.releaseLock()
			s.runtimeMu.Unlock()
		})
	}
}

func (s *Store) runtimeGenerationRetained(key runtimeGenerationKey) bool {
	state, err := s.readNamedState(key.name)
	if err != nil || !state.Enabled || state.Generation != key.generation {
		return false
	}
	// Current host state is normalized with one entry per member. Empty maps are
	// retained for legacy state whose member inventory predates these switches;
	// a later acquisition will validate or rebuild the cache before use.
	if len(state.Skills) == 0 && len(state.MCPServers) == 0 {
		return true
	}
	for _, enabled := range state.Skills {
		if enabled {
			return true
		}
	}
	for _, enabled := range state.MCPServers {
		if enabled {
			return true
		}
	}
	return false
}

func (s *Store) closeRuntimeGenerations() {
	if s == nil {
		return
	}
	s.runtimeMu.Lock()
	copies := s.runtimeCopies
	s.runtimeCopies = nil
	s.runtimeMu.Unlock()
	for key, current := range copies {
		if !s.runtimeGenerationRetained(key) {
			_ = os.RemoveAll(current.root)
		}
		current.releaseLock()
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
