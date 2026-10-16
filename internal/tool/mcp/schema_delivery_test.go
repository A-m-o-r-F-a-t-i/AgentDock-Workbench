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

func TestSchemaDeliveryUsesConversationRevisionAndForceFull(t *testing.T) {
	now := time.Unix(1000, 0)
	state := schemaDeliveryState{now: func() time.Time { return now }}
	binding := activity.Binding{
		SourceOwnerKey: "owner", ConversationID: "conversation", WorkspaceID: "workspace",
		TaskID: "task", ThreadID: "main", BindingRevision: 1,
	}
	ctx := activity.WithBinding(t.Context(), binding)

	full := state.prepare(ctx, testSchema("v1"), false)
	if full["delivery"] != "full" || full["schema_omitted"] != false || full["input_schema"] == nil {
		t.Fatalf("first schema delivery is incomplete: %#v", full)
	}
	reference := state.prepare(ctx, testSchema("v1"), false)
	if reference["delivery"] != "reference" || reference["schema_omitted"] != true || reference["input_schema"] != nil || reference["refresh_tool"] != ToolInspect {
		t.Fatalf("repeat schema delivery is not compact: %#v", reference)
	}

	changedBinding := binding
	changedBinding.WorkspaceID = "other-workspace"
	changedBinding.BindingRevision++
	reused := state.prepare(activity.WithBinding(t.Context(), changedBinding), testSchema("v1"), false)
	if reused["delivery"] != "reference" {
		t.Fatalf("binding change duplicated a conversation schema: %#v", reused)
	}
	forced := state.prepare(ctx, testSchema("v1"), true)
	if forced["delivery"] != "full" || forced["input_schema"] == nil {
		t.Fatalf("force_full did not restore the schema: %#v", forced)
	}
	changed := state.prepare(ctx, testSchema("v2"), false)
	if changed["delivery"] != "full" || changed["input_schema"] == nil {
		t.Fatalf("new schema revision was compacted: %#v", changed)
	}
	now = now.Add(schemaDeliveryWindow)
	expired := state.prepare(ctx, testSchema("v2"), false)
	if expired["delivery"] != "full" || expired["input_schema"] == nil {
		t.Fatalf("expired schema reference was reused: %#v", expired)
	}

	for range 2 {
		legacy := state.prepare(t.Context(), testSchema("v1"), false)
		if legacy["delivery"] != "full" || legacy["input_schema"] == nil {
			t.Fatalf("unattributed client lost full schemas: %#v", legacy)
		}
	}
}
