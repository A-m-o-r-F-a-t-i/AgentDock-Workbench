package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
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
	OutputFailure         *ToolError
	PermissionRevision    uint64
	ID                    string
	Tool                  string
	ArgumentsHash         string
	SelectorArguments     map[string]any
	ResumeArguments       map[string]any
	Binding               activity.Binding
	Encoded               []byte
	CreatedAt             time.Time
	ExpiresAt             time.Time
	SourceURI             string
	SourceState           string
	SourceReason          string
	SourceComplete        bool
	OversizedItems        bool
	Required              bool
	ContextStamp          string
	ContextGuardStamp     string
	ContextRevision       string
	ContextWorkdir        string
	NextPage              int
	Delivered             bool
	FilePath              string
	FileRuntime           string
	FileDistribution      string
	FileRevision          string
	FileSequenceStart     int64
	FileByteStart         int64
	FileByteEnd           int64
	FileRangeEnd          int64
	FileFullRange         bool
	FileDeliveredOffset   int64
	PageBudget            int
	PageCount             int
	SourceDeliveredOffset int64
}

type structuredOutputCursor struct {
	SnapshotID string
	PathIndex  int
	ItemIndex  int
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Consumed   bool
	NextCursor string
}

type structuredOutputResume struct {
	Snapshot  structuredOutputSnapshot
	PathIndex int
	ItemIndex int
	Reference bool
	Cursor    string
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
		case "cursor", "max_response_bytes", "task_id", "thread_id", "step_id", "workspace_id", "retry_of_call_id", "activity_label", "force_refresh", "known_context_revision":
			continue
		default:
			filtered[key] = value
		}
	}
	return filtered
}

func structuredResumeArguments(args map[string]any) map[string]any {
	resume := structuredSelectorArguments(args)
	if value, exists := args["max_response_bytes"]; exists {
		resume["max_response_bytes"] = value
	}
	return resume
}

func structuredSelectorSubsetMatches(expected, provided map[string]any) bool {
	for key, value := range provided {
		original, exists := expected[key]
		if !exists || !structuredArgumentEqual(original, value) {
			return false
		}
	}
	return true
}

