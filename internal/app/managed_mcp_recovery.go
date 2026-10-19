package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

const managedMCPRecoveryWindow = 10 * time.Minute
const maxMCPRecoveryRequestBytes = 1 << 20
const mcpRecoveryReadPageBytes = 32768

func executionNextAction(category, tool, reason, origin string, args map[string]any, retrySafe bool) map[string]any {
	action := map[string]any{"category": category, "required": true, "execution_performed": false, "reason_code": reason, "retry_safe": retrySafe}
	if tool != "" {
		action["tool"] = tool
	}
	if args != nil {
		action["arguments"] = args
	}
	if origin != "" {
		action["origin_call_id"] = origin
	}
	return action
}
func managedMCPRecoveryError(err error, origin string) error {
	code := "MCP_RECOVERY_FAILED"
	switch {
	case errors.Is(err, activity.ErrRecoveryOrigin):
		code = "MCP_RECOVERY_ORIGIN_INVALID"
	case errors.Is(err, activity.ErrRecoveryScope):
		code = "MCP_RECOVERY_SCOPE_MISMATCH"
	case errors.Is(err, activity.ErrRecoveryExpired):
		code = "MCP_RECOVERY_EXPIRED"
	case errors.Is(err, activity.ErrRecoveryUsed):
		code = "MCP_RECOVERY_ALREADY_USED"
	case errors.Is(err, activity.ErrRecoveryFailed):
		code = "MCP_RECOVERY_FAILED"
	case errors.Is(err, activity.ErrRecoveryUnsafe):
		code = "MCP_RECOVERY_EFFECTS_UNKNOWN"
	case errors.Is(err, activity.ErrRecoveryPending):
		code = "MCP_RECOVERY_PENDING"
	}
	failure := toolErrorDetails(code, err.Error(), "validation", map[string]any{"origin_call_id": origin, "executed": false, "execution_performed": false, "effect_state": "none", "retry_safe": false, "required_action": executionNextAction("model_decision", "", strings.ToLower(code), origin, nil, false)})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(failure, err)
	}
	return failure
}
func (r *Runtime) managedMCPRecoveryOrigin(ctx context.Context, id, server string) (activity.ExecutionCall, mcpclient.ServerConfig, error) {
	origin, err := r.activity.MCPRecoveryOrigin(ctx, id, activity.FromContext(ctx), server)
	if err != nil {
		return origin, mcpclient.ServerConfig{}, managedMCPRecoveryError(err, id)
	}
	if origin.ToolExecution.Recovery.State == "recovery_failed" {
		return origin, mcpclient.ServerConfig{}, managedMCPRecoveryError(activity.ErrRecoveryFailed, id)
	}
	cfg, _, err := r.capabilityManager.Inspect(server)
	if err != nil {
		return origin, cfg, managedMCPRecoveryError(err, id)
	}
	if !cfg.Enabled {
		return origin, cfg, managedMCPRecoveryError(activity.ErrRecoveryScope, id)
	}
	fingerprint, err := r.capabilityManager.ExecutionFingerprint(ctx, server)
	if err != nil {
		return origin, cfg, managedMCPRecoveryError(err, id)
	}
	if origin.ToolExecution.ServerFingerprint == "" || fingerprint != origin.ToolExecution.ServerFingerprint {
		return origin, cfg, managedMCPRecoveryError(activity.ErrRecoveryScope, id)
	}
	return origin, cfg, nil
}
func recoveryMCPIdentity(origin activity.ExecutionCall) mcpclient.PreparedCallSummary {
	detail := origin.ToolExecution
	return mcpclient.PreparedCallSummary{QualifiedName: detail.QualifiedName, ServerFingerprint: detail.ServerFingerprint, ToolSchemaDigest: detail.SchemaDigest, ToolAnnotationsDigest: detail.AnnotationsDigest}
}
func (r *Runtime) readMCPRecoveryRequest(ctx context.Context, origin activity.ExecutionCall) (map[string]any, error) {
	unavailable := func() error {
		return toolErrorDetails("MCP_RECOVERY_REQUEST_UNAVAILABLE", "The exact original arguments are unavailable or redacted; reconstruct a new explicit request instead of replaying a preview", "validation", map[string]any{"origin_call_id": origin.CallID, "executed": false, "effect_state": "none", "retry_safe": false})
	}
	// Truncated describes the preview. Completeness is checked against the actual
	// immutable source, not the short preview shown in the Activity list.
	source := origin.Request
	if source == nil || source.State != "complete" || source.Ref == "" || source.Bytes <= 0 || source.Bytes > maxMCPRecoveryRequestBytes {
		return nil, unavailable()
	}
	var raw bytes.Buffer
	for offset := int64(0); offset < source.Bytes; {
		page, err := r.activity.ReadCallPayload(ctx, origin.CallID, "request", offset, mcpRecoveryReadPageBytes)
		if err != nil {
			return nil, err
		}
		if page.NextOffset <= offset || page.NextOffset > source.Bytes || raw.Len()+len(page.Text) > maxMCPRecoveryRequestBytes {
			return nil, unavailable()
		}
		raw.WriteString(page.Text)
		offset = page.NextOffset
		if !page.HasMore && offset != source.Bytes {
			return nil, unavailable()
		}
	}
	if int64(raw.Len()) != source.Bytes || bytes.Contains(raw.Bytes(), []byte("[REDACTED]")) {
		return nil, unavailable()
	}
	var request map[string]any
	if toolcore.DecodeJSON(bytes.NewReader(raw.Bytes()), &request) != nil || stringArg(request, "name") != origin.ToolExecution.QualifiedName {
		return nil, unavailable()
	}
	if _, ok := request["arguments"].(map[string]any); !ok {
		return nil, unavailable()
	}
	return mcpRecoveryBusinessRequest(request), nil
}
func mcpRecoveryBusinessRequest(request map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"name", "arguments", "interaction_intent"} {
		if value, exists := request[key]; exists {
			result[key] = value
		}
	}
	return result
}
func (r *Runtime) authorizeMCPRecoverySkill(ctx context.Context, id, server, skillRef string) error {
	if id == "" {
		return toolErrorDetails("MCP_RECOVERY_ORIGIN_REQUIRED", "A recovery Skill requires the original connection-failed MCP call", "validation", map[string]any{"executed": false, "effect_state": "none", "skill_ref": skillRef, "server": server})
	}
	_, cfg, err := r.managedMCPRecoveryOrigin(ctx, id, server)
	if err != nil {
		return err
	}
	if cfg.RecoverySkill != "" && cfg.RecoverySkill != skillRef {
		return managedMCPRecoveryError(activity.ErrRecoveryScope, id)
	}
	actor := activity.FromContext(ctx)
	if _, err = r.activity.TransitionMCPRecovery(ctx, id, actor, activity.MCPRecoveryTransition{Phase: "claim", Server: server, SkillRef: skillRef}); err != nil {
		return managedMCPRecoveryError(err, id)
	}
	if err = r.appendExecution(activity.Event{Binding: actor, Kind: "call.recovery_origin", ToolName: "exec_command", ToolExecution: &activity.ToolExecutionDetails{Server: server, OriginCallID: id}}); err != nil {
		return r.failManagedMCPRecovery(ctx, id, server, actor, err)
	}
	return nil
}

