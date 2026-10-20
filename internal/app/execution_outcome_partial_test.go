package app

import (
	"reflect"
	"testing"
)

func TestPartialMCPOutcomeKeepsChangedAndSavedFacts(t *testing.T) {
	counts := map[string]any{"applied": 36, "failed": 6}
	result := Result{"result": map[string]any{
		"isError": true,
		"structuredContent": map[string]any{
			"outcome":     "PARTIAL",
			"counts":      counts,
			"effectState": "changed",
			"saved":       true,
		},
	}}
	if !businessOutcomeFailure("mcp_tool_call", result) {
		t.Fatal("partial MCP result was treated as complete success")
	}
	failure := outcomeFailure("mcp_tool_call", result, nil)
	if failure.Code != "MCP_BUSINESS_PARTIAL" || !reflect.DeepEqual(failure.Details["counts"], counts) || failure.Details["saved"] != true {
		t.Fatalf("partial failure details = %#v", failure)
	}
	control := failureControl("mcp_tool_call", result, failure, executionObservation{executed: true})
	if control["operation_status"] != "partial" || control["effect_state"] != "partial" || control["execution_performed"] != true || control["retry_safe"] != false {
		t.Fatalf("partial execution control = %#v", control)
	}
}
