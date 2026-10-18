package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func setOutputBudget(t *testing.T, r *Runtime, enabled bool, limit int) {
	t.Helper()
	_, err := r.RuntimeUpdateDisplaySettings(context.Background(), config.DisplayChange{ExpectedRevision: r.MCPPresentationSettings().Revision, ToolOutput: &config.ToolOutputSettings{Enabled: enabled, MaxChars: limit}})
	if err != nil {
		t.Fatal(err)
	}
}

func outputCall(t *testing.T, r *Runtime, ctx context.Context, name string, args map[string]any, body Result) Result {
	t.Helper()
	spec := ToolSpec{Name: name, Handler: func(context.Context, *Runtime, map[string]any) (Result, error) { return body, nil }}
	result, err := r.callObserved(WithModelProjection(ctx), spec, args)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestToolOutputSharedUnicodeBudgetAndDurableRead(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("output-owner")
	setOutputBudget(t, r, true, 1000)
	stdout := strings.Repeat("中😀e\u0301\r\n", 140)
	stderr := strings.Repeat("错误", 200)
	result := outputCall(t, r, ctx, "session_observe", map[string]any{"action": "list"}, Result{"stdout": stdout, "stderr": stderr, "status": "failed", "exit_code": 7, "command_ok": false})
	policy := result["output_policy"].(map[string]any)
	if policy["truncated"] != true || result["exit_code"] != 7 || result["command_ok"] != false || utf8.RuneCountInString(stringArg(result, "stdout"))+utf8.RuneCountInString(stringArg(result, "stderr")) > 1000 {
		t.Fatalf("budget or control changed: %v", policy)
	}
	for _, key := range []string{"unit", "limit_chars", "scope", "displayed_chars", "fields", "source_scope", "storage_redaction"} {
		if policy[key] != nil {
			t.Fatalf("verbose output policy field %s leaked to the model: %v", key, policy)
		}
	}
	continuation, ok := policy["continue_read"].(map[string]any)
	if !ok {
		t.Fatalf("no readable continuation: %v", policy)
	}
	args := continuation["arguments"].(map[string]any)
	uri := stringArg(args, "path")
	var all strings.Builder
	offset := int64(0)
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("pagination did not terminate")
		}
		page, err := r.Call(ctx, "read_file", map[string]any{"path": uri, "offset": offset, "limit_chars": 1000})
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, "read_file", page)
		text := stringArg(page, "content")
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 1000 {
			t.Fatal("invalid scalar page")
		}
		next := page["next_offset"].(int64)
		if next-offset != int64(len(text)) {
			t.Fatal("offset is not consumed UTF-8 bytes")
		}
		all.WriteString(text)
		if page["has_more"] != true {
			break
		}
		if next <= offset {
			t.Fatal("page did not advance")
		}
		offset = next
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(all.String()), &saved); err != nil || saved["stdout"] != stdout || saved["stderr"] != stderr {
		t.Fatalf("retained source mismatch: %v", err)
	}
	call, err := r.activity.Call(ctx, stringArg(result, "call_id"))
	if err != nil || call.OutputSource == nil || call.Response == nil || call.OutputSource.Ref == call.Response.Ref {
		t.Fatal("returned envelope overwrote the original source")
	}
	_, err = r.Call(scopeHost("output-other-conversation"), "read_file", map[string]any{"path": uri})
	if err == nil {
		t.Fatal("unbound different conversation read protected output")
	}
	intruder := activity.WithSource(context.Background(), activity.Source{Principal: "another-owner", HostConversationID: "intruder"})
	_, err = r.Call(intruder, "read_file", map[string]any{"path": uri})
	if err == nil {
		t.Fatal("another owner read protected output")
	}
	_, err = r.Call(ctx, "read_file", map[string]any{"path": uri, "start_line": 1})
	if err == nil {
		t.Fatal("mixed offset units accepted")
	}
	if err = os.Remove(filepath.Join(r.cfg.AgentDockHome, "tasks", "activity", "payloads", call.OutputSource.Ref+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Call(ctx, "read_file", map[string]any{"path": uri}); err == nil {
		t.Fatal("expired blob claimed readable")
	}
}

func TestToolOutputFixedBudgetAndRequiredFilePaging(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := WithModelProjection(scopeHost("output-boundaries"))
	for _, enabled := range []bool{false, true} {
		for _, limit := range []int{1000, 1733, 100000} {
			setOutputBudget(t, r, enabled, limit)
			for _, length := range []int{limit - 1, limit, limit + 1} {
				result := outputCall(t, r, ctx, "session_observe", map[string]any{"action": "list"}, Result{"status": "exited", "stdout": strings.Repeat("😀", length)})
				text := stringArg(result, "stdout")
				budget := toolcontract.ModelVisibleBodyChars
				if enabled {
					budget = min(budget, limit)
				}
				chars, _ := toolcontract.JSONChars(text)
				if chars-2 > budget {
					t.Fatalf("escaped output exceeded fixed budget: %d/%d", chars-2, budget)
				}
				if utf8.RuneCountInString(text) < length && modelObject(result["capture"])["source_ref"] == "" {
					t.Fatal("bounded result lost original source")
				}
			}
		}
	}
	setOutputBudget(t, r, true, 1000)
	text := strings.Repeat("规则💡", 2000)
	path := filepath.Join(r.cfg.AgentDockDefaultDir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"path": path}
	recovered := ""
	for index := 0; index < 100; index++ {
		result, err := r.Call(ctx, "read_file", args)
		if err != nil {
			t.Fatal(err)
		}
		recovered += stringArg(result, "content")
		if result["has_more"] != true {
			break
		}
		if modelObject(result["delivery"])["continuation_required"] != true {
			t.Fatal("contract continuation became optional")
		}
		args = modelObject(modelObject(result["continue_read"])["arguments"])
	}
	if recovered != text {
		t.Fatal("required file paging omitted rules")
	}
}

func TestToolOutputSnapshotHotChangeDoesNotAlterInFlight(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := WithModelProjection(scopeHost("output-hot-change"))
	setOutputBudget(t, r, true, 1000)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan Result, 1)
	failed := make(chan error, 1)
	spec := ToolSpec{Name: "session_observe", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		close(entered)
		<-release
		return Result{"stdout": strings.Repeat("A", 4000), "status": "exited"}, nil
	}}
	go func() {
		result, err := r.callObserved(ctx, spec, map[string]any{"action": "list"})
		done <- result
		failed <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture did not enter")
	}
	setOutputBudget(t, r, true, 3000)
	close(release)
	result := <-done
	if err := <-failed; err != nil {
		t.Fatal(err)
	}
	if modelObject(result["output_policy"])["budget_chars"] != 1000 {
		t.Fatal("in-flight policy changed")
	}
	result = outputCall(t, r, ctx, "session_observe", map[string]any{"action": "list"}, Result{"stdout": strings.Repeat("B", 4000), "status": "exited"})
	if modelObject(result["output_policy"])["budget_chars"] != 3000 {
		t.Fatal("subsequent call did not use updated policy")
	}
}

