package app

import "encoding/json"

// Decode only the server-marked text-only envelope. Nested third-party text
// cannot publish host delivery acknowledgements by naming a reserved field.
func deliveredModelPayload(value any) map[string]any {
	envelope := modelObject(value)
	if structured := modelObject(envelope["structuredContent"]); structured != nil {
		return structured
	}
	if modelObject(envelope["_meta"])["agentdock/output_format"] == "text_only" {
		encoded, err := json.Marshal(envelope["content"])
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err == nil && json.Unmarshal(encoded, &blocks) == nil && len(blocks) > 0 && blocks[0].Type == "text" {
			var structured map[string]any
			if json.Unmarshal([]byte(blocks[0].Text), &structured) == nil {
				return structured
			}
		}
	}
	return envelope
}
