package mcp

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	catalogDeliveryCapacity = 2048
	catalogDeliveryWindow   = 5 * time.Minute
)

type catalogDeliveryKey struct {
	owner, conversation, server string
}

type catalogDeliveryEntry struct {
	signature string
	delivered time.Time
}

type catalogDeliveryState struct {
	mu      sync.Mutex
	entries map[catalogDeliveryKey]catalogDeliveryEntry
	now     func() time.Time
}

// prepare returns one complete catalog for each trusted conversation and
// catalog generation, then compact references while that conversation remains
// active. Calls without a trusted conversation retain full-catalog behavior.
func (s *catalogDeliveryState) prepare(ctx context.Context, catalog map[string]any, forceFull bool) map[string]any {
	full := maps.Clone(catalog)
	full["delivery"] = "full"
	full["catalog_delivery"] = "full"
	full["tools_omitted"] = false

	binding := activity.FromContext(ctx)
	server, _ := catalog["server"].(string)
	revision, _ := catalog["catalog_revision"].(string)
	if binding.ConversationID == "" || server == "" || revision == "" || catalog["complete"] != true || catalog["stale"] == true || catalogToolCount(catalog) == 0 {
		return full
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	key := catalogDeliveryKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		server: server,
	}
	signature := fmt.Sprintf("%s|%t|%t|%d", revision, catalog["complete"] == true, catalog["stale"] == true, catalogTotal(catalog))

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[catalogDeliveryKey]catalogDeliveryEntry)
	}
	for candidate, entry := range s.entries {
		age := now.Sub(entry.delivered)
		if age < 0 || age >= catalogDeliveryWindow {
			delete(s.entries, candidate)
		}
	}
	if entry, ok := s.entries[key]; ok && entry.signature == signature && !forceFull {
		entry.delivered = now
		s.entries[key] = entry
		reference := maps.Clone(full)
		delete(reference, "tools")
		reference["delivery"] = "reference"
		reference["catalog_delivery"] = "reference"
		reference["tools_omitted"] = true
		reference["refresh_tool"] = ToolList
		return reference
	}
	if len(s.entries) >= catalogDeliveryCapacity {
		var oldestKey catalogDeliveryKey
		var oldest time.Time
		for candidate, entry := range s.entries {
			if oldest.IsZero() || entry.delivered.Before(oldest) {
				oldestKey, oldest = candidate, entry.delivered
			}
		}
		delete(s.entries, oldestKey)
	}
	s.entries[key] = catalogDeliveryEntry{signature: signature, delivered: now}
	return full
}

func catalogToolCount(catalog map[string]any) int {
	switch tools := catalog["tools"].(type) {
	case []map[string]any:
		return len(tools)
	case []any:
		return len(tools)
	default:
		return 0
	}
}

func catalogTotal(catalog map[string]any) int {
	switch value := catalog["total"].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return catalogToolCount(catalog)
	}
}
