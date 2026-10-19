package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/activity"
)

func (r *Runtime) recordExecutionPayload(binding activity.Binding, name, kind string, value any, redactor activity.Redactor) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	event, ok := r.executionPayloadEvent(ctx, binding, name, kind, value, redactor)
	if !ok {
		return
	}
	if err := r.appendExecution(event); err != nil {
		slog.Warn("Activity payload reference could not be saved", "call_id", binding.CallID, "kind", kind)
	}
}

func (r *Runtime) executionPayloadEvent(ctx context.Context, binding activity.Binding, name, kind string, value any, redactor activity.Redactor) (activity.Event, bool) {
	if r.activity == nil || binding.CallID == "" || binding.ParentCallID != "" || binding.Visibility == "diagnostic" {
		return activity.Event{}, false
	}
	var payload *activity.Payload
	if kind == "request" {
		payload = r.activity.CaptureRequestPayload(ctx, value, "complete", redactor)
	} else {
		payload = r.activity.CapturePayload(ctx, value, "complete", redactor)
	}
	event := activity.Event{Binding: binding, Kind: "call.payload", ToolName: name}
	if kind == "request" {
		event.Request = payload
	} else {
		event.Response = payload
		source := deliveredModelPayload(value)
		if source["projection"] != nil {
			encoded, err := json.Marshal(source)
			stages := &activity.OutputStages{}
			if err == nil && json.Unmarshal(encoded, stages) == nil {
				stages.ActivityPreview = &activity.PreviewMeasurement{Chars: utf8.RuneCountInString(payload.Preview), Truncated: payload.Truncated}
				event.OutputStages = stages
			}
		}
	}
	return event, true
}

func bindResponseAudit(ctx context.Context, binding activity.Binding, name string, redactor activity.Redactor, received time.Time, status string) bool {
	response, _ := ctx.Value(toolResponseKey{}).(*ToolResponse)
	if response == nil || binding.ParentCallID != "" {
		return false
	}
	response.mu.Lock()
	defer response.mu.Unlock()
	response.auditBinding, response.auditName, response.auditRedactor = binding, name, redactor
	response.auditReceived, response.auditStatus = received, status
	return true
}

// RecordToolResponse is called only at the adapter boundary, after catalog and
// authenticated insertions are attached. The optional logical envelope keeps
// host diagnostics in Activity while delivery acknowledgement uses the actual
// model envelope. It updates the same root call without another output event.
func (r *Runtime) RecordToolResponse(response *ToolResponse, envelope any, logical ...any) {
	if response == nil {
		return
	}
	response.mu.Lock()
	if response.auditRecorded {
		response.mu.Unlock()
		return
	}
	response.auditRecorded = true
	binding, name, redactor := response.auditBinding, response.auditName, response.auditRedactor
	for _, message := range response.additions.UserMessages {
		redactor = redactor.WithSecrets(message.ReceiptToken)
	}
	received, status := response.auditReceived, response.auditStatus
	response.mu.Unlock()
	audit := envelope
	if len(logical) > 0 && logical[0] != nil {
		audit = logical[0]
	}
	r.recordExecutionPayload(binding, name, "response", audit, redactor)
	if payload := deliveredModelPayload(envelope); payload != nil {
		r.commitStructuredDelivery(binding, Result(payload))
	}
	// This server-side boundary includes final-envelope serialization and
	// observation storage. It does not invent downstream network delivery time.
	if !received.IsZero() {
		if err := r.recordRPCReturnStatus(binding, name, received, status); err != nil {
			slog.Warn("Final response timing could not be saved", "call_id", binding.CallID)
		}
	}
}
