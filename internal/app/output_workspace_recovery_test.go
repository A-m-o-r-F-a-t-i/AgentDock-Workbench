package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestRetainedOutputWorkspaceMismatchGuidesAuthorizedReselection(t *testing.T) {
	r := executionTestRuntime(t)
	root := t.TempDir()
	record, err := r.workspaceRegistry.EnsureRoot(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := activity.WithSource(t.Context(), activity.Source{
		Principal: "workspace-recovery-test", Namespace: "mcp",
	})
	owner := activity.SourceOwnerKey(ctx)
	original := activity.Binding{SourceOwnerKey: owner, WorkspaceID: record.ID}
	other := original
	other.WorkspaceID = "wsp_0000000000000001"
	err = r.authorizeStructuredSnapshot(ctx, original, other)
	var problem *ToolError
	if !errors.As(err, &problem) || problem.Code != "OUTPUT_WORKSPACE_MISMATCH" ||
		problem.Category != "conflict" {
		t.Fatalf("workspace mismatch = %#v", err)
	}
	if problem.Details["source_workspace_id"] != record.ID ||
		problem.Details["current_workspace_id"] != other.WorkspaceID ||
		problem.Details["source_workdir"] != filepath.Join(record.Root, record.DefaultWorkdir) ||
		problem.Details["retry_safe"] != true || problem.Details["execution_performed"] != false {
		t.Fatalf("missing authenticated workspace recovery: %#v", problem.Details)
	}
	action := modelObject(problem.Details["required_action"])
	arguments := modelObject(action["arguments"])
	if action["action"] != "select_workspace" || action["tool"] != "agentdock_context" ||
		action["required"] != true || stringArg(arguments, "workdir") != filepath.Join(record.Root, record.DefaultWorkdir) {
		t.Fatalf("recovery needs an exact, non-executing context call: %#v", action)
	}
	if err = r.authorizeStructuredSnapshot(ctx, original, original); err != nil {
		t.Fatalf("restoring the owning workspace did not allow continuation: %v", err)
	}
}

func TestRetainedWorkspaceRecoveryDoesNotLeakForeignSource(t *testing.T) {
	r := executionTestRuntime(t)
	ctx := activity.WithSource(t.Context(), activity.Source{Principal: "workspace-foreign", Namespace: "mcp"})
	original := activity.Binding{SourceOwnerKey: "foreign-owner", WorkspaceID: "wsp_0000000000000001"}
	current := activity.Binding{SourceOwnerKey: activity.SourceOwnerKey(ctx), WorkspaceID: "wsp_0000000000000002"}
	err := r.authorizeStructuredSnapshot(ctx, original, current)
	var problem *ToolError
	if !errors.As(err, &problem) || problem.Code != "OUTPUT_OWNER_MISMATCH" {
		t.Fatalf("foreign owner not rejected: %#v", err)
	}
	if problem.Details["source_workdir"] != nil || problem.Details["source_workspace_id"] != nil {
		t.Fatalf("foreign workspace path was exposed: %#v", problem.Details)
	}

	// An unattributed historical snapshot must not reveal another workdir.
	err = r.retainedWorkspaceMismatch(context.Background(), original.WorkspaceID, current.WorkspaceID, false)
	if !errors.As(err, &problem) || problem.Details["source_workdir"] != nil ||
		problem.Details["source_workspace_id"] != nil {
		t.Fatalf("unattributed output disclosed a workspace: %#v", err)
	}
}
