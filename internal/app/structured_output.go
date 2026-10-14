package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const (
	defaultStructuredResponseBytes = 24 << 10
	structuredMetadataReserveBytes = 1 << 10
	structuredSnapshotTTL          = 15 * time.Minute
	structuredSnapshotMaxBytes     = 16 << 20
	structuredSnapshotCacheBytes   = 64 << 20
	structuredCursorCacheEntries   = 4096
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

type structuredOutputPath []string

type structuredPageState struct {
	PathIndex int
	ItemIndex int
}

func structuredOutputPaths(name string) []structuredOutputPath {
	switch name {
	case "search_text":
		return []structuredOutputPath{{"matches"}}
	case "list_dir":
		return []structuredOutputPath{{"entries"}, {"skipped_paths"}}
	case "mcp_tool_search":
		return []structuredOutputPath{{"tools"}, {"catalogs"}, {"builtin_tools"}}
	case "mcp_tool_list":
		return []structuredOutputPath{{"tools"}, {"mcp_catalog", "tools"}}
	case "mcp_tool_inspect":
		return []structuredOutputPath{{"tools"}, {"errors"}, {"catalogs"}, {"builtin_tools"}}
	case "agentdock_context":
		return []structuredOutputPath{
			{"rules"}, {"warnings"}, {"instruction_files", "files"},
			{"skills"}, {"common_skills", "items"},
			{"plugins"}, {"dynamic_mcp"}, {"acp", "profiles"}, {"workflow_templates"},
			{"recall", "items"}, {"tasks", "items"},
		}
	default:
		return nil
	}
}

func structuredOutputBudget(name string, args map[string]any) int {
	fallback := defaultStructuredResponseBytes
	value := integerArgument(args["max_response_bytes"])
	if value == 0 {
		value = fallback
	}
	if value < toolcontract.StructuredResponseMinBytes {
		value = toolcontract.StructuredResponseMinBytes
	}
	if value > toolcontract.StructuredResponseMaxBytes {
		value = toolcontract.StructuredResponseMaxBytes
	}
	return value
}

func structuredExecutionEnvelopeReserve(name string) int {
	if name == "agentdock_context" {
		return 4 << 10
	}
	return 1 << 10
}

func integerArgument(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		if typed <= uint64(^uint(0)>>1) {
			return int(typed)
		}
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	}
	return 0
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

func decodeStructuredResult(data []byte) (Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneStructuredResult(result Result) (Result, []byte, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	cloned, err := decodeStructuredResult(encoded)
	return cloned, encoded, err
}

func sourceResultComplete(result Result) (bool, string) {
	for _, key := range []string{"truncated", "partial", "stdout_truncated", "stderr_truncated"} {
		if result[key] == true {
			return false, "The source operation returned a bounded or partial result before model-facing pagination."
		}
	}
	if diagnostics, ok := result["context_diagnostics"].(map[string]any); ok {
		if complete, exists := diagnostics["complete"].(bool); exists && !complete {
			return false, "AgentDock context generation was incomplete before model-facing pagination."
		}
	}
	if complete, exists := result["complete"].(bool); exists && !complete {
		return false, "The source operation reported an incomplete result before model-facing pagination."
	}
	if result["status"] == "running" {
		return false, "The source operation was still running when this result snapshot was created."
	}
	return true, ""
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

func (r *Runtime) applyStructuredOutputPolicy(p *preparedExecution, result Result) Result {
	paths := structuredOutputPaths(p.spec.Name)
	if len(paths) == 0 || result == nil || p.state.binding.ParentCallID != "" {
		return result
	}
	full, encoded, err := cloneStructuredResult(result)
	if err != nil {
		return result
	}
	budget := structuredOutputBudget(p.spec.Name, p.args)
	businessBudget := max(1<<10, budget-structuredExecutionEnvelopeReserve(p.spec.Name))
	if p.structuredResume == nil && len(encoded) <= businessBudget {
		return result
	}

	var snapshot structuredOutputSnapshot
	position := structuredPageState{}
	if p.structuredResume != nil {
		snapshot = p.structuredResume.Snapshot
		position = structuredPageState{PathIndex: p.structuredResume.PathIndex, ItemIndex: p.structuredResume.ItemIndex}
		encoded = append([]byte(nil), snapshot.Encoded...)
		full, err = decodeStructuredResult(encoded)
		if err != nil {
			return Result{"structured_output": map[string]any{"truncated": true, "continuation_unavailable": "The retained structured result could not be decoded."}}
		}
	} else {
		complete, reason := sourceResultComplete(full)
		snapshot = structuredOutputSnapshot{
			Tool: p.spec.Name, Binding: structuredSnapshotBinding(p, full), Encoded: append([]byte(nil), encoded...),
			CreatedAt: time.Now(), ExpiresAt: time.Now().Add(structuredSnapshotTTL),
			SourceComplete: complete, SourceReason: reason,
		}
		snapshot.ArgumentsHash, _ = structuredArgumentsHash(p.args)
		snapshot.SourceState = "complete"
		if !complete {
			snapshot.SourceState = "partial"
		}
		r.retainStructuredActivitySource(p, full, &snapshot)
		if len(encoded) <= structuredSnapshotMaxBytes {
			if stored, storeErr := r.storeStructuredSnapshot(&snapshot); storeErr == nil {
				snapshot = *stored
			} else {
				snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, storeErr.Error())
			}
		} else {
			snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, "Exact cursor retention was skipped because the serialized result exceeded 16 MiB.")
		}
	}

	hardBusinessBudget := toolcontract.StructuredResponseMaxBytes - structuredExecutionEnvelopeReserve(p.spec.Name)
	page, next, returned, minimumBytes, oversized := buildStructuredPage(full, paths, position, businessBudget, hardBusinessBudget)
	hasMore := structuredPageHasMore(full, paths, next)
	deliveryIncomplete := hasMore || oversized != nil
	if oversized != nil {
		snapshot.OversizedItems = true
		r.markStructuredSnapshotOversized(snapshot.ID)
	}
	meta := map[string]any{
		"truncated":             deliveryIncomplete || position.PathIndex != 0 || position.ItemIndex != 0,
		"budget_bytes":          budget,
		"business_budget_bytes": businessBudget,
		"source_bytes":          len(encoded),
		"retained_bytes":        len(snapshot.Encoded),
		"dropped_before_bytes":  0,
		"source_complete":       snapshot.SourceComplete,
		"source_state":          snapshot.SourceState,
		"returned_items":        returned,
		"cursor_expires_at":     snapshot.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if snapshot.SourceURI != "" {
		meta["source"] = snapshot.SourceURI
	}
	if snapshot.SourceReason != "" {
		meta["source_reason"] = snapshot.SourceReason
	}
	if snapshot.OversizedItems {
		meta["oversized_items_omitted"] = true
	}
	if minimumBytes > 0 {
		meta["minimum_response_bytes"] = minimumBytes + structuredExecutionEnvelopeReserve(p.spec.Name)
	}
	if oversized != nil {
		meta["oversized_item"] = oversized
	}
	if hasMore && snapshot.ID != "" {
		if cursor, cursorErr := r.newStructuredCursor(snapshot.ID, next); cursorErr == nil {
			page["next_cursor"] = cursor
		} else {
			meta["continuation_unavailable"] = cursorErr.Error()
		}
	} else if hasMore {
		meta["continuation_unavailable"] = "The exact structured result was not retained; use the activity source when available."
	}
	page["structured_output"] = meta
	if p.spec.Name == "agentdock_context" {
		setContextDeliveryComplete(page, !deliveryIncomplete && !snapshot.OversizedItems)
	}
	for range 3 {
		marshaled, marshalErr := json.Marshal(page)
		if marshalErr != nil || meta["page_bytes"] == len(marshaled) {
			break
		}
		meta["page_bytes"] = len(marshaled)
	}
	return page
}

func setContextDeliveryComplete(page Result, delivered bool) {
	diagnostics, ok := page["context_diagnostics"].(map[string]any)
	if !ok {
		return
	}
	sourceComplete, _ := diagnostics["complete"].(bool)
	diagnostics["complete"] = sourceComplete && delivered
}

func structuredSnapshotBinding(p *preparedExecution, result Result) activity.Binding {
	binding := p.state.binding
	if p.spec.Name != "agentdock_context" {
		return binding
	}
	if workspaceID := stringArg(result, "workspace_id"); workspaceID != "" {
		binding.WorkspaceID = workspaceID
	}
	var state activity.ConversationState
	if remarshal(result["conversation_state"], &state) == nil {
		binding.TaskID = state.ActiveTaskID
		binding.ThreadID = state.ActiveTaskThreadID
		if state.WorkspaceID != "" {
			binding.WorkspaceID = state.WorkspaceID
		}
	}
	return binding
}

func appendStructuredReason(existing, addition string) string {
	if existing == "" {
		return addition
	}
	if addition == "" {
		return existing
	}
	return existing + " " + addition
}

func (r *Runtime) retainStructuredActivitySource(p *preparedExecution, full Result, snapshot *structuredOutputSnapshot) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(ctx, full, snapshot.SourceState, r.executionRedactor(p.args))
	if payload.Ref == "" {
		snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, payload.Reason)
		return
	}
	if snapshot.SourceReason != "" {
		payload.Reason = snapshot.SourceReason
	}
	if _, err := r.activity.Append(ctx, activity.Event{Binding: p.state.binding, Kind: "call.payload", ToolName: p.spec.Name, OutputSource: payload}); err != nil {
		snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, "The activity source could not be journaled.")
		return
	}
	snapshot.SourceURI = "activity://call/" + p.state.binding.CallID + "/source"
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