// Recovery is a single attempt. Persist failure even when its transport context
// was cancelled; only a still-running recovery step may remain pending. A new
// explicit business request is required after failure, never an automatic replay.
func (r *Runtime) failManagedMCPRecovery(ctx context.Context, id, server string, actor activity.Binding, cause error) error {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, persistErr := r.activity.TransitionMCPRecovery(settleCtx, id, actor, activity.MCPRecoveryTransition{Phase: "fail", Server: server})
	failure := managedMCPRecoveryError(cause, id)
	if persistErr != nil {
		return errors.Join(failure, fmt.Errorf("recovery failure could not be persisted: %w", persistErr))
	}
	return failure
}

// Command startup can fail after the recovery Skill claimed its origin. Settle
// only that exact claim, after the existing command lifecycle persisted a
// terminal outcome. A running Skill and a confirmed successful Skill stay owned
// by the normal recovery continuation; neither may be consumed by an observer.
func (r *Runtime) settleManagedMCPRecoverySkill(binding activity.Binding, args map[string]any, status string) error {
	id := stringArg(args, "origin_call_id")
	if id == "" || !activity.CallTerminal(status) || status == "succeeded" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	origin, err := r.activity.Call(ctx, id)
	if err != nil {
		return err
	}
	if origin.ToolExecution == nil || origin.ToolExecution.Recovery == nil {
		return nil
	}
	recovery := origin.ToolExecution.Recovery
	if recovery.CallID != binding.CallID || recovery.SkillRef == "" || recovery.State != "claimed" || recovery.ConsumedAt != nil {
		return nil
	}
	_, err = r.activity.TransitionMCPRecovery(ctx, id, binding, activity.MCPRecoveryTransition{Phase: "fail", Server: origin.ToolExecution.Server})
	return err
}

