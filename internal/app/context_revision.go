package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const (
	contextDeliveryFull = "full"
	contextRevisionKey  = "context_revision"
)

var contextRevisionFields = []string{
	"workspace",
	"tasks",
	"instruction_files",
	"runtime",
	"skills",
	"common_skills",
	"plugins",
	"dynamic_mcp",
	"acp",
	"workflow_templates",
	"recall",
	"rules",
	"warnings",
	"android_executor",
	"workspace_id",
	"conversation_state",
}

// attachContextRevision fingerprints only stable context facts. Timing diagnostics,
// call correlation, permission details and whether this request changed the binding
// never invalidate a client-held business context snapshot.
func attachContextRevision(result Result) error {
	diagnostics, _ := result["context_diagnostics"].(map[string]any)
	if diagnostics["complete"] != true {
		return nil
	}
	stable := make(map[string]any, len(contextRevisionFields))
	for _, field := range contextRevisionFields {
		if value, exists := result[field]; exists {
			stable[field] = value
		}
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return fmt.Errorf("encode context revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	result[contextRevisionKey] = fmt.Sprintf("ctxrev_%x", digest)
	result["delivery"] = contextDeliveryFull
	result["unchanged"] = false
	return nil
}