func buildStructuredPage(full Result, paths []structuredOutputPath, start structuredPageState, budget, hardBudget int) (Result, structuredPageState, int, int, map[string]any) {
	page, _, err := cloneStructuredResult(full)
	if err != nil {
		return full, start, 0, 0, nil
	}
	for _, path := range paths {
		setStructuredPath(page, path, []any{})
	}
	position := normalizeStructuredPosition(full, paths, start)
	returned := 0
	minimumBytes := 0
	var oversized map[string]any
	for position.PathIndex < len(paths) {
		path := paths[position.PathIndex]
		items := structuredPathItems(full, path)
		if position.ItemIndex >= len(items) {
			position.PathIndex++
			position.ItemIndex = 0
			continue
		}
		current := append([]any{}, structuredPathItems(page, path)...)
		trialItems := append(current, items[position.ItemIndex])
		setStructuredPath(page, path, trialItems)
		encoded, marshalErr := json.Marshal(page)
		if marshalErr == nil && len(encoded)+structuredMetadataReserveBytes <= budget {
			returned++
			position.ItemIndex++
			continue
		}
		setStructuredPath(page, path, current)
		if returned > 0 {
			break
		}
		required := len(encoded) + structuredMetadataReserveBytes
		if marshalErr == nil && required <= hardBudget {
			minimumBytes = required
			break
		}
		oversized = map[string]any{
			"collection": structuredPathName(path), "index": position.ItemIndex,
			"serialized_bytes": max(0, len(encoded)),
			"reason":           "The item exceeds the maximum semantic page size; read it from the retained activity source.",
		}
		position.ItemIndex++
		break
	}
	position = normalizeStructuredPosition(full, paths, position)
	return page, position, returned, minimumBytes, oversized
}

