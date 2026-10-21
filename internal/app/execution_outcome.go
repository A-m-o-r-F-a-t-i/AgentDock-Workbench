package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

const maxPrerequisiteReceipts = 256
const prerequisiteReceiptTTL = 10 * time.Minute

type prerequisiteReceipt struct {
	origin  string
	count   int
	action  map[string]any
	expires time.Time
}
type executionOutcomeError struct {
	primary  *ToolError
	original error
}

func (e *executionOutcomeError) Error() string   { return e.primary.Error() }
func (e *executionOutcomeError) Unwrap() []error { return []error{e.primary, e.original} }

func outcomeObject(value any) map[string]any {
	if object := modelObject(value); object != nil {
		return object
	}
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > 16384 {
		return nil
	}
	var object map[string]any
	_ = json.Unmarshal(data, &object)
	return object
}
func remoteOutcome(result Result) map[string]any {
	if details := modelObject(result["upstream_failure"]); details != nil {
		return maps.Clone(details)
	}
	details, _ := mcpclient.ResultFailure(modelObject(result["result"]))
	return details
}
func resolvedBuiltinDiscoveryOutcome(err error) (Result, bool) {
	if !toolmcp.IsBuiltinDiscoveryError(err) {
		return nil, false
	}
	var route *ToolError
	if !errors.As(err, &route) {
		return nil, false
	}
	result := Result(maps.Clone(route.Details))
	result["route_code"] = route.Code
	result["effect_state"] = "none"
	result["execution_performed"] = false
	result["retry_safe"] = false
	return result, true
}

