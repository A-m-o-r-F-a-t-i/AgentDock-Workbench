package app

func contextReuseAction() map[string]any {
	return map[string]any{
		"action": "reuse_context", "required": false, "execution_performed": false,
		"reason_code": "context_current",
		"text":        "Context is current. Continue the task and do not call agentdock_context again unless a later tool reports context loss or revision change, or another workspace is required.",
	}
}
