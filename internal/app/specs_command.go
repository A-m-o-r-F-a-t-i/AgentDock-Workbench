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
			if request.OriginCallID != "" && request.Backend != "" && request.Backend != "default" {
				return nil, toolErrorDetails("MCP_RECOVERY_SKILL_REQUIRED", "Recovery uses the declared host Skill without switching execution backend", "validation", map[string]any{"executed": false})
			}
			if modelProjectionRequested(ctx) {
				ctx = toolcommand.WithModelOutputProjection(ctx)
			}
			return r.command.Exec(ctx, request)
		})},
		{Name: "session_observe", Contract: commandToolContract, Title: "Observe command sessions", Description: "Read or wait for an existing exec_command session only when the next decision requires its result. Reuse the exact session_id and returned output cursor. Completion may arrive on a later root call. An unchanged running result is not a reason to poll again. Do not restart the command to obtain its result.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("session_observe", func(ctx context.Context, r *Runtime, request toolcommand.SessionObserveRequest) (Result, error) {
			return r.observeCommand(ctx, request)
		})},
		{Name: "session_act", Contract: commandToolContract, Title: "Act on command sessions", Description: "Write stdin to, signal, or stop an existing command session. This tool mutates process state; use session_observe for read-only status.", Annotations: mutatingToolAnnotations(true, true), Handler: typedToolHandler("session_act", func(_ context.Context, r *Runtime, request toolcommand.SessionActRequest) (Result, error) {
			return r.command.Act(request)
		})},
	}
}
