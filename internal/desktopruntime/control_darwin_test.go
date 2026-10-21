//go:build darwin

package desktopruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/desktopcontrol"
)

func TestDispatchControlRequestIncludesNexusConnection(t *testing.T) {
	runtimeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeRoot, "agentdock.env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// 隔离真实 launchd，确保测试只验证本地控制状态的注入契约。
	t.Setenv("AGENTDOCK_LAUNCHCTL_BIN", "/usr/bin/false")
	params, err := json.Marshal(controlActionParams{RuntimeRoot: runtimeRoot})
	if err != nil {
		t.Fatal(err)
	}

	result, err := DispatchControlRequest(
		context.Background(),
		desktopcontrol.Request{ID: "test", Method: "service.status", Params: params},
		ControlRuntimeStatus{NexusConnected: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.(ServiceStatus)
	if !ok {
		t.Fatalf("service.status returned %T, want ServiceStatus", result)
	}
	if !status.NexusConnected {
		t.Fatal("service.status did not include live Nexus connection state")
	}
}

func TestLaunchdPIDFromDescription(t *testing.T) {
	description := `gui/501/com.uvwt.agentdock.core = {
	active count = 1
	pid = 4321
}`
	if pid := launchdPIDFromDescription(description); pid != 4321 {
		t.Fatalf("pid=%d want 4321", pid)
	}
	for _, value := range []string{"state = waiting", "pid = 1", "pid = invalid"} {
		if pid := launchdPIDFromDescription(value); pid != 0 {
			t.Fatalf("description %q produced pid=%d", value, pid)
		}
	}
}
