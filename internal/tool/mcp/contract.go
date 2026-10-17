package mcp

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolManage         = "mcp_manage"
	ToolSearch         = "mcp_tool_search"
	ToolList           = "mcp_tool_list"
	ToolInspect        = "mcp_tool_inspect"
	ToolCall           = "mcp_tool_call"
	ToolResultContinue = "mcp_result_continue"
	ToolResultRaw      = "mcp_result_raw"
)

func InputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	boolProp := toolcontract.Boolean
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string

	switch name {
	case ToolManage:
		props["action"] = map[string]any{"type": "string", "description": "Dynamic MCP server or isolated environment action.", "enum": []string{"list", "inspect", "add", "remove", "enable", "disable", "env_set", "env_unset", "env_list", "refresh", "update", "reset_override"}}
		props["name"] = stringProp("Dynamic MCP server name. Use a stable short identifier such as figma or github.")
		props["description"] = stringProp("Short capability description shown in agentdock_context.")
		props["transport"] = map[string]any{"type": "string", "description": "MCP transport for action=add.", "enum": []string{"streamable_http", "stdio"}}
		props["url"] = stringProp("Absolute MCP endpoint URL for transport=streamable_http.")
		props["command"] = stringProp("Executable name or path for transport=stdio.")
		props["args"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command arguments for transport=stdio."}
		props["cwd"] = stringProp("Optional absolute working directory for transport=stdio.")
		props["header_env"] = map[string]any{"type": "object", "description": "HTTP header name to host environment variable name. Secret values are never stored in the MCP registry.", "additionalProperties": map[string]any{"type": "string"}}
		props["env_from_env"] = map[string]any{"type": "object", "description": "Child process environment variable name to host environment variable name for stdio. Secret values are never stored in the MCP registry.", "additionalProperties": map[string]any{"type": "string"}}
		props["key"] = stringProp("Environment variable name for env_set/env_unset.")
		props["value"] = stringProp("Environment variable value for env_set. Secret values are never returned.")
		props["enabled"] = boolProp("Enable the server after registration. Defaults to true.")
		props["timeout_ms"] = boundedIntProp("Per-request timeout. Defaults to 30000 and is capped at 300000.", 1, 300000)
		patchProps := map[string]any{}
		for _, field := range []string{"description", "transport", "url", "command", "args", "cwd", "header_env", "env_from_env", "timeout_ms"} {
			patchProps[field] = props[field]
		}
		props["patch"] = map[string]any{"type": "object", "description": "Editable host overlay fields for update. Does not rewrite plugin files, permissions, enabled state or discovered facts.", "properties": patchProps, "additionalProperties": false, "minProperties": 1}
		props["scope"] = map[string]any{"type": "string", "enum": []string{"runtime", "persistent"}, "description": "Required for update/reset_override. Runtime lasts for the current process; persistent survives restart."}
		props["expected_revision"] = stringProp("Current server.revision from inspect; required for compare-and-swap update/reset_override.")
		required = []string{"action"}
	case ToolSearch:
		props["query"] = stringProp("Dynamic MCP capability query. Known AgentDock built-ins use the caller host namespace directly; misuse returns a non-retryable routing error.")
		props["server"] = stringProp("Optional dynamic MCP server name from agentdock_context.")
		props["limit"] = boundedIntProp("Maximum matching tools. Defaults to 10 and is capped at 100.", 1, 100)
		required = []string{"query"}
	case ToolList:
		props["server"] = stringProp("Known enabled MCP server. Returns every visible tool name and one-line description without a search limit.")
		props["force_full"] = boolProp("Return the complete catalog even when this conversation already received the same revision.")
		required = []string{"server"}
	case ToolInspect:
		props["name"] = stringProp("Qualified dynamic MCP tool name in <server>:<tool> form.")
		props["names"] = map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 3}, "description": "Selected server:tool names and server:* selectors, including mixed services."}
		props["include_schema"] = boolProp("Include complete schemas for a batch. A single explicit tool includes its schema by default; batches default to summaries and digests.")
		props["force_full"] = boolProp("Return complete schemas even when this conversation already received the same schema digest. Implies include_schema=true.")
	case ToolCall:
		props["name"] = stringProp("Qualified dynamic MCP tool name in <server>:<tool> form.")
		toolcontract.ActivityProperties(props)
		props["interaction_intent"] = map[string]any{"type": "string", "enum": []string{"desktop_gui"}, "description": "Required as desktop_gui for Computer Use tools. Omit for unrelated MCP tools."}
		props["reason"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "description": "Concrete user-task reason for an explicitly selected Computer Use call. Required with interaction_intent."}
		// arguments 的结构由上游 MCP tool schema 决定，是这里唯一需要保持开放的动态叶节点。
		props["arguments"] = map[string]any{"type": "object", "description": "Arguments matching the schema returned by mcp_tool_inspect.", "additionalProperties": true}
		required = []string{"name", "arguments"}
	case ToolResultContinue:
		props["call_id"] = stringProp("Original mcp_tool_call activity call ID.")
		props["cursor"] = stringProp("Opaque AgentDock host result cursor returned by the original call or previous continuation.")
		required = []string{"call_id", "cursor"}
	case ToolResultRaw:
		props["call_id"] = stringProp("Original mcp_tool_call activity call ID.")
		props["offset"] = boundedIntProp("UTF-8 byte offset for explicit raw-response diagnostics.", 0, 1<<30)
		props["limit"] = boundedIntProp("Maximum UTF-8 bytes to return; defaults to 8000 and is capped at 65536.", 1, 65536)
		props["restart"] = boolProp("Allow an explicit diagnostic reread from the requested offset, including offset zero.")
		required = []string{"call_id"}
	default:
		return nil, false
	}
	if name == ToolSearch || name == ToolList || name == ToolInspect {
		toolcontract.StructuredPagingInput(props)
	}
	schema := toolcontract.InputObject(props, required...)
	if name == ToolInspect {
		schema["oneOf"] = []map[string]any{{"required": []string{"names"}}, {"required": []string{"name"}}}
	}
	return schema, true
}

func OutputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	objectProp := toolcontract.OpenObject
	props := map[string]any{}

	switch name {
	case ToolManage:
		props["action"] = stringProp("Completed dynamic MCP management action.")
		props["servers"] = arrayProp("Registered dynamic MCP server summaries.")
		props["server"] = objectProp("Dynamic MCP server summary.")
		props["config"] = objectProp("Dynamic MCP server configuration containing only non-secret values and environment variable names.")
		props["tools"] = arrayProp("Discovered lightweight MCP tool summaries.")
		props["tool_count"] = intProp("Discovered tool count.")
		props["count"] = intProp("Registered server count.")
		props["name"] = stringProp("Dynamic MCP server name.")
		props["removed"] = boolProp("Whether the server or environment variable was removed.")
		props["key"] = stringProp("Environment variable name. Secret values are never returned.")
		props["configured"] = boolProp("Whether the environment variable has a non-empty configured value.")
		props["items"] = arrayProp("Environment variable names and configured status without values.")
	case ToolSearch:
		props["query"] = stringProp("Capability query used.")
		props["server"] = stringProp("Optional server filter used.")
		props["tools"] = arrayProp("Matching lightweight MCP tool summaries.")
		props["count"] = intProp("Matching dynamic tool count. Host-tool namespace misuse returns a terminal error instead of a success result.")
		props["catalogs"] = arrayProp("Visible server catalog metadata, including unknown/stale directories and the selected-service next action. No connection is started by an unscoped search.")
		props["complete"] = boolProp("All visible catalogs are current and matching results fit the response limit.")
		props["truncated"] = boolProp("Tool matches or server catalog summaries exceeded their response limit.")
		props["cache_only"] = boolProp("No remote service is initialized or refreshed by this unscoped search.")
		props["match_kind"] = stringProp("Match classification for the highest-ranked result, such as exact_name, identifier_coverage, or none.")
		props["route_kind"] = stringProp("Resolved namespace: host_builtin or dynamic_mcp.")
		props["resolved"] = boolProp("Whether the request has a deterministic route.")
		props["terminal"] = boolProp("Whether discovery must stop and the returned host route must be used.")
		props["builtin_match_count"] = intProp("Matching AgentDock host built-ins.")
		props["dynamic_match_count"] = intProp("Matching dynamic MCP tools.")
		props["matched_tool"] = stringProp("Canonical matched AgentDock built-in name.")
		props["matched_alias"] = stringProp("Recognized compatibility alias when one was used.")
		props["route_summary"] = stringProp("Compact activity title for the resolved route.")
		props["builtin_tools"] = arrayProp("Compact AgentDock host built-in route summaries without input or output schemas.")
		props["execution_performed"] = boolProp("False for a host handoff: discovery did not execute the tool.")
		props["next_action"] = stringProp("Next discovery action; never authorizes or automatically executes a business operation.")
		props["route_code"] = stringProp("Diagnostic classification for a resolved host route, including repeated wrong-namespace attempts.")
		props["route_status"] = stringProp("Terminal host-route error status.")
		props["message"] = stringProp("Caller action and prohibited fallback searches for a resolved host route.")
		props["filesystem_lookup_required"] = boolProp("Always false for a resolved AgentDock host tool.")
		props["dynamic_discovery_required"] = boolProp("Always false for a resolved AgentDock host tool.")
		props["recovery_source"] = stringProp("Trusted source of the host schema, normally caller_tools_list.")
	case ToolList:
		props = catalogSchemaProperties()
	case ToolInspect:
		props["tools"] = arrayProp("Selected tool schemas or compact same-revision references. UI resources, icons and embedded image data are omitted from metadata.")
		props["errors"] = arrayProp("Explicit per-selector errors; successful services remain present.")
		props["catalogs"] = arrayProp("Service generations used by this batch.")
		props["count"] = intProp("Successfully resolved selected tool count, including compact same-revision references.")
		props["total"] = intProp("Total successfully resolved selected tools in this untruncated response.")
		props["schema_full_count"] = intProp("Selected tools returned with complete schemas in this response.")
		props["schema_reference_count"] = intProp("Selected tools returned as compact references to schemas already delivered in this conversation.")
		props["complete"] = boolProp("All selectors resolved to dynamic schemas without errors. Mixed host built-in selectors are reported as per-selector routing errors.")
		props["route_kind"] = stringProp("Resolved namespace: host_builtin, dynamic_mcp, or mixed.")
		props["resolved"] = boolProp("Whether the selector batch has a deterministic terminal route.")
		props["terminal"] = boolProp("Whether dynamic inspection must stop and host tools must be used.")
		props["builtin_match_count"] = intProp("Selectors resolved as AgentDock host built-ins.")
		props["dynamic_match_count"] = intProp("Successfully returned dynamic MCP schemas.")
		props["route_summary"] = stringProp("Compact activity title for a terminal host route.")
		props["builtin_tools"] = arrayProp("Compact AgentDock host built-in route summaries without schemas.")
		props["execution_performed"] = boolProp("False for a host handoff: no business action ran.")
		props["next_action"] = stringProp("Caller-side host handoff after completed inspection.")
		props["route_code"] = stringProp("Diagnostic classification for a resolved pure host route.")
		props["route_status"] = stringProp("Terminal host-route error status.")
		props["message"] = stringProp("Caller action and prohibited fallback searches for a resolved host route.")
		props["filesystem_lookup_required"] = boolProp("Always false for a resolved AgentDock host tool.")
		props["dynamic_discovery_required"] = boolProp("Always false for a resolved AgentDock host tool.")
		props["recovery_source"] = stringProp("Trusted source of the host schema, normally caller_tools_list.")
		props["metadata"] = objectProp("Decision-relevant upstream metadata after removing icons, UI resources and embedded image data.")
		props["name"] = stringProp("Qualified MCP tool name.")
		props["server"] = stringProp("Dynamic MCP server name.")
		props["tool_name"] = stringProp("Upstream MCP tool name.")
		props["catalog_revision"] = stringProp("Revision of the inspected catalog.")
		props["server_version"] = stringProp("Version reported by initialize, empty when unknown.")
		props["delivery"] = stringProp("Legacy schema delivery marker retained for direct Runtime callers.")
		props["schema_delivery"] = stringProp("full, summary, or reference; remains available after the host execution delivery envelope is added.")
		props["schema_omitted"] = boolProp("True only for a compact same-revision schema reference.")
		props["refresh_tool"] = stringProp("Use mcp_tool_inspect with force_full=true when the referenced schema is absent from current model context.")
		props["title"] = stringProp("Tool title.")
		props["description"] = stringProp("Tool description.")
		props["input_schema"] = objectProp("Complete upstream MCP tool input schema.")
		props["output_schema"] = objectProp("Optional upstream MCP tool output schema.")
		props["annotations"] = objectProp("Optional upstream MCP tool annotations.")
	case ToolCall:
		props["name"] = stringProp("Qualified MCP tool name.")
		props["call_id"] = stringProp("Activity call ID used by host result continuation and raw diagnostics.")
		props["result"] = objectProp("Normalized upstream result page. structuredContent is preferred and equivalent JSON text is removed.")
		props["executed"] = boolProp("Whether the upstream tools/call request was dispatched.")
		props["content_kinds"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		props["host_cursor"] = stringProp("Opaque AgentDock host cursor for mcp_result_continue. This is separate from any domain cursor inside result.")
		props["host_page"] = objectProp("Host projection page metadata and high-water-safe continuation state.")
		props["continuation_available"] = boolProp("Whether another host projection page is available.")
		props["continuation_required"] = boolProp("Always false; continue only when more of this result is needed.")
		props["raw_result_ref"] = stringProp("Activity reference to the complete original upstream response when retained.")
		props["mcp_catalog"] = toolcontract.OutputObject(catalogSchemaProperties())
	case ToolResultContinue:
		props["call_id"] = stringProp("Original mcp_tool_call activity call ID.")
		props["name"] = stringProp("Qualified MCP tool name.")
		props["result"] = objectProp("Next normalized host projection page.")
		props["host_cursor"] = stringProp("Opaque cursor for the next page.")
		props["host_page"] = objectProp("Host projection page metadata.")
		props["continuation_available"] = boolProp("Whether another host projection page is available.")
		props["continuation_required"] = boolProp("Always false.")
		props["replayed"] = boolProp("True for an idempotent repeated cursor receipt.")
		props["content_returned"] = boolProp("False when a repeated cursor returns only an idempotent receipt.")
		props["resume_cursor"] = stringProp("Current high-water cursor when no content was returned.")
	case ToolResultRaw:
		props["call_id"] = stringProp("Original mcp_tool_call activity call ID.")
		props["offset"] = intProp("Returned UTF-8 byte offset.")
		props["next_offset"] = intProp("Next explicit diagnostic offset.")
		props["limit"] = intProp("Requested diagnostic byte limit.")
		props["data"] = stringProp("Raw upstream response JSON fragment.")
		props["complete"] = boolProp("Whether the complete raw response has been returned.")
		props["raw_result_ref"] = stringProp("Activity reference to the retained complete response.")
	default:
		return nil, false
	}
	if name == ToolCall || name == ToolResultContinue || name == ToolResultRaw {
		props["result_call_id"] = stringProp("Original MCP result identity; call_id identifies this current host invocation.")
		props["continue_result"] = objectProp("Exact host continuation tool and arguments for this retained result.")
	}
	if name == ToolSearch || name == ToolList || name == ToolInspect {
		toolcontract.StructuredPagingOutput(props)
	}
	return toolcontract.OutputObject(props), true
}

