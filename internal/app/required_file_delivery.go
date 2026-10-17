package app

import (
	"context"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
)

func (r *Runtime) completedRequiredFileSnapshot(ctx context.Context, binding activity.Binding, request toolfile.ReadRequest, revision string) (*structuredOutputSnapshot, error) {
	var selected *structuredOutputSnapshot
	r.structuredOutputMu.Lock()
	r.pruneStructuredOutputLocked(time.Now())
	for _, candidate := range r.structuredSnapshots {
		if candidate.Tool != "read_file" || candidate.OutputFailure != nil || !candidate.Required || !candidate.Delivered || !candidate.FileFullRange ||
			candidate.FilePath != request.Path || candidate.FileRuntime != request.Runtime ||
			candidate.FileDistribution != request.WSLDistribution || candidate.FileRevision != revision ||
			!sameContextScope(candidate.Binding, binding) {
			continue
		}
		if selected == nil || candidate.CreatedAt.After(selected.CreatedAt) {
			selected = cloneStructuredSnapshot(candidate)
		}
	}
	r.structuredOutputMu.Unlock()
	if selected == nil {
		return nil, nil
	}
	if err := r.authorizeStructuredSnapshot(ctx, selected.Binding, binding); err != nil {
		return nil, err
	}
	return selected, nil
}

func requiredFileReference(snapshot structuredOutputSnapshot) (Result, error) {
	full, err := decodeStructuredResult(snapshot.Encoded)
	if err != nil {
		return nil, err
	}
	return Result{
		"path":            snapshot.FilePath,
		"content":         "",
		"encoding":        "utf-8",
		"size_bytes":      full["size_bytes"],
		"start_line":      full["start_line"],
		"end_line":        full["end_line"],
		"total_lines":     full["total_lines"],
		"offset":          snapshot.FileRangeEnd,
		"next_offset":     snapshot.FileRangeEnd,
		"range_end":       snapshot.FileRangeEnd,
		"has_more":        false,
		"truncated":       false,
		"source_complete": true,
		"source_revision": snapshot.FileRevision,
		"snapshot_id":     snapshot.ID,
		"unchanged":       true,
		"capture": map[string]any{
			"state": snapshot.SourceState, "complete": snapshot.SourceComplete,
			"retained_bytes": len(snapshot.Encoded), "source_ref": snapshot.SourceURI,
		},
		"projection": map[string]any{"mode": "reference", "complete": true, "visible_chars": 0},
	}, nil
}

func (r *Runtime) commitRequiredFile(binding activity.Binding, result Result) {
	delivery := modelObject(result["file_delivery"])
	if delivery == nil {
		return
	}
	id := stringArg(delivery, "snapshot_id")
	start := int64(integerArgument(delivery["byte_start"]))
	end := int64(integerArgument(delivery["byte_end"]))
	if id == "" || end < start {
		return
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	stored := r.structuredSnapshots[id]
	if stored == nil || stored.Tool != "read_file" || stored.OutputFailure != nil || !stored.Required ||
		stored.Binding.SourceOwnerKey != binding.SourceOwnerKey || stored.Binding.ConversationID != binding.ConversationID ||
		stored.FileDeliveredOffset != start {
		return
	}
	stored.FileDeliveredOffset = end
	if delivery["has_more"] == false && stored.FileFullRange && stored.FileSequenceStart == 0 && end == stored.FileRangeEnd {
		stored.Delivered = true
	}
}
