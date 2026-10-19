package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	resultStoreCapacity        = 512
	resultStoreTTL             = 30 * time.Minute
	resultCallProjectionBudget = 96 << 10
	resultRootProjectionBudget = 256 << 10
	resultRawReadLimit         = 8
	resultRawDefaultBytes      = 8000
	resultRawMaximumBytes      = 64 << 10
)

type resultCursorState struct {
	CallID    string
	PageIndex int
}

// The marker serializes as a boolean, but a JSON response from a remote server
// cannot manufacture the in-process type used to identify a completed host read.
type successfulResultRead bool

// ResultReadSucceeded recognizes a completed host continuation, never an
// identically shaped metadata object supplied by an upstream JSON response.
func ResultReadSucceeded(result map[string]any) bool {
	marker, ok := resultMap(result["host_page"])["read_succeeded"].(successfulResultRead)
	return ok && bool(marker)
}

type resultRootBudget struct {
	Used      int
	TouchedAt time.Time
}

type storedMCPResult struct {
	CallID         string
	Name           string
	Binding        activity.Binding
	ScopeKey       string
	Raw            map[string]any
	RawBytes       []byte
	RawReference   string
	Failure        map[string]any
	FailureBytes   int
	Normalized     map[string]any
	ContentKinds   []string
	ParsedJSONText bool
	Pages          []resultPage
	NextPage       int
	NextCursor     string
	LastCursor     string
	LastPage       int
	ProjectedBytes int
	RawHighWater   int
	RawReads       int
	SupersededBy   string
	CreatedAt      time.Time
	TouchedAt      time.Time
}

type resultStore struct {
	mu          sync.Mutex
	entries     map[string]*storedMCPResult
	cursors     map[string]resultCursorState
	latestScope map[string]string
	rootBudgets map[string]resultRootBudget
	now         func() time.Time
}

func (s *resultStore) store(binding activity.Binding, name string, raw, normalized map[string]any, contentKinds []string, parsedJSONText bool) (Result, error) {
	failure := retainResultFailure(raw)
	failureBytes := resultFailureProjectionBytes(failure)
	pages, err := buildResultPages(normalized, resultPayloadBudget(failureBytes))
	if err != nil {
		return nil, toolErrorCause("MCP_RESULT_ENCODING_FAILED", "encode normalized MCP result", "runtime", nil, err)
	}
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, toolErrorCause("MCP_RESULT_ENCODING_FAILED", "encode raw MCP result", "runtime", nil, err)
	}
	now := s.currentTime()
	callID := binding.CallID
	if callID == "" {
		callID = "mcp_call_" + randomResultToken()
	}
	entry := &storedMCPResult{
		CallID: callID, Name: name, Binding: binding, ScopeKey: resultScopeKey(binding, callID),
		Raw: cloneResultMap(raw), RawBytes: rawBytes, Normalized: cloneResultMap(normalized),
		Failure: failure, FailureBytes: failureBytes,
		ContentKinds: append([]string{}, contentKinds...), ParsedJSONText: parsedJSONText,
		Pages: pages, NextPage: 1, LastPage: -1, CreatedAt: now, TouchedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if previousID := s.latestScope[entry.ScopeKey]; previousID != "" && previousID != callID {
		if previous := s.entries[previousID]; previous != nil && previous.NextPage < len(previous.Pages) {
			previous.SupersededBy = callID
		}
	}
	if existing := s.entries[callID]; existing != nil {
		s.deleteEntryLocked(existing.CallID)
	}
	s.entries[callID] = entry
	s.latestScope[entry.ScopeKey] = callID
	if len(s.entries) > resultStoreCapacity {
		s.deleteOldestLocked()
	}
	if len(pages) > 1 {
		entry.NextCursor = s.newCursorLocked(callID, 1)
	}
	if !s.consumeProjectionBudgetLocked(entry, pages[0].Bytes+failureBytes, true) {
		return resultBudgetProjection(entry, "root_task"), nil
	}
	return resultPageProjection(entry, 0, entry.NextCursor, false), nil
}