func catalogSchemaProperties() map[string]any {
	return map[string]any{
		"server": toolcontract.String("Dynamic MCP server."), "catalog_revision": toolcontract.String("Referenced complete catalog generation."),
		"complete":         toolcontract.Boolean("All tools in the referenced visible catalog are included; false if unavailable."),
		"stale":            toolcontract.Boolean("The last complete directory is stale and has not been represented as current."),
		"total":            toolcontract.Integer("Total tools in the referenced catalog."),
		"error":            toolcontract.String("Catalog attachment problem; never changes the business outcome."),
		"delivery":         toolcontract.String("Legacy catalog delivery marker retained for direct Runtime callers."),
		"catalog_delivery": toolcontract.String("full, summary, or reference; remains available after the host execution delivery envelope is added."),
		"tools_omitted":    toolcontract.Boolean("True when catalog entries are omitted; delivery distinguishes a summary from a delivered reference."),
		"refresh_tool":     toolcontract.String("Use mcp_tool_list when the referenced complete catalog is absent from the current context."),
		"tools":            map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "description"}, "properties": map[string]any{"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "tool_name": map[string]any{"type": "string"}, "qualified_name": map[string]any{"type": "string"}, "server": map[string]any{"type": "string"}, "server_fingerprint": map[string]any{"type": "string"}, "catalog_revision": map[string]any{"type": "string"}, "schema_digest": map[string]any{"type": "string"}, "annotations_digest": map[string]any{"type": "string"}, "source_type": map[string]any{"type": "string"}, "plugin_name": map[string]any{"type": "string"}, "annotations": toolcontract.OpenObject("Standard MCP tool annotations.")}}},
	}
}
