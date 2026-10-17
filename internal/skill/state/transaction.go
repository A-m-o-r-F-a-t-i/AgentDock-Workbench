package state

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
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/skill/packagehash"
)

const (
	SwapTransactionSchemaVersion = 1

	swapPhasePrepared       = "prepared"
	swapPhasePreviousMoved  = "previous_moved"
	swapPhaseCandidateMoved = "candidate_moved"
	swapPhaseRemovalMoved   = "removal_moved"
	swapPhaseStateCommitted = "state_committed"

	swapRolePrevious  = "previous"
	swapRoleCandidate = "candidate"
	swapRoleRemoval   = "removal"

	swapCommitKeep   = "keep"
	swapCommitRemove = "remove"

	swapRollbackRestore = "restore"
	swapRollbackDiscard = "discard"
)

type SwapMove struct {
	Role        string `json:"role"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Digest      string `json:"digest"`
	Commit      string `json:"commit"`
	Rollback    string `json:"rollback"`
}

type SwapTransaction struct {
	SchemaVersion     int        `json:"schema_version"`
	Skill             string     `json:"skill"`
	Operation         string     `json:"operation"`
	Phase             string     `json:"phase"`
	AppliedMoves      int        `json:"applied_moves"`
	PreviousSelection Selection  `json:"previous_selection"`
	TargetSelection   Selection  `json:"target_selection"`
	Moves             []SwapMove `json:"moves"`
	CleanupPath       string     `json:"cleanup_path,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func (s *Store) recoverInterruptedSwaps() error {
	transactions, err := s.listSwapTransactions()
	if err != nil {
		return fmt.Errorf("load Skill swap transactions: %w", err)
	}
	for _, transaction := range transactions {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		release, lockErr := s.AcquireWrite(ctx, transaction.Skill)
		cancel()
		if lockErr != nil {
			if errors.Is(lockErr, context.DeadlineExceeded) {
				// A live process may still own this Skill lifecycle writer. The next
				// writer retries recovery after the lock is released or becomes stale.
				continue
			}
			return fmt.Errorf("lock Skill swap transaction %s: %w", transaction.Skill, lockErr)
		}
		recoverErr := s.recoverSwapLocked(transaction.Skill)
		release()
		if recoverErr != nil {
			return fmt.Errorf("recover Skill swap %s: %w", transaction.Skill, recoverErr)
		}
	}
	return nil
}

func (s *Store) recoverSwapLocked(skill string) error {
	transaction, err := s.loadSwapTransaction(skill)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := s.load(skill)
	if err != nil {
		return fmt.Errorf("load current Skill selection: %w", err)
	}

	commit := transaction.Phase == swapPhaseStateCommitted
	previousMatches := selectionsEqual(current, transaction.PreviousSelection)
	targetMatches := selectionsEqual(current, transaction.TargetSelection)
	if !selectionsEqual(transaction.PreviousSelection, transaction.TargetSelection) && targetMatches {
		commit = true
	}
	if !previousMatches && !targetMatches {
		return errors.New("Skill selection matches neither side of the durable swap transaction")
	}
	if commit {
		if err := s.finalizeSwapCommit(transaction); err != nil {
			return err
		}
		if !targetMatches {
			if err := s.persistSelection(skill, transaction.TargetSelection); err != nil {
				return fmt.Errorf("persist committed Skill selection: %w", err)
			}
		}
	} else {
		if err := s.rollbackSwap(transaction); err != nil {
			return err
		}
		if !previousMatches {
			if err := s.persistSelection(skill, transaction.PreviousSelection); err != nil {
				return fmt.Errorf("persist recovered Skill selection: %w", err)
			}
		}
	}
	if err := s.cleanupSwapPath(transaction.CleanupPath); err != nil {
		return err
	}
	return s.deleteSwapTransaction(skill)
}

