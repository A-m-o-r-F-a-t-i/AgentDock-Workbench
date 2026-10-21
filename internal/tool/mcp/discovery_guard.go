package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	builtinDiscoveryCapacity = 1024
	builtinDiscoveryWindow   = 5 * time.Minute
)

var errBuiltinDiscovery = errors.New("AgentDock built-in used in dynamic discovery")

// IsBuiltinDiscoveryError accepts only errors created locally by this service.
// An upstream code or result field cannot forge a caller-host handoff.
func IsBuiltinDiscoveryError(err error) bool { return errors.Is(err, errBuiltinDiscovery) }

type builtinDiscoveryKey struct {
	owner, conversation, tool string
}

type builtinDiscoveryEntry struct {
	firstCall   string
	started     time.Time
	repeatCount int
}

type builtinDiscoveryRoutes struct {
	mu      sync.Mutex
	entries map[builtinDiscoveryKey]builtinDiscoveryEntry
	now     func() time.Time
}

// remember records only trusted ingress identity. Unattributed calls never share
// a bucket, and normal list/inspect/call operations never consult this state.
func (g *builtinDiscoveryRoutes) remember(ctx context.Context, names []string) (bool, string, int) {
	binding := activity.FromContext(ctx)
	if binding.ConversationID == "" {
		return false, "", 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	if g.entries == nil {
		g.entries = make(map[builtinDiscoveryKey]builtinDiscoveryEntry)
	}
	repeated, previous, repeatCount := false, "", 0
	for _, name := range names {
		key := builtinDiscoveryKey{owner: binding.SourceOwnerKey, conversation: binding.ConversationID, tool: name}
		if entry, ok := g.entries[key]; ok && now.Sub(entry.started) >= 0 && now.Sub(entry.started) < builtinDiscoveryWindow {
			repeated = true
			entry.repeatCount++
			g.entries[key] = entry
			if entry.repeatCount > repeatCount {
				repeatCount = entry.repeatCount
			}
			if previous == "" {
				previous = entry.firstCall
			}
			continue
		}
		delete(g.entries, key)
		if len(g.entries) >= builtinDiscoveryCapacity {
			var oldestKey builtinDiscoveryKey
			var oldest time.Time
			for candidate, entry := range g.entries {
				age := now.Sub(entry.started)
				if age < 0 || age >= builtinDiscoveryWindow {
					delete(g.entries, candidate)
					continue
				}
				if oldest.IsZero() || entry.started.Before(oldest) {
					oldestKey, oldest = candidate, entry.started
				}
			}
			if len(g.entries) >= builtinDiscoveryCapacity {
				delete(g.entries, oldestKey)
			}
		}
		g.entries[key] = builtinDiscoveryEntry{firstCall: binding.CallID, started: now}
	}
	return repeated, previous, repeatCount
}

// resolveBuiltinDiscovery completes the namespace lookup without executing a host
// tool. A wrong-namespace query is a successful routing result, even when
// repeated. Host tools/list visibility belongs to the caller, not this service.
func (s *Service) resolveBuiltinDiscovery(ctx context.Context, builtins []BuiltinToolSummary) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	builtins = sortedBuiltinTools(builtins)
	names := builtinToolNames(builtins)
	repeated, previous, repeatCount := s.discoveryRoutes.remember(ctx, names)
	code := "HOST_BUILTIN_NOT_DYNAMIC_MCP"
	status := "resolved"
	if repeated {
		code = "ROUTING_LOOP_BLOCKED"
		status = "already_routed"
	}
	label := strings.Join(names, ", ")
	result := Result{
		"route_kind": routeKindHostBuiltin, "resolved": true, "terminal": true,
		"route_status": status, "resolution_status": "resolved", "route_code": code,
		"execution_performed": false, "retry_dynamic_search": false,
		"builtin_tools": builtins, "required_host_tools": names,
		"next_action": "call_host_tool", "repeated": repeated,
		"scope": ScopeAgentDockBuiltin, "origin": ScopeAgentDockBuiltin,
		"requested_scope": ScopeAgentDockMCP, "invoke_via": InvokeDirect,
		"route_summary":          "定位宿主工具 · " + label,
		"wrong_namespace_search": true, "wrong_namespace_repeat_count": repeatCount,
		"host_schema_state": "caller_managed", "direct_schema_state": "caller_managed",
		"host_directory_visibility": "unknown", "host_discovery_method": "tools/list",
		"filesystem_lookup_required": false, "dynamic_discovery_required": false,
		"recovery_source":   "caller_tools_list",
		"already_delivered": repeated,
	}
	action := map[string]any{
		"category": "protocol", "action": "select_host_tool",
		"required": true, "execution_performed": false, "reason_code": "host_tool_handoff",
		"tools": names,
		"text":  "Use the caller's current tools/list to call this AgentDock built-in directly. If absent, refresh the host tool directory. Do not search nested MCP again.",
	}
	if len(names) == 1 {
		result["required_host_tool"] = names[0]
		action["action"] = "call_host_tool"
		action["tool"] = names[0]
	}
	result["required_action"] = action
	if previous != "" {
		result["previous_call_id"] = previous
	}
	if repeated {
		delete(result, "builtin_tools")
		result["route_reference"] = previous
	}
	return result, nil
}
