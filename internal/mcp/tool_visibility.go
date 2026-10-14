package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

func (s *Server) filterBuiltinToolDirectory(ctx context.Context, result mcpsdk.Result) mcpsdk.Result {
	listed, ok := result.(*mcpsdk.ListToolsResult)
	if !ok || listed == nil || s == nil || s.runtime == nil {
		return result
	}
	copy := *listed
	copy.Tools = make([]*mcpsdk.Tool, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		if tool != nil && s.runtime.BuiltinToolVisible(ctx, tool.Name) {
			copy.Tools = append(copy.Tools, tool)
		}
	}
	return &copy
}

// Loading a conversation-scoped bundle changes only the per-request directory
// projection. Re-register one stable loader descriptor to emit a single global
// tools/list_changed notification; each client then receives its own filtered list.
func (s *Server) refreshBuiltinToolDirectory() {
	if s == nil || s.sdk == nil || s.runtime == nil {
		return
	}
	definition, ok := s.runtime.ToolDefinition(toolcapability.ToolLoad)
	if !ok {
		return
	}
	s.toolMu.Lock()
	s.registerTool(definition)
	if s.registeredTools == nil {
		s.registeredTools = map[string]struct{}{}
	}
	s.registeredTools[definition.Name] = struct{}{}
	s.toolMu.Unlock()
}
