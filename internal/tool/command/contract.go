package command

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolExecCommand    = "exec_command"
	ToolSessionObserve = "session_observe"
	ToolSessionAct     = "session_act"
)

func InputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	boolProp := toolcontract.Boolean
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string

	switch name {
	case ToolExecCommand:
		toolcontract.ActivityProperties(props)
		toolcontract.TargetProperties(props)
		props["backend"] = map[string]any{"type": "string", "enum": []string{"default", "termux_host", "android_shizuku"}, "description": "Explicit execution backend; default preserves current behavior. Android requires a ready paired provider, an absolute backend workdir and no host Skill/runtime context. Never retries on another backend."}
		props["cmd"] = stringProp("Command to run.")
		props["workdir"] = stringProp(WorkdirDescription())
		AddRuntimeProperties(props)
		props["skill"] = stringProp("Optional installed Skill name or exact scoped Skill reference. Explicit workdir keeps its normal priority.")
		props["skill_env"] = stringProp("Optional isolated Skill environment without changing the default command directory.")
		props["skill_ref"] = stringProp("Optional exact Skill reference returned by AgentDock context. When workdir is omitted, the command runs from that resolved Skill root and keeps its content read-locked for the command lifetime. Managed Skills also receive a private persistent SKILL_DATA_DIR.")
		props["env"] = map[string]any{"type": "object", "description": "Explicit command environment values. These override the selected managed Skill environment, except reserved runtime values such as SKILL_DATA_DIR.", "additionalProperties": map[string]any{"type": "string"}}
		props["timeout_ms"] = boundedIntProp("Timeout in milliseconds. Must be positive and is capped at 86400000.", 1, 86400000)
		props["execution_mode"] = map[string]any{"type": "string", "description": "Execution mode. Defaults to auto: wait up to yield_time_ms, then return a running session. Use sync when the next action requires final exit status. async returns a session immediately.", "enum": []string{"auto", "sync", "async"}}
		props["yield_time_ms"] = boundedIntProp("Foreground wait threshold for execution_mode=auto. Defaults to 10000 and is capped at 30000 milliseconds.", 0, 30000)
		props["max_output_bytes"] = boundedIntProp("Maximum output bytes. Defaults to 65536 and is capped at 4194304.", 1, MaxOutputBytes)
		props["stdin"] = stringProp("Initial stdin.")
		props["tty"] = boolProp("Keep stdin open.")
		required = []string{"cmd"}
	case ToolSessionObserve:
		props["action"] = map[string]any{
			"type":        "string",
			"description": "Command-session action: status for a known running session; list only for session recovery.",
			"enum":        []string{"list", "status"},
		}
		props["session_id"] = map[string]any{
			"type":        "string",
			"minLength":   1,
			"description": "Exact session id from exec_command; required for status.",
		}
		props["max_output_bytes"] = boundedIntProp("Output byte cap for status; accepted without effect for list compatibility.", 1, MaxOutputBytes)
		props["wait_ms"] = boundedIntProp("Status wait in milliseconds. Omit for adaptive waits; 0 reads immediately. Invalid for list.", 0, MaxSessionObserveWaitMS)
		props["offset"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"stdout", "stderr"}, "properties": map[string]any{
			"stdout": map[string]any{"type": "integer", "minimum": 0}, "stderr": map[string]any{"type": "integer", "minimum": 0},
		}, "description": "Exact next_offset returned by the previous observation; UTF-8 byte positions independent of other readers."}
		return sessionObserveInputSchema(props), true
	case ToolSessionAct:
		props["action"] = map[string]any{"type": "string", "description": "Mutating session action.", "enum": []string{"write", "kill", "kill_all"}}
		props["session_id"] = stringProp("Session id returned by exec_command, required for write/kill.")
		props["chars"] = stringProp("Characters to write when action=write.")
		props["max_output_bytes"] = boundedIntProp("Maximum output bytes. Defaults to 65536 and is capped at 4194304.", 1, MaxOutputBytes)
	default:
		return nil, false
	}
	return toolcontract.InputObject(props, required...), true
}