func (s *Store) executeSwapLocked(transaction SwapTransaction) error {
	if err := validateSwapTransaction(transaction); err != nil {
		return err
	}
	if _, err := s.loadSwapTransaction(transaction.Skill); err == nil {
		return fmt.Errorf("Skill %s already has a pending swap transaction", transaction.Skill)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	transaction.Phase = swapPhasePrepared
	transaction.AppliedMoves = 0
	if err := s.saveSwapTransaction(transaction); err != nil {
		return fmt.Errorf("prepare Skill swap transaction: %w", err)
	}

	for index, move := range transaction.Moves {
		if err := s.applySwapMove(move); err != nil {
			return s.recoverSwapFailure(transaction, fmt.Errorf("apply Skill swap move %d: %w", index, err))
		}
		transaction.AppliedMoves = index + 1
		transaction.Phase = swapPhaseForRole(move.Role)
		if err := s.saveSwapTransaction(transaction); err != nil {
			return s.recoverSwapFailure(transaction, fmt.Errorf("record Skill swap move %d: %w", index, err))
		}
	}

	if !selectionsEqual(transaction.PreviousSelection, transaction.TargetSelection) {
		if err := s.persistSelection(transaction.Skill, transaction.TargetSelection); err != nil {
			return s.recoverSwapFailure(transaction, fmt.Errorf("commit Skill selection: %w", err))
		}
	}
	transaction.Phase = swapPhaseStateCommitted
	if err := s.saveSwapTransaction(transaction); err != nil {
		return s.recoverSwapFailure(transaction, fmt.Errorf("record committed Skill swap: %w", err))
	}
	if err := s.finalizeSwapCommit(transaction); err != nil {
		return fmt.Errorf("finalize committed Skill swap: %w", err)
	}
	if err := s.cleanupSwapPath(transaction.CleanupPath); err != nil {
		return err
	}
	return s.deleteSwapTransaction(transaction.Skill)
}

func (s *Store) recoverSwapFailure(transaction SwapTransaction, cause error) error {
	recoverErr := s.recoverSwapLocked(transaction.Skill)
	if recoverErr != nil {
		return errors.Join(cause, fmt.Errorf("recover failed Skill swap: %w", recoverErr))
	}
	current, err := s.load(transaction.Skill)
	if err == nil && !selectionsEqual(transaction.PreviousSelection, transaction.TargetSelection) && selectionsEqual(current, transaction.TargetSelection) {
		return nil
	}
	return cause
}

func (s *Store) applySwapMove(move SwapMove) error {
	source, err := s.resolveSwapPath(move.Source)
	if err != nil {
		return err
	}
	destination, err := s.resolveSwapPath(move.Destination)
	if err != nil {
		return err
	}
	digest, exists, err := directoryDigest(source)
	if err != nil {
		return fmt.Errorf("inspect source %q: %w", move.Source, err)
	}
	if !exists || digest != move.Digest {
		return fmt.Errorf("source %q does not match the recorded digest", move.Source)
	}
	if _, exists, err := directoryDigest(destination); err != nil {
		return fmt.Errorf("inspect destination %q: %w", move.Destination, err)
	} else if exists {
		return fmt.Errorf("destination %q already exists", move.Destination)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return nil
}

func (s *Store) rollbackSwap(transaction SwapTransaction) error {
	rollbackSources := make(map[string]string, len(transaction.Moves))
	for _, move := range transaction.Moves {
		rollbackSources[filepath.Clean(move.Source)] = move.Digest
	}
	for index := len(transaction.Moves) - 1; index >= 0; index-- {
		move := transaction.Moves[index]
		source, err := s.resolveSwapPath(move.Source)
		if err != nil {
			return err
		}
		destination, err := s.resolveSwapPath(move.Destination)
		if err != nil {
			return err
		}
		sourceDigest, sourceExists, err := directoryDigest(source)
		if err != nil {
			return fmt.Errorf("inspect rollback source %q: %w", move.Source, err)
		}
		destinationDigest, destinationExists, err := directoryDigest(destination)
		if err != nil {
			return fmt.Errorf("inspect rollback destination %q: %w", move.Destination, err)
		}

		switch move.Rollback {
		case swapRollbackRestore:
			if sourceExists && sourceDigest == move.Digest {
				if destinationExists {
					expected, shared := rollbackSources[filepath.Clean(move.Destination)]
					if !shared || destinationDigest != expected {
						return fmt.Errorf("rollback destination %q contains unexpected content", move.Destination)
					}
				}
				continue
			}
			if sourceExists {
				return fmt.Errorf("rollback source %q contains unexpected content", move.Source)
			}
			if !destinationExists || destinationDigest != move.Digest {
				return fmt.Errorf("rollback destination %q does not match the recorded digest", move.Destination)
			}
			if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
				return err
			}
			if err := os.Rename(destination, source); err != nil {
				return fmt.Errorf("restore Skill swap move %d: %w", index, err)
			}
		case swapRollbackDiscard:
			if sourceExists {
				if sourceDigest != move.Digest {
					return fmt.Errorf("discard source %q contains unexpected content", move.Source)
				}
				if err := os.RemoveAll(source); err != nil {
					return fmt.Errorf("remove uncommitted Skill candidate: %w", err)
				}
			}
			if destinationExists {
				if destinationDigest != move.Digest {
					return fmt.Errorf("discard destination %q contains unexpected content", move.Destination)
				}
				if err := os.RemoveAll(destination); err != nil {
					return fmt.Errorf("remove published Skill candidate: %w", err)
				}
			}
		default:
			return fmt.Errorf("unsupported Skill swap rollback action %q", move.Rollback)
		}
	}
	return nil
}

func (s *Store) finalizeSwapCommit(transaction SwapTransaction) error {
	finalKeep := make(map[string]string)
	for _, move := range transaction.Moves {
		if move.Commit == swapCommitKeep {
			finalKeep[filepath.Clean(move.Destination)] = move.Digest
		}
	}
	for index, move := range transaction.Moves {
		source, err := s.resolveSwapPath(move.Source)
		if err != nil {
			return err
		}
		destination, err := s.resolveSwapPath(move.Destination)
		if err != nil {
			return err
		}
		sourceDigest, sourceExists, err := directoryDigest(source)
		if err != nil {
			return fmt.Errorf("inspect committed source %q: %w", move.Source, err)
		}
		destinationDigest, destinationExists, err := directoryDigest(destination)
		if err != nil {
			return fmt.Errorf("inspect committed destination %q: %w", move.Destination, err)
		}

		switch move.Commit {
		case swapCommitKeep:
			if !destinationExists || destinationDigest != move.Digest {
				return fmt.Errorf("committed destination %q does not match the recorded digest", move.Destination)
			}
			if sourceExists {
				expected, shared := finalKeep[filepath.Clean(move.Source)]
				if shared && sourceDigest == expected {
					continue
				}
				if sourceDigest == move.Digest {
					return fmt.Errorf("committed Skill move %d remains duplicated at its source", index)
				}
				return fmt.Errorf("committed source %q contains unexpected content", move.Source)
			}
		case swapCommitRemove:
			if sourceExists && destinationExists {
				return fmt.Errorf("committed Skill removal %d exists at both transaction locations", index)
			}
			if sourceExists {
				if sourceDigest != move.Digest {
					return fmt.Errorf("removed source %q contains unexpected content", move.Source)
				}
				if err := os.RemoveAll(source); err != nil {
					return fmt.Errorf("finish committed Skill removal: %w", err)
				}
			}
			if destinationExists {
				if destinationDigest != move.Digest {
					return fmt.Errorf("removal backup %q contains unexpected content", move.Destination)
				}
				if err := os.RemoveAll(destination); err != nil {
					return fmt.Errorf("remove committed Skill backup: %w", err)
				}
			}
		default:
			return fmt.Errorf("unsupported Skill swap commit action %q", move.Commit)
		}
	}
	return nil
}

func (s *Store) transactionWorkPath(skill string) (string, string, error) {
	owner, err := newLockOwner()
	if err != nil {
		return "", "", fmt.Errorf("create Skill transaction owner: %w", err)
	}
	absolute := filepath.Join(s.root, tempDirectory, transactionDirectory, skill+"-"+owner)
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", "", err
	}
	relative, err := s.relativeSwapPath(absolute)
	if err != nil {
		_ = os.RemoveAll(absolute)
		return "", "", err
	}
	return absolute, relative, nil
}

