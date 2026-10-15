package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolcapability "github.com/uvwt/agentdock/internal/tool/capability"
)

const (
	builtinToolBundleSessionCapacity = 1024
	builtinToolBundleSessionTTL      = 24 * time.Hour
)

type builtinToolBundleSession struct {
	loaded         map[string]struct{}
	conversationID string
	updated        time.Time
}

type builtinToolBundleSessions struct {
	mu       sync.Mutex
	entries  map[string]*builtinToolBundleSession
	listener func()
	now      func() time.Time
}

type capabilityBuiltinToolBundleItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Loaded      bool   `json:"loaded"`
	ToolCount   int    `json:"tool_count"`
}

func builtinToolScopeKey(ctx context.Context) (string, bool) {
	source := activity.SourceFromContext(ctx)
	identity, quality := source.HostConversationID, "host_metadata"
	if identity == "" && source.ConnectionIsConversation && source.ConnectionID != "" {
		identity, quality = source.ConnectionID, "connection_fallback"
	}
	if identity == "" {
		return "", false
	}
	raw, _ := json.Marshal([]string{
		activity.SourceOwnerKey(ctx), source.Provider, source.Namespace, quality, identity,
	})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), true
}

func (s *builtinToolBundleSessions) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *builtinToolBundleSessions) pruneLocked(now time.Time) {
	for key, entry := range s.entries {
		age := now.Sub(entry.updated)
		if age < 0 || age >= builtinToolBundleSessionTTL {
			delete(s.entries, key)
		}
	}
}

func (s *builtinToolBundleSessions) reserveLocked() {
	if len(s.entries) < builtinToolBundleSessionCapacity {
		return
	}
	oldestKey := ""
	var oldest time.Time
	for key, entry := range s.entries {
		if oldestKey == "" || entry.updated.Before(oldest) {
			oldestKey, oldest = key, entry.updated
		}
	}
	delete(s.entries, oldestKey)
}

func (s *builtinToolBundleSessions) setListener(listener func()) {
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
}

func (s *builtinToolBundleSessions) statuses(ctx context.Context) ([]capabilityBuiltinToolBundleItem, bool) {
	key, scoped := builtinToolScopeKey(ctx)
	loaded := map[string]struct{}{}
	if scoped {
		s.mu.Lock()
		if s.entries == nil {
			s.entries = map[string]*builtinToolBundleSession{}
		}
		now := s.clock()
		s.pruneLocked(now)
		if entry := s.entries[key]; entry != nil {
			entry.updated = now
			for name := range entry.loaded {
				loaded[name] = struct{}{}
			}
		}
		s.mu.Unlock()
	}
	bundles := toolcapability.Bundles()
	items := make([]capabilityBuiltinToolBundleItem, 0, len(bundles))
	for _, bundle := range bundles {
		_, isLoaded := loaded[bundle.Name]
		if !scoped {
			isLoaded = true
		}
		items = append(items, capabilityBuiltinToolBundleItem{
			Name: bundle.Name, Description: bundle.Description, Loaded: isLoaded, ToolCount: len(bundle.Tools),
		})
	}
	return items, scoped
}

func (s *builtinToolBundleSessions) visible(ctx context.Context, tool string) bool {
	bundle, bundled := toolcapability.ForTool(tool)
	if !bundled {
		return true
	}
	key, scoped := builtinToolScopeKey(ctx)
	if !scoped {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		return false
	}
	now := s.clock()
	s.pruneLocked(now)
	entry := s.entries[key]
	if entry == nil {
		return false
	}
	if _, loaded := entry.loaded[bundle.Name]; !loaded {
		return false
	}
	entry.updated = now
	return true
}

func (s *builtinToolBundleSessions) load(ctx context.Context, bundle toolcapability.Bundle) (bool, bool) {
	key, scoped := builtinToolScopeKey(ctx)
	if !scoped {
		return false, false
	}
	now := s.clock()
	binding := activity.FromContext(ctx)
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*builtinToolBundleSession{}
	}
	s.pruneLocked(now)
	entry := s.entries[key]
	if entry == nil {
		s.reserveLocked()
		entry = &builtinToolBundleSession{loaded: map[string]struct{}{}, conversationID: binding.ConversationID}
		s.entries[key] = entry
	}
	if entry.conversationID == "" {
		entry.conversationID = binding.ConversationID
	}
	_, alreadyLoaded := entry.loaded[bundle.Name]
	entry.loaded[bundle.Name] = struct{}{}
	entry.updated = now
	listener := s.listener
	s.mu.Unlock()
	changed := !alreadyLoaded
	if changed && listener != nil {
		listener()
	}
	return changed, true
}

