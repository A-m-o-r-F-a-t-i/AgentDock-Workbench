package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func TestCanonicalOutputValidationCannotBeSatisfiedByProjection(t *testing.T) {
	schema := canonicalExecutionOutputSchema("apply_patch", map[string]any{
		"type": "object", "required": []string{"files_changed"},
		"properties": map[string]any{"files_changed": map[string]any{"type": "integer"}},
	})
	validator, err := compileBuiltInValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{toolOutputValidators: map[string]*toolcontract.InputValidator{"apply_patch": validator}}
	p := &preparedExecution{spec: ToolSpec{Name: "apply_patch"}, state: executionObservation{executed: true}}
	result := Result{
		"effect_state": "complete", "changed": true,
		"operation": map[string]any{}, "capture": map[string]any{},
		"projection": map[string]any{"mode": "compact_receipt", "complete": true},
		"delivery": map[string]any{
			"class": "compact_receipt", "sequence_complete": true,
			"continuation_required": false, "continuation_available": false,
		},
	}
	err = r.validateExecutionResult(p, result)
	var failure *ToolError
	if !errors.As(err, &failure) || failure.Code != "OUTPUT_SCHEMA_MISMATCH" {
		t.Fatalf("missing business result passed canonical validation: %v", err)
	}
	if failure.Details["execution_performed"] != true || failure.Details["effect_state"] != "complete" || failure.Details["retry_safe"] != false {
		t.Fatalf("validation failure lost known execution effects: %#v", failure.Details)
	}
	if result["changed"] != true || result["call_id"] != nil || result["permission"] != nil {
		t.Fatalf("validation mutated the retained business result: %#v", result)
	}
	result["files_changed"] = 1
	if err := r.validateExecutionResult(p, result); err != nil {
		t.Fatalf("valid canonical result rejected: %v", err)
	}
}

func TestOutputContractFailureCannotAuthorizeRetry(t *testing.T) {
	for _, code := range []string{"OUTPUT_SCHEMA_MISMATCH", "MCP_OUTPUT_SCHEMA_MISMATCH", "OUTPUT_VALIDATOR_UNAVAILABLE"} {
		t.Run(code, func(t *testing.T) {
			r := &Runtime{}
			result := Result{"executed": false, "execution_performed": false, "retry_safe": true, "effect_state": "none"}
			failure := toolErrorDetails(code, "Invalid output", "runtime", map[string]any{
				"execution_performed": true, "effect_state": "none", "retry_safe": false,
			})
			_, err, metadata := r.executionOutcome("mcp_tool_call", nil, result, failure, executionObservation{executed: true}, nil)
			var got *ToolError
			if !errors.As(err, &got) || got.Details["retry_safe"] != false || got.Details["execution_performed"] != true {
				t.Fatalf("unvalidated output replaced execution controls: %#v", err)
			}
			if metadata == nil || metadata.RetrySafe == nil || *metadata.RetrySafe || metadata.ExecutionPerformed == nil || !*metadata.ExecutionPerformed {
				t.Fatalf("Activity disagrees with the contract failure: %#v", metadata)
			}
		})
	}
}

