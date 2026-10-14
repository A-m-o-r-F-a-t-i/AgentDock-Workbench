package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestStructuredOutputPagesRetainedResultWithoutReexecution(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "structured-output-test", Namespace: "mcp", HostConversationID: "structured-output-pages",
	})
	matches := make([]map[string]any, 0, 60)
	for index := 0; index < 60; index++ {
		matches = append(matches, map[string]any{
			"path": "activity.jsonl", "line": index + 1,
			"preview": fmt.Sprintf("match-%03d-%s", index, strings.Repeat("x", 700)),
		})
	}
	body := Result{"query": "needle", "engine": "go_fallback", "matches": matches, "total_matches": len(matches), "truncated": false}
	handlerCalls := 0
	spec := ToolSpec{Name: "search_text", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		handlerCalls++
		return body, nil
	}}
	args := map[string]any{"query": "needle", "max_response_bytes": 4096}
	result, err := r.callObserved(ctx, spec, args)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectStructuredMatchLines(t, result)
	meta, ok := result["structured_output"].(map[string]any)
	if !ok || meta["source_complete"] != true || meta["source"] == nil {
		t.Fatalf("first page metadata = %#v", result["structured_output"])
	}
	if pageBytes := integerArgument(meta["page_bytes"]); pageBytes <= 0 || pageBytes > 4096 {
		t.Fatalf("first page bytes = %d, want 1..4096", pageBytes)
	}
	guidance, _ := result["agentdock_guidance"].(map[string]any)
	steps, ok := guidance["next_required"].([]map[string]any)
	if !ok || len(steps) != 1 || steps[0]["action"] != "continue_structured_output" || steps[0]["tool"] != "search_text" {
		t.Fatalf("first page continuation guidance = %#v", guidance["next_required"])
	}
	stepArguments, _ := steps[0]["arguments"].(map[string]any)
	if stepArguments["cursor"] != result["next_cursor"] || stepArguments["query"] != "needle" || integerArgument(stepArguments["max_response_bytes"]) != 4096 {
		t.Fatalf("continuation arguments = %#v", stepArguments)
	}

	// Change the handler result after the first page. Cursor continuation must use
	// the retained immutable result and must not invoke the handler again.
	body = Result{"query": "needle", "engine": "go_fallback", "matches": []map[string]any{{"line": 9999}}, "total_matches": 1, "truncated": false}
	for pages := 1; ; pages++ {
		cursor, _ := result["next_cursor"].(string)
		if cursor == "" {
			break
		}
		if pages > 100 {
			t.Fatal("structured pagination did not terminate")
		}
		result, err = r.callObserved(ctx, spec, map[string]any{"query": "needle", "max_response_bytes": 4096, "cursor": cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, collectStructuredMatchLines(t, result)...)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
	if len(seen) != len(matches) {
		t.Fatalf("returned matches = %d, want %d", len(seen), len(matches))
	}
	for index, line := range seen {
		if line != index+1 {
			t.Fatalf("match %d line = %d, want %d", index, line, index+1)
		}
	}
}

func TestStructuredOutputCursorRejectsSelectorChanges(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "structured-output-test", Namespace: "mcp", HostConversationID: "structured-output-mismatch",
	})
	spec := ToolSpec{Name: "list_dir", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		entries := make([]map[string]any, 40)
		for index := range entries {
			entries[index] = map[string]any{"name": fmt.Sprintf("file-%03d", index), "path": fmt.Sprintf("file-%03d", index), "type": "file", "size_bytes": 1024, "modified": "2026-10-13T00:00:00Z", "is_hidden": false}
		}
		return Result{"path": ".", "entries": entries, "truncated": false, "partial": false, "skipped_paths": []string{}}, nil
	}}
	result, err := r.callObserved(ctx, spec, map[string]any{"path": ".", "max_response_bytes": 4096})
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := result["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("expected continuation cursor")
	}
	_, err = r.callObserved(ctx, spec, map[string]any{"path": "other", "max_response_bytes": 4096, "cursor": cursor})
	if err == nil {
		t.Fatal("cursor accepted different selector arguments")
	}
	toolErr, ok := err.(*ToolError)
	if !ok || toolErr.Code != "STRUCTURED_CURSOR_MISMATCH" {
		t.Fatalf("error = %T %#v, want STRUCTURED_CURSOR_MISMATCH", err, err)
	}
}