func (s *resultStore) continueResult(ctxBinding activity.Binding, callID, cursor string) (Result, error) {
	now := s.currentTime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	entry := s.entries[callID]
	if entry == nil {
		return nil, toolErrorDetails("MCP_RESULT_NOT_FOUND", "MCP result is unavailable or expired", "validation", map[string]any{"call_id": callID})
	}
	if !resultBindingAllowed(entry.Binding, ctxBinding) {
		return nil, toolErrorDetails("MCP_RESULT_SCOPE_MISMATCH", "MCP result belongs to a different execution scope", "auth", map[string]any{"call_id": callID})
	}
	entry.TouchedAt = now
	if entry.SupersededBy != "" {
		return nil, toolErrorDetails("MCP_RESULT_SUPERSEDED", "A newer MCP tool result superseded this default continuation", "validation", map[string]any{"call_id": callID, "superseded_by": entry.SupersededBy})
	}
	state, ok := s.cursors[cursor]
	if !ok || state.CallID != callID {
		return nil, toolErrorDetails("MCP_RESULT_CURSOR_EXPIRED", "MCP host result cursor is unknown, expired or belongs to another call", "validation", map[string]any{"call_id": callID, "cursor": cursor, "resume": entry.NextCursor})
	}
	if cursor == entry.LastCursor && state.PageIndex == entry.LastPage {
		return withResultFailure(Result{
			"call_id": callID, "result_call_id": callID, "cursor": cursor, "replayed": true, "content_returned": false,
			"complete": entry.NextPage >= len(entry.Pages), "resume_cursor": entry.NextCursor,
		}, entry), nil
	}
	if state.PageIndex < entry.NextPage {
		return nil, toolErrorDetails("RESULT_CURSOR_REGRESSION", "MCP host result cursor moved behind the consumed high-water mark", "validation", map[string]any{
			"call_id": callID, "requested": cursor, "requested_page": state.PageIndex,
			"resume": entry.NextCursor, "consumed_until_page": entry.NextPage - 1, "content_returned": false,
		})
	}
	if state.PageIndex > entry.NextPage {
		return nil, toolErrorDetails("MCP_RESULT_CURSOR_GAP", "MCP host result cursor skipped an unconsumed page", "validation", map[string]any{"call_id": callID, "requested": cursor, "resume": entry.NextCursor})
	}
	if entry.NextPage >= len(entry.Pages) {
		return nil, toolErrorDetails("RESULT_ALREADY_CONSUMED", "All projected pages for this MCP result were already consumed", "validation", map[string]any{"call_id": callID, "complete": true})
	}
	pageIndex := entry.NextPage
	page := entry.Pages[pageIndex]
	if !s.consumeProjectionBudgetLocked(entry, page.Bytes+entry.FailureBytes, false) {
		return nil, resultBudgetError(entry)
	}
	entry.LastCursor, entry.LastPage = cursor, pageIndex
	entry.NextPage++
	entry.NextCursor = ""
	if entry.NextPage < len(entry.Pages) {
		entry.NextCursor = s.newCursorLocked(callID, entry.NextPage)
	}
	return resultPageProjection(entry, pageIndex, entry.NextCursor, false), nil
}