func structuredArgumentEqual(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func restoreStructuredResumeArguments(args, stored map[string]any) {
	for key, value := range stored {
		if _, exists := args[key]; !exists {
			args[key] = value
		}
	}
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
	if len(structuredOutputPaths(p.spec.Name)) == 0 && p.spec.Name != "task_manage" {
		return nil, nil, toolError("INVALID_ARGUMENT", "This tool does not support structured result cursors.", "validation")
	}
	argumentsHash, err := structuredArgumentsHash(p.args)
	if err != nil {
		return nil, nil, toolErrorDetails("INVALID_ARGUMENT", "tool arguments cannot be fingerprinted", "validation", map[string]any{"reason": err.Error()})
	}
	now := time.Now()
	r.structuredOutputMu.Lock()
	position, found := r.structuredCursors[cursor]
	snapshot, snapshotFound := r.structuredSnapshots[position.SnapshotID]
	if found && snapshotFound {
		snapshot = cloneStructuredSnapshot(snapshot)
	}
	r.pruneStructuredOutputLocked(now)
	r.structuredOutputMu.Unlock()
	if !found || !snapshotFound || snapshot == nil || now.After(position.ExpiresAt) || now.After(snapshot.ExpiresAt) {
		p.state.executed = false
		if found && snapshotFound && snapshot != nil && snapshot.Tool == p.spec.Name &&
			structuredSelectorSubsetMatches(snapshot.SelectorArguments, structuredSelectorArguments(p.args)) {
			if result, resume, err := r.expiredContextDetails(ctx, p, snapshot); result != nil || err != nil {
				return result, resume, err
			}
		}
		return nil, nil, toolErrorDetails("STRUCTURED_CURSOR_EXPIRED", "The retained detail cursor expired or is unavailable. Request a fresh snapshot only if those details are needed.", "validation", map[string]any{
			"execution_performed": false, "executed": false, "effect_state": "none", "retry_safe": true,
		})
	}
	providedSelectors := structuredSelectorArguments(p.args)
	selectorsMatch := snapshot.ArgumentsHash == argumentsHash
	if snapshot.SelectorArguments != nil {
		selectorsMatch = structuredSelectorSubsetMatches(snapshot.SelectorArguments, providedSelectors)
	}
	if snapshot.Tool != p.spec.Name || !selectorsMatch {
		p.state.executed = false
		retry := maps.Clone(snapshot.ResumeArguments)
		if retry == nil {
			retry = map[string]any{}
		}
		retry["cursor"] = cursor
		return nil, nil, toolErrorDetails(
			"STRUCTURED_CURSOR_MISMATCH",
			"The cursor does not match this tool or an explicitly supplied selector changed. Reuse the retained continuation arguments without changing selectors.",
			"validation",
			map[string]any{
				"retryable": true, "retry_safe": true,
				"executed": false, "execution_performed": false,
				"operation_status": "failed", "effect_state": "none",
				"required_action": map[string]any{
					"action": "continue_structured_output", "tool": snapshot.Tool,
					"arguments": retry,
					"text":      "Retry once with these retained selector arguments and cursor.",
				},
			},
		)
	}
	restoreStructuredResumeArguments(p.args, snapshot.ResumeArguments)
	if err := r.authorizeStructuredSnapshot(ctx, snapshot.Binding, p.state.binding); err != nil {
		return nil, nil, err
	}
	isContext := snapshot.Tool == "agentdock_context" || snapshot.Tool == "workspace_context"
	if isContext && snapshot.PermissionRevision != p.decision.Revision {
		r.discardStructuredSnapshot(snapshot.ID)
		return nil, nil, toolError("CONTEXT_REVISION_CHANGED", "The permission contract changed during paging; request the current Context snapshot.", "conflict")
	}
	if isContext {
		stamp, stampErr := r.contextContinuationStamp(ctx, snapshot.ContextWorkdir, snapshot.Binding)
		if stampErr != nil {
			return nil, nil, stampErr
		}
		if snapshot.ContextGuardStamp == "" || stamp != snapshot.ContextGuardStamp {
			r.discardStructuredSnapshot(snapshot.ID)
			return nil, nil, toolError("CONTEXT_REVISION_CHANGED", "Context rules or workspace changed during paging. Call agentdock_context once to receive the current snapshot.", "conflict")
		}
	}
	if position.Consumed && snapshot.OutputFailure == nil {
		p.state.executed = false
		receipt := r.structuredCursorReplayReceipt(snapshot, position)
		return receipt, &structuredOutputResume{
			Snapshot: *snapshot, ItemIndex: position.ItemIndex, Reference: true, Cursor: cursor,
		}, nil
	}
	result, err := decodeStructuredResult(snapshot.Encoded)
	if err != nil {
		return nil, nil, toolErrorDetails("STRUCTURED_CURSOR_INVALID", "The retained structured result can no longer be decoded.", "runtime", map[string]any{"reason": err.Error()})
	}
	resume := &structuredOutputResume{Snapshot: *snapshot, PathIndex: position.PathIndex, ItemIndex: position.ItemIndex, Cursor: cursor}
	if snapshot.OutputFailure != nil {
		return result, resume, cloneOutputFailure(snapshot.OutputFailure)
	}
	return result, resume, nil
}

func (r *Runtime) structuredCursorReplayReceipt(snapshot *structuredOutputSnapshot, position structuredOutputCursor) Result {
	nextCursor := position.NextCursor
	sequenceComplete := nextCursor == ""
	receipt := Result{
		"already_delivered": true,
		"replayed":          true,
		"snapshot_id":       snapshot.ID,
		"has_more":          nextCursor != "",
		"source_complete":   snapshot.SourceComplete,
		"operation": map[string]any{
			"state": "completed", "ok": true, "executed": false,
		},
		"capture": map[string]any{
			"state": "complete", "complete": snapshot.SourceComplete,
		},
		"delivery": map[string]any{
			"class": "compact_receipt", "sequence_complete": sequenceComplete,
			"continuation_required": false, "continuation_available": nextCursor != "",
		},
		"output_class": "compact_receipt",
	}
	if snapshot.Required {
		receipt["page_index"] = position.ItemIndex + 1
		receipt["page_count"] = snapshot.PageCount
	}
	if snapshot.ContextRevision != "" {
		receipt["context_revision"] = snapshot.ContextRevision
	}
	if nextCursor == "" {
		receipt["required_action"] = map[string]any{
			"action": "stop", "required": false,
			"text": "This retained result page was already delivered and the sequence has no unread page.",
		}
		return receipt
	}
	arguments := map[string]any{}
	if !isContextTool(snapshot.Tool) {
		arguments = maps.Clone(snapshot.ResumeArguments)
		if arguments == nil {
			arguments = map[string]any{}
		}
	}
	arguments["cursor"] = nextCursor
	receipt["next_cursor"] = nextCursor
	receipt["next_arguments"] = arguments
	receipt["required_action"] = map[string]any{
		"action": "continue_structured_output", "tool": snapshot.Tool,
		"arguments": arguments, "required": snapshot.Required,
		"text": "The requested page was already delivered. Continue from this next unread cursor without replaying the old cursor.",
	}
	return receipt
}

func cloneStructuredSnapshot(snapshot *structuredOutputSnapshot) *structuredOutputSnapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Encoded = append([]byte(nil), snapshot.Encoded...)
	copy.SelectorArguments = maps.Clone(snapshot.SelectorArguments)
	copy.ResumeArguments = maps.Clone(snapshot.ResumeArguments)
	copy.OutputFailure = cloneOutputFailure(snapshot.OutputFailure)
	return &copy
}

