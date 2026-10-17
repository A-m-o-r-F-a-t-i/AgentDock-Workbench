package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/filelock"
)

const (
	lifecyclePhasePrepared        = "prepared"
	lifecyclePhaseCandidateStaged = "candidate_staged"
	lifecyclePhaseVisibleSwitched = "visible_switched"
	lifecyclePhaseStateCommitted  = "state_committed"
)

type LifecycleTransaction struct {
	SchemaVersion         int       `json:"schema_version"`
	OwnerID               string    `json:"owner_id"`
	Name                  string    `json:"name"`
	Operation             string    `json:"operation"`
	Phase                 string    `json:"phase"`
	Previous              *State    `json:"previous,omitempty"`
	Candidate             *State    `json:"candidate,omitempty"`
	PreviousStateDigest   string    `json:"previous_state_digest"`
	CandidateStateDigest  string    `json:"candidate_state_digest"`
	PreviousVisibleDigest string    `json:"previous_visible_digest,omitempty"`
	CandidateDigest       string    `json:"candidate_digest,omitempty"`
	PreviousGeneration    string    `json:"previous_generation,omitempty"`
	CandidateGeneration   string    `json:"candidate_generation,omitempty"`
	WorkPath              string    `json:"work_path"`
	CreatedAt             time.Time `json:"created_at"`
}

type ActivationCandidate struct {
	OwnerID       string
	Name          string
	Operation     string
	Generation    string
	PackageDigest string
	Definition    Definition
}

// The lifecycle lock spans staging, MCP reconciliation and publication. The
// shorter store lock must be released during reconciliation because acquiring
// runtime packages takes it again. filelock retains a live-process owner and
// heartbeat so another Core cannot recover an operation that is still running.
type lifecycleClaim struct {
	name    string
	owner   string
	release func()
}

func (s *Store) acquireLifecycle(ctx context.Context) (func(), error) {
	s.lifecycleMu.Lock()
	claim := s.lifecycleClaim
	s.lifecycleMu.Unlock()
	if claim != nil {
		return nil, newError("PLUGIN_ACTIVATION_IN_PROGRESS", "Plugin lifecycle transaction is already in progress", map[string]any{"plugin": claim.name}, nil)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	release, err := filelock.Acquire(lockCtx, filepath.Join(s.root, ".locks", "lifecycle.lock"))
	if err != nil {
		return nil, newError("PLUGIN_STORE_LOCK_FAILED", "lock Plugin lifecycle", nil, err)
	}
	return release, nil
}

func (s *Store) retainLifecycleClaim(name, owner string, release func()) {
	s.lifecycleMu.Lock()
	s.lifecycleClaim = &lifecycleClaim{name: name, owner: owner, release: release}
	s.lifecycleMu.Unlock()
}

func (s *Store) ownsLifecycleClaim(name, owner string) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	claim := s.lifecycleClaim
	return claim != nil && claim.name == name && claim.owner == owner
}

func (s *Store) releaseLifecycleClaim(name, owner string) {
	s.lifecycleMu.Lock()
	claim := s.lifecycleClaim
	if claim == nil || claim.name != name || claim.owner != owner {
		s.lifecycleMu.Unlock()
		return
	}
	s.lifecycleClaim = nil
	s.lifecycleMu.Unlock()
	claim.release()
}

func (s *Store) closeLifecycleClaim() {
	s.lifecycleMu.Lock()
	claim := s.lifecycleClaim
	s.lifecycleClaim = nil
	s.lifecycleMu.Unlock()
	if claim != nil {
		claim.release()
	}
}

func (s *Store) saveLifecycleTransaction(transaction LifecycleTransaction) error {
	if err := validateLifecycleTransaction(transaction); err != nil {
		return err
	}
	path, err := s.transactionPath(transaction.Name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0o600)
}

