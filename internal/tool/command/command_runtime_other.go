//go:build !windows

package command

import (
	"context"
	"strings"
)

func (svc *Service) prepareCommandInvocation(ctx context.Context, request ExecRequest) (commandInvocation, error) {
	if runtimeName := strings.TrimSpace(request.Runtime); runtimeName != "" {
		return commandInvocation{}, toolError("INVALID_ARGUMENT", "runtime is only supported by AgentDock on Windows", "validation")
	}
	if distribution := strings.TrimSpace(request.WSLDistribution); distribution != "" {
		return commandInvocation{}, toolError("INVALID_ARGUMENT", "wsl_distribution is only supported by AgentDock on Windows", "validation")
	}
	return svc.newHostCommandInvocation(ctx, request)
}

func AddRuntimeProperties(_ map[string]any) {}

func WorkdirDescription() string {
	return "Working directory; omitted paths inherit the bound workspace, otherwise the device default."
}

func Description() string {
	return "Run a command with the configured shell and one main success boundary. Use search_text for source search. Separate network operations, GitHub queries, source changes and builds when their outcomes require independent decisions. A running response preserves the session for later observation."
}
