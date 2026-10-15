package app

import (
	"context"
	"github.com/uvwt/agentdock/internal/activity"
)

func (r *Runtime) describeRequiredSourcePage(ctx context.Context, binding activity.Binding, source string, page activity.PayloadPage, result Result) error {
	r.structuredOutputMu.Lock()
	var selected *structuredOutputSnapshot
	for _, stored := range r.structuredSnapshots {
		if stored.Required && stored.SourceURI == source && stored.Binding.SourceOwnerKey == binding.SourceOwnerKey && stored.Binding.ConversationID == binding.ConversationID {
			selected = cloneStructuredSnapshot(stored)
			break
		}
	}
	r.structuredOutputMu.Unlock()
	if selected == nil {
		return nil
	}
	if selected.ContextStamp != "" {
		stamp, err := r.contextSourceStamp(ctx, selected.ContextWorkdir, binding)
		if err != nil {
			return err
		}
		if stamp != selected.ContextStamp {
			r.discardStructuredSnapshot(selected.ID)
			result["context_ready"] = false
			result["source_freshness"] = "historical"
			// Historical facts remain readable, but cannot complete the current Context.
			return nil
		}
	}
	result["output_class"] = "required_full"
	result["source_delivery"] = map[string]any{"snapshot_id": selected.ID, "byte_start": page.Offset, "byte_end": page.NextOffset, "has_more": page.HasMore}
	if selected.Tool == "agentdock_context" || selected.Tool == "workspace_context" {
		result["context_ready"] = !page.HasMore && selected.SourceComplete && selected.SourceDeliveredOffset == page.Offset
	}
	return nil
}

// Both byte-source recovery and semantic pages commit only after final encoding.
func (r *Runtime) commitRequiredSource(binding activity.Binding, result Result) {
	delivered := modelObject(result["source_delivery"])
	if delivered == nil {
		return
	}
	id := stringArg(delivered, "snapshot_id")
	start, end := int64(integerArgument(delivered["byte_start"])), int64(integerArgument(delivered["byte_end"]))
	if end < start {
		return
	}
	r.structuredOutputMu.Lock()
	stored := r.structuredSnapshots[id]
	if stored == nil || !stored.Required || stored.SourceURI != stringArg(result, "path") || stored.Binding.SourceOwnerKey != binding.SourceOwnerKey || stored.Binding.ConversationID != binding.ConversationID || stored.SourceDeliveredOffset != start {
		r.structuredOutputMu.Unlock()
		return
	}
	stored.SourceDeliveredOffset = end
	var schema []byte
	if delivered["has_more"] == false {
		if !stored.Delivered && stored.Tool == "mcp_tool_inspect" {
			schema = append([]byte(nil), stored.Encoded...)
		}
		stored.Delivered, stored.NextPage = true, stored.PageCount
	}
	r.structuredOutputMu.Unlock()
	if len(schema) > 0 {
		if value, err := decodeStructuredResult(schema); err == nil {
			r.dynamicMCP.CommitSchemas(binding, value)
		}
	}
}
