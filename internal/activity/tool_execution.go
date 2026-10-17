package activity

import "time"

// ToolExecutionDetails are runtime-owned routing and outcome facts. Displayed
// next actions never grant permissions or change conversation bindings.
type ToolExecutionDetails struct {
	RouteStatus         string                 `json:"route_status,omitempty"`
	Server              string                 `json:"server,omitempty"`
	QualifiedName       string                 `json:"qualified_name,omitempty"`
	ServerRevision      string                 `json:"server_revision,omitempty"`
	ServerFingerprint   string                 `json:"server_fingerprint,omitempty"`
	CatalogRevision     string                 `json:"catalog_revision,omitempty"`
	CatalogState        string                 `json:"catalog_state,omitempty"`
	MatchKind           string                 `json:"match_kind,omitempty"`
	SchemaDigest        string                 `json:"schema_digest,omitempty"`
	AnnotationsDigest   string                 `json:"annotations_digest,omitempty"`
	OperationStatus     string                 `json:"operation_status,omitempty"`
	ErrorKind           string                 `json:"error_kind,omitempty"`
	EffectState         string                 `json:"effect_state,omitempty"`
	ExecutionPerformed  *bool                  `json:"execution_performed,omitempty"`
	RetrySafe           *bool                  `json:"retry_safe,omitempty"`
	HostRefreshRequired bool                   `json:"host_refresh_required,omitempty"`
	OriginCallID        string                 `json:"origin_call_id,omitempty"`
	RepeatCount         int                    `json:"repeat_count,omitempty"`
	RequiredAction      *RequiredActionSummary `json:"required_action,omitempty"`
	Recovery            *MCPRecoveryState      `json:"recovery,omitempty"`
}
type RequiredActionSummary struct {
	Category           string `json:"category,omitempty"`
	Tool               string `json:"tool,omitempty"`
	ReasonCode         string `json:"reason_code,omitempty"`
	Arguments          string `json:"arguments,omitempty"`
	ArgumentsTruncated bool   `json:"arguments_truncated,omitempty"`
	Required           bool   `json:"required"`
}

// Recovery consumption is kept on the original call in the existing journal.
type MCPRecoveryState struct {
	CallID     string     `json:"call_id,omitempty"`
	RefreshID  string     `json:"refresh_call_id,omitempty"`
	RetryID    string     `json:"retry_call_id,omitempty"`
	SkillRef   string     `json:"skill_ref,omitempty"`
	State      string     `json:"state"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

func (d *ToolExecutionDetails) clone() *ToolExecutionDetails {
	if d == nil {
		return nil
	}
	out := *d
	out.ExecutionPerformed = copyValue(d.ExecutionPerformed)
	out.RetrySafe = copyValue(d.RetrySafe)
	if d.RequiredAction != nil {
		action := *d.RequiredAction
		out.RequiredAction = &action
	}
	if d.Recovery != nil {
		recovery := *d.Recovery
		recovery.ClaimedAt = copyValue(d.Recovery.ClaimedAt)
		recovery.ConsumedAt = copyValue(d.Recovery.ConsumedAt)
		out.Recovery = &recovery
	}
	return &out
}
func (r Redactor) toolExecution(d *ToolExecutionDetails) *ToolExecutionDetails {
	d = d.clone()
	if d == nil {
		return nil
	}
	for _, field := range []*string{&d.Server, &d.QualifiedName, &d.ServerRevision, &d.ServerFingerprint, &d.CatalogRevision, &d.CatalogState, &d.MatchKind, &d.SchemaDigest, &d.AnnotationsDigest, &d.OperationStatus, &d.RouteStatus, &d.ErrorKind, &d.EffectState, &d.OriginCallID} {
		*field = r.Text(*field, 256)
	}
	if action := d.RequiredAction; action != nil {
		action.Category = r.Text(action.Category, 32)
		action.Tool = r.Text(action.Tool, 160)
		action.ReasonCode = r.Text(action.ReasonCode, 128)
		action.ArgumentsTruncated = action.ArgumentsTruncated || len(action.Arguments) > 2048
		action.Arguments = r.Text(action.Arguments, 2048)
	}
	if recovery := d.Recovery; recovery != nil {
		recovery.CallID = r.Text(recovery.CallID, 128)
		recovery.RefreshID = r.Text(recovery.RefreshID, 128)
		recovery.RetryID = r.Text(recovery.RetryID, 128)
		recovery.SkillRef = r.Text(recovery.SkillRef, 512)
		recovery.State = r.Text(recovery.State, 64)
	}
	return d
}

// Only these recorded business failures may open a managed-MCP recovery.
// Authentication, invalid arguments, discovery and cancellation are excluded.
func IsMCPConnectionError(code string) bool {
	switch code {
	case "MCP_CONNECTION_FAILED", "MCP_CLIENT_UNAVAILABLE", "MCP_START_FAILED", "MCP_TRANSPORT_UNAVAILABLE", "MCP_TRANSPORT_TIMEOUT", "MCP_TIMEOUT", "WINDOW_DISCONNECTED", "NO_ACTIVE_APPLICATION_WINDOW", "BRIDGE_UNAVAILABLE", "BRIDGE_PROTOCOL_MISMATCH":
		return true
	}
	return false
}
