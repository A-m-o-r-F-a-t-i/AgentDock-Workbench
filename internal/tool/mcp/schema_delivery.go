package mcp

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	schemaDeliveryCapacity = 4096
	schemaDeliveryWindow   = 5 * time.Minute
)

type schemaDeliveryKey struct {
	owner, conversation, server, revision, tool string
}

type schemaDeliveryState struct {
	mu      sync.Mutex
	entries map[schemaDeliveryKey]time.Time
	now     func() time.Time
}

// prepare delivers each complete schema once per trusted conversation and
// catalog revision. A caller can explicitly request a complete copy after
// losing context without refreshing or reconnecting the upstream service.
func (s *schemaDeliveryState) prepare(ctx context.Context, schema map[string]any, forceFull bool) map[string]any {
	full := maps.Clone(schema)
	full["delivery"] = "full"
	full["schema_omitted"] = false

	binding := activity.FromContext(ctx)
	server, _ := schema["server"].(string)
	revision, _ := schema["catalog_revision"].(string)
	tool, _ := schema["tool_name"].(string)
	if binding.ConversationID == "" || server == "" || revision == "" || tool == "" || schema["input_schema"] == nil {
		return full
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	key := schemaDeliveryKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		server: server, revision: revision, tool: tool,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[schemaDeliveryKey]time.Time)
	}
	for candidate, delivered := range s.entries {
		age := now.Sub(delivered)
		if age < 0 || age >= schemaDeliveryWindow {
			delete(s.entries, candidate)
		}
	}
	if _, delivered := s.entries[key]; delivered && !forceFull {
		s.entries[key] = now
		return map[string]any{
			"name": schema["name"], "server": server, "tool_name": tool,
			"catalog_revision": revision, "server_version": schema["server_version"],
			"delivery": "reference", "schema_omitted": true, "refresh_tool": ToolInspect,
		}
	}
	if len(s.entries) >= schemaDeliveryCapacity {
		var oldestKey schemaDeliveryKey
		var oldest time.Time
		for candidate, delivered := range s.entries {
			if oldest.IsZero() || delivered.Before(oldest) {
				oldestKey, oldest = candidate, delivered
			}
		}
		delete(s.entries, oldestKey)
	}
	s.entries[key] = now
	return full
}