func TestCommandOutputContractRejectsMissingSessionFacts(t *testing.T) {
	r := executionTestRuntime(t)
	for _, test := range []struct {
		name  string
		valid []Result
	}{
		{"exec_command", []Result{{"session_id": "fixture-session", "status": "running"}}},
		{"session_observe", []Result{{"session_id": "fixture-session", "status": "exited", "command_ok": true}, {"sessions": []any{}, "count": 0}}},
		{"session_act", []Result{{"session_id": "fixture-session", "status": "cancelled", "command_ok": false}, {"sessions": []any{}, "count": 0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &preparedExecution{spec: ToolSpec{Name: test.name}, state: executionObservation{executed: true}}
			// Use the real registered owner contract, before projection or common
			// execution fields could make an empty handler result look complete.
			err := r.validateExecutionResult(p, Result{})
			var failure *ToolError
			if !errors.As(err, &failure) || failure.Code != "OUTPUT_SCHEMA_MISMATCH" {
				t.Fatalf("empty command output passed its owner contract: %v", err)
			}
			for _, valid := range test.valid {
				if err := r.validateExecutionResult(p, valid); err != nil {
					t.Fatalf("valid session/collection facts rejected: %#v %v", valid, err)
				}
			}
		})
	}
}

func TestMCPBusinessFailureRetainsOriginalAction(t *testing.T) {
	r := &Runtime{}
	action := map[string]any{"category": "model_decision", "reason_code": "fix_input", "required": false}
	result := Result{"result": map[string]any{"structuredContent": map[string]any{
		"ok": false, "error": map[string]any{"code": "INVALID_DOCUMENT", "message": "Check the document."},
		"required_action": action, "effect_state": "none", "execution_performed": false,
	}}}
	got, err, metadata := r.executionOutcome("mcp_tool_call", nil, result, nil, executionObservation{executed: true}, nil)
	if err != nil || got["error_code"] != "INVALID_DOCUMENT" || !businessOutcomeFailure("mcp_tool_call", got) {
		t.Fatalf("business failure was reclassified: %#v %v", got, err)
	}
	if metadata == nil || metadata.RequiredAction == nil || metadata.RequiredAction.ReasonCode != "fix_input" {
		t.Fatalf("business required_action was lost: %#v", metadata)
	}
}

func TestFailedSchemaOutputPagesRetainFailureWithoutReexecution(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(context.Background(), activity.Source{
		Principal: "output-validation-test", Namespace: "mcp", HostConversationID: "failed-schema-pages",
	})
	calls := 0
	spec := ToolSpec{Name: "mcp_tool_inspect", Handler: func(context.Context, *Runtime, map[string]any) (Result, error) {
		calls++
		return Result{
			"name": "demo:read", "server": "demo", "tool_name": "read",
			"server_fingerprint": "server-v1", "catalog_revision": "catalog-v1", "schema_digest": "schema-v1",
			"input_schema": map[string]any{"type": "object", "description": strings.Repeat("schema guidance ", 1600)},
			"count":        "invalid integer",
		}, nil
	}}
	args := map[string]any{"name": "demo:read", "max_response_bytes": 4096}
	result, err := r.callObserved(ctx, spec, args)
	snapshotID := stringArg(modelObject(result["structured_output"]), "snapshot_id")
	if snapshotID == "" || stringArg(result, "next_cursor") == "" {
		t.Fatalf("invalid full output was not retained with lossless pages: %#v %v", result, err)
	}
	pages := 0
	for {
		pages++
		var failure *ToolError
		if !errors.As(err, &failure) || failure.Code != "OUTPUT_SCHEMA_MISMATCH" || failure.Details["retry_safe"] != false {
			t.Fatalf("page %d lost the original output failure: %#v %v", pages, result, err)
		}
		call, callErr := r.activity.Call(ctx, stringArg(result, "call_id"))
		if callErr != nil || call.Status != "failed" || call.ErrorCode != "OUTPUT_SCHEMA_MISMATCH" {
			t.Fatalf("page %d has a successful Activity: %#v %v", pages, call, callErr)
		}
		cursor := stringArg(result, "next_cursor")
		if cursor == "" {
			break
		}
		if pages > 100 {
			t.Fatal("failed output pagination did not terminate")
		}
		result, err = r.callObserved(ctx, spec, map[string]any{"name": "demo:read", "max_response_bytes": 4096, "cursor": cursor})
	}
	if calls != 1 {
		t.Fatalf("failed output continuation invoked the handler %d times", calls)
	}
	r.structuredOutputMu.Lock()
	snapshot := cloneStructuredSnapshot(r.structuredSnapshots[snapshotID])
	r.structuredOutputMu.Unlock()
	if snapshot == nil || snapshot.OutputFailure == nil || snapshot.Delivered || snapshot.NextPage != 0 {
		t.Fatalf("failed schema was acknowledged as callable: %#v", snapshot)
	}
	if snapshot.SourceURI == "" {
		t.Fatal("failed schema has no diagnostic source")
	}
	// Diagnostic source reads must not provide an alternate acknowledgement path.
	read := Result{"path": snapshot.SourceURI}
	page := activity.PayloadPage{Offset: 0, NextOffset: int64(len(snapshot.Encoded)), HasMore: false}
	if err := r.describeRequiredSourcePage(ctx, snapshot.Binding, snapshot.SourceURI, page, read); err != nil {
		t.Fatal(err)
	}
	if read["source_delivery"] != nil {
		t.Fatalf("failed schema source advertised delivery acknowledgement: %#v", read)
	}
	read["source_delivery"] = map[string]any{"snapshot_id": snapshot.ID, "byte_start": 0, "byte_end": len(snapshot.Encoded), "has_more": false}
	r.commitRequiredSource(snapshot.Binding, read)
	r.structuredOutputMu.Lock()
	snapshot = cloneStructuredSnapshot(r.structuredSnapshots[snapshotID])
	r.structuredOutputMu.Unlock()
	if snapshot.Delivered || snapshot.SourceDeliveredOffset != 0 {
		t.Fatal("raw source read made an invalid schema callable")
	}
}

func TestMCPFragmentFailureRetainsBusinessFacts(t *testing.T) {
	r := &Runtime{}
	result := Result{
		"result": map[string]any{"format": "json_fragment", "json_fragment": "{\"long_payload\":"},
		"upstream_failure": map[string]any{
			"code": "INVALID_DOCUMENT", "message": "Inspect the document.", "effect_state": "partial",
			"execution_performed": true, "retry_safe": false,
			"required_action": map[string]any{"category": "model_decision", "reason_code": "verify_document", "required": false},
		},
	}
	got, err, metadata := r.executionOutcome("mcp_tool_call", nil, result, nil, executionObservation{executed: true}, nil)
	if err != nil || got["error_code"] != "INVALID_DOCUMENT" || got["effect_state"] != "partial" || got["execution_performed"] != true || got["retry_safe"] != false {
		t.Fatalf("fragment lost upstream failure facts: %#v %v", got, err)
	}
	if metadata == nil || metadata.RequiredAction == nil || metadata.RequiredAction.ReasonCode != "verify_document" {
		t.Fatalf("fragment lost the original required action: %#v", metadata)
	}
}
