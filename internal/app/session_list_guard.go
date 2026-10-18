package app

import (
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	emptySessionListGuardCapacity = 512
	emptySessionListGuardWindow   = 5 * time.Minute
)

type emptySessionListKey struct {
	owner        string
	conversation string
	workspace    string
}

type emptySessionListEntry struct {
	firstCallID string
	lastCallID  string
	lastSeen    time.Time
	repeatCount int
}

type emptySessionListGuard struct {
	mu      sync.Mutex
	entries map[emptySessionListKey]emptySessionListEntry
	now     func() time.Time
}

func emptySessionListScope(binding activity.Binding) (emptySessionListKey, bool) {
	if strings.TrimSpace(binding.SourceOwnerKey) == "" || strings.TrimSpace(binding.ConversationID) == "" {
		return emptySessionListKey{}, false
	}
	return emptySessionListKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		workspace: binding.WorkspaceID,
	}, true
}

func isSessionListRequest(name string, args map[string]any) bool {
	if name != "session_observe" {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	return action == "" || action == "list"
}

func emptySessionListRequiredAction() map[string]any {
	return map[string]any{
		"action": "wait_for_running_session",
		"text":   "Do not call session_observe action=list again until exec_command returns status=running. Then observe that exact session_id with action=status.",
	}
}

// reject stops another empty catalog probe before it reaches the command
// service. A new running command or a non-empty list clears the scope.
func (g *emptySessionListGuard) reject(binding activity.Binding, name string, args map[string]any) error {
	if !isSessionListRequest(name, args) {
		return nil
	}
	key, ok := emptySessionListScope(binding)
	if !ok {
		return nil
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.pruneLocked(now)
	entry, found := g.entries[key]
	if !found || now.Sub(entry.lastSeen) < 0 || now.Sub(entry.lastSeen) >= emptySessionListGuardWindow {
		return nil
	}
	if entry.lastCallID == binding.CallID {
		return nil
	}
	previous := entry.lastCallID
	entry.lastCallID = binding.CallID
	entry.lastSeen = now
	entry.repeatCount++
	g.entries[key] = entry
	return toolErrorDetails(
		"REPEATED_CALL_BLOCKED",
		"The same empty command-session list was already delivered. Wait for a new running command before listing sessions again.",
		"validation",
		map[string]any{
			"retryable": false, "first_call_id": entry.firstCallID,
			"previous_call_id": previous, "repeat_count": entry.repeatCount,
			"required_action": emptySessionListRequiredAction(),
		},
	)
}

func (g *emptySessionListGuard) record(binding activity.Binding, result Result) {
	key, ok := emptySessionListScope(binding)
	if !ok || result == nil {
		return
	}
	count, countKnown := integerResult(result["count"])
	if !countKnown || count != 0 {
		g.clear(binding)
		return
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = make(map[emptySessionListKey]emptySessionListEntry)
	}
	g.pruneLocked(now)
	if entry, found := g.entries[key]; found {
		entry.lastCallID = binding.CallID
		entry.lastSeen = now
		g.entries[key] = entry
		return
	}
	if len(g.entries) >= emptySessionListGuardCapacity {
		g.removeOldestLocked()
	}
	g.entries[key] = emptySessionListEntry{
		firstCallID: binding.CallID, lastCallID: binding.CallID, lastSeen: now,
	}
}

func integerResult(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		return 0, false
	}
}

func (g *emptySessionListGuard) clear(binding activity.Binding) {
	key, ok := emptySessionListScope(binding)
	if !ok {
		return
	}
	g.mu.Lock()
	delete(g.entries, key)
	g.mu.Unlock()
}

func (g *emptySessionListGuard) pruneLocked(now time.Time) {
	for key, entry := range g.entries {
		age := now.Sub(entry.lastSeen)
		if age < 0 || age >= emptySessionListGuardWindow {
			delete(g.entries, key)
		}
	}
}

func (g *emptySessionListGuard) removeOldestLocked() {
	var oldestKey emptySessionListKey
	var oldest time.Time
	for key, entry := range g.entries {
		if oldest.IsZero() || entry.lastSeen.Before(oldest) {
			oldestKey, oldest = key, entry.lastSeen
		}
	}
	if !oldest.IsZero() {
		delete(g.entries, oldestKey)
	}
}
