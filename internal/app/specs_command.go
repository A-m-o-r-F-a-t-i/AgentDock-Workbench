package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/activity"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
)

func commandToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "exec_command", Contract: commandToolContract, Title: "Run command", Description: toolcommand.Description(), Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("exec_command", func(ctx context.Context, r *Runtime, request toolcommand.ExecRequest) (Result, error) {
			request.Binding = activity.FromContext(ctx)
			if modelProjectionRequested(ctx) {
				ctx = toolcommand.WithModelOutputProjection(ctx)
			}
			return r.command.Exec(ctx, request)
		})},
		{Name: "session_observe", Contract: commandToolContract, Title: "Observe command sessions", Description: "Read or await an exact exec_command session only when its result is needed. Completed background commands are also reported on a later root call in the same conversation. Continue independent work first; when blocked use action=status with wait_ms (up to 300000), or omit it for adaptive waiting. Completion and request cancellation return early. action=list is recovery-only; do not poll unrelated AgentDock state.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("session_observe", func(ctx context.Context, r *Runtime, request toolcommand.SessionObserveRequest) (Result, error) {
			return r.command.ObserveContext(ctx, request)
		})},
		{Name: "session_act", Contract: commandToolContract, Title: "Act on command sessions", Description: "Write to or stop command sessions through a mutating session tool.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("session_act", func(_ context.Context, r *Runtime, request toolcommand.SessionActRequest) (Result, error) {
			return r.command.Act(request)
		})},
	}
}
