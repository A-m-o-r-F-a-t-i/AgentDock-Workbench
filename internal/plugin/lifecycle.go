package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) BeginPreparedActivation(ctx context.Context, candidate PreparedSource, reviewToken string, replace, confirmSourceChange bool) (ActivationCandidate, error) {
	if err := verifyReviewToken(candidate.Review, reviewToken); err != nil {
		return ActivationCandidate{}, err
	}
	if !candidate.Review.Valid {
		return ActivationCandidate{}, pluginError("PLUGIN_UNSUPPORTED_COMPONENT", "install.validate", errors.New(strings.Join(candidate.Review.Issues, ", ")))
	}
	pkg, err := LoadPackage(candidate.Root)
	if err != nil {
		return ActivationCandidate{}, err
	}
	if pkg.PackageDigest != candidate.Review.PackageDigest || pkg.Manifest.Name != candidate.Review.Name || pkg.Manifest.Version != candidate.Review.Version {
		return ActivationCandidate{}, pluginError("PLUGIN_REVIEW_CHANGED", "install.review", errors.New("prepared Plugin no longer matches the reviewed package"))
	}
	return s.beginPreparedActivation(ctx, candidate, replace, confirmSourceChange)
}

func (s *Store) beginPreparedActivation(ctx context.Context, candidate PreparedSource, replace, confirmSourceChange bool) (ActivationCandidate, error) {
	if err := ctx.Err(); err != nil {
		return ActivationCandidate{}, err
	}
	pkg, err := LoadPackage(candidate.Root)
	if err != nil {
		return ActivationCandidate{}, err
	}

	release, err := s.acquire()
	if err != nil {
		return ActivationCandidate{}, err
	}
	defer func() {
		s.Invalidate()
		release()
	}()
	if err := s.rejectPendingLifecycleUnlocked(pkg.Manifest.Name); err != nil {
		return ActivationCandidate{}, err
	}
	installed, err := s.scanUnlocked()
	if err != nil {
		return ActivationCandidate{}, err
	}
	existing, exists := installed[pkg.Manifest.Name]
	if !exists && replace {
		return ActivationCandidate{}, newError("PLUGIN_NOT_FOUND", "plugin update requires an existing installed plugin", map[string]any{"name": pkg.Manifest.Name}, nil)
	}
	if exists && !replace {
		return ActivationCandidate{}, newError("PLUGIN_ALREADY_INSTALLED", "plugin is already installed", map[string]any{"name": pkg.Manifest.Name, "version": existing.manifest.Version}, nil)
	}
	candidateRecord, err := readPackage(candidate.Root, false)
	if err != nil {
		return ActivationCandidate{}, err
	}
	if err := ensureUniqueOwnership(installed, candidateRecord, pkg.Manifest.Name); err != nil {
		return ActivationCandidate{}, err
	}

	state := defaultState(candidateRecord)
	var previous *State
	previousVisibleDigest := ""
	if exists {
		current := normalizeState(existing.state, existing)
		previous = &current
		state = mergeState(current, candidateRecord)
		if current.Source != nil && !samePluginSourceBinding(*current.Source, candidate.Source) && !confirmSourceChange {
			return ActivationCandidate{}, pluginError("PLUGIN_SOURCE_CHANGE_CONFIRMATION_REQUIRED", "update.source", errors.New("Plugin source binding changed; set confirmed_source_change=true to rebind"))
		}
		previousVisibleDigest, _, err = packageDigestAt(existing.root)
		if err != nil {
			return ActivationCandidate{}, err
		}
	}
	if candidate.Enabled != nil {
		state.Enabled = *candidate.Enabled
	}
	if candidate.Review.Name != "" || candidate.Source.Type != "" || candidate.Compatibility.Adapter != "" {
		source := candidate.Source
		compatibility := candidate.Compatibility
		state.Source = &source
		state.Compatibility = &compatibility
	}
	state.PackageDigest = pkg.PackageDigest
	state.InstalledAt = time.Now().UTC()

	if exists && previous != nil && previous.PackageDigest == pkg.PackageDigest && existing.manifest.Version == pkg.Manifest.Version {
		state.Generation = previous.Generation
		if err := s.writeNamedState(pkg.Manifest.Name, state); err != nil {
			return ActivationCandidate{}, err
		}
		definition, err := s.definitionForState(pkg.Manifest.Name, state)
		if err != nil {
			return ActivationCandidate{}, err
		}
		return ActivationCandidate{Name: pkg.Manifest.Name, Operation: "update", Generation: state.Generation, PackageDigest: state.PackageDigest, Definition: definition}, nil
	}

	owner, err := newLifecycleOwner()
	if err != nil {
		return ActivationCandidate{}, err
	}
	generation, err := newLifecycleOwner()
	if err != nil {
		return ActivationCandidate{}, err
	}
	state.Generation = generation
	previousDigest, err := stateFingerprint(previous)
	if err != nil {
		return ActivationCandidate{}, err
	}
	candidateDigest, err := stateFingerprint(&state)
	if err != nil {
		return ActivationCandidate{}, err
	}
	work, err := s.transactionWorkPath(pkg.Manifest.Name, owner)
	if err != nil {
		return ActivationCandidate{}, err
	}
	workRelative, err := s.relativeLifecyclePath(work)
	if err != nil {
		return ActivationCandidate{}, err
	}
	operation := "install"
	if exists {
		operation = "update"
	}
	transaction := LifecycleTransaction{
		SchemaVersion: lifecycleTransactionSchemaVersion, OwnerID: owner, Name: pkg.Manifest.Name,
		Operation: operation, Phase: lifecyclePhasePrepared, Previous: previous, Candidate: &state,
		PreviousStateDigest: previousDigest, CandidateStateDigest: candidateDigest,
		PreviousVisibleDigest: previousVisibleDigest, CandidateDigest: pkg.PackageDigest,
		CandidateGeneration: generation, WorkPath: workRelative, CreatedAt: time.Now().UTC(),
	}
	if previous != nil {
		transaction.PreviousGeneration = previous.Generation
	}
	if err := s.saveLifecycleTransaction(transaction); err != nil {
		return ActivationCandidate{}, err
	}

	cleanupPrepared := func(cause error) (ActivationCandidate, error) {
		generationPath, _ := s.generationPath(pkg.Manifest.Name, generation)
		cleanupErr := errors.Join(os.RemoveAll(work), os.RemoveAll(generationPath), s.deleteLifecycleTransaction(pkg.Manifest.Name))
		return ActivationCandidate{}, errors.Join(cause, cleanupErr)
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		return cleanupPrepared(err)
	}
	generationPath, err := s.generationPath(pkg.Manifest.Name, generation)
	if err != nil {
		return cleanupPrepared(err)
	}
	if err := os.MkdirAll(filepath.Dir(generationPath), 0o700); err != nil {
		return cleanupPrepared(err)
	}
	if err := copyPackageTree(candidate.Root, generationPath); err != nil {
		return cleanupPrepared(err)
	}
	stagedVisible := filepath.Join(work, "visible")
	if err := copyPackageTree(generationPath, stagedVisible); err != nil {
		return cleanupPrepared(err)
	}
	for label, path := range map[string]string{"generation": generationPath, "visible": stagedVisible} {
		digest, present, digestErr := packageDigestAt(path)
		if digestErr != nil || !present || digest != pkg.PackageDigest {
			return cleanupPrepared(newError("PLUGIN_CANDIDATE_DRIFT", "staged Plugin candidate does not match reviewed content", map[string]any{"plugin": pkg.Manifest.Name, "stage": label}, digestErr))
		}
	}
	transaction.Phase = lifecyclePhaseCandidateStaged
	if err := s.saveLifecycleTransaction(transaction); err != nil {
		return cleanupPrepared(err)
	}
	record, err := s.runtimeRecordForState(pkg.Manifest.Name, state)
	if err != nil {
		return cleanupPrepared(err)
	}
	return ActivationCandidate{
		OwnerID: owner, Name: pkg.Manifest.Name, Operation: operation, Generation: generation,
		PackageDigest: pkg.PackageDigest, Definition: cloneDefinition(record.definition),
	}, nil
}

