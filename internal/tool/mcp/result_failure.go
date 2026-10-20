package mcp

import (
	"encoding/json"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

const (
	resultFailureBudget     = 1024
	resultFailurePageBudget = 4096
)

// Freeze the original business failure before any semantic or fragment paging.
// Decision fields and complete actions take precedence over display text. An
// oversized action is omitted as a whole; its original remains in the raw result.
func retainResultFailure(raw map[string]any) map[string]any {
	details, failed := mcpclient.ResultFailure(raw)
	if !failed {
		return nil
	}
	fields := []string{"code", "operation_status", "effect_state", "execution_performed", "executed", "retry_safe", "required_action", "outcome", "counts", "saved", "message"}
	omissionEnvelope, _ := json.Marshal(map[string]any{"omitted_fields": fields})
	limit := resultFailureBudget - len(omissionEnvelope)
	facts := map[string]any{}
	omitted := []any{}
	sources := []map[string]any{details, resultMap(details["error"]), raw, resultMap(raw["error"])}
	for _, key := range fields {
		var value any
		for _, source := range sources {
			if source[key] != nil {
				value = source[key]
				break
			}
		}
		if value == nil {
			continue
		}
		valid := false
		switch key {
		case "execution_performed", "executed", "retry_safe", "saved":
			_, valid = value.(bool)
		case "required_action", "counts":
			valid = resultMap(value) != nil
		default:
			_, valid = value.(string)
		}
		if valid {
			facts[key] = value
			encoded, err := json.Marshal(facts)
			valid = err == nil && len(encoded) <= limit
		}
		if !valid {
			delete(facts, key)
			omitted = append(omitted, key)
			continue
		}
		facts[key] = cloneResultValue(value)
	}
	if len(omitted) > 0 {
		facts["omitted_fields"] = omitted
	}
	return facts
}

func resultFailureProjectionBytes(failure map[string]any) int {
	if failure == nil {
		return 0
	}
	encoded, _ := json.Marshal(map[string]any{"upstream_failure": failure})
	return len(encoded)
}

func resultPayloadBudget(failureBytes int) int {
	budget := resultPageBudget
	if failureBytes > 0 {
		budget = min(budget, resultFailurePageBudget)
	}
	return budget - resultMetadataReserve - failureBytes
}

func withResultFailure(result Result, entry *storedMCPResult) Result {
	if entry.Failure != nil {
		result["upstream_failure"] = cloneResultMap(entry.Failure)
	}
	return result
}
