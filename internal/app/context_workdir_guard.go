package app

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

const (
	contextWorkdirFailureCapacity = 512
	contextWorkdirFailureWindow   = 5 * time.Minute
)

type contextWorkdirFailureKey struct {
	owner        string
	conversation string
	workdir      string
}

type contextWorkdirFailureEntry struct {
	firstCallID string
	lastCallID  string
	lastSeen    time.Time
	repeatCount int
}

type contextWorkdirFailureGuard struct {
	mu      sync.Mutex
	entries map[contextWorkdirFailureKey]contextWorkdirFailureEntry
	now     func() time.Time
}

func normalizeContextWorkdir(workdir string) string {
	workdir = filepath.Clean(strings.TrimSpace(workdir))
	if runtime.GOOS == "windows" {
		workdir = strings.ToLower(workdir)
	}
	return workdir
}

func contextWorkdirRequiredAction() map[string]any {
	return map[string]any{
		"action": "check_path_or_select_workspace",
		"text":   "Verify that workdir exists and is a directory, or omit workdir to reuse the current workspace before retrying.",
	}
}

func (g *contextWorkdirFailureGuard) reject(ctx context.Context, workdir, message string) error {
	binding := activity.FromContext(ctx)
	details := map[string]any{
		"workdir": workdir, "retryable": false,
		"next_action":     "check_path_or_select_workspace",
		"required_action": contextWorkdirRequiredAction(),
	}
	if binding.ConversationID == "" || binding.CallID == "" {
		return toolErrorDetails("INVALID_ARGUMENT", message, "validation", details)
	}

	key := contextWorkdirFailureKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		workdir: normalizeContextWorkdir(workdir),
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = make(map[contextWorkdirFailureKey]contextWorkdirFailureEntry)
	}
	g.pruneLocked(now)
	if entry, found := g.entries[key]; found && now.Sub(entry.lastSeen) >= 0 && now.Sub(entry.lastSeen) < contextWorkdirFailureWindow {
		if entry.lastCallID == binding.CallID {
			details["first_call_id"] = entry.firstCallID
			return toolErrorDetails("INVALID_ARGUMENT", message, "validation", details)
		}
		previous := entry.lastCallID
		entry.lastCallID = binding.CallID
		entry.lastSeen = now
		entry.repeatCount++
		g.entries[key] = entry
		details["first_call_id"] = entry.firstCallID
		details["previous_call_id"] = previous
		details["repeat_count"] = entry.repeatCount
		details["attempt_count"] = entry.repeatCount + 1
		return toolErrorDetails(
			"REPEATED_CALL_BLOCKED",
			"The same invalid instruction workdir was already rejected. Check the directory or select a workspace before another Context call.",
			"validation",
			details,
		)
	}
	if len(g.entries) >= contextWorkdirFailureCapacity {
		g.removeOldestLocked()
	}
	g.entries[key] = contextWorkdirFailureEntry{
		firstCallID: binding.CallID,
		lastCallID:  binding.CallID,
		lastSeen:    now,
	}
	details["first_call_id"] = binding.CallID
	return toolErrorDetails("INVALID_ARGUMENT", message, "validation", details)
}

func (g *contextWorkdirFailureGuard) clear(ctx context.Context, workdir string) {
	binding := activity.FromContext(ctx)
	if binding.ConversationID == "" {
		return
	}
	key := contextWorkdirFailureKey{
		owner: binding.SourceOwnerKey, conversation: binding.ConversationID,
		workdir: normalizeContextWorkdir(workdir),
	}
	g.mu.Lock()
	delete(g.entries, key)
	g.mu.Unlock()
}

func (g *contextWorkdirFailureGuard) pruneLocked(now time.Time) {
	for key, entry := range g.entries {
		age := now.Sub(entry.lastSeen)
		if age < 0 || age >= contextWorkdirFailureWindow {
			delete(g.entries, key)
		}
	}
}

func (g *contextWorkdirFailureGuard) removeOldestLocked() {
	var oldestKey contextWorkdirFailureKey
	var oldest time.Time
	for key, entry := range g.entries {
		if oldest.IsZero() || entry.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = entry.lastSeen
		}
	}
	if !oldest.IsZero() {
		delete(g.entries, oldestKey)
	}
}
