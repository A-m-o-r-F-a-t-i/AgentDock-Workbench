package app

import "github.com/uvwt/agentdock-protocol/mcpcontract"

// workbenchCanonicalOutputSchema keeps the Workbench tool contract stable while
// newer protocol packages provide Bridge pairing and capability negotiation.
func workbenchCanonicalOutputSchema(name string) (map[string]any, bool) {
	switch name {
	case mcpcontract.ToolAgentDockContext:
		return workbenchAgentDockContextOutputSchema(), true
	case mcpcontract.ToolWorkspaceContext:
		return workbenchWorkspaceContextOutputSchema(), true
	}
	schema, ok := mcpcontract.OutputSchema(name)
	if !ok {
		return nil, false
	}
	if name == mcpcontract.ToolRecallWrite {
		schema = cloneJSONSchemaMap(schema)
		properties, _ := schema["properties"].(map[string]any)
		properties["warnings"] = workbenchArrayObjects("Review warnings before writing.")
	}
	return schema, true
}

func workbenchStringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func workbenchIntegerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func workbenchBooleanProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func workbenchEnumProperty(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func workbenchStrictObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

func workbenchArrayObjects(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "object", "additionalProperties": true},
	}
}

func workbenchWorkspaceContextOutputSchema() map[string]any {
	instruction := workbenchStrictObject(map[string]any{
		"scope":        workbenchEnumProperty("Instruction scope.", "global", "workspace"),
		"path":         workbenchStringProperty("Host path to the AGENTS.md candidate."),
		"status":       workbenchEnumProperty("Load status.", "loaded", "not_found", "empty", "duplicate", "skipped", "error"),
		"content":      workbenchStringProperty("Complete instruction text when status=loaded."),
		"sha256":       workbenchStringProperty("SHA-256 of the complete source bytes when status=loaded."),
		"size_bytes":   workbenchIntegerProperty("Source size in bytes when known."),
		"reason":       workbenchStringProperty("Machine-readable reason when the file was skipped or could not be loaded."),
		"duplicate_of": workbenchStringProperty("Earlier physical file path when status=duplicate."),
	}, "scope", "path", "status")
	skill := workbenchStrictObject(map[string]any{
		"name":        workbenchStringProperty("Workspace Skill name."),
		"description": workbenchStringProperty("Short workspace Skill capability description."),
		"file":        workbenchStringProperty("Exact skill:// resource URI for the workspace-local SKILL.md."),
		"skill_ref":   workbenchStringProperty("Exact host-issued Skill reference for runtime binding."),
		"source_type": workbenchEnumProperty("Skill source type.", "workspace"),
		"source_id":   workbenchStringProperty("Opaque workspace source identity issued by the host."),
	}, "name", "description", "file", "skill_ref", "source_type", "source_id")
	warning := workbenchStrictObject(map[string]any{
		"source":  workbenchStringProperty("Workspace context section identifier."),
		"message": workbenchStringProperty("Safe warning message."),
	}, "source", "message")
	return workbenchStrictObject(map[string]any{
		"workdir":          workbenchStringProperty("Resolved workspace directory inspected for this request."),
		"workspace_root":   workbenchStringProperty("Workspace inheritance root used for AGENTS.md and .agents/skills discovery."),
		"instructions":     map[string]any{"type": "array", "items": instruction},
		"workspace_skills": map[string]any{"type": "array", "items": skill},
		"warnings":         map[string]any{"type": "array", "items": warning},
	}, "workdir", "workspace_root", "instructions", "workspace_skills", "warnings")
}

func workbenchAgentDockContextOutputSchema() map[string]any {
	props := workbenchLocalContextProperties(true)
	props["runtime"] = workbenchAgentDockRuntimeSchema()
	return workbenchStrictObject(props, "runtime", "skills", "dynamic_mcp", "workflow_templates", "rules")
}

