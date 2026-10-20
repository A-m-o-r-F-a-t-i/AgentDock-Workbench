package client

import (
	"reflect"
	"testing"
)

func TestResultFailurePreservesPartialOutcomeFacts(t *testing.T) {
	counts := map[string]any{"applied": 36, "failed": 6}
	action := map[string]any{"action": "REVIEW_UNCOMPLETED_OPERATIONS"}
	details, failed := ResultFailure(map[string]any{
		"isError": true,
		"structuredContent": map[string]any{
			"outcome":        "PARTIAL",
			"counts":         counts,
			"effectState":    "changed",
			"saved":          true,
			"requiredAction": action,
		},
	})
	if !failed {
		t.Fatal("partial result was not classified as an incomplete business outcome")
	}
	if details["operation_status"] != "partial" || details["effect_state"] != "partial" || details["execution_performed"] != true || details["retry_safe"] != false {
		t.Fatalf("partial control facts = %#v", details)
	}
	if details["outcome"] != "PARTIAL" || details["saved"] != true || !reflect.DeepEqual(details["counts"], counts) || !reflect.DeepEqual(details["required_action"], action) {
		t.Fatalf("partial business facts were not preserved: %#v", details)
	}
}

func TestResultFailureKeepsCompleteFailureSemantics(t *testing.T) {
	details, failed := ResultFailure(map[string]any{
		"isError": true,
		"structuredContent": map[string]any{
			"code":         "BROKEN",
			"effect_state": "none",
		},
	})
	if !failed || details["operation_status"] == "partial" || details["effect_state"] != "none" {
		t.Fatalf("complete failure semantics changed: %#v", details)
	}
}