func TestToolOutputDynamicPlainTextAndUnknownStructuredContract(t *testing.T) {
	r := executionTestRuntime(t)
	p := &preparedExecution{spec: ToolSpec{Name: "mcp_tool_call"}, args: map[string]any{}, outputPolicy: config.ToolOutputSettings{Enabled: true, MaxChars: 1000}}
	// Pure formatter test: no journal reference is advertised without identity.
	first, second := strings.Repeat("A", 800), strings.Repeat("汉", 700)
	remote := map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": first}, map[string]any{"type": "image", "data": "abc", "mimeType": "image/png"}, map[string]any{"type": "text", "text": second}}}
	result := r.applyToolOutputPolicy(p, Result{"result": remote})
	limited := result["result"].(map[string]any)
	blocks := limited["content"].([]any)
	if limited["isError"] != true || utf8.RuneCountInString(blocks[0].(map[string]any)["text"].(string))+utf8.RuneCountInString(blocks[2].(map[string]any)["text"].(string)) > 1000 || blocks[1].(map[string]any)["data"] != "abc" {
		t.Fatal("dynamic content or status contract changed")
	}
	if remote["content"].([]any)[2].(map[string]any)["text"] != second {
		t.Fatal("formatter mutated a shared upstream result")
	}
	structured := map[string]any{"text": strings.Repeat("长", 2000), "status": "ok"}
	encoded, _ := json.Marshal(structured)
	remote = map[string]any{"structuredContent": structured, "content": []any{map[string]any{"type": "text", "text": string(encoded)}}}
	result = r.applyToolOutputPolicy(p, Result{"result": remote})
	if remote["structuredContent"].(map[string]any)["text"] != structured["text"] || result["output_policy"] != nil {
		t.Fatal("unknown schema was silently altered")
	}
}

