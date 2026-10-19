package mcp

import (
	"strings"
	"testing"
)

func TestDeviceRuntimeInstructionsEncourageTaskProgressManagement(t *testing.T) {
	for _, required := range []string{
		"task_manage", "create or resume", "concrete steps and checkpoints", "status and next_action", "do not mark work complete before verification",
	} {
		if !strings.Contains(deviceRuntimeInstructions, required) {
			t.Fatalf("device runtime instructions missing task progress guidance %q", required)
		}
	}
}