func (s *Store) FinalizeActivation(name, owner string) (Definition, error) {
	name = strings.TrimSpace(name)
	release, err := s.acquire()
	if err != nil {
		return Definition{}, err
	}
	defer func() {
		s.Invalidate()
		release()
	}()
	transaction, err := s.loadOwnedLifecycle(name, owner)
	if err != nil {
		return Definition{}, err
	}
	if transaction.Operation == "remove" || transaction.Candidate == nil {
		return Definition{}, newError("PLUGIN_LIFECYCLE_INVALID", "Plugin transaction is not an activation", map[string]any{"plugin": name}, nil)
	}
	if transaction.Phase != lifecyclePhaseCandidateStaged && transaction.Phase != lifecyclePhaseVisibleSwitched && transaction.Phase != lifecyclePhaseStateCommitted {
		return Definition{}, newError("PLUGIN_LIFECYCLE_INVALID", "Plugin activation is not ready to finalize", map[string]any{"plugin": name, "phase": transaction.Phase}, nil)
	}
	currentDigest, _, err := s.currentStateFingerprint(name)
	if err != nil {
		return Definition{}, err
	}
	if currentDigest == transaction.CandidateStateDigest {
		if err := s.commitRecoveredLifecycle(transaction); err != nil {
			return Definition{}, err
		}
		return s.definitionForState(name, *transaction.Candidate)
	}
	if currentDigest != transaction.PreviousStateDigest {
		return Definition{}, newError("PLUGIN_STATE_CHANGED", "Plugin formal state changed during activation", map[string]any{"plugin": name}, nil)
	}

	visible := filepath.Join(s.root, name)
	work, err := s.resolveLifecyclePath(transaction.WorkPath)
	if err != nil {
		return Definition{}, err
	}
	staged := filepath.Join(work, "visible")
	backup := filepath.Join(work, "previous")
	candidateDigest, candidateExists, err := packageDigestAt(staged)
	if err != nil || !candidateExists || candidateDigest != transaction.CandidateDigest {
		return Definition{}, newError("PLUGIN_CANDIDATE_DRIFT", "Plugin activation candidate is unavailable or changed", map[string]any{"plugin": name}, err)
	}
	generationPath, err := s.generationPath(name, transaction.CandidateGeneration)
	if err != nil {
		return Definition{}, err
	}
	generationDigest, generationExists, err := packageDigestAt(generationPath)
	if err != nil || !generationExists || generationDigest != transaction.CandidateDigest {
		return Definition{}, newError("PLUGIN_CANDIDATE_DRIFT", "Plugin activation generation is unavailable or changed", map[string]any{"plugin": name}, err)
	}

	if transaction.Phase == lifecyclePhaseCandidateStaged {
		visibleDigest, visibleExists, err := packageDigestAt(visible)
		if err != nil {
			return Definition{}, err
		}
		if transaction.Previous == nil {
			if visibleExists {
				return Definition{}, newError("PLUGIN_STATE_CHANGED", "Plugin install destination appeared during activation", map[string]any{"plugin": name}, nil)
			}
		} else {
			if !visibleExists || visibleDigest != transaction.PreviousVisibleDigest {
				return Definition{}, newError("PLUGIN_STATE_CHANGED", "previous Plugin package changed during activation", map[string]any{"plugin": name}, nil)
			}
			if _, backupExists, backupErr := packageDigestAt(backup); backupErr != nil {
				return Definition{}, backupErr
			} else if backupExists {
				return Definition{}, newError("PLUGIN_RECOVERY_REQUIRED", "Plugin activation backup already exists", map[string]any{"plugin": name}, nil)
			}
			if err := os.Rename(visible, backup); err != nil {
				return Definition{}, newError("PLUGIN_ACTIVATION_FAILED", "move previous Plugin package", map[string]any{"plugin": name}, err)
			}
		}
		if err := os.Rename(staged, visible); err != nil {
			return Definition{}, newError("PLUGIN_ACTIVATION_FAILED", "publish Plugin candidate", map[string]any{"plugin": name}, err)
		}
		transaction.Phase = lifecyclePhaseVisibleSwitched
		if err := s.saveLifecycleTransaction(transaction); err != nil {
			return Definition{}, err
		}
	}
	if transaction.Phase == lifecyclePhaseVisibleSwitched {
		if err := s.writeNamedState(name, *transaction.Candidate); err != nil {
			return Definition{}, err
		}
		transaction.Phase = lifecyclePhaseStateCommitted
		if err := s.saveLifecycleTransaction(transaction); err != nil {
			return Definition{}, err
		}
	}
	if err := s.commitRecoveredLifecycle(transaction); err != nil {
		return Definition{}, err
	}
	return s.definitionForState(name, *transaction.Candidate)
}