func (s *Store) newSwapTransaction(skill, operation string, previous, target Selection, moves []SwapMove, cleanupPath string) SwapTransaction {
	return SwapTransaction{
		SchemaVersion:     SwapTransactionSchemaVersion,
		Skill:             skill,
		Operation:         operation,
		Phase:             swapPhasePrepared,
		PreviousSelection: previous,
		TargetSelection:   target,
		Moves:             append([]SwapMove(nil), moves...),
		CleanupPath:       cleanupPath,
		CreatedAt:         time.Now().UTC(),
	}
}

func (s *Store) newSwapMove(role, source, destination, digest, commit, rollback string) (SwapMove, error) {
	sourceRelative, err := s.relativeSwapPath(source)
	if err != nil {
		return SwapMove{}, err
	}
	destinationRelative, err := s.relativeSwapPath(destination)
	if err != nil {
		return SwapMove{}, err
	}
	move := SwapMove{Role: role, Source: sourceRelative, Destination: destinationRelative, Digest: digest, Commit: commit, Rollback: rollback}
	if err := validateSwapMove(move); err != nil {
		return SwapMove{}, err
	}
	return move, nil
}

func (s *Store) saveSwapTransaction(transaction SwapTransaction) error {
	if err := validateSwapTransaction(transaction); err != nil {
		return err
	}
	data, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(s.swapTransactionPath(transaction.Skill), data, 0o600)
}

