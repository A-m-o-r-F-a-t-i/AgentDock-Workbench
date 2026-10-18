//go:build windows

package selfupdate

import (
	"context"
	"os/exec"

	processcontrol "github.com/uvwt/agentdock/internal/process"
)

func hiddenWindowsCommand(name string, args ...string) *exec.Cmd {
	command := exec.Command(name, args...)
	processcontrol.Configure(command)
	return command
}

func hiddenWindowsCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	processcontrol.Configure(command)
	return command
}