func (s *Store) AbortActivation(name, owner string) error {
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer func() {
		s.Invalidate()
		release()
	}()
	transaction, err := s.loadOwnedLifecycle(strings.TrimSpace(name), owner)
	if err != nil {
		return err
	}
	if err := s.recoverLifecycleTransactionUnlocked(transaction.Name); err != nil {
		return err
	}
	return nil
}

func (s *Store) BeginRemoval(ctx context.Context, name string) (ActivationCandidate, error) {
	name = strings.TrimSpace(name)
	if err := validateIdentifier("plugin", name); err != nil {
		return ActivationCandidate{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActivationCandidate{}, err
	}
	release, err := s.acquire()
	if err != nil {
		return ActivationCandidate{}, err
	}
	defer func() {
		s.Invalidate()
		release()
	}()
	if err := s.rejectPendingLifecycleUnlocked(name); err != nil {
		return ActivationCandidate{}, err
	}
	record, err := s.readInstalledUnlocked(name)
	if err != nil {
		return ActivationCandidate{}, err
	}
	state := normalizeState(record.state, record)
	previousDigest, err := stateFingerprint(&state)
	if err != nil {
		return ActivationCandidate{}, err
	}
	absentDigest, err := stateFingerprint(nil)
	if err != nil {
		return ActivationCandidate{}, err
	}
	visibleDigest, _, err := packageDigestAt(record.root)
	if err != nil {
		return ActivationCandidate{}, err
	}
	owner, err := newLifecycleOwner()
	if err != nil {
		return ActivationCandidate{}, err
	}
	work, err := s.transactionWorkPath(name, owner)
	if err != nil {
		return ActivationCandidate{}, err
	}
	workRelative, err := s.relativeLifecyclePath(work)
	if err != nil {
		return ActivationCandidate{}, err
	}
	transaction := LifecycleTransaction{
		SchemaVersion: lifecycleTransactionSchemaVersion, OwnerID: owner, Name: name, Operation: "remove",
		Phase: lifecyclePhaseCandidateStaged, Previous: &state,
		PreviousStateDigest: previousDigest, CandidateStateDigest: absentDigest,
		PreviousVisibleDigest: visibleDigest, PreviousGeneration: state.Generation,
		WorkPath: workRelative, CreatedAt: time.Now().UTC(),
	}
	if err := s.saveLifecycleTransaction(transaction); err != nil {
		return ActivationCandidate{}, err
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		_ = s.deleteLifecycleTransaction(name)
		return ActivationCandidate{}, err
	}
	return ActivationCandidate{OwnerID: owner, Name: name, Operation: "remove", Generation: state.Generation, PackageDigest: state.PackageDigest, Definition: cloneDefinition(record.definition)}, nil
}

func (s *Store) FinalizeRemoval(name, owner string) error {
	name = strings.TrimSpace(name)
	release, err := s.acquire()
	if err != nil {
		return err
	}
	defer func() {
		s.Invalidate()
		release()
	}()
	transaction, err := s.loadOwnedLifecycle(name, owner)
	if err != nil {
		return err
	}
	if transaction.Operation != "remove" || transaction.Previous == nil {
		return newError("PLUGIN_LIFECYCLE_INVALID", "Plugin transaction is not a removal", map[string]any{"plugin": name}, nil)
	}
	currentDigest, _, err := s.currentStateFingerprint(name)
	if err != nil {
		return err
	}
	if currentDigest == transaction.CandidateStateDigest {
		return s.commitRecoveredLifecycle(transaction)
	}
	if currentDigest != transaction.PreviousStateDigest {
		return newError("PLUGIN_STATE_CHANGED", "Plugin formal state changed during removal", map[string]any{"plugin": name}, nil)
	}
	visible := filepath.Join(s.root, name)
	work, err := s.resolveLifecyclePath(transaction.WorkPath)
	if err != nil {
		return err
	}
	backup := filepath.Join(work, "previous")
	if transaction.Phase == lifecyclePhaseCandidateStaged {
		digest, exists, err := packageDigestAt(visible)
		if err != nil || !exists || digest != transaction.PreviousVisibleDigest {
			return newError("PLUGIN_STATE_CHANGED", "Plugin package changed during removal", map[string]any{"plugin": name}, err)
		}
		if err := os.Rename(visible, backup); err != nil {
			return newError("PLUGIN_REMOVAL_FAILED", "stage Plugin package removal", map[string]any{"plugin": name}, err)
		}
		transaction.Phase = lifecyclePhaseVisibleSwitched
		if err := s.saveLifecycleTransaction(transaction); err != nil {
			return err
		}
	}
	if transaction.Phase == lifecyclePhaseVisibleSwitched {
		statePath := filepath.Join(s.root, ".state", name+".json")
		if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		transaction.Phase = lifecyclePhaseStateCommitted
		if err := s.saveLifecycleTransaction(transaction); err != nil {
			return err
		}
	}
	if err := s.commitRecoveredLifecycle(transaction); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadOwnedLifecycle(name, owner string) (LifecycleTransaction, error) {
	transaction, err := s.loadLifecycleTransaction(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LifecycleTransaction{}, newError("PLUGIN_ACTIVATION_NOT_FOUND", "Plugin lifecycle transaction was not found", map[string]any{"plugin": name}, err)
		}
		return LifecycleTransaction{}, err
	}
	if owner == "" || transaction.OwnerID != owner {
		return LifecycleTransaction{}, newError("PLUGIN_ACTIVATION_OWNER_MISMATCH", "Plugin lifecycle transaction owner does not match", map[string]any{"plugin": name}, nil)
	}
	return transaction, nil
}

func (s *Store) rejectPendingLifecycleUnlocked(name string) error {
	if _, err := s.loadLifecycleTransaction(name); err == nil {
		return newError("PLUGIN_ACTIVATION_IN_PROGRESS", "Plugin lifecycle transaction is already in progress", map[string]any{"plugin": name}, nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
