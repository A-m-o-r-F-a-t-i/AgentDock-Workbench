package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type preparedCallTestClient struct {
	revision atomic.Uint64
	calls    atomic.Int32
	mu       sync.Mutex
	tools    []Tool
	lastArgs map[string]any
}

func (c *preparedCallTestClient) initialize(context.Context) error { return nil }

func (c *preparedCallTestClient) listTools(context.Context) ([]Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tools := make([]Tool, 0, len(c.tools))
	for _, tool := range c.tools {
		tools = append(tools, cloneTool(tool))
	}
	return tools, nil
}

func (c *preparedCallTestClient) callTool(ctx context.Context, _ string, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.calls.Add(1)
	c.mu.Lock()
	c.lastArgs = cloneJSONMap(arguments)
	c.mu.Unlock()
	return map[string]any{"structuredContent": map[string]any{"ok": true}}, nil
}

func (c *preparedCallTestClient) close() error          { return nil }
func (c *preparedCallTestClient) ToolsRevision() uint64 { return c.revision.Load() }

func preparedCallInputSchema(extraType string) map[string]any {
	properties := map[string]any{"value": map[string]any{"type": "string"}}
	if extraType != "" {
		properties["optional"] = map[string]any{"type": extraType}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"value"},
		"properties":           properties,
	}
}

func preparedCallTool(extraType string, readOnly bool) Tool {
	return Tool{
		Name:        "write",
		Description: "prepared call fixture",
		InputSchema: preparedCallInputSchema(extraType),
		Annotations: map[string]any{"readOnlyHint": readOnly, "destructiveHint": !readOnly},
	}
}

func newPreparedCallTestManager(t *testing.T) (*Manager, *serverState, *preparedCallTestClient, ServerConfig) {
	t.Helper()
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	cfg := ServerConfig{
		Name:        "fixture",
		Description: "prepared call fixture",
		Transport:   TransportStreamableHTTP,
		URL:         "http://example.invalid/mcp",
		Enabled:     true,
		TimeoutMS:   1000,
	}
	if _, err = manager.Add(cfg); err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	state := manager.states[cfg.Name]
	cfg = manager.servers[cfg.Name]
	manager.mu.RUnlock()
	client := &preparedCallTestClient{tools: []Tool{preparedCallTool("string", false)}}
	client.revision.Store(1)
	compiled, err := compileCatalog(context.Background(), cfg, client.tools)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.client = client
	state.clientGeneration = 1
	state.catalogGeneration = 1
	state.tools = compiled
	state.discovered = true
	state.indexRevision = 1
	state.refreshedAt = time.Now().UTC()
	publishStateLocked(state)
	state.mu.Unlock()
	return manager, state, client, cfg
}

func assertPreparedCallChanged(t *testing.T, err error) *Error {
	t.Helper()
	var mcpErr *Error
	if !errors.As(err, &mcpErr) || mcpErr.Code != "MCP_TOOL_CHANGED_AFTER_APPROVAL" {
		t.Fatalf("error = %#v, want MCP_TOOL_CHANGED_AFTER_APPROVAL", err)
	}
	if mcpErr.Retryable || mcpErr.Details["executed"] != false || mcpErr.Details["next_action"] != "inspect_tool_again" {
		t.Fatalf("unexpected changed-call details: %#v", mcpErr)
	}
	return mcpErr
}

func TestPreparedCallExecutesFixedArgumentsOnce(t *testing.T) {
	manager, _, client, _ := newPreparedCallTestManager(t)
	arguments := map[string]any{"value": "original"}
	prepared, err := manager.PrepareCall(context.Background(), "fixture:write", arguments)
	if err != nil {
		t.Fatal(err)
	}
	arguments["value"] = "mutated"
	if !prepared.Matches("fixture:write", map[string]any{"value": "original"}) || prepared.Matches("fixture:write", arguments) {
		t.Fatal("prepared argument identity was not fixed")
	}
	if _, err = manager.CallPrepared(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("tools/call count = %d, want 1", client.calls.Load())
	}
	client.mu.Lock()
	got := client.lastArgs["value"]
	client.mu.Unlock()
	if got != "original" {
		t.Fatalf("dispatched argument = %#v, want original", got)
	}
}