func TestStructuredSnapshotBindingUsesCommittedContextScope(t *testing.T) {
	p := &preparedExecution{
		spec: ToolSpec{Name: "agentdock_context"},
		state: executionObservation{binding: activity.Binding{
			ConversationID: "conv-context", TaskID: "task-old", ThreadID: "thread-old", WorkspaceID: "workspace-old",
		}},
	}
	result := Result{
		"workspace_id": "workspace-new",
		"conversation_state": activity.ConversationState{
			ActiveTaskID: "task-new", ActiveTaskThreadID: "thread-new", WorkspaceID: "workspace-new",
		},
	}
	binding := structuredSnapshotBinding(p, result)
	if binding.ConversationID != "conv-context" || binding.TaskID != "task-new" || binding.ThreadID != "thread-new" || binding.WorkspaceID != "workspace-new" {
		t.Fatalf("snapshot binding = %#v", binding)
	}
}

func TestSourceResultCompleteUsesContextDiagnostics(t *testing.T) {
	complete, reason := sourceResultComplete(Result{
		"context_diagnostics": map[string]any{"complete": false},
	})
	if complete || !strings.Contains(reason, "context generation was incomplete") {
		t.Fatalf("source completeness = %v, reason = %q", complete, reason)
	}
	complete, reason = sourceResultComplete(Result{
		"context_diagnostics": map[string]any{"complete": true},
	})
	if !complete || reason != "" {
		t.Fatalf("complete context source = %v, reason = %q", complete, reason)
	}
}