func businessOutcomeFailure(name string, result Result) bool {
	if resultReportsFailure(result) {
		return true
	}
	if name != "mcp_tool_call" {
		return false
	}
	if modelObject(result["upstream_failure"]) != nil {
		return true
	}
	_, failed := mcpclient.ResultFailure(modelObject(result["result"]))
	return failed
}
func outcomeFailure(name string, result Result, failure error) *ToolError {
	var known *ToolError
	if errors.As(failure, &known) {
		copy := *known
		copy.Details = maps.Clone(known.Details)
		return &copy
	}
	var provider *mcpclient.Error
	if errors.As(failure, &provider) {
		return &ToolError{Code: provider.Code, Message: provider.Message, Category: "external", Retryable: provider.Retryable, Details: maps.Clone(provider.Details)}
	}
	if failure != nil {
		return &ToolError{Code: "EXECUTION_FAILED", Message: failure.Error(), Category: "runtime", Details: map[string]any{}}
	}
	details := map[string]any{}
	code, message := "OPERATION_FAILED", "The operation reported failure."
	if name == "mcp_tool_call" {
		details = remoteOutcome(result)
		code = stringArg(details, "code")
		message = stringArg(details, "message")
		if code == "" {
			if stringArg(details, "operation_status") == "partial" {
				code = "MCP_BUSINESS_PARTIAL"
			} else {
				code = "MCP_BUSINESS_FAILED"
			}
		}
	} else if isCommandExecutionTool(name) {
		code = "COMMAND_FAILED"
		message = stringArg(result, "command_error")
	}
	if len(code) > 128 || strings.ContainsAny(code, "\r\n\t") {
		code = "OPERATION_FAILED"
	}
	if message == "" {
		message = "The operation reported failure; inspect its returned outcome."
	}
	return &ToolError{Code: code, Message: message, Category: "external", Details: details}
}
func failureControl(name string, result Result, failure *ToolError, state executionObservation) map[string]any {
	control := map[string]any{}
	contractFailure := isOutputContractFailure(failure.Code)
	for _, key := range []string{"operation_status", "effect_state", "execution_performed", "retry_safe"} {
		if value, exists := failure.Details[key]; exists {
			control[key] = value
		}
		if value, exists := result[key]; exists && !contractFailure {
			control[key] = value
		}
	}
	performed, known := control["execution_performed"].(bool)
	if !known {
		if !state.executed || failure.Details["executed"] == false || result["executed"] == false {
			performed, known = false, true
		} else if name != "mcp_tool_call" {
			performed, known = true, true
		}
	}
	effect := stringArg(control, "effect_state")
	switch effect {
	case "none", "complete", "partial", "unknown":
	default:
		effect = "unknown"
		if !state.executed || known && !performed {
			effect = "none"
		}
	}
	if spec, ok := toolSpecByName(name); ok && spec.Annotations != nil && spec.Annotations.ReadOnlyHint {
		effect = "none"
	}
	control["effect_state"] = effect
	if known {
		control["execution_performed"] = performed
	} else {
		control["execution_performed"] = nil
	}
	safe, _ := control["retry_safe"].(bool)
	safe = safe && effect == "none"
	if name == "mcp_tool_call" && activity.IsMCPConnectionError(failure.Code) && effect == "none" {
		safe = true
	}
	if contractFailure {
		safe = false
	}
	control["retry_safe"] = safe
	status := stringArg(control, "operation_status")
	if status != "failed" && status != "blocked" && status != "partial" {
		status = "failed"
		if effect == "none" && (failure.Category == "permission" || failure.Category == "auth" || failure.Code == "TOOL_BUNDLE_REQUIRED") {
			status = "blocked"
		}
	}
	control["operation_status"], control["error_kind"] = status, strings.ToLower(failure.Code)
	return control
}
func (r *Runtime) failureRequiredAction(name string, args map[string]any, failure *ToolError, original error, state executionObservation, control map[string]any) map[string]any {
	if action := modelObject(failure.Details["required_action"]); action != nil {
		copied, _ := cloneExecutionArguments(action)
		return copied
	}
	if toolmcp.IsBuiltinDiscoveryError(original) {
		return executionNextAction("protocol", stringArg(failure.Details, "required_host_tool"), "host_tool_namespace", state.binding.CallID, nil, false)
	}
	if name != "mcp_tool_call" && failure.Code == "TOOL_BUNDLE_REQUIRED" && stringArg(failure.Details, "bundle") != "" {
		return executionNextAction("protocol", "capability_load", "tool_bundle_required", state.binding.CallID, map[string]any{"action": "load", "name": failure.Details["bundle"]}, true)
	}
	if failure.Category == "permission" || failure.Category == "auth" {
		return executionNextAction("authorization", "", strings.ToLower(failure.Code), state.binding.CallID, nil, false)
	}
	switch failure.Code {
	case "MCP_CATALOG_CHANGED", "MCP_CATALOG_REFRESH_FAILED", "MCP_TOOL_CHANGED_AFTER_APPROVAL", "MCP_PREPARED_CALL_REVALIDATION_FAILED", "MCP_TOOL_CHANGED_AFTER_RECOVERY":
		if target := stringArg(args, "name"); target != "" {
			server, _, ok := strings.Cut(target, ":")
			if ok {
				return executionNextAction("protocol", "mcp_tool_search", "tool_catalog_refresh_required", state.binding.CallID, map[string]any{"query": target, "server": server, "refresh": true}, true)
			}
		}
	}
	return executionNextAction("model_decision", "", strings.ToLower(failure.Code), state.binding.CallID, nil, false)
}
func (r *Runtime) rememberPrerequisite(name string, args map[string]any, state executionObservation, reason string, action map[string]any) (string, int, map[string]any) {
	scope := state.binding
	if scope.SourceOwnerKey == "" || scope.ConversationID == "" || scope.WorkspaceID == "" || action["category"] != "protocol" {
		return scope.CallID, 1, action
	}
	revision := ""
	if r.capabilityManager != nil {
		revision = r.capabilityManager.DirectoryRevision()
	}
	raw, err := json.Marshal([]any{scope.SourceOwnerKey, scope.ConversationID, scope.WorkspaceID, name, args, reason, revision})
	if err != nil {
		return scope.CallID, 1, action
	}
	// The key replaces repeated comparisons of the potentially large request;
	// matching receipts take a different, compact presentation path.
	digest := sha256.Sum256(raw)
	key := string(digest[:])
	now := time.Now()
	r.prerequisiteMu.Lock()
	defer r.prerequisiteMu.Unlock()
	if r.prerequisites == nil {
		r.prerequisites = map[string]*prerequisiteReceipt{}
	}
	for key, item := range r.prerequisites {
		if !now.Before(item.expires) {
			delete(r.prerequisites, key)
		}
	}
	if item := r.prerequisites[key]; item != nil {
		item.count = min(item.count+1, 65535)
		copied, _ := cloneExecutionArguments(item.action)
		return item.origin, item.count, copied
	}
	if len(r.prerequisites) >= maxPrerequisiteReceipts {
		oldest := ""
		var expires time.Time
		for candidate, item := range r.prerequisites {
			if oldest == "" || item.expires.Before(expires) {
				oldest, expires = candidate, item.expires
			}
		}
		delete(r.prerequisites, oldest)
	}
	stored, _ := cloneExecutionArguments(action)
	r.prerequisites[key] = &prerequisiteReceipt{origin: scope.CallID, count: 1, action: stored, expires: now.Add(prerequisiteReceiptTTL)}
	return scope.CallID, 1, action
}
func (r *Runtime) toolExecutionDetails(name string, args map[string]any, result Result, control map[string]any, prepared *toolmcp.PreparedCall) *activity.ToolExecutionDetails {
	detail := &activity.ToolExecutionDetails{OperationStatus: stringArg(control, "operation_status"), ErrorKind: stringArg(control, "error_kind"), EffectState: stringArg(control, "effect_state"), OriginCallID: stringArg(control, "origin_call_id"), MatchKind: stringArg(result, "match_kind"), RouteStatus: stringArg(result, "resolution_status"), HostRefreshRequired: result["host_refresh_required"] == true}
	if detail.OriginCallID == "" {
		detail.OriginCallID = stringArg(args, "origin_call_id")
	}
	if detail.RouteStatus == "" {
		detail.RouteStatus = stringArg(control, "route_status")
	}
	if value, ok := control["execution_performed"].(bool); ok {
		detail.ExecutionPerformed = &value
	}
	if value, ok := control["retry_safe"].(bool); ok {
		detail.RetrySafe = &value
	}
	detail.RepeatCount, _ = control["repeat_count"].(int)
	action := modelObject(control["required_action"])
	if action == nil {
		action = modelObject(result["required_action"])
	}
	if action != nil {
		raw, _ := json.Marshal(action["arguments"])
		detail.RequiredAction = &activity.RequiredActionSummary{Category: stringArg(action, "category"), Tool: stringArg(action, "tool"), ReasonCode: stringArg(action, "reason_code"), Arguments: string(raw), Required: action["required"] == true}
	}
	catalog := modelObject(result["mcp_catalog"])
	if name == "mcp_tool_list" {
		catalog = map[string]any(result)
	}
	selected := modelObject(result["selected"])
	if selected != nil {
		detail.Server = stringArg(selected, "server")
		detail.QualifiedName = stringArg(selected, "qualified_name")
		if catalog == nil {
			catalog = selected
		}
	}
	if name == "mcp_tool_call" {
		detail.QualifiedName = stringArg(args, "name")
		detail.Server, _, _ = strings.Cut(detail.QualifiedName, ":")
	}
	if detail.Server == "" {
		detail.Server = stringArg(args, "server")
	}
	if name == "mcp_manage" {
		detail.Server = stringArg(args, "name")
		if server := outcomeObject(result["server"]); server != nil {
			catalog = server
		}
	}
	state := outcomeObject(catalog["catalog_state"])
	if state == nil {
		state = outcomeObject(result["catalog_state"])
	}
	detail.CatalogState, detail.CatalogRevision, detail.ServerRevision = stringArg(state, "state"), stringArg(catalog, "catalog_revision"), stringArg(state, "server_revision")
	if detail.CatalogRevision == "" {
		detail.CatalogRevision = stringArg(state, "catalog_revision")
	}
	if prepared != nil {
		summary := prepared.Summary()
		detail.ServerFingerprint, detail.SchemaDigest, detail.AnnotationsDigest = summary.ServerFingerprint, summary.ToolSchemaDigest, summary.ToolAnnotationsDigest
		if detail.CatalogRevision == "" {
			detail.CatalogRevision = summary.CatalogRevision
		}
	}
	if detail.Server == "" && detail.OperationStatus == "" && detail.RequiredAction == nil && detail.OriginCallID == "" && !detail.HostRefreshRequired && detail.MatchKind == "" {
		return nil
	}
	return detail
}
func (r *Runtime) executionOutcome(name string, args map[string]any, result Result, err error, state executionObservation, prepared *toolmcp.PreparedCall) (Result, error, *activity.ToolExecutionDetails) {
	if route, ok := resolvedBuiltinDiscoveryOutcome(err); ok {
		return route, nil, r.toolExecutionDetails(name, args, route, map[string]any(route), prepared)
	}
	if err == nil && !businessOutcomeFailure(name, result) {
		control := map[string]any{}
		if action := modelObject(result["required_action"]); action != nil && result["resolution_status"] == "incomplete" {
			id, count, action := r.rememberPrerequisite(name, args, state, stringArg(action, "reason_code"), action)
			result = maps.Clone(result)
			result["origin_call_id"], result["repeat_count"], result["required_action"] = id, count, action
			if count > 1 {
				result["error_kind"], result["operation_status"] = "prerequisite_unchanged", "blocked"
			}
			control = map[string]any(result)
		}
		return result, nil, r.toolExecutionDetails(name, args, result, control, prepared)
	}
	base := outcomeFailure(name, result, err)
	if base.Details == nil {
		base.Details = map[string]any{}
	}
	control := failureControl(name, result, base, state)
	control["message"] = r.executionRedactor(args).Text(base.Message, 1024)
	control["required_action"] = r.failureRequiredAction(name, args, base, err, state, control)
	metadata := r.toolExecutionDetails(name, args, result, control, prepared)
	if name == "mcp_tool_call" && activity.IsMCPConnectionError(base.Code) && state.binding.RetryOfCallID == "" && !errors.Is(err, context.Canceled) && state.binding.ConversationID != "" && state.binding.WorkspaceID != "" && state.binding.SourceOwnerKey != "" {
		server, _, ok := strings.Cut(stringArg(args, "name"), ":")
		if ok && metadata != nil {
			cfg, summary, lookupErr := r.capabilityManager.Inspect(server)
			if lookupErr == nil {
				if metadata.ServerFingerprint == "" {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					metadata.ServerFingerprint, _ = r.capabilityManager.ExecutionFingerprint(ctx, server)
					cancel()
				}
				if metadata.ServerFingerprint != "" {
					metadata.ServerRevision = summary.CatalogState.ServerRevision
					metadata.CatalogState = summary.CatalogState.State
					metadata.Recovery = &activity.MCPRecoveryState{State: "eligible", ExpiresAt: time.Now().UTC().Add(managedMCPRecoveryWindow)}
					control["origin_call_id"] = state.binding.CallID
					recoveryArgs := map[string]any{"action": "recover", "name": server, "origin_call_id": state.binding.CallID}
					control["recovery"] = map[string]any{"tool": "mcp_manage", "arguments": recoveryArgs, "expires_at": metadata.Recovery.ExpiresAt}
					action := executionNextAction("recovery", "mcp_manage", "managed_mcp_connection_failed", state.binding.CallID, recoveryArgs, false)
					if cfg.RecoverySkill != "" {
						action = executionNextAction("recovery", "read_file", "managed_mcp_recovery_skill_required", state.binding.CallID, map[string]any{"path": cfg.RecoverySkill + "/SKILL.md"}, false)
						control["recovery"].(map[string]any)["skill_ref"] = cfg.RecoverySkill
					}
					control["required_action"] = action
				}
			}
		}
	}
	action := modelObject(control["required_action"])
	if action != nil && action["category"] == "protocol" && !toolmcp.IsBuiltinDiscoveryError(err) {
		id, count, action := r.rememberPrerequisite(name, args, state, base.Code, action)
		control["origin_call_id"], control["repeat_count"], control["required_action"] = id, count, action
		if count > 1 {
			control["operation_status"], control["error_kind"] = "blocked", "prerequisite_unchanged"
			control["message"] = "The prerequisite is unchanged. Follow the original required_action."
			base.Details = map[string]any{}
		}
	}
	updated := r.toolExecutionDetails(name, args, result, control, prepared)
	if updated != nil && metadata != nil {
		updated.Recovery = metadata.Recovery
		if updated.ServerFingerprint == "" {
			updated.ServerFingerprint = metadata.ServerFingerprint
		}
		if updated.CatalogState == "" {
			updated.CatalogState = metadata.CatalogState
		}
		if updated.ServerRevision == "" {
			updated.ServerRevision = metadata.ServerRevision
		}
	}
	if err == nil {
		result = maps.Clone(result)
		if result == nil {
			result = Result{}
		}
		for _, key := range []string{"outcome", "counts", "saved"} {
			if value, exists := base.Details[key]; exists {
				result[key] = value
			}
		}
		for key, value := range control {
			result[key] = value
		}
		result["error_code"] = base.Code
		return result, nil, updated
	}
	for key, value := range control {
		base.Details[key] = value
	}
	base.Message = stringArg(control, "message")
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || toolmcp.IsBuiltinDiscoveryError(err) {
		return result, &executionOutcomeError{primary: base, original: err}, updated
	}
	return result, base, updated
}

// Called only after prepareModelOutput retained the full canonical result.
func compactPrerequisiteProjection(result Result) Result {
	if result["error_kind"] != "prerequisite_unchanged" {
		return result
	}
	compact := Result{"output_class": "compact_receipt"}
	for _, key := range []string{"operation_status", "error_kind", "effect_state", "execution_performed", "retry_safe", "message", "origin_call_id", "repeat_count", "required_action", "resolution_status", "absence_authoritative", "count", "catalog_state", "operation", "capture", "source_complete", "call_id"} {
		if value, exists := result[key]; exists {
			compact[key] = value
		}
	}
	return compact
}
