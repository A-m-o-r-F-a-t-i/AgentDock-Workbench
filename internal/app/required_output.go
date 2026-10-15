package app

import (
	"context"
	"maps"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func (r *Runtime) applyRequiredOutputPolicy(p *preparedExecution, result Result) Result {
	if p.structuredResume != nil && p.structuredResume.Reference {
		return result
	}
	full, encoded, err := cloneStructuredResult(result)
	if err != nil {
		return result
	}
	isContext := p.spec.Name == "agentdock_context" || p.spec.Name == "workspace_context"
	budget := min(toolcontract.ModelVisiblePayloadChars, structuredOutputBudget(p.spec.Name, p.args)) - 1800
	if isContext {
		budget -= 800
	}
	budget = max(1024, budget)
	var retained structuredOutputSnapshot
	start := 0
	if p.structuredResume != nil {
		retained = p.structuredResume.Snapshot
		start = p.structuredResume.ItemIndex
		full, err = decodeStructuredResult(retained.Encoded)
		if err != nil {
			return Result{"code": "OUTPUT_SNAPSHOT_INVALID", "error": err.Error(), "context_ready": false}
		}
		encoded = retained.Encoded
	} else {
		complete, reason := sourceResultComplete(full)
		retained = structuredOutputSnapshot{
			Tool: p.spec.Name, Binding: structuredSnapshotBinding(p, full), Encoded: encoded,
			Required: true, PageBudget: budget, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(structuredSnapshotTTL),
			SourceComplete: complete, SourceReason: reason, SourceState: "complete",
		}
		if !complete {
			retained.SourceState = "partial"
		}
		retained.ArgumentsHash, _ = structuredArgumentsHash(p.args)
		retained.SourceURI = stringArg(modelObject(full["capture"]), "source_ref")
		if retained.SourceURI == "" {
			r.retainStructuredActivitySource(p, full, &retained)
		}
		if isContext {
			retained.ContextWorkdir = stringArg(modelObject(full["instruction_files"]), "workdir")
			if retained.ContextWorkdir == "" {
				retained.ContextWorkdir = stringArg(full, "workdir")
			}
			if retained.ContextWorkdir == "" {
				retained.ContextWorkdir = stringArg(p.args, "workdir")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ctx = activity.WithSource(ctx, p.source)
			retained.ContextStamp, err = r.contextSourceStamp(ctx, retained.ContextWorkdir, retained.Binding)
			cancel()
			if err != nil {
				retained.SourceReason = appendStructuredReason(retained.SourceReason, "Context source identity could not be revalidated: "+err.Error())
			}
		}
		retained.PageBudget = budget
		retained.PermissionRevision = p.decision.Revision
		if isContext && p.args["force_refresh"] == true {
			r.discardContextSequences(retained)
		}
		if stored, storeErr := r.storeStructuredSnapshot(&retained); storeErr == nil {
			retained = *stored
		} else {
			retained.SourceReason = appendStructuredReason(retained.SourceReason, storeErr.Error())
		}
	}
	if retained.PageBudget > 0 {
		budget = retained.PageBudget
	}
	if p.structuredResume == nil && retained.ID != "" {
		if storedFull, decodeErr := decodeStructuredResult(retained.Encoded); decodeErr == nil {
			full = storedFull
			encoded = retained.Encoded
		}
	}
	chars, _ := toolcontract.JSONChars(full)
	var page Result
	next, count, parts := 1, 1, len(full)
	if chars <= budget {
		page = maps.Clone(full)
	} else {
		page, next, count, parts = buildRequiredOutputPage(full, start, budget)
	}
	r.structuredOutputMu.Lock()
	if stored := r.structuredSnapshots[retained.ID]; stored != nil {
		stored.PageCount = count
	}
	r.structuredOutputMu.Unlock()
	hasMore := next < count
	meta := map[string]any{
		"snapshot_id": retained.ID, "page_index": next, "page_count": count,
		"class": toolcontract.OutputRequiredFull, "truncated": count > 1,
		"required_delivery_complete": !hasMore, "source_complete": retained.SourceComplete,
		"source_bytes": len(encoded), "retained_bytes": len(retained.Encoded),
		"budget_chars": toolcontract.ModelVisibleTotalChars, "payload_budget_chars": budget,
		"semantic_parts": parts, "source": retained.SourceURI,
		"cursor_expires_at": retained.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if retained.SourceURI != "" {
		meta["continue_read"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": retained.SourceURI, "offset": 0, "limit_chars": 8000}}
	}
	if retained.SourceReason != "" {
		meta["source_reason"] = retained.SourceReason
	}
	if hasMore {
		cursor, cursorErr := r.newStructuredCursor(retained.ID, structuredPageState{ItemIndex: next})
		if cursorErr == nil {
			page["next_cursor"] = cursor
		} else {
			meta["continuation_unavailable"] = cursorErr.Error()
		}
	}
	if received, exists := page["received_sections"]; exists {
		meta["received_sections"] = received
	}
	if page["required_unit_unavailable"] != nil {
		delete(page, "next_cursor")
		meta["continuation_unavailable"] = "Read the exact retained source to recover this indivisible semantic unit."
	}
	page["structured_output"] = meta
	page["has_more"] = hasMore
	if isContext {
		setContextDeliveryComplete(page, !hasMore && retained.SourceComplete)
	}
	return page
}

// Delivery progress is committed from the final projection, not from the raw
// Context handler. An encoder fallback therefore cannot skip an undelivered page.
func (r *Runtime) commitStructuredDelivery(binding activity.Binding, result Result) {
	meta := modelObject(result["structured_output"])
	id := stringArg(meta, "snapshot_id")
	index, count := integerArgument(meta["page_index"]), integerArgument(meta["page_count"])
	if id == "" || index < 1 || count < index {
		return
	}
	r.structuredOutputMu.Lock()
	stored := r.structuredSnapshots[id]
	if stored == nil || !stored.Required || stored.Binding.SourceOwnerKey != binding.SourceOwnerKey || stored.Binding.ConversationID != binding.ConversationID {
		r.structuredOutputMu.Unlock()
		return
	}
	wasDelivered := stored.Delivered
	if index == stored.NextPage+1 {
		stored.NextPage = index
	}
	stored.Delivered = stored.NextPage == count && result["required_unit_unavailable"] == nil
	var schemaSource []byte
	if !wasDelivered && stored.Delivered && stored.Tool == "mcp_tool_inspect" {
		schemaSource = append([]byte(nil), stored.Encoded...)
	}
	r.structuredOutputMu.Unlock()
	if len(schemaSource) > 0 {
		if schemas, err := decodeStructuredResult(schemaSource); err == nil {
			r.dynamicMCP.CommitSchemas(binding, schemas)
		}
	}
}
