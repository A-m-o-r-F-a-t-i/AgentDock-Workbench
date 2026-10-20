package mcp

import (
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func testSchema(revision string) map[string]any {
	return map[string]any{
		"name": "fixture:read", "server": "fixture", "tool_name": "read",
		"catalog_revision": revision, "server_version": "1.0.0",
		"server_fingerprint": "fixture-server", "schema_digest": "fixture-schema", "annotations_digest": "fixture-annotations",
		"input_schema": map[string]any{"type": "object"},
		"description":  "Read fixture data.",
	}
}

func TestSchemaDeliveryUsesFingerprintDigestAndForceFull(t *testing.T) {
	now := time.Unix(1000, 0)
	state := schemaDeliveryState{now: func() time.Time { return now }}
	binding := activity.Binding{
		SourceOwnerKey: "owner", ConversationID: "conversation", WorkspaceID: "workspace",
		TaskID: "task", ThreadID: "main", BindingRevision: 1,
	}
	ctx := activity.WithBinding(t.Context(), binding)

	full := state.prepare(ctx, testSchema("v1"), false)
	if full["delivery"] != nil || full["schema_delivery"] != "full" || full["schema_omitted"] != false || full["input_schema"] == nil {
		t.Fatalf("first schema delivery is incomplete: %#v", full)
	}
	reference := state.prepare(ctx, testSchema("v1"), false)
	if reference["delivery"] != nil || reference["schema_delivery"] != "reference" || reference["schema_omitted"] != true || reference["input_schema"] != nil || reference["refresh_tool"] != ToolInspect {
		t.Fatalf("repeat schema delivery is not compact: %#v", reference)
	}

	changedBinding := binding
	changedBinding.WorkspaceID = "other-workspace"
	changedBinding.BindingRevision++
	reused := state.prepare(activity.WithBinding(t.Context(), changedBinding), testSchema("v1"), false)
	if reused["schema_delivery"] != "reference" {
		t.Fatalf("binding change duplicated a conversation schema: %#v", reused)
	}
	forced := state.prepare(ctx, testSchema("v1"), true)
	if forced["schema_delivery"] != "full" || forced["input_schema"] == nil {
		t.Fatalf("force_full did not restore the schema: %#v", forced)
	}
	changed := state.prepare(ctx, testSchema("v2"), false)
	if changed["schema_delivery"] != "reference" || changed["input_schema"] != nil || changed["catalog_revision"] != "v2" {
		t.Fatalf("unchanged schema was resent after unrelated catalog revision: %#v", changed)
	}
	newSchema := testSchema("v2")
	newSchema["schema_digest"] = "new-schema-digest"
	newSchema["input_schema"] = map[string]any{"type": "object", "required": []string{"path"}}
	changed = state.prepare(ctx, newSchema, false)
	if changed["schema_delivery"] != "full" || changed["input_schema"] == nil {
		t.Fatalf("changed schema digest was not delivered: %#v", changed)
	}
	newServer := testSchema("v2")
	newServer["server_fingerprint"] = "replacement-server"
	changed = state.prepare(ctx, newServer, false)
	if changed["schema_delivery"] != "full" || changed["input_schema"] == nil {
		t.Fatalf("new server fingerprint reused a different server's schema: %#v", changed)
	}
	now = now.Add(schemaDeliveryWindow)
	expired := state.prepare(ctx, testSchema("v2"), false)
	if expired["schema_delivery"] != "full" || expired["input_schema"] == nil {
		t.Fatalf("expired schema reference was reused: %#v", expired)
	}

	for range 2 {
		legacy := state.prepare(t.Context(), testSchema("v1"), false)
		if legacy["schema_delivery"] != "full" || legacy["input_schema"] == nil {
			t.Fatalf("unattributed client lost full schemas: %#v", legacy)
		}
	}
}

func TestSchemaReferencePreservesCurrentAnnotationsAndConversationScope(t *testing.T) {
	state := schemaDeliveryState{}
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation"}
	ctx := activity.WithBinding(t.Context(), binding)
	state.prepare(ctx, testSchema("v1"), false)
	current := testSchema("v2")
	current["annotations_digest"] = "new-annotations"
	current["annotations"] = map[string]any{"readOnlyHint": false, "destructiveHint": true}
	current["metadata"] = map[string]any{"mcp/www_authenticate": []string{"Bearer"}}
	reference := state.prepare(ctx, current, false)
	if reference["schema_delivery"] != "reference" || reference["annotations_digest"] != "new-annotations" || reference["annotations"] == nil || reference["metadata"] == nil {
		t.Fatalf("schema reuse hid changed annotations or decision metadata: %#v", reference)
	}
	for _, other := range []activity.Binding{
		{SourceOwnerKey: "other-owner", ConversationID: "conversation"},
		{SourceOwnerKey: "owner", ConversationID: "other-conversation"},
	} {
		if full := state.prepare(activity.WithBinding(t.Context(), other), current, false); full["schema_delivery"] != "full" || full["input_schema"] == nil {
			t.Fatalf("schema reference crossed a conversation/owner boundary: %#v", full)
		}
	}
}

func TestSchemaDeliveryAcknowledgesOnlyCompletedPages(t *testing.T) {
	service := &Service{}
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation"}
	ctx := activity.WithBinding(t.Context(), binding)
	schema := testSchema("v1")
	for range 2 {
		if pending := service.schemaDeliveries.prepare(WithDeferredSchemaDelivery(ctx), schema, false); pending["input_schema"] == nil {
			t.Fatal("an uncommitted schema page became a compact reference")
		}
	}
	service.CommitSchemas(binding, map[string]any{"tools": []any{schema}})
	if reference := service.schemaDeliveries.prepare(WithDeferredSchemaDelivery(ctx), schema, false); reference["input_schema"] != nil || reference["schema_delivery"] != "reference" {
		t.Fatalf("complete encoded schema was not acknowledged: %#v", reference)
	}
}