func workbenchLocalContextProperties(includeShared bool) map[string]any {
	skill := map[string]any{
		"type": "object", "properties": map[string]any{
			"name": workbenchStringProperty("Skill name."), "description": workbenchStringProperty("Short capability description."),
			"file":           workbenchStringProperty("Exact skill:// resource URI for SKILL.md."),
			"skill_ref":      workbenchStringProperty("Exact host-issued Skill reference for runtime binding."),
			"source_type":    workbenchEnumProperty("Skill source type.", "managed", "plugin"),
			"source_id":      workbenchStringProperty("Opaque source identity issued by the host."),
			"plugin_name":    workbenchStringProperty("Owning Plugin name when source_type is plugin."),
			"content_digest": workbenchStringProperty("Current package content digest when the source has immutable managed content."),
		}, "required": []string{"name", "description", "file", "skill_ref", "source_type", "source_id"}, "additionalProperties": false,
	}
	commonSkill := map[string]any{
		"type": "object", "properties": map[string]any{
			"name": workbenchStringProperty("Common Skill name."), "description": workbenchStringProperty("Short capability description."),
			"file":           workbenchStringProperty("Exact skill:// resource URI for the common SKILL.md."),
			"skill_ref":      workbenchStringProperty("Exact host-issued Skill reference for runtime binding."),
			"source_type":    workbenchEnumProperty("Skill source type.", "shared"),
			"source_id":      workbenchStringProperty("Opaque shared source identity issued by the host."),
			"content_digest": workbenchStringProperty("Optional content digest when the host can provide one."),
		}, "required": []string{"name", "description", "file", "skill_ref", "source_type", "source_id"}, "additionalProperties": false,
	}
	commonSkills := workbenchStrictObject(map[string]any{
		"root":      workbenchStringProperty("Common Agent Skills root path."),
		"total":     workbenchIntegerProperty("Total valid common Skills discovered before truncation."),
		"truncated": workbenchBooleanProperty("Whether the common Skill index was truncated."),
		"items":     map[string]any{"type": "array", "items": commonSkill},
	}, "root", "total", "truncated", "items")
	commonSkills["description"] = "Lower-priority common Agent Skill capability index; installed AgentDock Skills take precedence on conflicts."
	dynamicItem := workbenchDynamicMCPItemSchema()
	indexItem := workbenchContextItemSchema(false)
	warning := map[string]any{
		"type": "object", "properties": map[string]any{
			"source": workbenchStringProperty("Context section identifier."), "message": workbenchStringProperty("Safe warning message."),
		}, "required": []string{"source", "message"}, "additionalProperties": false,
	}
	props := map[string]any{
		"skills":        map[string]any{"type": "array", "description": "Installed document Skill capability index.", "items": skill},
		"common_skills": commonSkills,
		"dynamic_mcp":   map[string]any{"type": "array", "description": "Enabled dynamic MCP server capability index.", "items": dynamicItem},
		"acp": workbenchStrictObject(map[string]any{
			"enabled": workbenchBooleanProperty("Whether ACP is enabled."), "agent": workbenchStringProperty("Configured ACP agent name."), "description": workbenchStringProperty("Short ACP usage orientation."),
		}, "enabled", "agent", "description"),
		"rules":    map[string]any{"type": "array", "description": "Operational rules for using this AgentDock runtime.", "items": map[string]any{"type": "string"}},
		"warnings": map[string]any{"type": "array", "description": "Best-effort context sections that could not be loaded.", "items": warning},
	}
	if includeShared {
		props["workflow_templates"] = map[string]any{"type": "array", "description": "Active NexusDock Workflow template index; empty when Nexus is unavailable.", "items": indexItem}
		props["recall"] = workbenchStrictObject(map[string]any{
			"enabled": workbenchBooleanProperty("Whether NexusDock Recall context is configured."),
			"items":   map[string]any{"type": "array", "items": indexItem},
		}, "enabled", "items")
	}
	return props
}

func workbenchAgentDockRuntimeSchema() map[string]any {
	return workbenchStrictObject(map[string]any{
		"version":               workbenchStringProperty("AgentDock runtime version."),
		"os":                    workbenchStringProperty("Host operating system."),
		"arch":                  workbenchStringProperty("Host architecture."),
		"agentdock_home":        workbenchStringProperty("AgentDock state and configuration directory."),
		"agentdock_default_dir": workbenchStringProperty("AgentDock default working directory."),
		"default_cwd":           workbenchStringProperty("Default cwd relative to the AgentDock working directory when applicable."),
		"path_model":            workbenchStringProperty("Path resolution model used by host tools."),
	}, "version", "os", "arch", "agentdock_home", "agentdock_default_dir", "default_cwd", "path_model")
}

func workbenchContextItemSchema(requireDescription bool) map[string]any {
	required := []string{"name"}
	if requireDescription {
		required = append(required, "description")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        workbenchStringProperty("Capability name."),
			"description": workbenchStringProperty("Short capability description."),
		},
		"required":             required,
		"additionalProperties": false,
	}
}

func workbenchDynamicMCPItemSchema() map[string]any {
	schema := workbenchContextItemSchema(true)
	properties := schema["properties"].(map[string]any)
	properties["display_name"] = workbenchStringProperty("Human-facing server component name when it differs from the stable runtime name.")
	properties["source_type"] = workbenchEnumProperty("MCP ownership source type.", "standalone", "plugin")
	properties["plugin_name"] = workbenchStringProperty("Owning Plugin name when source_type is plugin.")
	properties["status"] = map[string]any{
		"type": "string", "description": "Runtime health state: idle, ready, or error.",
		"enum": []string{"idle", "ready", "error"},
	}
	properties["tool_count"] = workbenchIntegerProperty("Discovered tool count from the latest successful refresh.")
	properties["last_error_code"] = workbenchStringProperty("Safe machine-readable code for the latest refresh error.")
	return schema
}
