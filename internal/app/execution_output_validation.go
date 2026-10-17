package app

import (
	"errors"
	"maps"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

func isOutputContractFailure(code string) bool {
	switch code {
	case "OUTPUT_SCHEMA_MISMATCH", "MCP_OUTPUT_SCHEMA_MISMATCH", "OUTPUT_VALIDATOR_UNAVAILABLE":
		return true
	default:
		return false
	}
}

func cloneOutputFailure(failure *ToolError) *ToolError {
	if failure == nil {
		return nil
	}
	copy := *failure
	copy.Details = maps.Clone(failure.Details)
	return &copy
}

func outputFailureFromError(err error) *ToolError {
	var failure *ToolError
	if errors.As(err, &failure) && isOutputContractFailure(failure.Code) {
		return cloneOutputFailure(failure)
	}
	return nil
}

// File windows are retained inside their handler before the final output can
// be validated. Invalidate only the snapshot returned by this exact scope.
func (r *Runtime) retainFileOutputFailure(p *preparedExecution, result Result) {
	if p.outputFailure == nil || p.spec.Name != "read_file" {
		return
	}
	id := stringArg(result, "snapshot_id")
	if id == "" {
		id = stringArg(modelObject(result["file_delivery"]), "snapshot_id")
	}
	r.structuredOutputMu.Lock()
	defer r.structuredOutputMu.Unlock()
	if stored := r.structuredSnapshots[id]; stored != nil && stored.Tool == "read_file" && sameContextScope(stored.Binding, p.state.binding) {
		stored.OutputFailure = cloneOutputFailure(p.outputFailure)
		stored.Delivered = false
	}
}

// validateExecutionResult runs before capture and projection so previews cannot
// hide missing business fields. The caller retains the original result on error;
// a contract failure never authorizes replaying an operation that already ran.
func (r *Runtime) validateExecutionResult(p *preparedExecution, result Result) error {
	validator := r.toolOutputValidators[p.spec.Name]
	code := "OUTPUT_VALIDATOR_UNAVAILABLE"
	message := "The tool's canonical output validator is unavailable."
	reason := ""
	if validator != nil {
		if err := validator.Validate(executionResultEnvelope(result, p)); err != nil {
			code = "OUTPUT_SCHEMA_MISMATCH"
			message = "The tool returned an invalid canonical result. Inspect its retained output and side effects before another call."
			reason = r.executionRedactor(p.args).Text(toolcontract.CompactValidationError(err), 1024)
		} else {
			return nil
		}
	}
	effect := stringArg(result, "effect_state")
	switch effect {
	case "none", "complete", "partial", "unknown":
	default:
		effect = "unknown"
	}
	performed := p.state.executed
	if !performed || p.spec.Annotations != nil && p.spec.Annotations.ReadOnlyHint {
		effect = "none"
	}
	return toolErrorDetails(code, message, "runtime", map[string]any{
		"tool": p.spec.Name, "reason": reason,
		"error_kind": "output_contract", "operation_status": "failed",
		"execution_performed": performed, "effect_state": effect, "retry_safe": false,
	})
}
