package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	pluginReaderPrefix      = "reader-"
	pluginReaderStaleAfter  = 25 * time.Hour
	pluginHeartbeatInterval = time.Minute
)

func generationLeaseKey(name, generation string) (string, error) {
	if err := validateIdentifier("plugin", name); err != nil {
		return "", err
	}
	if err := validateGeneration(generation); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("plugin-generation\x00" + name + "\x00" + generation))
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) AcquireGenerationRead(ctx context.Context, name, generation string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := generationLeaseKey(name, generation)
	if err != nil {
		return nil, err
	}
	generationPath, err := s.generationPath(name, generation)
	if err != nil {
		return nil, err
	}
	if _, exists, err := packageDigestAt(generationPath); err != nil {
		return nil, err
	} else if !exists {
		return nil, newError("PLUGIN_GENERATION_MISSING", "Plugin generation is unavailable", map[string]any{"plugin": name, "generation": generation}, nil)
	}

	readers := filepath.Join(s.root, ".locks", "generations", key, "readers")
	if err := os.MkdirAll(readers, 0o700); err != nil {
		return nil, err
	}
	cleanupStaleGenerationReaders(readers)
	owner, err := newLifecycleOwner()
	if err != nil {
		return nil, err
	}
	readerPath := filepath.Join(readers, pluginReaderPrefix+owner)
	file, err := os.OpenFile(readerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create Plugin generation reader: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(readerPath)
		return nil, err
	}
	stop := maintainGenerationHeartbeat(readerPath)
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			if err := os.Remove(readerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("release Plugin generation reader failed", "plugin", name, "generation", generation, "error", err)
			}
			_ = s.cleanupObsoleteGeneration(name, generation)
		})
	}, nil
}

func maintainGenerationHeartbeat(path string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(pluginHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				if err := os.Chtimes(path, now, now); err != nil {
					return
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}

func cleanupStaleGenerationReaders(readers string) {
	entries, err := os.ReadDir(readers)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), pluginReaderPrefix) {
			continue
		}
		info, err := entry.Info()
		if err == nil && now.Sub(info.ModTime()) > pluginReaderStaleAfter {
			_ = os.Remove(filepath.Join(readers, entry.Name()))
		}
	}
}

func (s *Store) generationReadersEmpty(name, generation string) (bool, error) {
	key, err := generationLeaseKey(name, generation)
	if err != nil {
		return false, err
	}
	readers := filepath.Join(s.root, ".locks", "generations", key, "readers")
	if err := os.MkdirAll(readers, 0o700); err != nil {
		return false, err
	}
	cleanupStaleGenerationReaders(readers)
	entries, err := os.ReadDir(readers)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), pluginReaderPrefix) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) cleanupObsoleteGeneration(name, generation string) error {
	if generation == "" {
		return nil
	}
	state, err := s.readNamedState(name)
	if err == nil && state.Generation == generation {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	transaction, txErr := s.loadLifecycleTransaction(name)
	if txErr == nil {
		if transaction.CandidateGeneration == generation || transaction.PreviousGeneration == generation {
			return nil
		}
	} else if !errors.Is(txErr, os.ErrNotExist) {
		return txErr
	}
	empty, err := s.generationReadersEmpty(name, generation)
	if err != nil || !empty {
		return err
	}
	path, err := s.generationPath(name, generation)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}
