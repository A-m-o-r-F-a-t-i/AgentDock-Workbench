package desktopruntime

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunTunnelCommandRejectsUnknownAction(t *testing.T) {
	err := RunTunnelCommand(context.Background(), []string{"unknown"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "agentdock tunnel") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTunnelCommandRequiresRuntimeRoot(t *testing.T) {
	for _, action := range []string{"start", "verify", "repair"} {
		t.Run(action, func(t *testing.T) {
			err := RunTunnelCommand(context.Background(), []string{action}, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "--runtime-root") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunTunnelCommandValidatesConfigureModeBeforePlatformAccess(t *testing.T) {
	err := RunTunnelCommand(
		context.Background(),
		[]string{"configure", "--runtime-root", t.TempDir(), "--mode", "invalid"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTunnelCommandValidatesAutostartBooleanBeforePlatformAccess(t *testing.T) {
	err := RunTunnelCommand(
		context.Background(),
		[]string{"autostart", "--runtime-root", t.TempDir(), "--enabled", "yes"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}
