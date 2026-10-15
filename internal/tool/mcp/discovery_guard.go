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

func (s *Service) rejectBuiltinDiscovery(ctx context.Context, builtins []BuiltinToolSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	builtins = sortedBuiltinTools(builtins)
	names := builtinToolNames(builtins)
	repeated, previous, repeatCount := s.discoveryRoutes.remember(ctx, names)
	code := "HOST_BUILTIN_NOT_DYNAMIC_MCP"
	if repeated {
		code = "ROUTING_LOOP_BLOCKED"
	}
	label := strings.Join(names, ", ")
	details := map[string]any{
		"route_kind": routeKindHostBuiltin, "resolved": true, "terminal": true,
		"retryable": false, "execution_performed": false, "retry_dynamic_search": false,
		"builtin_tools": builtins, "required_host_tools": names,
		"next_action": "call_host_tool", "repeated": repeated,
		"route_code": code, "route_status": "blocked",
		"route_summary":          "错误工具命名空间 · " + label,
		"wrong_namespace_search": true, "wrong_namespace_repeat_count": repeatCount,
		"host_schema_state":          "caller_managed",
		"host_discovery_method":      "tools/list",
		"filesystem_lookup_required": false,
		"dynamic_discovery_required": false,
		"recovery_source":            "caller_tools_list",
	}
	if len(names) == 1 {
		details["required_host_tool"] = names[0]
	}
	if previous != "" {
		details["previous_call_id"] = previous
	}
	if repeated {
		delete(details, "builtin_tools")
		details["route_reference"] = previous
	}

	return toolErrorCause(code,
		"AgentDock host tool "+label+" cannot be searched or inspected as dynamic MCP. Call it directly from the caller tools/list namespace. No business action ran.",
		"validation", details, errBuiltinDiscovery)
}