func TestToolOutputRedactionAndOriginalTruncationRemainExplicit(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("output-source-state")
	setOutputBudget(t, r, true, 1000)
	result := outputCall(t, r, ctx, "session_observe", map[string]any{"action": "list"}, Result{"stdout": "password=do-not-retain " + strings.Repeat("A", 1200), "stderr": "", "stdout_truncated": true, "status": "running"})
	policy := result["output_policy"].(map[string]any)
	if strings.Contains(stringArg(result, "stdout"), "do-not-retain") || modelObject(result["capture"])["complete"] != false || modelObject(result["capture"])["state"] != "partial" {
		t.Fatalf("source/redaction state lost: %v", policy)
	}
	t.Run("MCP source storage failure", func(t *testing.T) {
		fixture := newCatalogFixture(t, 1)
		fixture.largeResult.Store(true)
		cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
		runtime, err := newUnrestrictedTestRuntime(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		ctx := WithModelProjection(t.Context())
		if _, err := runtime.Call(ctx, "mcp_manage", map[string]any{"action": "add", "name": "pages", "description": "source retention fixture", "transport": "streamable_http", "url": fixture.server.URL}); err != nil {
			t.Fatal(err)
		}
		payloadDir := filepath.Join(cfg.AgentDockHome, "tasks", "activity", "payloads")
		preserved := payloadDir + "-preserved"
		if err := os.Rename(payloadDir, preserved); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(payloadDir, []byte("reject payload writes"), 0600); err != nil {
			t.Fatal(err)
		}
		first, err := runtime.Call(ctx, "mcp_tool_call", map[string]any{"name": "pages:tool_000", "arguments": map[string]any{"text": "ok"}})
		if err != nil {
			t.Fatal(err)
		}
		capture := modelObject(first["capture"])
		if capture["state"] != "not_stored" || capture["complete"] != false || first["source_complete"] != false || first["truncation_stage"] != "capture" || stringArg(capture, "source_ref") != "" || first["raw_result_ref"] != nil {
			t.Fatalf("unavailable full source was reported complete: capture=%#v complete=%#v stage=%#v", capture, first["source_complete"], first["truncation_stage"])
		}
		callID := stringArg(first, "call_id")
		call, err := runtime.activity.Call(ctx, callID)
		if err != nil {
			t.Fatal(err)
		}
		if call.Status != "succeeded" || call.OutputSource != nil || fixture.calls.Load() != 1 {
			t.Fatal("source storage failure changed or repeated the completed business operation")
		}
		next, err := runtime.Call(ctx, "mcp_result_continue", map[string]any{"call_id": callID, "cursor": first["host_cursor"]})
		if err != nil {
			t.Fatal(err)
		}
		capture = modelObject(next["capture"])
		if capture["state"] != "not_stored" || capture["complete"] != false || next["source_complete"] != false || next["truncation_stage"] != "capture" || stringArg(capture, "source_ref") != "" || next["raw_result_ref"] != nil {
			t.Fatalf("continuation claimed the failed source was retained: capture=%#v complete=%#v stage=%#v", capture, next["source_complete"], next["truncation_stage"])
		}
		if err := os.Remove(payloadDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(preserved, payloadDir); err != nil {
			t.Fatal(err)
		}
		pending := &ToolResponse{auditBinding: call.Binding, auditName: "mcp_tool_call", auditRedactor: runtime.executionRedactor(nil)}
		source, err := runtime.RetainResponseSource(pending, first)
		if err != nil {
			t.Fatal(err)
		}
		assertCatalogFixtureSource(t, runtime, ctx, callID, source)
		if fixture.calls.Load() != 1 {
			t.Fatal("adapter source recovery repeated the business operation")
		}
	})
}
