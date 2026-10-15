package client

import "errors"

func pluginRegistryFailure(err error) *Error {
	details := map[string]any{"stage": "plugin_registry", "cause": err.Error(), "last_verified": false}
	var diagnostic interface{ DiagnosticDetails() map[string]any }
	if errors.As(err, &diagnostic) {
		for key, value := range diagnostic.DiagnosticDetails() {
			if key == "stage" {
				details["cause_stage"] = value
			} else {
				details[key] = value
			}
		}
	}
	retryable, specified := details["retryable"].(bool)
	if !specified {
		retryable = true
	}
	return newError("MCP_PLUGIN_REGISTRY_READ_FAILED", "read plugin-owned MCP servers", retryable, details, err)
}