func (r *Runtime) recoverySkillRequired(ctx context.Context, origin activity.ExecutionCall, skillRef string) error {
	resolved, release, err := r.skills.Acquire(ctx, skillRef)
	if err != nil {
		return managedMCPRecoveryError(err, origin.CallID)
	}
	defer release()
	if resolved.RecoveryMCPServer != origin.ToolExecution.Server {
		return managedMCPRecoveryError(activity.ErrRecoveryScope, origin.CallID)
	}
	action := executionNextAction("recovery", "read_file", "managed_mcp_recovery_skill_required", origin.CallID, map[string]any{"path": resolved.SkillRef + "/SKILL.md"}, false)
	return toolErrorDetails("MCP_RECOVERY_SKILL_REQUIRED", "Run the declared recovery Skill with this origin_call_id, then resume mcp_manage recover", "validation", map[string]any{"origin_call_id": origin.CallID, "skill_ref": resolved.SkillRef, "server": resolved.RecoveryMCPServer, "executed": false, "effect_state": "none", "required_action": action})
}
func (r *Runtime) recoverManagedMCP(ctx context.Context, request toolmcp.ManageRequest) (Result, error) {
	actor := activity.FromContext(ctx)
	origin, cfg, err := r.managedMCPRecoveryOrigin(ctx, request.OriginCallID, request.Name)
	if err != nil {
		return nil, err
	}
	recovery := origin.ToolExecution.Recovery
	if recovery.ConsumedAt != nil || recovery.RetryID != "" {
		return nil, managedMCPRecoveryError(activity.ErrRecoveryUsed, origin.CallID)
	}
	if recovery.State == "refreshing" {
		step, stepErr := r.activity.Call(ctx, recovery.RefreshID)
		if stepErr != nil {
			return nil, managedMCPRecoveryError(stepErr, origin.CallID)
		}
		if !activity.CallTerminal(step.Status) {
			return nil, managedMCPRecoveryError(activity.ErrRecoveryPending, origin.CallID)
		}
		return nil, r.failManagedMCPRecovery(ctx, origin.CallID, request.Name, actor, activity.ErrRecoveryFailed)
	}
	if recovery.State != "recovered" {
		if recovery.CallID == "" {
			if cfg.RecoverySkill != "" {
				return nil, r.recoverySkillRequired(ctx, origin, cfg.RecoverySkill)
			}
			origin, err = r.activity.TransitionMCPRecovery(ctx, origin.CallID, actor, activity.MCPRecoveryTransition{Phase: "claim", Server: request.Name})
			if err != nil {
				return nil, managedMCPRecoveryError(err, request.OriginCallID)
			}
		} else {
			if cfg.RecoverySkill != "" && cfg.RecoverySkill != recovery.SkillRef {
				return nil, managedMCPRecoveryError(activity.ErrRecoveryScope, origin.CallID)
			}
			step, stepErr := r.activity.Call(ctx, recovery.CallID)
			if stepErr != nil {
				return nil, managedMCPRecoveryError(stepErr, origin.CallID)
			}
			if !activity.CallTerminal(step.Status) {
				return nil, managedMCPRecoveryError(activity.ErrRecoveryPending, origin.CallID)
			}
			if recovery.SkillRef == "" || step.Status != "succeeded" || step.CommandOK == nil || !*step.CommandOK {
				return nil, r.failManagedMCPRecovery(ctx, origin.CallID, request.Name, actor, activity.ErrRecoveryFailed)
			}
		}
		origin, err = r.activity.TransitionMCPRecovery(ctx, origin.CallID, actor, activity.MCPRecoveryTransition{Phase: "refresh", Server: request.Name})
		if err != nil {
			if errors.Is(err, activity.ErrRecoveryPending) || errors.Is(err, activity.ErrRecoveryUsed) || errors.Is(err, activity.ErrRecoveryScope) {
				return nil, managedMCPRecoveryError(err, request.OriginCallID)
			}
			return nil, r.failManagedMCPRecovery(ctx, request.OriginCallID, request.Name, actor, err)
		}
		fixedContext := mcpclient.WithExpectedMCPIdentity(ctx, recoveryMCPIdentity(origin))
		if _, _, err = r.capabilityManager.Refresh(fixedContext, request.Name); err != nil {
			return nil, r.failManagedMCPRecovery(ctx, origin.CallID, request.Name, actor, err)
		}
		origin, err = r.activity.TransitionMCPRecovery(ctx, origin.CallID, actor, activity.MCPRecoveryTransition{Phase: "ready", Server: request.Name})
		if err != nil {
			return nil, r.failManagedMCPRecovery(ctx, request.OriginCallID, request.Name, actor, err)
		}
	}
	result := Result{"action": "recover", "origin_call_id": origin.CallID, "recovery": origin.ToolExecution.Recovery, "retry_performed": false}
	if origin.ToolExecution.RetrySafe == nil || !*origin.ToolExecution.RetrySafe {
		result["required_action"] = executionNextAction("model_decision", "", "reconcile_unknown_mcp_effects", origin.CallID, nil, false)
		return result, nil
	}
	originalArgs, err := r.readMCPRecoveryRequest(ctx, origin)
	if err != nil {
		return result, err
	}
	originalArgs["retry_of_call_id"] = origin.CallID
	retryBinding := actor
	retryBinding.TaskID, retryBinding.ThreadID, retryBinding.StepID, retryBinding.WorkspaceID = origin.TaskID, origin.ThreadID, origin.StepID, origin.WorkspaceID
	retried, retryErr := r.Call(activity.WithBinding(ctx, retryBinding), "mcp_tool_call", originalArgs)
	result["retry"], result["retry_performed"] = retried, retried["executed"] == true
	if retryErr != nil || resultReportsFailure(retried) {
		result["operation_status"] = "failed"
	}
	if retried["status"] == "pending_approval" {
		result["required_action"] = executionNextAction("authorization", "", "fixed_mcp_retry_approval", origin.CallID, nil, false)
	}
	if current, readErr := r.activity.Call(ctx, origin.CallID); readErr == nil && current.ToolExecution != nil {
		result["recovery"] = current.ToolExecution.Recovery
	}
	return result, retryErr
}