func sessionObserveInputSchema(properties map[string]any) map[string]any {
	schema := toolcontract.InputObject(properties, "action")
	schema["oneOf"] = []map[string]any{
		{
			"type": "object",
			"properties": map[string]any{
				"action":           map[string]any{"const": "list"},
				"max_output_bytes": properties["max_output_bytes"],
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
		{
			"type": "object",
			"properties": map[string]any{
				"action":           map[string]any{"const": "status"},
				"session_id":       properties["session_id"],
				"max_output_bytes": properties["max_output_bytes"],
				"wait_ms":          properties["wait_ms"],
				"offset":           properties["offset"],
			},
			"required":             []string{"action", "session_id"},
			"additionalProperties": false,
		},
	}
	return schema
}

func OutputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	props := map[string]any{
		"task_id":                     stringProp("Persisted task binding inherited by subsequent session operations."),
		"thread_id":                   stringProp("Execution thread fixed when the command was started."),
		"step_id":                     stringProp("Step associated with the original command."),
		"workspace_id":                stringProp("Resolved workspace for the original command."),
		"activity_warning":            stringProp("Command execution state is valid but the activity journal is incomplete."),
		"sessions":                    arrayProp("Command session summaries returned by list or bulk session actions."),
		"count":                       intProp("Command session count when a list or bulk action returns multiple sessions."),
		"session_id":                  stringProp("Command session id."),
		"status":                      stringProp("Process status: running, exited, timeout, cancelled, lost or outcome_unknown."),
		"operation_status":            stringProp("Dispatch/observation status, independent of the command exit code."),
		"changed":                     boolProp("Whether this observation contains new output or a terminal state."),
		"waited_ms":                   intProp("Actual bounded wait for this observation."),
		"unchanged_observation_count": intProp("Consecutive unchanged observations for this authenticated scope and cursor."),
		"already_delivered":           boolProp("The terminal fact was already delivered or explicitly consumed."),
		"output_source":               stringProp("Retained command output reference."),
		"runtime":                     stringProp("Command runtime when reported by the host, such as windows or wsl."),
		"wsl_distribution":            stringProp("WSL distribution selected for the command when explicitly configured."),
		"workdir":                     stringProp("Logical command working directory in the selected runtime."),
		"stdout":                      stringProp("Captured stdout segment."),
		"stderr":                      stringProp("Captured stderr segment."),
		"stdin_error":                 stringProp("Initial remote stdin could not be acknowledged; preserve and observe the original session instead of replaying the command."),
		"outcome_unknown":             boolProp("The remote outcome is unconfirmed; do not replay the original command."),
		"command_ok":                  boolProp("Whether a completed command exited successfully. Omitted while the command is still running."),
		"outcome":                     stringProp("Command outcome: running, command_succeeded, command_failed, timed_out, or unknown; distinct from successful tool transport."),
		"observation_shared":          boolProp("This response shares an identical in-flight observation; the session cursor advanced only once."),
		"command_error":               stringProp("Command process error when execution did not succeed."),
		"exit_code":                   intProp("Process exit code, when available."),
		"elapsed_ms":                  intProp("Session elapsed milliseconds."),
		"timed_out":                   boolProp("Whether the command timed out."),
		"source_complete":             boolProp("Whether no produced stdout or stderr bytes have been discarded from the retained session buffers."),
		"displayed_bytes":             intProp("UTF-8 bytes delivered in stdout and stderr in this response."),
		"total_output_bytes":          intProp("Total stdout and stderr bytes produced by the command so far."),
		"retained_bytes":              intProp("Current stdout and stderr bytes retained in the bounded session buffers."),
		"dropped_before_bytes":        intProp("Produced stdout and stderr bytes discarded from the front of the bounded buffers."),
		"offset":                      toolcontract.OpenObject("Absolute UTF-8 byte offsets where this stdout and stderr page starts."),
		"next_offset":                 toolcontract.OpenObject("Absolute UTF-8 byte offsets for the next stdout and stderr observation."),
		"offset_unit":                 stringProp("Offset unit, always utf8_byte."),
		"stdout_output_bytes":         intProp("Stdout bytes delivered in this response; omitted when zero."),
		"stderr_output_bytes":         intProp("Stderr bytes delivered in this response; omitted when zero."),
		"stdout_total_bytes":          intProp("Total stdout bytes produced so far; omitted when zero."),
		"stderr_total_bytes":          intProp("Total stderr bytes produced so far; omitted when zero."),
		"stdout_dropped_bytes":        intProp("Stdout prefix bytes no longer retained; omitted when zero."),
		"stderr_dropped_bytes":        intProp("Stderr prefix bytes no longer retained; omitted when zero."),
		"stdout_omitted_bytes":        intProp("Retained stdout bytes not delivered in this page; omitted when zero."),
		"stderr_omitted_bytes":        intProp("Retained stderr bytes not delivered in this page; omitted when zero."),
		"stdout_output_lines":         intProp("Stdout logical lines delivered in this response; omitted when zero."),
		"stderr_output_lines":         intProp("Stderr logical lines delivered in this response; omitted when zero."),
		"stdout_truncated":            boolProp("Whether retained stdout remains after this page; omitted when false."),
		"stderr_truncated":            boolProp("Whether retained stderr remains after this page; omitted when false."),
	}
	switch name {
	case ToolExecCommand:
		props["session_reason"] = stringProp("Why exec_command returned a session instead of a completed result.")
	case ToolSessionObserve, ToolSessionAct:
	default:
		return nil, false
	}
	props["observe_after_ms"] = intProp("Recommended wait_ms for the next session_observe status call. The server returns early when the process exits.")
	return toolcontract.OutputObject(props), true
}
