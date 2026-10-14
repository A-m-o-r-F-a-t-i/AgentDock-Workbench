package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

type journalRestoreEntry struct {
	Original              string `json:"original"`
	Backup                string `json:"backup,omitempty"`
	Digest                string `json:"digest,omitempty"`
	NativeVersion         int    `json:"native_metadata_version,omitempty"`
	PreparedDigest        string `json:"prepared_digest,omitempty"`
	PreparedNativeVersion int    `json:"prepared_native_metadata_version,omitempty"`
	Existed               bool   `json:"existed"`
	Stage                 string `json:"stage"`
	Phase                 string `json:"phase"`
	HadCurrent            bool   `json:"had_current"`
}

func restorePathKey(path string) string {
	path = securepath.CanonicalSystemAncestors(path)
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
func restoreWithin(path, root string) bool {
	path, root = securepath.CanonicalSystemAncestors(path), securepath.CanonicalSystemAncestors(root)
	relative, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func restoreExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func validateRestorePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == filepath.Dir(filepath.Clean(path)) {
		return fmt.Errorf("unsafe restore target: %q", path)
	}
	path = securepath.CanonicalSystemAncestors(path)
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || current != filepath.Clean(path) && !info.IsDir()) {
			return fmt.Errorf("restore refuses link or non-directory ancestor: %s", current)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return nil
}

func (j *rollbackJournal) restoreTargets() ([]journalRestoreEntry, error) {
	targets := []journalRestoreEntry{}
	seen := map[string]bool{}
	// A parent backup already owns the removal of newly-created descendants.
	for i := len(j.Created) - 1; i >= 0; i-- {
		path := j.Created[i]
		covered := false
		for _, backup := range j.Backups {
			if restoreWithin(path, backup.Original) {
				covered = true
				break
			}
		}
		if covered || seen[restorePathKey(path)] {
			continue
		}
		seen[restorePathKey(path)] = true
		targets = append(targets, journalRestoreEntry{Original: filepath.Clean(path)})
	}
	for i := len(j.Backups) - 1; i >= 0; i-- {
		backup := j.Backups[i]
		if backup.Existed && !backupNativeMetadataSupported(backup.NativeVersion) {
			return nil, fmt.Errorf("backup uses unsupported native metadata version %d; preserve it for version-matched recovery", backup.NativeVersion)
		}
		if seen[restorePathKey(backup.Original)] {
			return nil, fmt.Errorf("duplicate restore target: %s", backup.Original)
		}
		seen[restorePathKey(backup.Original)] = true
		targets = append(targets, journalRestoreEntry{Original: filepath.Clean(backup.Original), Backup: backup.Backup, Digest: backup.Digest, NativeVersion: backup.NativeVersion, Existed: backup.Existed})
	}
	for _, target := range targets {
		if err := validateRestorePath(target.Original); err != nil {
			return nil, err
		}
		if restoreWithin(j.Dir, target.Original) || restoreWithin(target.Original, j.Dir) {
			return nil, errors.New("restore target overlaps its recovery journal")
		}
		if target.Existed {
			if !restoreWithin(target.Backup, j.Dir) || restorePathKey(target.Backup) == restorePathKey(j.Dir) || !filepath.IsAbs(target.Backup) {
				return nil, errors.New("backup is outside its transaction directory")
			}
			if len(target.Digest) != 64 {
				return nil, fmt.Errorf("backup has no supported integrity record; preserve the legacy backup for manual recovery: %s", target.Backup)
			}
		}
	}
	return targets, nil
}

func (j *rollbackJournal) stageOwner(entry journalRestoreEntry) []byte {
	data, _ := json.Marshal(struct{ Transaction, Target, Stage string }{j.TransactionID, entry.Original, entry.Stage})
	return data
}
func (j *rollbackJournal) validateStage(entry journalRestoreEntry) error {
	if !filepath.IsAbs(entry.Stage) || !strings.HasPrefix(filepath.Base(entry.Stage), ".agentdock-restore-") ||
		!restoreWithin(entry.Original, filepath.Dir(entry.Stage)) || restoreWithin(entry.Stage, entry.Original) {
		return errors.New("invalid restoration staging location")
	}
	if err := validateRestorePath(entry.Stage); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(entry.Stage, "owner.json"))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 32768 {
		return errors.New("invalid restoration owner marker")
	}
	owner, err := os.ReadFile(filepath.Join(entry.Stage, "owner.json"))
	if err != nil {
		return err
	}
	if string(owner) != string(j.stageOwner(entry)) {
		return errors.New("restoration staging owner does not match")
	}
	return nil
}
func (j *rollbackJournal) updateRestore(index int, entry journalRestoreEntry) error {
	next := j.nextState()
	next.RestoreEntries[index] = entry
	return j.commit(next)
}