func (s *resultStore) rawResult(ctxBinding activity.Binding, request ResultRawRequest) (Result, error) {
	now := s.currentTime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	entry := s.entries[request.CallID]
	if entry == nil {
		return nil, toolErrorDetails("MCP_RESULT_NOT_FOUND", "MCP result is unavailable or expired", "validation", map[string]any{"call_id": request.CallID})
	}
	if !resultBindingAllowed(entry.Binding, ctxBinding) {
		return nil, toolErrorDetails("MCP_RESULT_SCOPE_MISMATCH", "MCP result belongs to a different execution scope", "auth", map[string]any{"call_id": request.CallID})
	}
	entry.TouchedAt = now
	if entry.RawReads >= resultRawReadLimit {
		return nil, toolErrorDetails("MCP_RAW_RESULT_READ_LIMIT", "Raw MCP result diagnostic read limit reached", "validation", map[string]any{"call_id": request.CallID, "read_limit": resultRawReadLimit})
	}
	offset := request.Offset
	if offset < 0 || offset > len(entry.RawBytes) {
		return nil, toolErrorDetails("MCP_RAW_RESULT_OFFSET_INVALID", "Raw MCP result offset is outside the retained response", "validation", map[string]any{"call_id": request.CallID, "offset": offset, "size": len(entry.RawBytes)})
	}
	if offset < len(entry.RawBytes) && entry.RawBytes[offset]&0xC0 == 0x80 {
		return nil, toolErrorDetails("MCP_RAW_RESULT_OFFSET_INVALID", "Raw offset must start at a UTF-8 boundary", "validation", map[string]any{"offset": offset})
	}
	if request.Restart {
		entry.RawHighWater = offset
	} else if offset < entry.RawHighWater {
		return nil, toolErrorDetails("MCP_RAW_RESULT_OFFSET_REGRESSION", "Raw MCP result offset moved behind the diagnostic high-water mark; set restart=true for an explicit reread", "validation", map[string]any{"call_id": request.CallID, "requested": offset, "resume": entry.RawHighWater})
	} else if offset > entry.RawHighWater {
		return nil, toolErrorDetails("MCP_RAW_RESULT_OFFSET_GAP", "Raw MCP result offset skipped unread diagnostic bytes", "validation", map[string]any{"call_id": request.CallID, "requested": offset, "resume": entry.RawHighWater})
	}
	limit := boundedInt(request.Limit, resultRawDefaultBytes, 1, resultRawMaximumBytes)
	end := min(len(entry.RawBytes), offset+limit)
	for end > offset && end < len(entry.RawBytes) && entry.RawBytes[end]&0xC0 == 0x80 {
		end--
	}
	if end == offset && end < len(entry.RawBytes) {
		end = offset + 1
		for end < len(entry.RawBytes) && entry.RawBytes[end]&0xC0 == 0x80 {
			end++
		}
	}
	// A raw fragment is still sent inside JSON; account for escaping before advancing.
	for end > offset {
		encoded, _ := json.Marshal(string(entry.RawBytes[offset:end]))
		if len(encoded) <= resultPayloadBudget(entry.FailureBytes) {
			break
		}
		end = offset + (end-offset)/2
		for end > offset && end < len(entry.RawBytes) && entry.RawBytes[end]&0xC0 == 0x80 {
			end--
		}
	}
	entry.RawHighWater, entry.RawReads = end, entry.RawReads+1
	result := Result{
		"call_id": request.CallID, "offset": offset, "next_offset": end, "limit": limit,
		"data": string(entry.RawBytes[offset:end]), "complete": end >= len(entry.RawBytes),
		"raw_reads": entry.RawReads, "raw_read_limit": resultRawReadLimit,
	}
	if entry.RawReference != "" {
		result["raw_result_ref"] = entry.RawReference
	}
	return withResultFailure(result, entry), nil
}

func (s *resultStore) rawCopy(callID string) (map[string]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[callID]
	if entry == nil {
		return nil, false
	}
	entry.TouchedAt = s.currentTime()
	return cloneResultMap(entry.Raw), true
}

func (s *resultStore) setRawReference(callID, reference string) {
	if callID == "" || reference == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.entries[callID]; entry != nil {
		entry.RawReference = reference
		entry.TouchedAt = s.currentTime()
	}
}

