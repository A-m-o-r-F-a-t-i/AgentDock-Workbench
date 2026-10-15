package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/contextguide"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func sourceResultComplete(result Result) (bool, string) {
	for _, key := range []string{"truncated", "partial", "stdout_truncated", "stderr_truncated"} {
		if result[key] == true {
			return false, "The source operation returned a bounded or partial result before model-facing pagination."
		}
	}
	if diagnostics, ok := result["context_diagnostics"].(map[string]any); ok {
		if complete, exists := diagnostics["complete"].(bool); exists && !complete {
			return false, "AgentDock context generation was incomplete before model-facing pagination."
		}
	}
	if complete, exists := result["complete"].(bool); exists && !complete {
		return false, "The source operation reported an incomplete result before model-facing pagination."
	}
	if result["status"] == "running" {
		return false, "The source operation was still running when this result snapshot was created."
	}
	return true, ""
}

func (r *Runtime) applyStructuredOutputPolicy(p *preparedExecution, result Result) Result {
	if result != nil && p.state.binding.ParentCallID == "" && p.spec.Name != "read_file" && toolcontract.ResolveOutputClass(p.spec.Name, p.args, result) == toolcontract.OutputRequiredFull {
		return r.applyRequiredOutputPolicy(p, result)
	}
	paths := structuredOutputPaths(p.spec.Name)
	if len(paths) == 0 || result == nil || p.state.binding.ParentCallID != "" {
		return result
	}
	// Match against the complete, revalidated result before paging can mark its
	// delivery incomplete. Cursor requests must always deliver their next page.
	if p.structuredResume == nil && (p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context") {
		projected := contextguide.ProjectReference(p.spec.Name, stringArg(p.args, "known_context_revision"), result)
		if projected["delivery"] == "reference" {
			return projected
		}
	}
	full, encoded, err := cloneStructuredResult(result)
	if err != nil {
		return result
	}
	budget := min(toolcontract.ModelVisibleTotalChars, structuredOutputBudget(p.spec.Name, p.args))
	businessBudget := max(1<<10, budget-toolcontract.ModelVisibleControlChars)
	if chars, countErr := toolcontract.JSONChars(full); p.structuredResume == nil && countErr == nil && chars <= businessBudget {
		return result
	}

	var snapshot structuredOutputSnapshot
	position := structuredPageState{}
	if p.structuredResume != nil {
		snapshot = p.structuredResume.Snapshot
		position = structuredPageState{PathIndex: p.structuredResume.PathIndex, ItemIndex: p.structuredResume.ItemIndex}
		encoded = append([]byte(nil), snapshot.Encoded...)
		full, err = decodeStructuredResult(encoded)
		if err != nil {
			return Result{"structured_output": map[string]any{"truncated": true, "continuation_unavailable": "The retained structured result could not be decoded."}}
		}
	} else {
		complete, reason := sourceResultComplete(full)
		snapshot = structuredOutputSnapshot{
			Tool: p.spec.Name, Binding: structuredSnapshotBinding(p, full), Encoded: append([]byte(nil), encoded...),
			CreatedAt: time.Now(), ExpiresAt: time.Now().Add(structuredSnapshotTTL),
			SourceComplete: complete, SourceReason: reason,
		}
		snapshot.ArgumentsHash, _ = structuredArgumentsHash(p.args)
		snapshot.SourceState = "complete"
		if !complete {
			snapshot.SourceState = "partial"
		}
		r.retainStructuredActivitySource(p, full, &snapshot)
		if len(encoded) <= structuredSnapshotMaxBytes {
			if stored, storeErr := r.storeStructuredSnapshot(&snapshot); storeErr == nil {
				snapshot = *stored
			} else {
				snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, storeErr.Error())
			}
		} else {
			snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, "Exact cursor retention was skipped because the serialized result exceeded 16 MiB.")
		}
	}

	hardBusinessBudget := toolcontract.StructuredResponseMaxBytes - structuredExecutionEnvelopeReserve(p.spec.Name)
	page, next, returned, minimumBytes, oversized := buildStructuredPage(full, paths, position, businessBudget, hardBusinessBudget)
	hasMore := structuredPageHasMore(full, paths, next)
	deliveryIncomplete := hasMore || oversized != nil
	if oversized != nil {
		snapshot.OversizedItems = true
		r.markStructuredSnapshotOversized(snapshot.ID)
	}
	meta := map[string]any{
		"truncated":             deliveryIncomplete || position.PathIndex != 0 || position.ItemIndex != 0,
		"budget_chars":          budget,
		"snapshot_id":           snapshot.ID,
		"business_budget_chars": businessBudget,
		"source_bytes":          len(encoded),
		"retained_bytes":        len(snapshot.Encoded),
		"dropped_before_bytes":  0,
		"source_complete":       snapshot.SourceComplete,
		"source_state":          snapshot.SourceState,
		"returned_items":        returned,
		"cursor_expires_at":     snapshot.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if snapshot.SourceURI != "" {
		meta["source"] = snapshot.SourceURI
		meta["continue_read"] = map[string]any{
			"tool":      "read_file",
			"arguments": map[string]any{"path": snapshot.SourceURI, "offset": 0, "limit_chars": 8000},
		}
	}
	if snapshot.SourceReason != "" {
		meta["source_reason"] = snapshot.SourceReason
	}
	if snapshot.OversizedItems {
		meta["oversized_items_omitted"] = true
	}
	if minimumBytes > 0 {
		meta["minimum_response_bytes"] = minimumBytes + structuredExecutionEnvelopeReserve(p.spec.Name)
	}
	if oversized != nil {
		meta["oversized_item"] = oversized
	}
	if hasMore && snapshot.ID != "" {
		if cursor, cursorErr := r.newStructuredCursor(snapshot.ID, next); cursorErr == nil {
			page["next_cursor"] = cursor
		} else {
			meta["continuation_unavailable"] = cursorErr.Error()
		}
	} else if hasMore {
		meta["continuation_unavailable"] = "The exact structured result was not retained; use the activity source when available."
	}
	if oversized != nil {
		delete(page, "next_cursor")
		meta["continuation_unavailable"] = "An indivisible result item exceeds the page budget; recover its full content from source without re-running this operation."
	}
	meta["remaining_items"] = countRemainingStructuredItems(full, paths, next)
	page["has_more"] = hasMore
	page["structured_output"] = meta
	if p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context" {
		setContextDeliveryComplete(page, !deliveryIncomplete && !snapshot.OversizedItems)
	}
	for range 3 {
		marshaled, marshalErr := json.Marshal(page)
		if marshalErr != nil || meta["page_bytes"] == len(marshaled) {
			break
		}
		meta["page_bytes"] = len(marshaled)
	}
	return page
}