func normalizeRestoreEntry(entry *journalRestoreEntry, target journalRestoreEntry) (bool, error) {
	changed := false
	if entry.NativeVersion != target.NativeVersion {
		// Restore entries written before this field was added inherit the version
		// from the immutable backup record in the same journal.
		if entry.NativeVersion == 0 && target.NativeVersion != 0 {
			entry.NativeVersion = target.NativeVersion
			changed = true
		} else {
			return false, errors.New("restore entry native metadata version does not match the backup journal")
		}
	}
	if !entry.Existed {
		return changed, nil
	}
	if !backupNativeMetadataSupported(entry.NativeVersion) {
		return false, fmt.Errorf("unsupported restore entry native metadata version %d", entry.NativeVersion)
	}
	if entry.Phase == "preparing" {
		// Preparation is not committed until phase and projected integrity fields
		// are published together. Any files left by a crash are recopied below.
		if entry.PreparedDigest != "" || entry.PreparedNativeVersion != 0 {
			entry.PreparedDigest = ""
			entry.PreparedNativeVersion = 0
			changed = true
		}
		return changed, nil
	}
	if entry.PreparedDigest == "" && entry.PreparedNativeVersion == 0 {
		// Older installers copied preparation data without changing metadata
		// versions, so its integrity identity is the source backup identity.
		entry.PreparedDigest = entry.Digest
		entry.PreparedNativeVersion = entry.NativeVersion
		changed = true
	} else if entry.PreparedDigest == "" {
		return false, errors.New("restore entry has no prepared integrity digest")
	}
	if len(entry.PreparedDigest) != 64 {
		return false, errors.New("restore entry has an invalid prepared integrity digest")
	}
	if !backupNativeMetadataSupported(entry.PreparedNativeVersion) {
		return false, fmt.Errorf("unsupported prepared native metadata version %d", entry.PreparedNativeVersion)
	}
	return changed, nil
}

func (j *rollbackJournal) migrateLegacyBackups(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	next := j.nextState()
	changed := false
	for i, backup := range next.Backups {
		// Versions 1.1.0-1.1.6 persisted byte copies without an integrity
		// digest or native-metadata version. Bind those retained bytes to the
		// current platform format before any original target is moved.
		if !backup.Existed || backup.NativeVersion != 0 || backup.Digest != "" {
			continue
		}
		if !filepath.IsAbs(backup.Backup) || restorePathKey(backup.Backup) == restorePathKey(j.Dir) || !restoreWithin(backup.Backup, j.Dir) {
			return errors.New("legacy backup is outside its transaction directory")
		}
		if err := validateRestorePath(backup.Backup); err != nil {
			return fmt.Errorf("validate legacy backup %s: %w", backup.Backup, err)
		}
		digest, _, err := copyBackupTreeVersion(ctx, backup.Backup, "", backupNativeMetadataVersion, backupNativeMetadataVersion)
		if err != nil {
			return fmt.Errorf("bind legacy backup integrity %s: %w", backup.Backup, err)
		}
		next.Backups[i].Digest = digest
		next.Backups[i].NativeVersion = backupNativeMetadataVersion
		changed = true
	}
	if !changed {
		return nil
	}
	return j.commit(next)
}

