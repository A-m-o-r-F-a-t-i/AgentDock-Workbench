package app

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/textutil"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
)

func (r *Runtime) readProjectedFile(ctx context.Context, request toolfile.ReadRequest) (Result, error) {
	if !modelProjectionRequested(ctx) && request.SourceRevision == "" && request.Offset == nil && request.LimitChars == nil && request.EndOffset == nil {
		return r.files.ReadFile(ctx, request)
	}
	policy, ok := ctx.Value(outputPolicyContextKey{}).(config.ToolOutputSettings)
	if !ok {
		policy = r.MCPPresentationSettings().ToolOutput
	}
	bodyBudget := toolcontract.ModelVisiblePayloadChars - 2000
	if policy.Enabled && policy.MaxChars > 0 {
		bodyBudget = min(bodyBudget, policy.MaxChars)
	}
	binding := activity.FromContext(ctx)
	requiredFile := strings.HasPrefix(request.Path, "skill://")
	if requiredFile && request.SourceRevision == "" && request.Offset == nil && request.EndOffset == nil && request.StartLine == nil && request.EndLine == nil {
		revision, err := r.files.ReadSourceRevision(ctx, request)
		if err != nil {
			return nil, err
		}
		if completed, err := r.completedRequiredFileSnapshot(ctx, binding, request, revision); err != nil {
			return nil, err
		} else if completed != nil {
			return requiredFileReference(*completed)
		}
	}
	offset := int64(0)
	if request.Offset != nil {
		offset = *request.Offset
	}
	var retained *structuredOutputSnapshot
	var predecessor *structuredOutputSnapshot
	if request.SourceRevision != "" {
		revision, err := r.files.ReadSourceRevision(ctx, request)
		if err != nil {
			return nil, err
		}
		if revision != request.SourceRevision {
			return nil, toolErrorDetails("SOURCE_CHANGED_DURING_PAGING", "The file changed after the previous page. Start a new read of the current revision.", "conflict", map[string]any{"path": request.Path, "source_revision": revision, "expected_revision": request.SourceRevision})
		}
		r.structuredOutputMu.Lock()
		r.pruneStructuredOutputLocked(time.Now())
		for _, candidate := range r.structuredSnapshots {
			if candidate.Tool != "read_file" || candidate.OutputFailure != nil || candidate.FilePath != request.Path || candidate.FileRuntime != request.Runtime || candidate.FileDistribution != request.WSLDistribution || candidate.FileRevision != revision || !sameContextScope(candidate.Binding, binding) {
				continue
			}
			if candidate.FileByteEnd == offset && (predecessor == nil || candidate.CreatedAt.After(predecessor.CreatedAt)) {
				predecessor = cloneStructuredSnapshot(candidate)
			}
			if offset >= candidate.FileByteStart && (offset < candidate.FileByteEnd || offset == candidate.FileByteEnd && candidate.FileByteEnd == candidate.FileRangeEnd) {
				if retained == nil || candidate.CreatedAt.After(retained.CreatedAt) {
					retained = cloneStructuredSnapshot(candidate)
				}
			}
		}
		r.structuredOutputMu.Unlock()
		if predecessor != nil {
			if err := r.authorizeStructuredSnapshot(ctx, predecessor.Binding, binding); err != nil {
				return nil, err
			}
		}
	}
	var raw Result
	if retained != nil {
		if err := r.authorizeStructuredSnapshot(ctx, retained.Binding, binding); err != nil {
			return nil, err
		}
		var err error
		raw, err = decodeStructuredResult(retained.Encoded)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		raw, err = r.files.ReadFile(ctx, request)
		if err != nil {
			return nil, err
		}
		text := stringArg(raw, "content")
		textChars := utf8.RuneCountInString(text)
		encodedChars, countErr := toolcontract.JSONChars(raw)
		if !requiredFile && countErr == nil && encodedChars < min(4000, bodyBudget) && raw["has_more"] == false && (request.LimitChars == nil || *request.LimitChars >= textChars) {
			raw["capture"] = map[string]any{"state": "complete", "complete": true, "retained_with": "response"}
			raw["projection"] = map[string]any{"mode": "continuous_page", "complete": true, "visible_chars": textChars}
			return raw, nil
		}
		encodedRaw, encoded, err := cloneStructuredResult(raw)
		if err != nil {
			return nil, err
		}
		raw = encodedRaw
		complete, reason := sourceResultComplete(raw)
		sequenceStart := int64(integerArgument(raw["offset"]))
		if predecessor != nil {
			sequenceStart = predecessor.FileSequenceStart
		}
		retained = &structuredOutputSnapshot{
			Tool: "read_file", Binding: binding, Encoded: encoded, SourceComplete: complete, SourceReason: reason,
			Required: requiredFile, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(structuredSnapshotTTL), SourceState: "complete",
			FilePath: request.Path, FileRuntime: request.Runtime, FileDistribution: request.WSLDistribution,
			FileRevision: stringArg(raw, "source_revision"), FileSequenceStart: sequenceStart, FileByteStart: int64(integerArgument(raw["offset"])),
			FileByteEnd: int64(integerArgument(raw["next_offset"])), FileRangeEnd: int64(integerArgument(raw["range_end"])),
		}
		retained.FileDeliveredOffset = retained.FileByteStart
		retained.FileFullRange = retained.FileSequenceStart == 0 && retained.FileRangeEnd == int64(integerArgument(raw["size_bytes"]))
		if !complete {
			retained.SourceState = "partial"
		}
		payload := r.activity.CapturePayload(ctx, raw, retained.SourceState, r.executionRedactor(nil))
		if payload.Ref != "" {
			if _, journalErr := r.activity.Append(ctx, activity.Event{Binding: binding, Kind: "call.payload", ToolName: "read_file", OutputSource: payload}); journalErr == nil {
				retained.SourceURI = activity.SourceReference(binding.CallID, payload)
			}
		}
		if stored, err := r.storeStructuredSnapshot(retained); err == nil {
			retained = stored
		}
		offset = retained.FileByteStart
	}
	if request.SourceRevision != "" && request.SourceRevision != retained.FileRevision {
		return nil, toolError("SOURCE_CHANGED_DURING_PAGING", "The source revision changed while collecting the next window.", "conflict")
	}
	text := stringArg(raw, "content")
	relative := offset - retained.FileByteStart
	if relative < 0 || relative > int64(len(text)) || relative < int64(len(text)) && !utf8.RuneStart(text[relative]) {
		return nil, toolError("INVALID_ARGUMENT", "The byte cursor must identify a UTF-8 boundary in the selected source.", "validation")
	}
	end := retained.FileRangeEnd
	if request.EndOffset != nil {
		end = min(end, *request.EndOffset)
	}
	if end < offset {
		return nil, toolError("INVALID_ARGUMENT", "end_offset precedes the byte cursor.", "validation")
	}
	available := min(int64(len(text)), end-retained.FileByteStart)
	if available < int64(len(text)) && !utf8.RuneStart(text[available]) {
		return nil, toolError("INVALID_ARGUMENT", "end_offset must fall on a UTF-8 character boundary.", "validation")
	}
	candidate := text[relative:available]
	if request.LimitChars != nil {
		candidate, _, _ = textutil.ScalarPrefix(candidate, *request.LimitChars)
	}
	// Escape-aware body sizing is below the final encoder's all-fields assertion.
	// Cursors are derived only after selecting exactly these visible bytes.
	visible := toolcontract.JSONTextPrefix(candidate, bodyBudget)
	next := offset + int64(len(visible))
	startLine := integerArgument(raw["start_line"]) + strings.Count(text[:relative], "\n")
	endLine := startLine + strings.Count(visible, "\n")
	if strings.HasSuffix(visible, "\n") {
		endLine--
	}
	page := maps.Clone(raw)
	page["content"], page["offset"], page["next_offset"] = visible, offset, next
	page["range_end"], page["source_revision"], page["snapshot_id"] = end, retained.FileRevision, retained.ID
	page["start_line"], page["end_line"] = startLine, max(startLine, endLine)
	page["range"] = map[string]any{"byte_start": offset, "byte_end": next, "start_line": startLine, "end_line": max(startLine, endLine)}
	page["has_more"], page["truncated"], page["source_complete"] = next < end, next < end, retained.SourceComplete
	page["returned_chars"] = utf8.RuneCountInString(visible)
	page["projection"] = map[string]any{"mode": "continuous_page", "complete": offset == retained.FileByteStart && next == end, "visible_chars": utf8.RuneCountInString(visible), "omitted_bytes": end - next}
	page["capture"] = map[string]any{"state": retained.SourceState, "complete": retained.SourceComplete, "retained_bytes": len(retained.Encoded), "source_ref": retained.SourceURI}
	if retained.Required {
		page["output_class"] = string(toolcontract.OutputRequiredFull)
		page["file_delivery"] = map[string]any{"snapshot_id": retained.ID, "byte_start": offset, "byte_end": next, "has_more": next < end}
	}
	if next < end {
		page["next_cursor"] = map[string]any{"byte_offset": next}
		page["next_start_line"] = startLine + strings.Count(visible, "\n")
		arguments := map[string]any{"path": request.Path, "offset": next, "source_revision": retained.FileRevision, "end_offset": end}
		if request.Runtime != "" {
			arguments["runtime"] = request.Runtime
		}
		if request.WSLDistribution != "" {
			arguments["wsl_distribution"] = request.WSLDistribution
		}
		if request.LimitChars != nil {
			arguments["limit_chars"] = *request.LimitChars
		}
		if request.MaxBytes != nil {
			arguments["max_bytes"] = *request.MaxBytes
		}
		page["continue_read"] = map[string]any{"tool": "read_file", "arguments": arguments}
		page["truncated_reason"] = "model_page"
	} else {
		delete(page, "next_cursor")
		delete(page, "next_start_line")
		delete(page, "truncated_reason")
		delete(page, "continue_read")
	}
	if candidate != "" && visible == "" {
		return nil, fmt.Errorf("read_file: response budget cannot contain the next UTF-8 character")
	}
	return page, nil
}