func setContextDeliveryComplete(page Result, delivered bool) {
	diagnostics, ok := page["context_diagnostics"].(map[string]any)
	if !ok {
		return
	}
	sourceComplete, _ := diagnostics["complete"].(bool)
	diagnostics["complete"] = sourceComplete && delivered
}

func structuredSnapshotBinding(p *preparedExecution, result Result) activity.Binding {
	binding := p.state.binding
	if p.spec.Name != "agentdock_context" {
		return binding
	}
	if workspaceID := stringArg(result, "workspace_id"); workspaceID != "" {
		binding.WorkspaceID = workspaceID
	}
	var state activity.ConversationState
	if remarshal(result["conversation_state"], &state) == nil {
		binding.TaskID = state.ActiveTaskID
		binding.ThreadID = state.ActiveTaskThreadID
		if state.WorkspaceID != "" {
			binding.WorkspaceID = state.WorkspaceID
		}
	}
	return binding
}

func appendStructuredReason(existing, addition string) string {
	if existing == "" {
		return addition
	}
	if addition == "" {
		return existing
	}
	return existing + " " + addition
}

func (r *Runtime) retainStructuredActivitySource(p *preparedExecution, full Result, snapshot *structuredOutputSnapshot) {
	if source := stringArg(modelObject(full["capture"]), "source_ref"); source != "" {
		snapshot.SourceURI = source
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(ctx, full, snapshot.SourceState, r.executionRedactor(p.args))
	if payload.Ref == "" {
		snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, payload.Reason)
		return
	}
	if snapshot.SourceReason != "" {
		payload.Reason = snapshot.SourceReason
	}
	if _, err := r.activity.Append(ctx, activity.Event{Binding: p.state.binding, Kind: "call.payload", ToolName: p.spec.Name, OutputSource: payload}); err != nil {
		snapshot.SourceReason = appendStructuredReason(snapshot.SourceReason, "The activity source could not be journaled.")
		return
	}
	snapshot.SourceURI = activity.SourceReference(p.state.binding.CallID, payload)
}
