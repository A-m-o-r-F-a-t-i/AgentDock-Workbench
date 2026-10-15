package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/snapshot"
)

// Observe dependency identities without rebuilding Context or expanding MCPs.
// Authorization is independently revalidated by the normal execution admission.
func (r *Runtime) contextSourceStamp(ctx context.Context, workdir string, binding activity.Binding) (string, error) {
	ctx = activity.WithBinding(ctx, binding)
	selected, resolved, err := r.selectContextWorkspace(ctx, workdir)
	if err != nil {
		return "", err
	}
	pluginRevision, err := r.pluginStore.SourceRevision(ctx)
	if err != nil {
		return "", err
	}
	global := r.cfg.InstructionsFile
	if global == "" {
		global = filepath.Join(r.cfg.AgentDockHome, "AGENTS.md")
	}
	paths := snapshot.MetadataPaths(resolved, global)
	revisions := []string{}
	if r.contextSnapshots != nil {
		for _, files := range []contextFiles{r.contextSnapshots.ruleFiles, r.contextSnapshots.skillFiles, r.contextSnapshots.commonFiles} {
			if err := files.Sync(ctx); err != nil {
				return "", err
			}
			source, _ := files.Revisions()
			revisions = append(revisions, source)
		}
	}
	encoded, err := json.Marshal(map[string]any{
		"workspace": selected, "workdir": resolved, "plugins": pluginRevision,
		"files": revisions, "rules": snapshot.Stamps(paths...),
		"task_index": snapshot.Stamps(filepath.Join(r.cfg.AgentDockHome, "tasks")),

		"task": binding.TaskID, "thread": binding.ThreadID, "step": binding.StepID,
		"platform": buildinfo.FullCommit(), "mcp": r.capabilityManager.DirectoryRevision(),
		"builtin_tools": r.builtinToolBundleItems(ctx),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ctxsrc_%x", sha256.Sum256(encoded)), nil
}

func sameContextScope(a, b activity.Binding) bool {
	return a.SourceOwnerKey == b.SourceOwnerKey && a.ConversationID == b.ConversationID &&
		a.TaskID == b.TaskID && a.ThreadID == b.ThreadID && a.WorkspaceID == b.WorkspaceID
}

// Empty Context calls advance a unique immutable sequence. A fully delivered,
// unchanged snapshot becomes a compact reference; force_refresh starts over.
func (r *Runtime) resumeContextOutput(ctx context.Context, p *preparedExecution) (Result, *structuredOutputResume, error) {
	if p.spec.Name != "agentdock_context" && p.spec.Name != "workspace_context" {
		return nil, nil, nil
	}
	if p.args["force_refresh"] == true {
		return nil, nil, nil
	}
	if p.state.binding.ConversationID == "" {
		return nil, nil, nil
	}
	candidates := []*structuredOutputSnapshot{}
	r.structuredOutputMu.Lock()
	r.pruneStructuredOutputLocked(time.Now())
	for _, stored := range r.structuredSnapshots {
		if stored.Tool != p.spec.Name || !stored.Required || !sameContextScope(stored.Binding, p.state.binding) {
			continue
		}
		if workdir := stringArg(p.args, "workdir"); workdir != "" && filepath.Clean(workdir) != filepath.Clean(stored.ContextWorkdir) {
			continue
		}
		candidates = append(candidates, cloneStructuredSnapshot(stored))
	}
	r.structuredOutputMu.Unlock()
	valid := []*structuredOutputSnapshot{}
	for _, candidate := range candidates {
		stamp, err := r.contextSourceStamp(ctx, candidate.ContextWorkdir, p.state.binding)
		if err != nil {
			return nil, nil, err
		}
		if !candidate.SourceComplete || candidate.PermissionRevision != p.decision.Revision || candidate.ContextStamp == "" || stamp != candidate.ContextStamp {
			r.discardStructuredSnapshot(candidate.ID)
			continue
		}
		if err := r.authorizeStructuredSnapshot(ctx, candidate.Binding, p.state.binding); err != nil {
			return nil, nil, err
		}
		valid = append(valid, candidate)
	}
	pending := []*structuredOutputSnapshot{}
	for _, candidate := range valid {
		if !candidate.Delivered {
			pending = append(pending, candidate)
		}
	}
	if len(pending) > 1 {
		return nil, nil, toolError("PENDING_SEQUENCE_AMBIGUOUS", "Several Context sequences are pending; resume the intended snapshot with its cursor or workdir.", "validation")
	}
	var selected *structuredOutputSnapshot
	if len(pending) == 1 {
		selected = pending[0]
	} else {
		for _, candidate := range valid {
			if selected == nil || candidate.CreatedAt.After(selected.CreatedAt) {
				selected = candidate
			}
		}
	}
	if selected == nil {
		return nil, nil, nil
	}
	result, err := decodeStructuredResult(selected.Encoded)
	if err != nil {
		return nil, nil, err
	}
	resume := &structuredOutputResume{Snapshot: *selected, ItemIndex: selected.NextPage, Reference: selected.Delivered}
	if selected.Delivered {
		diagnostics := modelObject(result["context_diagnostics"])
		result = Result{
			"context_revision": result["context_revision"], "unchanged": true,
			"context_delivery": "reference", "context_ready": diagnostics["complete"] == true,
			"context_diagnostics": map[string]any{"complete": diagnostics["complete"], "components_complete": diagnostics["components_complete"], "binding_status": "unchanged"},
			"capture":             result["capture"], "operation": map[string]any{"state": "completed", "ok": true, "executed": false},
		}
	}
	return result, resume, nil
}

func (r *Runtime) discardStructuredSnapshot(id string) {
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	if stored := r.structuredSnapshots[id]; stored != nil {
		delete(r.structuredSnapshots, id)
		r.structuredSnapshotBytes -= int64(len(stored.Encoded))
		for cursor, position := range r.structuredCursors {
			if position.SnapshotID == id {
				delete(r.structuredCursors, cursor)
			}
		}
	}
}

func (r *Runtime) discardContextSequences(replacement structuredOutputSnapshot) {
	ids := []string{}
	r.structuredOutputMu.Lock()
	for id, stored := range r.structuredSnapshots {
		if stored.Tool == replacement.Tool && sameContextScope(stored.Binding, replacement.Binding) {
			ids = append(ids, id)
		}
	}
	r.structuredOutputMu.Unlock()
	for _, id := range ids {
		r.discardStructuredSnapshot(id)
	}
}

var errIncompleteContextBuild = errors.New("context build is incomplete")

// Share raw, immutable construction only. Per-request binding, permission,
// insertions and delivery progress are resolved after each waiter's clone.
func (r *Runtime) executeContextHandler(ctx context.Context, p *preparedExecution) (Result, error) {
	if p.spec.Name != "agentdock_context" || r.contextSnapshots == nil || r.contextSnapshots.complete == nil {
		return p.spec.Handler(ctx, r, p.args)
	}
	binding := activity.FromContext(ctx)
	for attempt := 0; attempt < 2; attempt++ {
		stamp, err := r.contextSourceStamp(ctx, stringArg(p.args, "workdir"), binding)
		if err != nil {
			return nil, err
		}
		key := binding.SourceOwnerKey + "|" + binding.ConversationID + "|" + stamp
		if p.args["force_refresh"] == true {
			key += "|" + binding.CallID
		}
		value, info, buildErr := r.contextSnapshots.complete.Get(ctx, key, func(buildCtx context.Context) (Result, error) {
			buildCtx = activity.WithSource(activity.WithBinding(buildCtx, binding), p.source)
			raw, err := p.spec.Handler(buildCtx, r, p.args)
			if err != nil {
				return raw, err
			}
			after, err := r.contextSourceStamp(buildCtx, stringArg(p.args, "workdir"), binding)
			if err != nil {
				return raw, err
			}
			if after != stamp {
				return raw, errContextVersionChanged
			}
			if modelObject(raw["context_diagnostics"])["components_complete"] != true {
				return raw, errIncompleteContextBuild
			}
			return raw, nil
		})
		if errors.Is(buildErr, errContextVersionChanged) {
			continue
		}
		if buildErr != nil && !errors.Is(buildErr, errIncompleteContextBuild) {
			return value, buildErr
		}
		cloned, _, err := cloneStructuredResult(value)
		if err != nil {
			return nil, err
		}
		if diagnostics := modelObject(cloned["context_diagnostics"]); diagnostics != nil {
			diagnostics["context_snapshot"] = info
		}
		return cloned, nil
	}
	return nil, toolErrorDetails("CONTEXT_REVISION_CHANGED", "Context sources changed while preparing the immutable snapshot.", "conflict", map[string]any{"stage": "response_projection"})
}
