package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/workspace"
)

func (r *Runtime) contextSessionCounts(ctx context.Context, binding activity.Binding, workspaceID string) (int, int, error) {
	binding.WorkspaceID = workspaceID
	running := r.command.RunningCount(binding)
	if r.commandCompletions == nil {
		return running, 0, nil
	}
	pending, err := r.commandCompletions.PendingCount(ctx, binding.SourceOwnerKey, binding.ConversationID)
	return running, pending, err
}

// Refresh volatile observations even when stable Context is a cached reference.
func (r *Runtime) refreshContextVolatileFacts(ctx context.Context, result Result, workdir string, binding activity.Binding) {
	selected, resolved, err := r.selectContextWorkspace(activity.WithBinding(ctx, binding), workdir)
	if err != nil {
		result["git"] = workspace.GitSnapshot{ErrorCode: "GIT_WORKSPACE_UNAVAILABLE"}
		return
	}
	result["git"] = workspace.ReadGitSnapshot(ctx, resolved)
	running, pending, err := r.contextSessionCounts(ctx, binding, selected.ID)
	result["running_sessions"] = running
	if err == nil {
		result["terminal_completions_pending"] = pending
	}
}
