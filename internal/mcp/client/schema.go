package client

import (
	"fmt"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

type toolInputValidator = toolcontract.InputValidator

func compileToolInputSchema(raw map[string]any) (*toolInputValidator, error) {
	return toolcontract.CompileInputSchema(raw)
}

// Validate the complete upstream structured content before it is retained and
// paged by the service. Error results have their own protocol shape and need not
// match a server's declared success schema. Keep raw output even on mismatch.
func validatePreparedOutput(prepared *PreparedMCPCall, result map[string]any) error {
	if prepared.outputValidator == nil {
		return nil
	}
	if _, failed := ResultFailure(result); failed {
		return nil
	}
	structured, ok := result["structuredContent"].(map[string]any)
	var err error
	if !ok {
		err = fmt.Errorf("structuredContent must be an object when outputSchema is declared")
	} else {
		err = prepared.outputValidator.Validate(structured)
	}
	if err == nil {
		return nil
	}
	effect := "unknown"
	if value, _ := structured["effect_state"].(string); value == "none" || value == "complete" || value == "partial" {
		effect = value
	}
	if prepared.tool.Annotations["readOnlyHint"] == true {
		effect = "none"
	}
	return newError("MCP_OUTPUT_SCHEMA_MISMATCH", "The MCP tool returned an invalid structured result. Inspect the retained result before another call.", false, map[string]any{
		"tool": prepared.QualifiedName, "reason": toolcontract.CompactValidationError(err),
		"executed": true, "execution_performed": true, "effect_state": effect,
		"retry_safe": false, "error_kind": "output_contract",
	}, err)
}

func validateToolArguments(tool Tool, arguments map[string]any) error {
	if arguments == nil {
		arguments = map[string]any{}
	}
	validator := tool.inputValidator
	if validator == nil {
		compiled, err := compileToolInputSchema(tool.InputSchema)
		if err != nil {
			return newError(
				"MCP_SCHEMA_INVALID",
				"upstream MCP tool returned an invalid input schema",
				false,
				map[string]any{"tool": tool.Name, "reason": err.Error()},
				err,
			)
		}
		validator = compiled
	}
	if err := validator.Validate(arguments); err != nil {
		return newError(
			"MCP_ARGUMENT_INVALID",
			"MCP tool arguments do not match the discovered input schema",
			false,
			map[string]any{"tool": tool.Name, "reason": toolcontract.CompactValidationError(err)},
			err,
		)
	}
	return nil
}