func normalizeStructuredPosition(full Result, paths []structuredOutputPath, position structuredPageState) structuredPageState {
	if position.PathIndex < 0 {
		position.PathIndex = 0
	}
	if position.ItemIndex < 0 {
		position.ItemIndex = 0
	}
	for position.PathIndex < len(paths) {
		if position.ItemIndex < len(structuredPathItems(full, paths[position.PathIndex])) {
			break
		}
		position.PathIndex++
		position.ItemIndex = 0
	}
	return position
}

func structuredPageHasMore(full Result, paths []structuredOutputPath, position structuredPageState) bool {
	position = normalizeStructuredPosition(full, paths, position)
	return position.PathIndex < len(paths)
}

func structuredPathItems(root map[string]any, path structuredOutputPath) []any {
	value, ok := structuredPathValue(root, path)
	if !ok {
		return nil
	}
	items, _ := value.([]any)
	return items
}

func structuredPathValue(root map[string]any, path structuredOutputPath) (any, bool) {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setStructuredPath(root map[string]any, path structuredOutputPath, value any) bool {
	if len(path) == 0 {
		return false
	}
	current := root
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	if _, exists := current[path[len(path)-1]]; !exists {
		return false
	}
	current[path[len(path)-1]] = value
	return true
}

func structuredPathName(path structuredOutputPath) string {
	if len(path) == 0 {
		return ""
	}
	name := path[0]
	for _, part := range path[1:] {
		name += "." + part
	}
	return name
}
