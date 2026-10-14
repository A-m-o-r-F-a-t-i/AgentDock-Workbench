package mcp

import (
	"maps"

	"github.com/uvwt/agentdock/internal/commandcompletion"
)

// Only adapter-owned typed events can reach this function. Never discover
// notifications by scanning a third-party result or marker-like command output.
func appendCommandCompletions(original map[string]any, events []commandcompletion.Event) map[string]any {
	if len(events) == 0 {
		return original
	}
	envelope := maps.Clone(original)
	structured := maps.Clone(asMap(original["structuredContent"]))
	if structured == nil {
		structured = map[string]any{}
		if original["structuredContent"] != nil {
			structured["result"] = original["structuredContent"]
		}
	}
	guidance := maps.Clone(asMap(structured["agentdock_guidance"]))
	if guidance == nil {
		guidance = map[string]any{"source": "agentdock"}
	}
	guidance["command_completions"] = commandcompletion.Clone(events)
	structured["agentdock_guidance"] = guidance
	envelope["structuredContent"] = structured
	text := commandcompletion.Text(events)
	for _, block := range envelopeBlocks(original["content"]) {
		if asMap(block)["type"] == "text" && asMap(block)["text"] == text {
			return envelope
		}
	}
	return appendResponseBlocks(envelope, []string{text})
}
