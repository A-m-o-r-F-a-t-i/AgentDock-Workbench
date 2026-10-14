package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	structuredSnapshotTTL        = 15 * time.Minute
	structuredSnapshotMaxBytes   = 16 << 20
	structuredSnapshotCacheBytes = 64 << 20
	structuredCursorCacheEntries = 4096
)

type structuredOutputSnapshot struct {
	ID             string
	Tool           string
	ArgumentsHash  string
	Binding        activity.Binding
	Encoded        []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceURI      string
	SourceState    string
	SourceReason   string
	SourceComplete bool
	OversizedItems bool
}

type structuredOutputCursor struct {
	SnapshotID string
	PathIndex  int
	ItemIndex  int
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

type structuredOutputResume struct {
	Snapshot  structuredOutputSnapshot
	PathIndex int
	ItemIndex int
}

func structuredArgumentsHash(args map[string]any) (string, error) {
	filtered := structuredSelectorArguments(args)
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func structuredSelectorArguments(args map[string]any) map[string]any {
	filtered := make(map[string]any, len(args))
	for key, value := range args {
		switch key {
		case "cursor", "max_response_bytes", "task_id", "thread_id", "step_id", "workspace_id", "retry_of_call_id", "activity_label":
			continue
		default:
			filtered[key] = value
		}
	}
	return filtered
}

func randomStructuredID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func (r *Runtime) resumeStructuredOutput(ctx context.Context, p *preparedExecution) (Result, *structuredOutputResume, error) {
	cursor := stringArg(p.args, "cursor")
	if cursor == "" {
		return nil, nil, nil
	}
	if len(structuredOutputPaths(p.spec.Name)) == 0 {
		return nil, nil, toolError("INVALID_ARGUMENT", "This tool does not support structured result cursors.", "validation")
	}
	argumentsHash, err := structuredArgumentsHash(p.args)
	if err != nil {
		return nil, nil, toolErrorDetails("INVALID_ARGUMENT", "tool arguments cannot be fingerprinted", "validation", map[string]any{"reason": err.Error()})
	}
	now := time.Now()
	r.structuredOutputMu.Lock()
	r.pruneStructuredOutputLocked(now)
	position, found := r.structuredCursors[cursor]
	snapshot, snapshotFound := r.structuredSnapshots[position.SnapshotID]
	if found && snapshotFound {
		snapshot = cloneStructuredSnapshot(snapshot)
	}
	r.structuredOutputMu.Unlock()
	if !found || !snapshotFound || snapshot == nil || now.After(position.ExpiresAt) || now.After(snapshot.ExpiresAt) {
		return nil, nil, toolError("STRUCTURED_CURSOR_EXPIRED", "The structured result cursor expired or is unavailable. Re-run the original read-only operation to create a fresh snapshot.", "runtime")
	}
	if snapshot.Tool != p.spec.Name || snapshot.ArgumentsHash != argumentsHash {
		return nil, nil, toolError("STRUCTURED_CURSOR_MISMATCH", "The cursor does not match this tool and its original selector arguments.", "validation")
	}
	if err := r.authorizeStructuredSnapshot(ctx, snapshot.Binding, p.state.binding); err != nil {
		return nil, nil, err
	}
	result, err := decodeStructuredResult(snapshot.Encoded)
	if err != nil {
		return nil, nil, toolErrorDetails("STRUCTURED_CURSOR_INVALID", "The retained structured result can no longer be decoded.", "runtime", map[string]any{"reason": err.Error()})
	}
	return result, &structuredOutputResume{Snapshot: *snapshot, PathIndex: position.PathIndex, ItemIndex: position.ItemIndex}, nil
}

func cloneStructuredSnapshot(snapshot *structuredOutputSnapshot) *structuredOutputSnapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Encoded = append([]byte(nil), snapshot.Encoded...)
	return &copy
}

func (r *Runtime) authorizeStructuredSnapshot(ctx context.Context, original, current activity.Binding) error {
	if original.SourceOwnerKey != "" && original.SourceOwnerKey != activity.SourceOwnerKey(ctx) {
		return toolError("OUTPUT_OWNER_MISMATCH", "The retained structured result belongs to another authenticated client.", "permission")
	}
	if original.ConversationID != "" {
		if err := r.conversations.Owns(ctx, original.ConversationID); err != nil {
			return err
		}
		if err := r.checkConversationGate(ctx, original.ConversationID); err != nil {
			return err
		}
		if current.ConversationID != original.ConversationID && (current.TaskID == "" || current.TaskID != original.TaskID) {
			return toolError("OUTPUT_CONVERSATION_MISMATCH", "Resume the owning task before reading this retained structured result from another conversation.", "permission")
		}
	}
	if original.WorkspaceID != "" && current.WorkspaceID != original.WorkspaceID {
		return toolError("OUTPUT_WORKSPACE_MISMATCH", "Select the original workspace before reading this retained structured result.", "permission")
	}
	return nil
}

func (r *Runtime) storeStructuredSnapshot(snapshot *structuredOutputSnapshot) (*structuredOutputSnapshot, error) {
	id, err := randomStructuredID("sout_")
	if err != nil {
		return nil, fmt.Errorf("create structured result snapshot: %w", err)
	}
	stored := cloneStructuredSnapshot(snapshot)
	stored.ID = id
	now := time.Now()
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = now
	}
	if stored.ExpiresAt.IsZero() {
		stored.ExpiresAt = now.Add(structuredSnapshotTTL)
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	r.pruneStructuredOutputLocked(now)
	r.structuredSnapshots[id] = stored
	r.structuredSnapshotBytes += int64(len(stored.Encoded))
	r.pruneStructuredOutputLocked(now)
	if _, exists := r.structuredSnapshots[id]; !exists {
		return nil, fmt.Errorf("structured result exceeded the in-memory retention budget")
	}
	return cloneStructuredSnapshot(stored), nil
}

func (r *Runtime) newStructuredCursor(snapshotID string, position structuredPageState) (string, error) {
	id, err := randomStructuredID("scur_")
	if err != nil {
		return "", fmt.Errorf("create structured result cursor: %w", err)
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	now := time.Now()
	r.pruneStructuredOutputLocked(now)
	snapshot := r.structuredSnapshots[snapshotID]
	if snapshot == nil || now.After(snapshot.ExpiresAt) {
		return "", fmt.Errorf("structured result snapshot expired")
	}
	for cursorID, existing := range r.structuredCursors {
		if existing.SnapshotID == snapshotID && existing.PathIndex == position.PathIndex && existing.ItemIndex == position.ItemIndex {
			return cursorID, nil
		}
	}
	r.pruneStructuredCursorCapacityLocked(structuredCursorCacheEntries - 1)
	r.structuredCursors[id] = structuredOutputCursor{
		SnapshotID: snapshotID, PathIndex: position.PathIndex, ItemIndex: position.ItemIndex, CreatedAt: now, ExpiresAt: snapshot.ExpiresAt,
	}
	return id, nil
}

func (r *Runtime) markStructuredSnapshotOversized(snapshotID string) {
	if snapshotID == "" {
		return
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	if snapshot := r.structuredSnapshots[snapshotID]; snapshot != nil {
		snapshot.OversizedItems = true
	}
}

func (r *Runtime) pruneStructuredOutputLocked(now time.Time) {
	removeSnapshot := func(id string, snapshot *structuredOutputSnapshot) {
		delete(r.structuredSnapshots, id)
		r.structuredSnapshotBytes -= int64(len(snapshot.Encoded))
		for cursorID, cursor := range r.structuredCursors {
			if cursor.SnapshotID == id {
				delete(r.structuredCursors, cursorID)
			}
		}
	}
	for id, snapshot := range r.structuredSnapshots {
		if now.After(snapshot.ExpiresAt) {
			removeSnapshot(id, snapshot)
		}
	}
	for id, cursor := range r.structuredCursors {
		if now.After(cursor.ExpiresAt) {
			delete(r.structuredCursors, id)
		}
	}
	if r.structuredSnapshotBytes > structuredSnapshotCacheBytes {
		type candidate struct {
			id      string
			created time.Time
		}
		ordered := make([]candidate, 0, len(r.structuredSnapshots))
		for id, snapshot := range r.structuredSnapshots {
			ordered = append(ordered, candidate{id: id, created: snapshot.CreatedAt})
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].created.Before(ordered[j].created) })
		for _, item := range ordered {
			if r.structuredSnapshotBytes <= structuredSnapshotCacheBytes {
				break
			}
			if snapshot := r.structuredSnapshots[item.id]; snapshot != nil {
				removeSnapshot(item.id, snapshot)
			}
		}
	}
	r.pruneStructuredCursorCapacityLocked(structuredCursorCacheEntries)
}

func (r *Runtime) pruneStructuredCursorCapacityLocked(limit int) {
	if limit < 0 {
		limit = 0
	}
	if len(r.structuredCursors) <= limit {
		return
	}
	type cursorCandidate struct {
		id      string
		created time.Time
	}
	cursors := make([]cursorCandidate, 0, len(r.structuredCursors))
	for id, cursor := range r.structuredCursors {
		cursors = append(cursors, cursorCandidate{id: id, created: cursor.CreatedAt})
	}
	sort.Slice(cursors, func(i, j int) bool {
		if cursors[i].created.Equal(cursors[j].created) {
			return cursors[i].id < cursors[j].id
		}
		return cursors[i].created.Before(cursors[j].created)
	})
	for _, item := range cursors[:len(cursors)-limit] {
		delete(r.structuredCursors, item.id)
	}
}