// Context roots keep their immutable entry binding in the journal, while their
// validated output can select a new workspace. Authorize that exact retained
// source in its selected workspace without allowing arbitrary cross-workspace
// output reads or accepting a caller-supplied workspace identity.
func (r *Runtime) ownsSelectedContextSource(source string, binding activity.Binding) bool {
	if binding.SourceOwnerKey == "" || binding.ConversationID == "" || binding.WorkspaceID == "" {
		return false
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	now := time.Now()
	for _, snapshot := range r.structuredSnapshots {
		if snapshot != nil && snapshot.Tool == "agentdock_context" && snapshot.SourceComplete &&
			snapshot.SourceURI == source && !now.After(snapshot.ExpiresAt) &&
			snapshot.Binding.SourceOwnerKey == binding.SourceOwnerKey &&
			snapshot.Binding.ConversationID == binding.ConversationID &&
			snapshot.Binding.WorkspaceID == binding.WorkspaceID {
			return true
		}
	}
	return false
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
		// Unattributed MCP calls do not persist a conversation workspace between
		// requests. An exact cursor remains bound to the authenticated owner and
		// immutable snapshot, so an empty current workspace may continue it.
		ownerScopedUnattributed := original.ConversationID == "" && current.WorkspaceID == "" &&
			original.SourceOwnerKey != "" && original.SourceOwnerKey == activity.SourceOwnerKey(ctx)
		if !ownerScopedUnattributed {
			return r.retainedWorkspaceMismatch(ctx, original.WorkspaceID, current.WorkspaceID, original.SourceOwnerKey != "" && original.SourceOwnerKey == activity.SourceOwnerKey(ctx))
		}
	}
	return nil
}

func (r *Runtime) storeStructuredSnapshot(snapshot *structuredOutputSnapshot) (*structuredOutputSnapshot, error) {
	if len(snapshot.Encoded) > structuredSnapshotMaxBytes {
		return nil, toolError("OUTPUT_SNAPSHOT_TOO_LARGE", "The complete snapshot exceeds the retained output limit; read the explicit source when available.", "resource_limit")
	}

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
	if r.structuredSnapshots == nil {
		r.structuredSnapshots = map[string]*structuredOutputSnapshot{}
	}
	if r.structuredCursors == nil {
		r.structuredCursors = map[string]structuredOutputCursor{}
	}
	if stored.ContextRevision != "" {
		for _, existing := range r.structuredSnapshots {
			if existing.OutputFailure == nil && stored.OutputFailure == nil && existing.Tool == stored.Tool &&
				existing.ContextRevision == stored.ContextRevision && filepath.Clean(existing.ContextWorkdir) == filepath.Clean(stored.ContextWorkdir) &&
				existing.ContextGuardStamp == stored.ContextGuardStamp && existing.PermissionRevision == stored.PermissionRevision && sameContextScope(existing.Binding, stored.Binding) {
				existing.ContextStamp = stored.ContextStamp
				return cloneStructuredSnapshot(existing), nil
			}
		}
	}
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
