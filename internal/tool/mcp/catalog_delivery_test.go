package mcp

import (
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func testCatalog(revision string) map[string]any {
	return map[string]any{
		"server": "fixture", "catalog_revision": revision, "complete": true, "stale": false, "total": 2,
		"tools": []map[string]any{{"name": "fixture:one", "description": "One."}, {"name": "fixture:two", "description": "Two."}},
	}
}

func TestCatalogDeliveryUsesTrustedScopeRevisionAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	state := catalogDeliveryState{now: func() time.Time { return now }}
	binding := activity.Binding{
		SourceOwnerKey: "owner", ConversationID: "conversation", WorkspaceID: "workspace",
		TaskID: "task", ThreadID: "main", BindingRevision: 1,
	}
	ctx := activity.WithBinding(t.Context(), binding)

	full := state.prepare(ctx, testCatalog("v1"), false)
	if full["delivery"] != "full" || full["catalog_delivery"] != "full" || full["tools_omitted"] != false || catalogToolCount(full) != 2 {
		t.Fatalf("first delivery is not complete: %#v", full)
	}
	reference := state.prepare(ctx, testCatalog("v1"), false)
	if reference["delivery"] != "reference" || reference["catalog_delivery"] != "reference" || reference["tools_omitted"] != true || reference["tools"] != nil || reference["refresh_tool"] != ToolList {
		t.Fatalf("repeat delivery is not a compact reference: %#v", reference)
	}

	otherThread := binding
	otherThread.ThreadID = "parallel"
	reused := state.prepare(activity.WithBinding(t.Context(), otherThread), testCatalog("v1"), false)
	if reused["delivery"] != "reference" || reused["tools_omitted"] != true {
		t.Fatalf("same conversation duplicated catalog after a thread change: %#v", reused)
	}

	forced := state.prepare(ctx, testCatalog("v1"), true)
	if forced["delivery"] != "full" || catalogToolCount(forced) != 2 {
		t.Fatalf("force_full did not restore the complete catalog: %#v", forced)
	}

	changed := state.prepare(ctx, testCatalog("v2"), false)
	if changed["delivery"] != "full" || catalogToolCount(changed) != 2 {
		t.Fatalf("new revision did not restore the full catalog: %#v", changed)
	}
	now = now.Add(catalogDeliveryWindow)
	expired := state.prepare(ctx, testCatalog("v2"), false)
	if expired["delivery"] != "full" || catalogToolCount(expired) != 2 {
		t.Fatalf("expired scope did not restore the full catalog: %#v", expired)
	}

	for range 2 {
		legacy := state.prepare(t.Context(), testCatalog("v1"), false)
		if legacy["delivery"] != "full" || catalogToolCount(legacy) != 2 {
			t.Fatalf("unattributed client lost legacy full catalog behavior: %#v", legacy)
		}
	}
}

func TestCatalogDeliveryKeepsIncompleteOrStaleCatalogsFull(t *testing.T) {
	state := catalogDeliveryState{}
	ctx := activity.WithBinding(t.Context(), activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation"})
	for _, mutation := range []func(map[string]any){
		func(value map[string]any) { value["complete"] = false },
		func(value map[string]any) { value["stale"] = true },
		func(value map[string]any) { value["catalog_revision"] = "" },
		func(value map[string]any) { value["tools"] = []map[string]any{} },
	} {
		catalog := testCatalog("v1")
		mutation(catalog)
		for range 2 {
			result := state.prepare(ctx, catalog, false)
			if result["delivery"] != "full" || result["tools_omitted"] != false {
				t.Fatalf("non-reusable catalog was compacted: %#v", result)
			}
		}
	}
}
