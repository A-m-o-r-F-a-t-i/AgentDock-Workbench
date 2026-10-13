package taskstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/filelock"
)

type Store struct {
	management map[string]taskManagement
	root       string
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("task state root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve task state root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create task state root: %w", err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("secure task state root: %w", err)
	}
	return &Store{root: abs}, nil
}

func (s *Store) acquireStoreLock() (func(), error) {
	return s.acquireStoreLockContext(context.Background())
}

// filelock.Acquire provides the single in-process and cross-process gate. Passing
// the caller context keeps queue admission cancellable without a second mutex.
func (s *Store) acquireStoreLockContext(parent context.Context) (func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	releaseFileLock, err := filelock.Acquire(ctx, filepath.Join(s.root, ".store.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock task state: %w", err)
	}
	if err := s.recoverThreadTransactionLocked(); err != nil {
		releaseFileLock()
		return nil, err
	}
	if err := s.loadManagementLocked(); err != nil {
		releaseFileLock()
		return nil, err
	}
	if err := s.recoverManagementDeleteLocked(); err != nil {
		releaseFileLock()
		return nil, err
	}
	return releaseFileLock, nil
}

func (s *Store) Root() string { return s.root }