func (s *resultStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

func (s *resultStore) initializeLocked() {
	if s.entries == nil {
		s.entries = map[string]*storedMCPResult{}
	}
	if s.cursors == nil {
		s.cursors = map[string]resultCursorState{}
	}
	if s.latestScope == nil {
		s.latestScope = map[string]string{}
	}
	if s.rootBudgets == nil {
		s.rootBudgets = map[string]resultRootBudget{}
	}
}

func (s *resultStore) sweepLocked(now time.Time) {
	s.initializeLocked()
	for callID, entry := range s.entries {
		age := now.Sub(entry.TouchedAt)
		if age < 0 || age >= resultStoreTTL {
			s.deleteEntryLocked(callID)
		}
	}
	for key, budget := range s.rootBudgets {
		age := now.Sub(budget.TouchedAt)
		if age < 0 || age >= resultStoreTTL {
			delete(s.rootBudgets, key)
		}
	}
}

func (s *resultStore) deleteEntryLocked(callID string) {
	entry := s.entries[callID]
	delete(s.entries, callID)
	for cursor, state := range s.cursors {
		if state.CallID == callID {
			delete(s.cursors, cursor)
		}
	}
	if entry != nil && s.latestScope[entry.ScopeKey] == callID {
		delete(s.latestScope, entry.ScopeKey)
	}
}

func (s *resultStore) deleteOldestLocked() {
	var oldestID string
	var oldest time.Time
	for callID, entry := range s.entries {
		if oldest.IsZero() || entry.TouchedAt.Before(oldest) {
			oldestID, oldest = callID, entry.TouchedAt
		}
	}
	if oldestID != "" {
		s.deleteEntryLocked(oldestID)
	}
}

func (s *resultStore) newCursorLocked(callID string, pageIndex int) string {
	for {
		cursor := "hostcursor_" + randomResultToken()
		if _, exists := s.cursors[cursor]; exists {
			continue
		}
		s.cursors[cursor] = resultCursorState{CallID: callID, PageIndex: pageIndex}
		return cursor
	}
}

func (s *resultStore) consumeProjectionBudgetLocked(entry *storedMCPResult, bytes int, first bool) bool {
	if entry.ProjectedBytes+bytes > resultCallProjectionBudget {
		return false
	}
	budget := s.rootBudgets[entry.ScopeKey]
	if budget.Used+bytes > resultRootProjectionBudget {
		return false
	}
	entry.ProjectedBytes += bytes
	budget.Used += bytes
	budget.TouchedAt = s.currentTime()
	s.rootBudgets[entry.ScopeKey] = budget
	return true
}

func resultPageProjection(entry *storedMCPResult, pageIndex int, nextCursor string, replayed bool) Result {
	page := entry.Pages[pageIndex]
	remaining := max(0, len(entry.Pages)-pageIndex-1)
	// Only the originating invocation inherits the upstream failure status.
	// Later pages are successful reads; the original failure remains in the
	// normalized payload and upstream_failure instead of failing the read again.
	result := Result{
		"call_id": entry.CallID, "result_call_id": entry.CallID, "name": entry.Name, "result": cloneResultMap(page.Data),
		"isError":                pageIndex == 0 && entry.Raw["isError"] == true,
		"content_kinds":          append([]string{}, entry.ContentKinds...),
		"json_text_parsed":       entry.ParsedJSONText,
		"continuation_available": nextCursor != "", "continuation_required": false,
		"host_page": map[string]any{
			"mode": page.Mode, "page_index": pageIndex, "page_count": len(entry.Pages),
			"returned_bytes": page.Bytes, "remaining_pages": remaining,
			"cursor": nextCursor, "complete": nextCursor == "", "replayed": replayed,
		},
	}
	if nextCursor != "" {
		result["host_cursor"] = nextCursor
		result["continue_result"] = map[string]any{"tool": ToolResultContinue, "arguments": map[string]any{"call_id": entry.CallID, "cursor": nextCursor}}
	}
	if pageIndex > 0 {
		resultMap(result["host_page"])["read_succeeded"] = successfulResultRead(true)
	}
	if entry.RawReference != "" {
		result["raw_result_ref"] = entry.RawReference
	}
	return withResultFailure(result, entry)
}

func resultBudgetProjection(entry *storedMCPResult, scope string) Result {
	return withResultFailure(Result{
		"call_id": entry.CallID, "result_call_id": entry.CallID, "name": entry.Name, "result_retained": true,
		"isError":                entry.Raw["isError"] == true,
		"continuation_available": false, "continuation_required": false,
		"projection_budget_exhausted": true, "budget_scope": scope,
		"message":   "The result is retained, but the automatic MCP projection budget is exhausted. Narrow the original MCP request by fields, objects, region or page size.",
		"host_page": map[string]any{"page_index": 0, "page_count": len(entry.Pages), "returned_bytes": 0, "remaining_pages": len(entry.Pages), "complete": false},
	}, entry)
}

func resultBudgetError(entry *storedMCPResult) error {
	return toolErrorDetails("MCP_RESULT_BUDGET_EXHAUSTED", "The result is retained, but the automatic MCP projection budget is exhausted. Narrow the original MCP request by fields, objects, region or page size.", "validation", withResultFailure(Result{
		"call_id": entry.CallID, "result_call_id": entry.CallID, "projected_bytes": entry.ProjectedBytes,
		"per_call_budget_bytes": resultCallProjectionBudget, "root_task_budget_bytes": resultRootProjectionBudget,
		"result_retained": true, "continuation_required": false,
	}, entry))
}

func resultScopeKey(binding activity.Binding, fallback string) string {
	if binding.SourceOwnerKey == "" && binding.ConversationID == "" && binding.TaskID == "" && binding.WorkspaceID == "" {
		return fallback
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", binding.SourceOwnerKey, binding.ConversationID, binding.TaskID, binding.ThreadID, binding.WorkspaceID)
}

func resultBindingAllowed(stored, current activity.Binding) bool {
	checks := [][2]string{
		{stored.SourceOwnerKey, current.SourceOwnerKey}, {stored.ConversationID, current.ConversationID},
		{stored.TaskID, current.TaskID}, {stored.ThreadID, current.ThreadID}, {stored.WorkspaceID, current.WorkspaceID},
	}
	for _, check := range checks {
		if check[1] != check[0] {
			return false
		}
	}
	return true
}

func randomResultToken() string {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err == nil {
		return base64.RawURLEncoding.EncodeToString(buffer)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
}