func (s *Store) loadLifecycleTransaction(name string) (LifecycleTransaction, error) {
	path, err := s.transactionPath(name)
	if err != nil {
		return LifecycleTransaction{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LifecycleTransaction{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var transaction LifecycleTransaction
	if err := decoder.Decode(&transaction); err != nil {
		return LifecycleTransaction{}, fmt.Errorf("decode Plugin lifecycle transaction: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return LifecycleTransaction{}, errors.New("Plugin lifecycle transaction contains multiple JSON values")
		}
		return LifecycleTransaction{}, err
	}
	if err := validateLifecycleTransaction(transaction); err != nil {
		return LifecycleTransaction{}, err
	}
	if transaction.Name != name {
		return LifecycleTransaction{}, errors.New("Plugin lifecycle transaction name does not match its file")
	}
	return transaction, nil
}

func validateLifecycleTransaction(transaction LifecycleTransaction) error {
	if transaction.SchemaVersion != lifecycleTransactionSchemaVersion {
		return fmt.Errorf("unsupported Plugin lifecycle transaction schema %d", transaction.SchemaVersion)
	}
	if err := validateIdentifier("plugin", transaction.Name); err != nil {
		return err
	}
	if err := validateGeneration(transaction.OwnerID); err != nil {
		return err
	}
	switch transaction.Operation {
	case "install":
		if transaction.Previous != nil {
			return errors.New("Plugin install transaction cannot contain previous formal state")
		}
	case "update":
		if transaction.Previous == nil {
			return errors.New("Plugin update transaction requires previous formal state")
		}
	case "remove":
		if transaction.Previous == nil {
			return errors.New("Plugin removal transaction requires previous formal state")
		}
	default:
		return fmt.Errorf("unsupported Plugin lifecycle operation %q", transaction.Operation)
	}
	switch transaction.Phase {
	case lifecyclePhasePrepared, lifecyclePhaseCandidateStaged, lifecyclePhaseVisibleSwitched, lifecyclePhaseStateCommitted:
	default:
		return fmt.Errorf("unsupported Plugin lifecycle phase %q", transaction.Phase)
	}
	previousDigest, err := stateFingerprint(transaction.Previous)
	if err != nil || previousDigest != transaction.PreviousStateDigest {
		return errors.New("Plugin lifecycle previous state digest does not match its payload")
	}
	candidateDigest, err := stateFingerprint(transaction.Candidate)
	if err != nil || candidateDigest != transaction.CandidateStateDigest {
		return errors.New("Plugin lifecycle candidate state digest does not match its payload")
	}
	if transaction.Operation == "remove" {
		if transaction.Candidate != nil || transaction.CandidateDigest != "" || transaction.CandidateGeneration != "" {
			return errors.New("Plugin removal transaction cannot contain a candidate package")
		}
	} else {
		if transaction.Candidate == nil || transaction.CandidateDigest == "" {
			return errors.New("Plugin activation transaction requires a candidate package")
		}
		if err := validateGeneration(transaction.CandidateGeneration); err != nil {
			return err
		}
		if transaction.Candidate.Generation != transaction.CandidateGeneration || transaction.Candidate.PackageDigest != transaction.CandidateDigest {
			return errors.New("Plugin lifecycle candidate package identity does not match candidate state")
		}
	}
	if transaction.Previous != nil {
		if err := validateGeneration(transaction.PreviousGeneration); err != nil {
			return err
		}
		if transaction.Previous.Generation != transaction.PreviousGeneration || transaction.Previous.PackageDigest != transaction.PreviousVisibleDigest {
			return errors.New("Plugin lifecycle previous package identity does not match previous state")
		}
	} else if transaction.PreviousGeneration != "" || transaction.PreviousVisibleDigest != "" {
		return errors.New("Plugin lifecycle without previous state cannot contain previous package identity")
	}
	wantWorkPath := filepath.ToSlash(filepath.Join(".state", "transactions", "data", transaction.Name+"-"+transaction.OwnerID))
	if filepath.ToSlash(transaction.WorkPath) != wantWorkPath {
		return errors.New("Plugin lifecycle work path is not bound to its Plugin and owner")
	}
	if transaction.CreatedAt.IsZero() {
		return errors.New("Plugin lifecycle created_at is required")
	}
	return nil
}

func (s *Store) deleteLifecycleTransaction(name string) error {
	path, err := s.transactionPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) recoverLifecycleTransactions() error {
	releaseLifecycle, err := s.acquireLifecycle(context.Background())
	if err != nil {
		return err
	}
	defer releaseLifecycle()
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer release()
	return s.recoverLifecycleTransactionsUnlocked()
}

func (s *Store) recoverLifecycleTransactionsUnlocked() error {
	root := filepath.Join(s.root, ".state", "transactions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateIdentifier("plugin", name); err != nil {
			return fmt.Errorf("invalid Plugin lifecycle transaction filename %q: %w", entry.Name(), err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := s.recoverLifecycleTransactionUnlocked(name); err != nil {
			return fmt.Errorf("recover Plugin lifecycle %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) recoverLifecycleTransactionUnlocked(name string) error {
	transaction, err := s.loadLifecycleTransaction(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	currentDigest, _, err := s.currentStateFingerprint(name)
	if err != nil {
		return err
	}
	switch currentDigest {
	case transaction.CandidateStateDigest:
		return s.commitRecoveredLifecycle(transaction)
	case transaction.PreviousStateDigest:
		return s.rollbackRecoveredLifecycle(transaction)
	default:
		return newError("PLUGIN_RECOVERY_REQUIRED", "formal Plugin state matches neither side of the retained lifecycle transaction", map[string]any{"plugin": name, "transaction_owner": transaction.OwnerID}, nil)
	}
}

func (s *Store) commitRecoveredLifecycle(transaction LifecycleTransaction) error {
	visible := filepath.Join(s.root, transaction.Name)
	work, err := s.resolveLifecyclePath(transaction.WorkPath)
	if err != nil {
		return err
	}
	staged := filepath.Join(work, "visible")
	backup := filepath.Join(work, "previous")

	if transaction.Candidate != nil {
		digest, exists, err := packageDigestAt(visible)
		if err != nil {
			return err
		}
		if !exists || digest != transaction.CandidateDigest {
			if exists && (transaction.Previous == nil || digest != transaction.PreviousVisibleDigest) {
				return newError("PLUGIN_RECOVERY_REQUIRED", "committed Plugin visible package matches neither transaction digest", map[string]any{"plugin": transaction.Name}, nil)
			}
			stagedDigest, stagedExists, stagedErr := packageDigestAt(staged)
			if stagedErr != nil || !stagedExists || stagedDigest != transaction.CandidateDigest {
				return newError("PLUGIN_RECOVERY_REQUIRED", "committed Plugin candidate is unavailable or has changed", map[string]any{"plugin": transaction.Name}, errors.Join(err, stagedErr))
			}
			if exists {
				if err := os.RemoveAll(visible); err != nil {
					return err
				}
			}
			if err := os.Rename(staged, visible); err != nil {
				return err
			}
		}
	} else if _, exists, err := packageDigestAt(visible); err != nil {
		return err
	} else if exists {
		return newError("PLUGIN_RECOVERY_REQUIRED", "removed Plugin directory still exists after formal state commit", map[string]any{"plugin": transaction.Name}, nil)
	}

	if backupDigest, exists, err := packageDigestAt(backup); err != nil {
		return err
	} else if exists {
		if transaction.Previous == nil || backupDigest != transaction.PreviousVisibleDigest {
			return newError("PLUGIN_RECOVERY_REQUIRED", "Plugin lifecycle backup does not match previous package digest", map[string]any{"plugin": transaction.Name}, nil)
		}
		if err := os.RemoveAll(backup); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := s.deleteLifecycleTransaction(transaction.Name); err != nil {
		return err
	}
	s.releaseLifecycleClaim(transaction.Name, transaction.OwnerID)
	if transaction.PreviousGeneration != "" {
		_ = s.cleanupObsoleteGenerationUnlocked(transaction.Name, transaction.PreviousGeneration)
	}
	return nil
}

func (s *Store) rollbackRecoveredLifecycle(transaction LifecycleTransaction) error {
	visible := filepath.Join(s.root, transaction.Name)
	work, err := s.resolveLifecyclePath(transaction.WorkPath)
	if err != nil {
		return err
	}
	staged := filepath.Join(work, "visible")
	backup := filepath.Join(work, "previous")
	visibleDigest, visibleExists, err := packageDigestAt(visible)
	if err != nil {
		return err
	}
	backupDigest, backupExists, err := packageDigestAt(backup)
	if err != nil {
		return err
	}

	if transaction.Previous == nil {
		if visibleExists {
			if visibleDigest != transaction.CandidateDigest {
				return newError("PLUGIN_RECOVERY_REQUIRED", "uncommitted Plugin install left unexpected visible content", map[string]any{"plugin": transaction.Name}, nil)
			}
			if err := os.RemoveAll(visible); err != nil {
				return err
			}
		}
	} else {
		switch {
		case visibleExists && visibleDigest == transaction.PreviousVisibleDigest:
			if backupExists && backupDigest != transaction.PreviousVisibleDigest {
				return newError("PLUGIN_RECOVERY_REQUIRED", "Plugin rollback backup contains unexpected content", map[string]any{"plugin": transaction.Name}, nil)
			}
		case backupExists && backupDigest == transaction.PreviousVisibleDigest:
			if visibleExists {
				if visibleDigest != transaction.CandidateDigest {
					return newError("PLUGIN_RECOVERY_REQUIRED", "Plugin visible package matches neither transaction digest", map[string]any{"plugin": transaction.Name}, nil)
				}
				if err := os.RemoveAll(visible); err != nil {
					return err
				}
			}
			if err := os.Rename(backup, visible); err != nil {
				return err
			}
		default:
			return newError("PLUGIN_RECOVERY_REQUIRED", "previous Plugin package cannot be restored from retained lifecycle material", map[string]any{"plugin": transaction.Name}, nil)
		}
	}

	if _, exists, err := packageDigestAt(staged); err != nil {
		return err
	} else if exists {
		if err := os.RemoveAll(staged); err != nil {
			return err
		}
	}
	if transaction.CandidateGeneration != "" {
		generationPath, err := s.generationPath(transaction.Name, transaction.CandidateGeneration)
		if err != nil {
			return err
		}
		if digest, exists, err := packageDigestAt(generationPath); err != nil {
			return err
		} else if exists {
			if digest != transaction.CandidateDigest {
				return newError("PLUGIN_RECOVERY_REQUIRED", "candidate Plugin generation changed during interrupted activation", map[string]any{"plugin": transaction.Name, "generation": transaction.CandidateGeneration}, nil)
			}
			// Keep the journal if reclaiming an unused candidate fails. An
			// active reader instead retains the generation until its release.
			if err := s.removeUnleasedGenerationUnlocked(transaction.Name, transaction.CandidateGeneration); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := s.deleteLifecycleTransaction(transaction.Name); err != nil {
		return err
	}
	s.releaseLifecycleClaim(transaction.Name, transaction.OwnerID)
	return nil
}

func (s *Store) resolveLifecyclePath(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return "", errors.New("invalid Plugin lifecycle path")
	}
	path := filepath.Join(s.root, filepath.FromSlash(relative))
	check, err := filepath.Rel(s.root, path)
	if err != nil || !filepath.IsLocal(check) || check == "." {
		return "", errors.New("Plugin lifecycle path escapes Plugin store")
	}
	return path, nil
}

func (s *Store) relativeLifecyclePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("Plugin lifecycle path escapes Plugin store")
	}
	return filepath.ToSlash(relative), nil
}