func (s *Store) loadSwapTransaction(skill string) (SwapTransaction, error) {
	if err := validateIdentifier("skill", skill); err != nil {
		return SwapTransaction{}, err
	}
	data, err := securepath.ReadRegular(s.root, filepath.Join(stateDirectory, transactionDirectory, skill+".json"), 1<<20)
	if err != nil {
		return SwapTransaction{}, err
	}
	return decodeSwapTransaction(data)
}

func (s *Store) listSwapTransactions() ([]SwapTransaction, error) {
	root := filepath.Join(s.root, stateDirectory, transactionDirectory)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []SwapTransaction{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]SwapTransaction, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := securepath.ReadRegular(s.root, filepath.Join(stateDirectory, transactionDirectory, entry.Name()), 1<<20)
		if err != nil {
			return nil, err
		}
		transaction, err := decodeSwapTransaction(data)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		items = append(items, transaction)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Skill < items[j].Skill })
	return items, nil
}

func decodeSwapTransaction(data []byte) (SwapTransaction, error) {
	var transaction SwapTransaction
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transaction); err != nil {
		return SwapTransaction{}, fmt.Errorf("decode Skill swap transaction: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return SwapTransaction{}, fmt.Errorf("decode trailing Skill swap transaction data: %w", err)
		}
		return SwapTransaction{}, errors.New("Skill swap transaction contains multiple JSON values")
	}
	if err := validateSwapTransaction(transaction); err != nil {
		return SwapTransaction{}, err
	}
	return transaction, nil
}

func (s *Store) deleteSwapTransaction(skill string) error {
	if err := validateIdentifier("skill", skill); err != nil {
		return err
	}
	if err := os.Remove(s.swapTransactionPath(skill)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Skill swap transaction: %w", err)
	}
	return nil
}

func (s *Store) swapTransactionPath(skill string) string {
	return filepath.Join(s.root, stateDirectory, transactionDirectory, skill+".json")
}

func (s *Store) cleanupSwapPath(relative string) error {
	if relative == "" {
		return nil
	}
	path, err := s.resolveSwapPath(relative)
	if err != nil {
		return err
	}
	tempRoot := filepath.Join(s.root, tempDirectory)
	rel, err := filepath.Rel(tempRoot, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return errors.New("Skill swap cleanup path must stay inside the private temp directory")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove Skill swap temporary data: %w", err)
	}
	return nil
}

func (s *Store) relativeSwapPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return "", errors.New("Skill swap path escapes its store")
	}
	return filepath.ToSlash(relative), nil
}

