package app

import "testing"

func TestContextReuseActionStopsContextPolling(t *testing.T) {
	action := contextReuseAction()
	if action["action"] != "reuse_context" || action["required"] != false || action["execution_performed"] != false || action["reason_code"] != "context_current" {
		t.Fatalf("context reuse action = %#v", action)
	}
}
