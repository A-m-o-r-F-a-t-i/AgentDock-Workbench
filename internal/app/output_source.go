package app

import (
	"context"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
)

type outputPolicyContextKey struct{}

var outputSourceURI = regexp.MustCompile(`^activity://call/(call_[a-f0-9]{32})/source(?:/([a-f0-9]{64}))?$`)

// An activity source is an existing immutable payload reached through its
// owning call, never a filesystem path or a blob hash supplied by the caller.
func (r *Runtime) readFileWithOutputSource(ctx context.Context, request toolfile.ReadRequest) (Result, error) {
	if !strings.HasPrefix(request.Path, "activity://") {
		return r.readProjectedFile(ctx, request)
	}
	parts := outputSourceURI.FindStringSubmatch(request.Path)
	if len(parts) != 3 || request.StartLine != nil || request.EndLine != nil || request.MaxBytes != nil || request.EndOffset != nil || request.SourceRevision != "" {
		return nil, toolError("INVALID_ARGUMENT", "Use an exact activity source URI with offset and limit_chars only.", "validation")
	}
	call, err := r.activity.Call(ctx, parts[1])
	if err != nil {
		return nil, err
	}
	bound := activity.FromContext(ctx)
	if !activity.IsLocalManagement(ctx) {
		if call.SourceOwnerKey == "" || call.SourceOwnerKey != activity.SourceOwnerKey(ctx) {
			return nil, toolError("OUTPUT_OWNER_MISMATCH", "The retained output belongs to another authenticated client.", "permission")
		}
		if call.ConversationID != "" {
			if err = r.conversations.Owns(ctx, call.ConversationID); err != nil {
				return nil, err
			}
			if err = r.checkConversationGate(ctx, call.ConversationID); err != nil {
				return nil, err
			}
			if bound.ConversationID != call.ConversationID && (bound.TaskID == "" || bound.TaskID != call.TaskID) {
				return nil, toolError("OUTPUT_CONVERSATION_MISMATCH", "Resume the owning task before reading output from another conversation.", "permission")
			}
		}
		if call.WorkspaceID != "" && bound.WorkspaceID != call.WorkspaceID && !r.ownsSelectedContextSource(request.Path, bound) {
			return nil, toolError("OUTPUT_WORKSPACE_MISMATCH", "Select the original workspace before reading this retained output.", "permission")
		}
	}
	if parts[2] == "" && (call.OutputSource == nil || call.OutputSource.Ref == "") {
		return nil, toolError("OUTPUT_NOT_STORED", "This call has no retained ordinary-output source.", "runtime")
	}
	policy, ok := ctx.Value(outputPolicyContextKey{}).(config.ToolOutputSettings)
	if !ok {
		policy = r.MCPPresentationSettings().ToolOutput
	}
	limit := toolcontract.ModelVisibleBodyChars
	if policy.Enabled && policy.MaxChars > 0 {
		limit = min(limit, policy.MaxChars)
	}
	if request.LimitChars != nil {
		limit = min(limit, *request.LimitChars)
	}
	offset := int64(0)
	if request.Offset != nil {
		offset = *request.Offset
	}
	page, err := r.activity.ReadCallPayloadVersionCharacters(ctx, parts[1], parts[2], offset, limit)
	if err != nil {
		return nil, err
	}
	visible := toolcontract.JSONTextPrefix(page.Text, toolcontract.ModelVisibleBodyChars-2000)
	page.HasMore = page.HasMore || len(visible) < len(page.Text)
	page.NextOffset = page.Offset + int64(len(visible))
	page.Text, page.ReturnedChars = visible, utf8.RuneCountInString(visible)
	descriptor := *page.Payload
	descriptor.Preview = ""
	source := activity.SourceReference(parts[1], page.Payload)
	result := Result{"path": source, "content": page.Text, "encoding": "utf-8", "size_bytes": page.Payload.Bytes, "offset": page.Offset, "next_offset": page.NextOffset, "has_more": page.HasMore, "unit": "unicode_scalar", "limit_chars": limit, "returned_chars": page.ReturnedChars, "source_state": page.Payload.State, "payload": descriptor}
	result["range"] = map[string]any{"byte_start": page.Offset, "byte_end": page.NextOffset}
	result["capture"] = map[string]any{"state": page.Payload.State, "complete": page.Payload.State == "complete", "retained_bytes": page.Payload.Bytes, "source_ref": source}
	result["projection"] = map[string]any{"mode": "continuous_page", "complete": page.Offset == 0 && !page.HasMore, "visible_chars": page.ReturnedChars, "omitted_bytes": max(0, page.Payload.Bytes-page.NextOffset)}
	if page.HasMore {
		result["next_cursor"] = map[string]any{"byte_offset": page.NextOffset}
		result["continue_read"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": source, "offset": page.NextOffset, "limit_chars": limit}}
	}
	if err := r.describeRequiredSourcePage(ctx, bound, source, page, result); err != nil {
		return nil, err
	}
	return result, nil
}