func TestStructuredOutputPagesRealSearchAndDirectoryResults(t *testing.T) {
	r := newRuntimeValidationTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "structured-output-test", Namespace: "mcp", HostConversationID: "structured-output-real-tools",
	})
	root := r.cfg.AgentDockDefaultDir
	if _, err := r.Call(ctx, "agentdock_context", map[string]any{"workdir": root}); err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	for index := 0; index < 80; index++ {
		fmt.Fprintf(&content, "needle-%03d-%s\n", index, strings.Repeat("x", 500))
	}
	if err := os.WriteFile(filepath.Join(root, "matches.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	searchItems := collectStructuredToolPages(t, r, ctx, "search_text", map[string]any{
		"path": root, "query": "needle", "max_results": 1000, "max_response_bytes": 4096,
	}, structuredOutputPath{"matches"})
	if len(searchItems) != 80 {
		t.Fatalf("search pages returned %d matches, want 80", len(searchItems))
	}

	directory := filepath.Join(root, "many-files")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 120; index++ {
		name := filepath.Join(directory, fmt.Sprintf("entry-%03d-%s.txt", index, strings.Repeat("n", 40)))
		if err := os.WriteFile(name, []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	listItems := collectStructuredToolPages(t, r, ctx, "list_dir", map[string]any{
		"path": directory, "max_response_bytes": 4096,
	}, structuredOutputPath{"entries"})
	if len(listItems) != 120 {
		t.Fatalf("directory pages returned %d entries, want 120", len(listItems))
	}
}

func TestStructuredOutputPagesLargeContextCollections(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "structured-output-test", Namespace: "mcp", HostConversationID: "structured-context-pages",
	})
	binding := activity.Binding{
		CallID: "call_structured_context", WorkspaceID: "workspace-context", SourceOwnerKey: activity.SourceOwnerKey(ctx),
	}
	items := func(prefix string, count, size int) []any {
		result := make([]any, 0, count)
		for index := 0; index < count; index++ {
			result = append(result, map[string]any{
				"name": fmt.Sprintf("%s-%03d", prefix, index), "description": strings.Repeat(prefix, size),
			})
		}
		return result
	}
	files := make([]any, 0, 4)
	for index := 0; index < 4; index++ {
		files = append(files, map[string]any{
			"scope": "workspace", "path": fmt.Sprintf("AGENTS-%d.md", index), "status": "loaded",
			"content": strings.Repeat("rule-content-", 450), "size_bytes": 5850,
		})
	}
	contextResult := Result{
		"workspace_id":        "workspace-context",
		"workspace":           map[string]any{"workspace_id": "workspace-context", "root": r.cfg.AgentDockDefaultDir, "runtime": "windows", "rules_revision": 1},
		"runtime":             map[string]any{"version": "1.1.9", "os": "windows", "arch": "amd64", "agentdock_home": r.cfg.AgentDockHome, "agentdock_default_dir": r.cfg.AgentDockDefaultDir, "default_cwd": r.cfg.AgentDockDefaultDir, "path_model": "windows"},
		"instruction_files":   map[string]any{"auto_load": true, "workdir": r.cfg.AgentDockDefaultDir, "workspace_root": r.cfg.AgentDockDefaultDir, "files": files},
		"skills":              items("skill", 24, 80),
		"common_skills":       map[string]any{"root": "skills://common", "total": 12, "truncated": false, "items": items("common", 12, 80)},
		"plugins":             items("plugin", 16, 80),
		"dynamic_mcp":         items("mcp", 12, 80),
		"acp":                 map[string]any{"enabled": true, "default_profile": "default", "description": "ACP", "profiles": items("profile", 6, 80)},
		"workflow_templates":  items("workflow", 12, 80),
		"recall":              map[string]any{"enabled": true, "items": items("recall", 10, 80)},
		"rules":               []any{strings.Repeat("rule-", 500), strings.Repeat("constraint-", 300)},
		"warnings":            items("warning", 6, 80),
		"tasks":               map[string]any{"total": 12, "truncated": false, "items": items("task", 12, 80)},
		"context_diagnostics": map[string]any{"complete": true, "components_complete": true, "binding_status": "updated"},
	}
	paths := structuredOutputPaths("agentdock_context")
	expected := map[string]int{}
	for _, path := range paths {
		expected[structuredPathName(path)] = len(structuredPathItems(contextResult, path))
	}
	seen := map[string]int{}
	p := &preparedExecution{spec: ToolSpec{Name: "agentdock_context"}, args: map[string]any{}, state: executionObservation{binding: binding}}
	page := r.applyStructuredOutputPolicy(p, contextResult)
	pageCount := 0
	for {
		pageCount++
		if pageCount > 100 {
			t.Fatal("context pagination did not terminate")
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		meta, ok := page["structured_output"].(map[string]any)
		if !ok || meta["source_complete"] != true {
			t.Fatalf("context page metadata = %#v", page["structured_output"])
		}
		if len(encoded) > integerArgument(meta["business_budget_bytes"]) {
			t.Fatalf("context business page = %d bytes, budget = %v", len(encoded), meta["business_budget_bytes"])
		}
		for _, path := range paths {
			seen[structuredPathName(path)] += len(structuredPathItems(page, path))
		}
		cursor, _ := page["next_cursor"].(string)
		diagnostics, _ := page["context_diagnostics"].(map[string]any)
		if cursor != "" && diagnostics["complete"] != false {
			t.Fatalf("paged context marked complete: %#v", diagnostics)
		}
		if cursor == "" && diagnostics["complete"] != true {
			t.Fatalf("final context page marked incomplete: %#v", diagnostics)
		}
		if cursor == "" {
			break
		}
		next := &preparedExecution{
			spec: ToolSpec{Name: "agentdock_context"}, args: map[string]any{"cursor": cursor},
			state: executionObservation{binding: binding},
		}
		full, resume, err := r.resumeStructuredOutput(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		next.structuredResume = resume
		page = r.applyStructuredOutputPolicy(next, full)
	}
	if pageCount < 2 {
		t.Fatal("large context was not paged")
	}
	for path, count := range expected {
		if seen[path] != count {
			t.Fatalf("context path %s returned %d items, want %d", path, seen[path], count)
		}
	}
}

func TestStructuredOutputGuidanceRequiresExactContinuation(t *testing.T) {
	r := executionTestRuntime(t)
	result := Result{
		"next_cursor": "scur_test",
		"structured_output": map[string]any{
			"minimum_response_bytes": 32768,
		},
	}
	guidance := r.executionGuidance("search_text", map[string]any{
		"path": ".", "query": "needle", "max_response_bytes": 4096,
		"task_id": "inherited-task", "activity_label": "ignored",
	}, executionObservation{}, result, false)
	steps, ok := guidance["next_required"].([]map[string]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("next_required = %#v", guidance["next_required"])
	}
	arguments, _ := steps[0]["arguments"].(map[string]any)
	if steps[0]["action"] != "continue_structured_output" || steps[0]["tool"] != "search_text" || arguments["cursor"] != "scur_test" || arguments["max_response_bytes"] != 32768 || arguments["path"] != "." || arguments["query"] != "needle" {
		t.Fatalf("continuation guidance = %#v", steps[0])
	}
	if _, exists := arguments["task_id"]; exists {
		t.Fatalf("continuation leaked inherited execution metadata: %#v", arguments)
	}
}

func TestStructuredOutputOversizedItemRequiresActivitySourceRead(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "structured-output-test", Namespace: "mcp", HostConversationID: "structured-output-oversized",
	})
	binding := activity.Binding{
		CallID: "call_structured_oversized", WorkspaceID: "workspace-oversized", SourceOwnerKey: activity.SourceOwnerKey(ctx),
	}
	p := &preparedExecution{
		spec:  ToolSpec{Name: "agentdock_context"},
		args:  map[string]any{"max_response_bytes": toolcontract.StructuredResponseMaxBytes},
		state: executionObservation{binding: binding},
	}
	page := r.applyStructuredOutputPolicy(p, Result{
		"workspace_id":        "workspace-oversized",
		"rules":               []any{strings.Repeat("oversized-rule-", 6000)},
		"context_diagnostics": map[string]any{"complete": true, "components_complete": true, "binding_status": "updated"},
	})
	meta, ok := page["structured_output"].(map[string]any)
	if !ok || meta["truncated"] != true || meta["oversized_item"] == nil {
		t.Fatalf("oversized metadata = %#v", page["structured_output"])
	}
	source, _ := meta["source"].(string)
	if source == "" {
		t.Fatalf("oversized result missing retained source: %#v", meta)
	}
	diagnostics, _ := page["context_diagnostics"].(map[string]any)
	if diagnostics["complete"] != false {
		t.Fatalf("oversized context marked complete: %#v", diagnostics)
	}
	if cursor, _ := page["next_cursor"].(string); cursor != "" {
		t.Fatalf("single oversized item unexpectedly returned cursor %q", cursor)
	}
	guidance := r.executionGuidance("agentdock_context", p.args, p.state, page, false)
	steps, ok := guidance["next_required"].([]map[string]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("oversized next_required = %#v", guidance["next_required"])
	}
	arguments, _ := steps[0]["arguments"].(map[string]any)
	if steps[0]["action"] != "read_structured_source" || steps[0]["tool"] != "read_file" || arguments["path"] != source || arguments["offset"] != 0 {
		t.Fatalf("oversized follow-up = %#v", steps[0])
	}
	if meta["oversized_items_omitted"] != true {
		t.Fatalf("oversized omission flag = %#v", meta)
	}

	page = r.applyStructuredOutputPolicy(p, Result{
		"workspace_id": "workspace-oversized",
		"rules":        []any{strings.Repeat("oversized-rule-", 6000)},
		"skills":       []any{map[string]any{"name": "after-oversized", "description": "must remain pageable"}},
		"context_diagnostics": map[string]any{
			"complete": true, "components_complete": true, "binding_status": "updated",
		},
	})
	cursor, _ := page["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("oversized item hid later pageable collections")
	}
	next := &preparedExecution{
		spec:  ToolSpec{Name: "agentdock_context"},
		args:  map[string]any{"cursor": cursor, "max_response_bytes": toolcontract.StructuredResponseMaxBytes},
		state: executionObservation{binding: binding},
	}
	full, resume, err := r.resumeStructuredOutput(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	next.structuredResume = resume
	finalPage := r.applyStructuredOutputPolicy(next, full)
	if skills := structuredPathItems(finalPage, structuredOutputPath{"skills"}); len(skills) != 1 {
		t.Fatalf("later pageable collection = %#v", skills)
	}
	finalDiagnostics, _ := finalPage["context_diagnostics"].(map[string]any)
	if finalDiagnostics["complete"] != false {
		t.Fatalf("context with an omitted oversized item marked complete: %#v", finalDiagnostics)
	}
	if finalCursor, _ := finalPage["next_cursor"].(string); finalCursor != "" {
		t.Fatalf("final context page returned another cursor %q", finalCursor)
	}
	finalMeta, _ := finalPage["structured_output"].(map[string]any)
	if finalMeta["oversized_items_omitted"] != true || finalMeta["source"] == nil {
		t.Fatalf("final oversized source metadata = %#v", finalMeta)
	}
}

