package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

func builtinToolTestContext(host string) context.Context {
	return activity.WithSource(context.Background(), activity.Source{
		Principal: "bundle-test-user", Provider: "openai", Namespace: "mcp", HostConversationID: host,
	})
}

func builtinToolBundleLoaded(items []capabilityBuiltinToolBundleItem, name string) (bool, bool) {
	for _, item := range items {
		if item.Name == name {
			return item.Loaded, true
		}
	}
	return false, false
}

func TestBuiltinToolBundlesIsolateScopesAndGateRuntimeCalls(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	ctxA := builtinToolTestContext("bundle-scope-a")
	ctxB := builtinToolTestContext("bundle-scope-b")

	contextResult, err := runtime.Call(ctxA, "agentdock_context", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var initial capabilityContext
	if err := remarshal(contextResult, &initial); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{toolcapability.BundleSystemManagement, toolcapability.BundleArtifactPublishing} {
		loaded, found := builtinToolBundleLoaded(initial.BuiltinToolBundles, name)
		if !found || loaded {
			t.Fatalf("initial bundle %s = found:%t loaded:%t; items=%#v", name, found, loaded, initial.BuiltinToolBundles)
		}
	}
	if runtime.BuiltinToolVisible(ctxA, "mcp_manage") || !runtime.BuiltinToolVisible(ctxA, "read_file") {
		t.Fatal("trusted scope did not hide only bundled tools")
	}
	if !runtime.BuiltinToolVisible(context.Background(), "mcp_manage") {
		t.Fatal("legacy unscoped client lost the complete tool directory")
	}

	_, err = runtime.Call(ctxA, "mcp_manage", map[string]any{"action": "list"})
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "TOOL_BUNDLE_REQUIRED" || toolErr.Details["executed"] != false {
		t.Fatalf("hidden direct call error = %T %#v", err, err)
	}
	if toolErr.Details["bundle"] != toolcapability.BundleSystemManagement || toolErr.Details["loader"] != toolcapability.ToolLoad {
		t.Fatalf("hidden direct call details = %#v", toolErr.Details)
	}
	guidance, _ := toolErr.Details["agentdock_guidance"].(map[string]any)
	if guidance["next_required"] == nil {
		t.Fatalf("hidden direct call has no load guidance: %#v", toolErr.Details)
	}

	var directoryChanges atomic.Int32
	runtime.OnBuiltinToolDirectoryChanged(func() { directoryChanges.Add(1) })
	loaded, err := runtime.Call(ctxA, toolcapability.ToolLoad, map[string]any{
		"action": toolcapability.ActionLoad, "name": toolcapability.BundleSystemManagement,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, toolcapability.ToolLoad, loaded)
	if loaded["changed"] != true || loaded["directory_changed"] != true || loaded["scoped"] != true || directoryChanges.Load() != 1 {
		t.Fatalf("first load result=%#v notifications=%d", loaded, directoryChanges.Load())
	}
	for _, name := range []string{"mcp_manage", "plugin_manage", "skill_package"} {
		if !runtime.BuiltinToolVisible(ctxA, name) {
			t.Fatalf("loaded scope did not reveal %s", name)
		}
		if runtime.BuiltinToolVisible(ctxB, name) {
			t.Fatalf("bundle visibility leaked to second scope for %s", name)
		}
	}
	if runtime.BuiltinToolVisible(ctxA, "file_publish") {
		t.Fatal("loading system-management revealed artifact publishing")
	}

	repeated, err := runtime.Call(ctxA, toolcapability.ToolLoad, map[string]any{
		"action": toolcapability.ActionLoad, "name": toolcapability.BundleSystemManagement,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeated["changed"] != false || repeated["directory_changed"] != false || directoryChanges.Load() != 1 {
		t.Fatalf("idempotent load result=%#v notifications=%d", repeated, directoryChanges.Load())
	}
	if _, err := runtime.Call(ctxA, "mcp_manage", map[string]any{"action": "list"}); err != nil {
		t.Fatalf("revealed mcp_manage call failed: %v", err)
	}

	afterResult, err := runtime.Call(ctxA, "agentdock_context", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var after capabilityContext
	if err := remarshal(afterResult, &after); err != nil {
		t.Fatal(err)
	}
	if loaded, found := builtinToolBundleLoaded(after.BuiltinToolBundles, toolcapability.BundleSystemManagement); !found || !loaded {
		t.Fatalf("context did not report loaded bundle: %#v", after.BuiltinToolBundles)
	}
	if loaded, found := builtinToolBundleLoaded(after.BuiltinToolBundles, toolcapability.BundleArtifactPublishing); !found || loaded {
		t.Fatalf("context reported unrelated bundle loaded: %#v", after.BuiltinToolBundles)
	}

	conversationID := stringArg(afterResult, "conversation_id")
	if conversationID == "" {
		t.Fatalf("agentdock_context returned no conversation id: %#v", afterResult)
	}
	if _, err := runtime.RuntimeConversationLifecycle(activity.WithLocalManagement(ctxA), conversationID, "terminate", ConversationLifecycleRequest{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if runtime.BuiltinToolVisible(ctxA, "mcp_manage") || directoryChanges.Load() != 2 {
		t.Fatalf("termination did not clear bundle state; notifications=%d", directoryChanges.Load())
	}
}

func TestBuiltinToolBundleStateIsBoundedAndExpires(t *testing.T) {
	bundle, _ := toolcapability.ByName(toolcapability.BundleSystemManagement)
	now := time.Unix(1_700_000_000, 0)
	state := builtinToolBundleSessions{now: func() time.Time { return now }}
	for index := 0; index <= builtinToolBundleSessionCapacity; index++ {
		ctx := builtinToolTestContext(fmt.Sprintf("capacity-%04d", index))
		changed, scoped := state.load(ctx, bundle)
		if !changed || !scoped {
			t.Fatalf("load %d = changed:%t scoped:%t", index, changed, scoped)
		}
		now = now.Add(time.Millisecond)
	}
	state.mu.Lock()
	count := len(state.entries)
	state.mu.Unlock()
	if count != builtinToolBundleSessionCapacity {
		t.Fatalf("bounded bundle sessions=%d want=%d", count, builtinToolBundleSessionCapacity)
	}
	if state.visible(builtinToolTestContext("capacity-0000"), "mcp_manage") {
		t.Fatal("oldest bundle session was not evicted")
	}
	if !state.visible(builtinToolTestContext(fmt.Sprintf("capacity-%04d", builtinToolBundleSessionCapacity)), "mcp_manage") {
		t.Fatal("newest bundle session was evicted")
	}

	expiring := builtinToolBundleSessions{now: func() time.Time { return now }}
	ctx := builtinToolTestContext("expires")
	if changed, scoped := expiring.load(ctx, bundle); !changed || !scoped {
		t.Fatalf("initial expiring load = changed:%t scoped:%t", changed, scoped)
	}
	now = now.Add(builtinToolBundleSessionTTL)
	if expiring.visible(ctx, "mcp_manage") {
		t.Fatal("expired bundle session remained visible")
	}
}