// This guard runs after normal permission admission but before the MCP client
// can initialize a server. The original request is never inferred from prose.
func (r *Runtime) prepareManagedMCPRetry(ctx context.Context, prepared *preparedExecution) (context.Context, error) {
	id := prepared.state.binding.RetryOfCallID
	if id == "" {
		return ctx, nil
	}
	previous, err := r.activity.Call(ctx, id)
	if err != nil {
		return ctx, managedMCPRecoveryError(err, id)
	}
	if !activity.IsMCPConnectionError(previous.ErrorCode) && (previous.ToolExecution == nil || previous.ToolExecution.Recovery == nil) {
		return ctx, nil
	}
	server, _, ok := strings.Cut(stringArg(prepared.args, "name"), ":")
	if !ok {
		return ctx, managedMCPRecoveryError(activity.ErrRecoveryScope, id)
	}
	origin, _, err := r.managedMCPRecoveryOrigin(ctx, id, server)
	if err != nil {
		return ctx, err
	}
	originalArgs, err := r.readMCPRecoveryRequest(ctx, origin)
	if err != nil {
		return ctx, err
	}
	originalJSON, _ := json.Marshal(originalArgs)
	retryJSON, marshalErr := json.Marshal(mcpRecoveryBusinessRequest(prepared.args))
	if marshalErr != nil || !bytes.Equal(originalJSON, retryJSON) {
		return ctx, managedMCPRecoveryError(activity.ErrRecoveryScope, id)
	}
	if _, err = r.activity.TransitionMCPRecovery(ctx, id, prepared.state.binding, activity.MCPRecoveryTransition{Phase: "retry", Server: server}); err != nil {
		return ctx, managedMCPRecoveryError(err, id)
	}
	return mcpclient.WithExpectedMCPIdentity(ctx, recoveryMCPIdentity(origin)), nil
}
func (r *Runtime) finishManagedMCPRetry(binding activity.Binding, args map[string]any, status string) {
	if binding.RetryOfCallID == "" {
		return
	}
	server, _, ok := strings.Cut(stringArg(args, "name"), ":")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = r.activity.TransitionMCPRecovery(ctx, binding.RetryOfCallID, binding, activity.MCPRecoveryTransition{Phase: "finish", Server: server, Status: status})
}
