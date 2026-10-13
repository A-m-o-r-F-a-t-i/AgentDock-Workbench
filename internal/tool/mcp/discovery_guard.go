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
	owner, conversation, workspace, tool string
	revision                             uint64
}

type builtinDiscoveryEntry struct {
	firstCall string
	started   time.Time
}

type builtinDiscoveryRoutes struct {
	mu      sync.Mutex
	entries map[builtinDiscoveryKey]builtinDiscoveryEntry
	now     func() time.Time
}

// remember records only trusted ingress identity. Unattributed calls never share
// a bucket, and normal list/inspect/call operations never consult this state.
func (g *builtinDiscoveryRoutes) remember(ctx context.Context, names []string) (bool, string) {
	binding := activity.FromContext(ctx)
	if binding.ConversationID == "" {
		return false, ""
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
	repeated, previous := false, ""
	for _, name := range names {
		key := builtinDiscoveryKey{owner: binding.SourceOwnerKey, conversation: binding.ConversationID, workspace: binding.WorkspaceID, revision: binding.BindingRevision, tool: name}
		if entry, ok := g.entries[key]; ok && now.Sub(entry.started) >= 0 && now.Sub(entry.started) < builtinDiscoveryWindow {
			repeated = true
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
	return repeated, previous
}

func (s *Service) rejectBuiltinDiscovery(ctx context.Context, builtins []BuiltinToolSummary) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	builtins = sortedBuiltinTools(builtins)
	names := builtinToolNames(builtins)
	repeated, previous := s.discoveryRoutes.remember(ctx, names)
	code := "HOST_BUILTIN_NOT_DYNAMIC_MCP"
	if repeated {
		code = "ROUTING_LOOP_BLOCKED"
	}
	details := map[string]any{
		"route_kind": routeKindHostBuiltin, "resolved": true, "terminal": true,
		"retryable": false, "execution_performed": false,
		"builtin_tools": builtins, "required_host_tools": names,
		"next_action": "use_host_tool_namespace", "repeated": repeated,
		"host_schema_state":     "caller_managed",
		"host_discovery_method": "tools/list",
	}
	if len(names) == 1 {
		details["required_host_tool"] = names[0]
	}
	if previous != "" {
		details["previous_call_id"] = previous
	}
	return toolErrorCause(code,
		"AgentDock built-in "+strings.Join(names, ", ")+": use the caller host tool namespace. If its schema is absent, load it there once. Do not repeat dynamic MCP search/inspect; no business action ran.",
		"validation", details, errBuiltinDiscovery)
}
