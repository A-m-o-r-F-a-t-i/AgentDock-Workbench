package mcp

import (
	"context"
	"encoding/json"
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
	owner, conversation, serverFingerprint, revision, tool, schemaDigest string
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
	serverFingerprint, _ := schema["server_fingerprint"].(string)
	revision, _ := schema["catalog_revision"].(string)
	tool, _ := schema["tool_name"].(string)
	schemaDigest, _ := schema["schema_digest"].(string)
	if binding.ConversationID == "" || server == "" || serverFingerprint == "" || revision == "" || tool == "" || schemaDigest == "" || schema["input_schema"] == nil {
		return full
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	key := schemaDeliveryKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		serverFingerprint: serverFingerprint, revision: revision, tool: tool, schemaDigest: schemaDigest,
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
			"server_fingerprint": serverFingerprint, "catalog_revision": revision, "server_version": schema["server_version"],
			"schema_digest": schemaDigest, "annotations_digest": schema["annotations_digest"],
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
	if deferred, _ := ctx.Value(deferredSchemaDeliveryKey{}).(bool); !deferred {
		s.entries[key] = now
	}
	return full
}

type deferredSchemaDeliveryKey struct{}

func WithDeferredSchemaDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferredSchemaDeliveryKey{}, true)
}

// CommitSchemas is called after the final required page was actually encoded.
// No transport refresh or business invocation is performed while acknowledging.
func (s *Service) CommitSchemas(binding activity.Binding, result map[string]any) {
	entries := []map[string]any{}
	if result["input_schema"] != nil {
		entries = append(entries, result)
	}
	if result["tools"] != nil {
		encoded, err := json.Marshal(result["tools"])
		if err == nil {
			var batch []map[string]any
			if json.Unmarshal(encoded, &batch) == nil {
				entries = append(entries, batch...)
			}
		}
	}
	ctx := activity.WithBinding(context.Background(), binding)
	for _, entry := range entries {
		if entry["input_schema"] != nil {
			s.schemaDeliveries.prepare(ctx, entry, true)
		}
	}
}
