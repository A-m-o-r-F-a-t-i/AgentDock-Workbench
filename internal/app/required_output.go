package app

import (
	"context"
	"maps"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const (
	requiredOutputWrapperReserveChars = 1800
	contextBootstrapReserveChars      = 800
)

func requiredOutputPageBudget(name string, args map[string]any) int {
	budget := min(toolcontract.ModelVisiblePayloadChars, structuredOutputBudget(name, args))
	if isContextTool(name) {
		// Context uses one server-owned model budget. A caller byte limit cannot
		// enlarge or shrink the immutable semantic page sequence.
		budget = toolcontract.ModelVisiblePayloadChars
	}
	budget -= requiredOutputWrapperReserveChars
	if isContextTool(name) {
		budget -= contextBootstrapReserveChars
	}
	return max(1024, budget)
}

func (r *Runtime) applyRequiredOutputPolicy(p *preparedExecution, result Result) Result {
	if p.structuredResume != nil && p.structuredResume.Reference {
		return result
	}
	full, encoded, err := cloneStructuredResult(result)
	if err != nil {
		return result
	}
	isContext := isContextTool(p.spec.Name)
	budget := requiredOutputPageBudget(p.spec.Name, p.args)
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
			OutputFailure: cloneOutputFailure(p.outputFailure),
			Tool:          p.spec.Name, Binding: structuredSnapshotBinding(p, full), Encoded: encoded,
			Required: true, PageBudget: budget, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(structuredSnapshotTTL),
			SourceComplete: complete, SourceReason: reason, SourceState: "complete",
		}
		retained.SelectorArguments, _ = cloneExecutionArguments(structuredSelectorArguments(p.args))
		retained.ResumeArguments, _ = cloneExecutionArguments(structuredResumeArguments(p.args))
		if !complete {
			retained.SourceState = "partial"
		}
		retained.ArgumentsHash, _ = structuredArgumentsHash(p.args)
		retained.SourceURI = stringArg(modelObject(full["capture"]), "source_ref")
		if retained.SourceURI == "" {
			r.retainStructuredActivitySource(p, full, &retained)
		}
		if isContext {
			retained.ContextRevision = stringArg(full, "context_revision")
			retained.ContextWorkdir = stringArg(modelObject(full["instruction_files"]), "workdir")
			if retained.ContextWorkdir == "" {
				retained.ContextWorkdir = stringArg(full, "workdir")
			}
			if retained.ContextWorkdir == "" {
				retained.ContextWorkdir = stringArg(p.args, "workdir")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ctx = activity.WithSource(ctx, p.source)
			// ContextStamp controls current-reference reuse and deduplication;
			// the separate guard below owns immutable continuation validity.
			retained.ContextStamp, err = r.contextCacheStamp(ctx, retained.ContextWorkdir, retained.Binding)
			if err != nil {
				retained.SourceReason = appendStructuredReason(retained.SourceReason, "Context cache eligibility could not be revalidated: "+err.Error())
			}
			retained.ContextGuardStamp, err = r.contextContinuationStamp(ctx, retained.ContextWorkdir, retained.Binding)
			cancel()
			if err != nil {
				retained.SourceReason = appendStructuredReason(retained.SourceReason, "Context continuation boundary could not be revalidated: "+err.Error())
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
	if p.structuredResume != nil && p.structuredResume.Cursor != "" {
		meta["request_cursor"] = p.structuredResume.Cursor
	}
	if retained.SourceURI != "" && requiredSourceReadNeeded(hasMore, retained.SourceComplete, page) {
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
	if p.spec.Name == "mcp_tool_call" {
		// Recovery is a read of the frozen source, never another dynamic write.
		delete(page, "next_cursor")
		page["output_class"] = string(toolcontract.OutputRequiredFull)
	}
	page["structured_output"] = meta
	page["has_more"] = hasMore
	if p.spec.Name == "mcp_tool_search" {
		// Inline schemas use the same lossless delivery as inspect, including on continuation pages.
		page["output_class"] = string(toolcontract.OutputRequiredFull)
	}
	if isContext {
		bootstrapReady := p.spec.Name == "agentdock_context" && page["bootstrap_complete"] == true
		setContextDeliveryComplete(page, retained.OutputFailure == nil && retained.SourceComplete && (!hasMore || bootstrapReady))
	}
	return page
}

// Delivery progress is committed from the final projection, not from the raw
// Context handler. An encoder fallback therefore cannot skip an undelivered page.
func (r *Runtime) commitStructuredDelivery(binding activity.Binding, result Result) {
	r.commitRequiredSource(binding, result)
	r.commitRequiredFile(binding, result)
	meta := modelObject(result["structured_output"])
	id := stringArg(meta, "snapshot_id")
	requestCursor := stringArg(meta, "request_cursor")
	if id != "" && requestCursor != "" {
		r.structuredOutputMu.Lock()
		position, cursorFound := r.structuredCursors[requestCursor]
		stored := r.structuredSnapshots[id]
		if cursorFound && position.SnapshotID == id && stored != nil && stored.OutputFailure == nil &&
			stored.Binding.SourceOwnerKey == binding.SourceOwnerKey && stored.Binding.ConversationID == binding.ConversationID {
			position.Consumed = true
			position.NextCursor = stringArg(result, "next_cursor")
			r.structuredCursors[requestCursor] = position
		}
		r.structuredOutputMu.Unlock()
	}
	index, count := integerArgument(meta["page_index"]), integerArgument(meta["page_count"])
	if id == "" || index < 1 || count < index {
		return
	}
	r.structuredOutputMu.Lock()
	stored := r.structuredSnapshots[id]
	if stored == nil || !stored.Required || stored.OutputFailure != nil || stored.Binding.SourceOwnerKey != binding.SourceOwnerKey || stored.Binding.ConversationID != binding.ConversationID {
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

func requiredSourceReadNeeded(hasMore, sourceComplete bool, page Result) bool {
	return hasMore || !sourceComplete || page["required_unit_unavailable"] != nil
}