func TestStructuredCursorCacheIsBoundedAndDeduplicated(t *testing.T) {
	r := executionTestRuntime(t)
	now := time.Now()
	snapshot, err := r.storeStructuredSnapshot(&structuredOutputSnapshot{
		Tool: "list_dir", ArgumentsHash: "selectors", Encoded: []byte(`{"entries":[]}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	position := structuredPageState{PathIndex: 0, ItemIndex: 1}
	first, err := r.newStructuredCursor(snapshot.ID, position)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.newStructuredCursor(snapshot.ID, position)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("duplicate cursor position produced %q and %q", first, second)
	}
	for index := 0; index < structuredCursorCacheEntries+64; index++ {
		if _, err := r.newStructuredCursor(snapshot.ID, structuredPageState{PathIndex: 1, ItemIndex: index}); err != nil {
			t.Fatal(err)
		}
	}
	r.structuredOutputMu.Lock()
	count := len(r.structuredCursors)
	r.structuredOutputMu.Unlock()
	if count > structuredCursorCacheEntries {
		t.Fatalf("cursor cache size = %d, want <= %d", count, structuredCursorCacheEntries)
	}
}

func collectStructuredToolPages(t *testing.T, r *Runtime, ctx context.Context, tool string, args map[string]any, path structuredOutputPath) []any {
	t.Helper()
	request := make(map[string]any, len(args)+1)
	for key, value := range args {
		request[key] = value
	}
	var items []any
	for pages := 0; ; pages++ {
		if pages > 200 {
			t.Fatalf("%s pagination did not terminate", tool)
		}
		result, err := r.Call(ctx, tool, request)
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, tool, result)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		budget := integerArgument(args["max_response_bytes"])
		if len(encoded) > budget {
			t.Fatalf("%s final page = %d bytes, exceeds %d-byte model-facing budget", tool, len(encoded), budget)
		}
		items = append(items, structuredPathItems(result, path)...)
		cursor, _ := result["next_cursor"].(string)
		if cursor == "" {
			return items
		}
		request["cursor"] = cursor
	}
}

func collectStructuredMatchLines(t *testing.T, result Result) []int {
	t.Helper()
	raw, ok := result["matches"].([]any)
	if !ok {
		encoded, _ := json.Marshal(result["matches"])
		t.Fatalf("matches type = %T value=%s", result["matches"], encoded)
	}
	lines := make([]int, 0, len(raw))
	for _, value := range raw {
		match, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("match type = %T", value)
		}
		lines = append(lines, integerArgument(match["line"]))
	}
	return lines
}