func (s *Store) resolveSwapPath(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return "", errors.New("invalid Skill swap relative path")
	}
	path := filepath.Join(s.root, filepath.FromSlash(relative))
	check, err := filepath.Rel(s.root, path)
	if err != nil || !filepath.IsLocal(check) || check == "." {
		return "", errors.New("Skill swap path escapes its store")
	}
	return path, nil
}

func directoryDigest(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", true, errors.New("managed Skill path is not a regular directory")
	}
	digest, err := packagehash.Directory(path)
	return digest, true, err
}

func validateSwapTransaction(transaction SwapTransaction) error {
	if transaction.SchemaVersion != SwapTransactionSchemaVersion {
		return fmt.Errorf("unsupported Skill swap transaction schema %d", transaction.SchemaVersion)
	}
	if err := validateIdentifier("skill", transaction.Skill); err != nil {
		return err
	}
	if transaction.Operation == "" {
		return errors.New("Skill swap transaction operation is required")
	}
	switch transaction.Phase {
	case swapPhasePrepared, swapPhasePreviousMoved, swapPhaseCandidateMoved, swapPhaseRemovalMoved, swapPhaseStateCommitted:
	default:
		return fmt.Errorf("invalid Skill swap transaction phase %q", transaction.Phase)
	}
	if transaction.AppliedMoves < 0 || transaction.AppliedMoves > len(transaction.Moves) {
		return errors.New("Skill swap transaction applied move count is invalid")
	}
	if err := validateSelection(transaction.PreviousSelection); err != nil {
		return fmt.Errorf("invalid previous Skill selection: %w", err)
	}
	if err := validateSelection(transaction.TargetSelection); err != nil {
		return fmt.Errorf("invalid target Skill selection: %w", err)
	}
	if len(transaction.Moves) == 0 {
		return errors.New("Skill swap transaction requires at least one move")
	}
	for _, move := range transaction.Moves {
		if err := validateSwapMove(move); err != nil {
			return err
		}
	}
	if transaction.CleanupPath != "" && (filepath.IsAbs(transaction.CleanupPath) || !filepath.IsLocal(filepath.FromSlash(transaction.CleanupPath))) {
		return errors.New("invalid Skill swap cleanup path")
	}
	if transaction.CreatedAt.IsZero() {
		return errors.New("Skill swap transaction created_at is required")
	}
	return nil
}

func validateSwapMove(move SwapMove) error {
	if move.Source == "" || move.Destination == "" || move.Source == move.Destination {
		return errors.New("Skill swap move requires distinct source and destination paths")
	}
	for _, value := range []string{move.Source, move.Destination} {
		if filepath.IsAbs(value) || !filepath.IsLocal(filepath.FromSlash(value)) {
			return errors.New("Skill swap move path must be relative to the Skill store")
		}
	}
	if move.Digest == "" {
		return errors.New("Skill swap move digest is required")
	}
	switch move.Role {
	case swapRolePrevious, swapRoleCandidate, swapRoleRemoval:
	default:
		return fmt.Errorf("invalid Skill swap move role %q", move.Role)
	}
	if move.Commit != swapCommitKeep && move.Commit != swapCommitRemove {
		return fmt.Errorf("invalid Skill swap commit action %q", move.Commit)
	}
	if move.Rollback != swapRollbackRestore && move.Rollback != swapRollbackDiscard {
		return fmt.Errorf("invalid Skill swap rollback action %q", move.Rollback)
	}
	return nil
}

func swapPhaseForRole(role string) string {
	switch role {
	case swapRolePrevious:
		return swapPhasePreviousMoved
	case swapRoleCandidate:
		return swapPhaseCandidateMoved
	default:
		return swapPhaseRemovalMoved
	}
}

func selectionsEqual(left, right Selection) bool {
	if left.ActiveVersion != right.ActiveVersion || left.Disabled != right.Disabled || left.System != right.System || !left.UpdatedAt.Equal(right.UpdatedAt) || len(left.History) != len(right.History) {
		return false
	}
	for index := range left.History {
		if left.History[index] != right.History[index] {
			return false
		}
	}
	return true
}