func TestPreparedCallRejectsDefinitionChangesWithoutDispatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *serverState, *preparedCallTestClient, ServerConfig)
	}{
		{
			name: "schema changes although both schemas accept arguments",
			mutate: func(t *testing.T, state *serverState, _ *preparedCallTestClient, cfg ServerConfig) {
				tools, err := compileCatalog(context.Background(), cfg, []Tool{preparedCallTool("number", false)})
				if err != nil {
					t.Fatal(err)
				}
				state.tools = tools
			},
		},
		{
			name: "annotations change from modifying to read only",
			mutate: func(t *testing.T, state *serverState, _ *preparedCallTestClient, cfg ServerConfig) {
				tools, err := compileCatalog(context.Background(), cfg, []Tool{preparedCallTool("string", true)})
				if err != nil {
					t.Fatal(err)
				}
				state.tools = tools
			},
		},
		{
			name: "tool removed",
			mutate: func(_ *testing.T, state *serverState, _ *preparedCallTestClient, _ ServerConfig) {
				state.tools = map[string]Tool{}
			},
		},
		{
			name: "client generation replaced with same tool",
			mutate: func(_ *testing.T, state *serverState, _ *preparedCallTestClient, _ ServerConfig) {
				state.clientGeneration++
			},
		},
		{
			name: "catalog revision changes",
			mutate: func(_ *testing.T, state *serverState, _ *preparedCallTestClient, _ ServerConfig) {
				state.indexRevision++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, state, client, cfg := newPreparedCallTestManager(t)
			prepared, err := manager.PrepareCall(context.Background(), "fixture:write", map[string]any{"value": "approved"})
			if err != nil {
				t.Fatal(err)
			}
			state.mu.Lock()
			test.mutate(t, state, client, cfg)
			state.refreshedAt = time.Now().UTC()
			publishStateLocked(state)
			state.mu.Unlock()
			_, err = manager.CallPrepared(context.Background(), prepared)
			assertPreparedCallChanged(t, err)
			if client.calls.Load() != 0 {
				t.Fatalf("changed prepared call dispatched %d times", client.calls.Load())
			}
		})
	}
}

func TestPreparedCallIgnoresDescriptionOnlyUpdate(t *testing.T) {
	manager, _, client, _ := newPreparedCallTestManager(t)
	prepared, err := manager.PrepareCall(context.Background(), "fixture:write", map[string]any{"value": "approved"})
	if err != nil {
		t.Fatal(err)
	}
	listed := manager.List()
	if len(listed) != 1 {
		t.Fatalf("server count = %d", len(listed))
	}
	raw, _ := json.Marshal("updated presentation description")
	if _, err = manager.Update(context.Background(), "fixture", listed[0].Revision, "runtime", ConfigPatch{"description": raw}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.CallPrepared(context.Background(), prepared); err != nil {
		t.Fatalf("description-only update invalidated prepared call: %v", err)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("tools/call count = %d, want 1", client.calls.Load())
	}
}

func TestPreparedCallSummaryExcludesArgumentsAndSchemas(t *testing.T) {
	manager, _, _, _ := newPreparedCallTestManager(t)
	prepared, err := manager.PrepareCall(context.Background(), "fixture:write", map[string]any{"value": "private-approved-argument"})
	if err != nil {
		t.Fatal(err)
	}
	summary := prepared.Summary()
	if summary.QualifiedName != "fixture:write" || summary.ServerFingerprint == "" || summary.ClientGeneration == 0 || summary.CatalogRevision == "" || summary.ToolSchemaDigest == "" || summary.ToolAnnotationsDigest == "" || summary.PreparedAt.IsZero() {
		t.Fatalf("incomplete prepared-call summary: %+v", summary)
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"private-approved-argument", "properties", "additionalProperties", "readOnlyHint"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("prepared-call summary leaked %q: %s", forbidden, text)
		}
	}
}

func TestPreparedCallCancellationDoesNotDispatch(t *testing.T) {
	manager, _, client, _ := newPreparedCallTestManager(t)
	prepared, err := manager.PrepareCall(context.Background(), "fixture:write", map[string]any{"value": "approved"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = manager.CallPrepared(ctx, prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if client.calls.Load() != 0 {
		t.Fatalf("cancelled prepared call dispatched %d times", client.calls.Load())
	}
}

func TestPreparedCallRejectsUnrepresentableOutputSchemaWithoutDispatch(t *testing.T) {
	manager, state, client, cfg := newPreparedCallTestManager(t)
	tool := preparedCallTool("string", false)
	tool.OutputSchema = map[string]any{
		"type": "object", "properties": map[string]any{
			"value": map[string]any{"type": "number", "minimum": json.Number("1e1000001")},
		},
	}
	compiled, err := compileCatalog(context.Background(), cfg, []Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.tools = compiled
	publishStateLocked(state)
	state.mu.Unlock()
	prepared, err := manager.PrepareCall(context.Background(), "fixture:write", map[string]any{"value": "approved"})
	var failure *Error
	if prepared != nil || !errors.As(err, &failure) || failure.Code != "MCP_OUTPUT_SCHEMA_INVALID" || failure.Retryable || failure.Details["executed"] != false {
		t.Fatalf("invalid output schema was not rejected before dispatch: %#v", err)
	}
	if client.calls.Load() != 0 {
		t.Fatalf("invalid output schema dispatched %d calls", client.calls.Load())
	}
}