func (s *builtinToolBundleSessions) clearConversation(conversationID string) {
	if conversationID == "" {
		return
	}
	s.mu.Lock()
	changed := false
	for key, entry := range s.entries {
		if entry.conversationID == conversationID {
			delete(s.entries, key)
			changed = true
		}
	}
	listener := s.listener
	s.mu.Unlock()
	if changed && listener != nil {
		listener()
	}
}

func (r *Runtime) BuiltinToolVisible(ctx context.Context, name string) bool {
	if r == nil {
		return false
	}
	return r.builtinToolBundles.visible(ctx, name)
}

func (r *Runtime) requireBuiltinToolVisible(ctx context.Context, name string) error {
	// Visibility belongs to the MCP model projection, never to Core API authorization.
	if !modelProjectionRequested(ctx) {
		return nil
	}
	// Authenticated local control-panel APIs are not model tool-directory calls.
	// They keep their existing direct management path and permission checks.
	if local, _ := ctx.Value(localUserActionKey{}).(bool); local {
		return nil
	}
	bundle, bundled := toolcapability.ForTool(name)
	if !bundled || r.BuiltinToolVisible(ctx, name) {
		return nil
	}
	return toolErrorDetails(
		"TOOL_BUNDLE_REQUIRED",
		"Load the conversation-scoped built-in tool bundle before using this low-frequency tool.",
		"validation",
		map[string]any{
			"tool": name, "bundle": bundle.Name, "loader": toolcapability.ToolLoad, "executed": false,
		},
	)
}

func (r *Runtime) builtinToolBundleItems(ctx context.Context) []capabilityBuiltinToolBundleItem {
	items, _ := r.builtinToolBundles.statuses(ctx)
	result := make([]capabilityBuiltinToolBundleItem, 0, len(items))
	for _, item := range items {
		bundle, _ := toolcapability.ByName(item.Name)
		item.ToolCount = len(r.availableBundleTools(bundle))
		if item.ToolCount > 0 {
			result = append(result, item)
		}
	}
	return result
}

func (r *Runtime) availableBundleTools(bundle toolcapability.Bundle) []string {
	available := map[string]bool{}
	for _, name := range r.ToolNames() {
		available[name] = true
	}
	tools := []string{}
	for _, name := range bundle.Tools {
		if available[name] {
			tools = append(tools, name)
		}
	}
	return tools
}

func (r *Runtime) loadBuiltinToolBundle(ctx context.Context, request toolcapability.Request) (Result, error) {
	if err := toolcapability.Validate(request); err != nil {
		return nil, toolError("INVALID_ARGUMENT", err.Error(), "validation")
	}
	changed, scoped := false, false
	var loadedBundle toolcapability.Bundle
	if request.Action == toolcapability.ActionLoad {
		loadedBundle, _ = toolcapability.ByName(request.Name)
		if len(r.availableBundleTools(loadedBundle)) == 0 {
			return nil, toolError("TOOL_BUNDLE_UNAVAILABLE", "This bundle has no enabled tools on this device.", "validation")
		}
		changed, scoped = r.builtinToolBundles.load(ctx, loadedBundle)
	} else {
		_, scoped = r.builtinToolBundles.statuses(ctx)
	}
	items := r.builtinToolBundleItems(ctx)
	bundles := make([]map[string]any, 0, len(items))
	definitions := toolcapability.Bundles()
	byName := make(map[string]toolcapability.Bundle, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	for _, item := range items {
		definition := byName[item.Name]
		bundles = append(bundles, map[string]any{
			"name": item.Name, "description": item.Description, "loaded": item.Loaded,
			"tool_count": item.ToolCount, "tools": r.availableBundleTools(definition),
		})
	}
	result := Result{
		"action": request.Action, "scoped": scoped, "changed": changed,
		"directory_changed": changed, "bundles": bundles,
	}
	if request.Action == toolcapability.ActionLoad {
		result["loaded_bundle"] = loadedBundle.Name
		result["revealed_tools"] = r.availableBundleTools(loadedBundle)
		result["bundles"] = []map[string]any{}
		if scoped {
			result["next_required_action"] = "Invoke the required revealed tool directly. Loading did not execute it."
		} else {
			result["next_required_action"] = "This client already has the tools; invoke the required tool directly."
		}
	}
	return result, nil
}

func (r *Runtime) OnBuiltinToolDirectoryChanged(listener func()) {
	if r != nil {
		r.builtinToolBundles.setListener(listener)
	}
}

func (r *Runtime) clearBuiltinToolBundles(conversationID string) {
	if r != nil {
		r.builtinToolBundles.clearConversation(conversationID)
	}
}
