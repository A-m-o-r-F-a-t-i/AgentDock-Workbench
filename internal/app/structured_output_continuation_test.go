package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructuredCursorRestoresOmittedSelectorsAndCompactsReplay(t *testing.T) {
	r := executionTestRuntime(t)
	path := filepath.Join(r.cfg.AgentDockDefaultDir, "structured-cursor-search.txt")
	var content strings.Builder
	for index := 0; index < 1000; index++ {
		fmt.Fprintf(&content, "needle-%04d %s\n", index, strings.Repeat("payload ", 8))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := scopeHost("structured-cursor-selector-recovery")
	initial := map[string]any{
		"path": path, "query": "needle-", "context_lines": 4,
		"max_results": 1000, "max_response_bytes": 4096,
	}

	first, err := r.Call(ctx, "search_text", initial)
	if err != nil {
		t.Fatal(err)
	}
	firstMeta := modelObject(first["structured_output"])
	cursor := stringArg(first, "next_cursor")
	if cursor == "" || stringArg(firstMeta, "snapshot_id") == "" {
		t.Fatalf("search result did not page: %#v", first)
	}

	continuation := map[string]any{
		"path": path, "query": "needle-", "max_results": 1000,
		"max_response_bytes": 4096, "cursor": cursor,
	}
	second, err := r.Call(ctx, "search_text", continuation)
	if err != nil {
		t.Fatalf("omitted unchanged selector was rejected: %v", err)
	}
	secondMeta := modelObject(second["structured_output"])
	if stringArg(secondMeta, "snapshot_id") != stringArg(firstMeta, "snapshot_id") ||
		integerArgument(secondMeta["remaining_items"]) >= integerArgument(firstMeta["remaining_items"]) {
		t.Fatalf("continuation did not advance the retained snapshot: first=%#v second=%#v", firstMeta, secondMeta)
	}
	nextCursor := stringArg(second, "next_cursor")
	if nextCursor == "" {
		t.Fatalf("fixture needs at least three pages: %#v", second)
	}
	r.structuredOutputMu.Lock()
	consumed := r.structuredCursors[cursor]
	r.structuredOutputMu.Unlock()
	if !consumed.Consumed || consumed.NextCursor != nextCursor {
		t.Fatalf("successful continuation was not committed: cursor=%#v second=%#v", consumed, secondMeta)
	}

	replay, err := r.Call(ctx, "search_text", continuation)
	if err != nil {
		t.Fatalf("replayed cursor returned an error instead of a receipt: %v", err)
	}
	if replay["already_delivered"] != true || replay["replayed"] != true || stringArg(replay, "next_cursor") != nextCursor {
		t.Fatalf("replayed cursor repeated business content: %#v", replay)
	}
	if operation := modelObject(replay["operation"]); operation["executed"] != false {
		t.Fatalf("replayed cursor was reported as a new execution: %#v", replay)
	}
	nextArguments := modelObject(replay["next_arguments"])
	if integerArgument(nextArguments["context_lines"]) != 4 || stringArg(nextArguments, "cursor") != nextCursor {
		t.Fatalf("replay receipt lost the canonical continuation selectors: %#v", nextArguments)
	}
}

func TestStructuredCursorRejectsExplicitSelectorChangesWithExactRetry(t *testing.T) {
	r := executionTestRuntime(t)
	path := filepath.Join(r.cfg.AgentDockDefaultDir, "structured-cursor-mismatch.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("needle selector payload\n", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := scopeHost("structured-cursor-selector-mismatch")
	first, err := r.Call(ctx, "search_text", map[string]any{
		"path": path, "query": "needle", "context_lines": 4,
		"max_results": 1000, "max_response_bytes": 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor := stringArg(first, "next_cursor")
	if cursor == "" {
		t.Fatalf("search result did not page: %#v", first)
	}

	_, err = r.Call(ctx, "search_text", map[string]any{
		"path": path, "query": "needle", "context_lines": 2,
		"max_results": 1000, "max_response_bytes": 4096, "cursor": cursor,
	})
	var failure *ToolError
	if !errors.As(err, &failure) || failure.Code != "STRUCTURED_CURSOR_MISMATCH" {
		t.Fatalf("changed selector was accepted: %#v", err)
	}
	if failure.Details["retry_safe"] != true || failure.Details["execution_performed"] != false || failure.Details["effect_state"] != "none" {
		t.Fatalf("selector mismatch was not classified as a safe non-execution: %#v", failure.Details)
	}
	action := modelObject(failure.Details["required_action"])
	retry := modelObject(action["arguments"])
	if integerArgument(retry["context_lines"]) != 4 || stringArg(retry, "cursor") != cursor {
		t.Fatalf("selector mismatch did not return exact retry arguments: %#v", failure.Details)
	}
}
