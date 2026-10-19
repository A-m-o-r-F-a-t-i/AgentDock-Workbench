package mcp

import (
	"context"
	"encoding/json"
	"maps"
)

func modelWireEnvelope(ctx context.Context, name string, logical map[string]any) (map[string]any, error) {
	logical = projectMCPModelEnvelope(name, logical)
	if responseTextMode(ctx, name) != responseTextOnly {
		return logical, nil
	}
	encoded, err := json.Marshal(logical["structuredContent"])
	if err != nil {
		return nil, err
	}
	out := maps.Clone(logical)
	blocks := []any{map[string]any{"type": "text", "text": string(encoded)}}
	for _, block := range envelopeBlocks(logical["content"]) {
		if asMap(block)["type"] != "text" {
			blocks = append(blocks, block)
		}
	}
	meta := maps.Clone(asMap(out["_meta"]))
	if meta == nil {
		meta = map[string]any{}
	}
	meta["agentdock/output_format"] = "text_only"
	out["_meta"], out["content"] = meta, blocks
	delete(out, "structuredContent")
	return out, nil
}

func modelWireChars(ctx context.Context, name string, logical map[string]any) (int, error) {
	wire, err := modelWireEnvelope(ctx, name, logical)
	if err != nil {
		return 0, err
	}
	return modelEnvelopeChars(wire)
}