func (j *rollbackJournal) prepareRestore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.RestoreStatus != "" && j.RestoreStatus != "preparing" && j.RestoreStatus != "prepared" {
		return errors.New("unsupported recovery journal status")
	}
	if err := j.migrateLegacyBackups(ctx); err != nil {
		return fmt.Errorf("migrate legacy recovery journal: %w", err)
	}
	targets, err := j.restoreTargets()
	if err != nil {
		return err
	}
	if j.RestoreStatus == "" {
		// Stage outside every affected subtree, but on the target's volume.
		for i := range targets {
			parent := filepath.Dir(targets[i].Original)
			for {
				info, statErr := os.Stat(parent)
				covered := false
				for _, target := range targets {
					if restoreWithin(parent, target.Original) {
						covered = true
						break
					}
				}
				if statErr == nil && info.IsDir() && !covered {
					break
				}
				if statErr != nil && !os.IsNotExist(statErr) {
					return statErr
				}
				if parent == filepath.Dir(parent) {
					return errors.New("cannot prepare a same-volume restore stage")
				}
				parent = filepath.Dir(parent)
			}
			stage, err := os.MkdirTemp(parent, ".agentdock-restore-")
			if err != nil {
				return err
			}
			targets[i].Stage, targets[i].Phase = stage, "preparing"
			if err := securepath.EnsurePrivate(stage); err != nil {
				return err
			}
			if err := atomicfile.Write(filepath.Join(stage, "owner.json"), j.stageOwner(targets[i]), 0o600); err != nil {
				return err
			}
		}
		next := j.nextState()
		next.RestoreEntries = targets
		next.RestoreStatus = "preparing"
		if err := j.commit(next); err != nil {
			return err
		}
	}
	if len(j.RestoreEntries) != len(targets) {
		return errors.New("restore target set changed")
	}
	for i, entry := range j.RestoreEntries {
		target := targets[i]
		if entry.Original != target.Original || entry.Backup != target.Backup || entry.Digest != target.Digest || entry.Existed != target.Existed {
			return errors.New("restore entry does not match the backup journal")
		}
		normalized, err := normalizeRestoreEntry(&entry, target)
		if err != nil {
			return err
		}
		if err := j.validateStage(entry); err != nil {
			return err
		}
		switch entry.Phase {
		case "preparing", "prepared", "moving_current", "current_saved", "installing", "verified":
		default:
			return fmt.Errorf("unsupported recovery phase %q", entry.Phase)
		}
		if normalized {
			if err := j.updateRestore(i, entry); err != nil {
				return err
			}
		}
		if entry.Phase != "preparing" {
			// A prepared copy may have changed since a previous process exited.
			// Verify it before this attempt moves any still-current target.
			if entry.Existed && (entry.Phase == "prepared" || entry.Phase == "moving_current" || entry.Phase == "current_saved") {
				digest, _, err := copyBackupTreeVersion(ctx, filepath.Join(entry.Stage, "prepared"), "", entry.PreparedNativeVersion, entry.PreparedNativeVersion)
				if err != nil {
					return fmt.Errorf("verify retained preparation: %w", err)
				}
				if digest != entry.PreparedDigest {
					return errors.New("retained preparation failed integrity verification")
				}
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Existed {
			if err := validateRestorePath(entry.Backup); err != nil {
				return err
			}
			prepared := filepath.Join(entry.Stage, "prepared")
			// Only uncommitted preparation data may be discarded and recopied.
			if err := os.RemoveAll(prepared); err != nil {
				return err
			}
			sourceDigest, preparedDigest, err := copyBackupTreeVersion(ctx, entry.Backup, prepared, entry.NativeVersion, backupNativeMetadataVersion)
			if err != nil {
				return fmt.Errorf("prepare %s: %w", entry.Original, err)
			}
			if sourceDigest != entry.Digest {
				return fmt.Errorf("backup integrity mismatch: %s", entry.Backup)
			}
			verified, _, err := copyBackupTreeVersion(ctx, prepared, "", backupNativeMetadataVersion, backupNativeMetadataVersion)
			if err != nil || verified != preparedDigest {
				return fmt.Errorf("prepared backup verification failed for %s: %w", entry.Original, errors.Join(err, errors.New("digest mismatch")))
			}
			entry.PreparedDigest = preparedDigest
			entry.PreparedNativeVersion = backupNativeMetadataVersion
		}
		entry.Phase = "prepared"
		if err := j.updateRestore(i, entry); err != nil {
			return err
		}
	}
	if j.RestoreStatus == "preparing" {
		next := j.nextState()
		next.RestoreStatus = "prepared"
		return j.commit(next)
	}
	return nil
}

// The move seam permits deterministic crash-boundary tests. Production always
// supplies os.Rename; no global mutable fault-injection state is involved.
func (j *rollbackJournal) advanceRestore(ctx context.Context, index int, move func(string, string) error) error {
	entry := j.RestoreEntries[index]
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.Phase == "verified" {
		return nil
	}
	if err := j.validateStage(entry); err != nil {
		return err
	}
	if err := validateRestorePath(entry.Original); err != nil {
		return err
	}
	prepared, previous := filepath.Join(entry.Stage, "prepared"), filepath.Join(entry.Stage, "previous")
	if entry.Phase == "prepared" {
		exists, err := restoreExists(entry.Original)
		if err != nil {
			return err
		}
		entry.HadCurrent, entry.Phase = exists, "moving_current"
		if err := j.updateRestore(index, entry); err != nil {
			return err
		}
	}
	if entry.Phase == "moving_current" {
		current, err := restoreExists(entry.Original)
		if err != nil {
			return err
		}
		saved, err := restoreExists(previous)
		if err != nil {
			return err
		}
		if entry.HadCurrent {
			if saved && current || !saved && !current {
				return errors.New("ambiguous current-target move; recovery materials retained")
			}
			if !saved {
				if err := move(entry.Original, previous); err != nil {
					return err
				}
			}
		} else if current || saved {
			return errors.New("unexpected target appeared during recovery")
		}
		entry.Phase = "current_saved"
		if err := j.updateRestore(index, entry); err != nil {
			return err
		}
	}
	if entry.Phase == "current_saved" {
		entry.Phase = "installing"
		if err := j.updateRestore(index, entry); err != nil {
			return err
		}
	}
	if entry.Phase == "installing" {
		current, err := restoreExists(entry.Original)
		if err != nil {
			return err
		}
		if entry.Existed {
			staged, err := restoreExists(prepared)
			if err != nil {
				return err
			}
			if staged && current || !staged && !current {
				return errors.New("ambiguous replacement; recovery materials retained")
			}
			if staged {
				if err := os.MkdirAll(filepath.Dir(entry.Original), 0o755); err != nil {
					return err
				}
				if err := move(prepared, entry.Original); err != nil {
					return err
				}
			}
			digest, _, err := copyBackupTreeVersion(ctx, entry.Original, "", entry.PreparedNativeVersion, entry.PreparedNativeVersion)
			if err != nil {
				return err
			}
			if digest != entry.PreparedDigest {
				return errors.New("restored target did not pass integrity verification")
			}
		} else if current {
			return errors.New("new target was not removed")
		}
		entry.Phase = "verified"
		return j.updateRestore(index, entry)
	}
	return fmt.Errorf("unsupported recovery phase %q", entry.Phase)
}

func (j *rollbackJournal) finishRestore() error {
	for _, entry := range j.RestoreEntries {
		if entry.Phase != "verified" {
			return errors.New("cannot discard an incomplete recovery")
		}
	}
	if j.RestoreStatus != "complete" {
		next := j.nextState()
		next.RestoreStatus = "complete"
		if err := j.commit(next); err != nil {
			return err
		}
	}
	for _, entry := range j.RestoreEntries {
		exists, err := restoreExists(entry.Stage)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := j.validateStage(entry); err != nil {
			return err
		}
		if err := os.RemoveAll(entry.Stage); err != nil {
			return fmt.Errorf("restored state verified; cleanup retained at %s: %w", entry.Stage, err)
		}
	}
	return nil
}
