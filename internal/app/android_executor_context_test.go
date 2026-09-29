package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/androidbridge"
)

func TestAndroidExecutorContextHasClosedSchemaAndNoLease(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := scopeHost("android-provider-context")
	check := func(connected bool) {
		t.Helper()
		result, err := r.Call(ctx, "agentdock_context", nil)
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, "agentdock_context", result)
		status, ok := result["android_executor"].(map[string]any)
		if !ok || status["connected"] != connected {
			t.Fatalf("unexpected provider status: %v", status)
		}
		raw, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "token") {
			t.Fatal("provider credential leaked into bootstrap")
		}
	}
	check(false)
	uid := 2000
	registration, err := r.androidExecutor.Register(androidbridge.RegisterRequest{
		Protocol: 1, WorkerID: strings.Repeat("a", 32),
		Backends: map[string]androidbridge.Capability{
			"termux_host":     {Ready: false, State: "verification_required"},
			"android_shizuku": {Ready: true, State: "verified", UID: &uid},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := r.androidExecutor.Disconnect(registration.LeaseToken); err != nil {
		t.Fatal(err)
	}
	check(false)
}
