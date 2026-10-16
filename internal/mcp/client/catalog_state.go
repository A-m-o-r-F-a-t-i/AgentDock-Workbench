package client

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

type indexSnapshot struct {
	Tools            map[string]Tool
	Client           protocolClient
	ClientGeneration uint64
	Generation       uint64
	Ready            bool
	Known            bool
	Count            int
	Revision         uint64
	Version          string
	LastError        string
	LastErrorCode    string
	RefreshedAt      time.Time
}

func stateSnapshotLocked(state *serverState) indexSnapshot {
	return indexSnapshot{Tools: state.tools, Client: state.client, ClientGeneration: state.clientGeneration, Generation: state.catalogGeneration, Ready: state.client != nil, Known: state.discovered, Count: len(state.tools), Revision: state.indexRevision,
		Version: state.serverVersion, LastError: state.lastError, LastErrorCode: state.lastErrorCode, RefreshedAt: state.refreshedAt}
}
func publishStateLocked(state *serverState) {
	previous := state.snapshot.Load()
	snapshot := stateSnapshotLocked(state)
	state.snapshot.Store(&snapshot)
	if state.catalogPublished != nil && (previous == nil || previous.Revision != snapshot.Revision || previous.Known != snapshot.Known || previous.ClientGeneration != snapshot.ClientGeneration || previous.Generation != snapshot.Generation) {
		state.catalogPublished()
	}
}

// Summary reads do not wait for a long-running MCP tools/call that owns state.mu.
func summaryForSnapshot(cfg ServerConfig, snapshot indexSnapshot) ServerSummary {
	if cfg.SourceType == "" {
		cfg.SourceType = "standalone"
	}
	status := "idle"
	if !cfg.Enabled {
		status = "disabled"
	} else if snapshot.LastError != "" {
		status = "error"
	} else if snapshot.Ready {
		status = "ready"
	}
	summary := ServerSummary{SourceType: cfg.SourceType, PluginName: cfg.PluginName, DisplayName: cfg.DisplayName, Plugin: cfg.PluginName, Name: cfg.Name, Description: cfg.Description, Transport: cfg.Transport, Enabled: cfg.Enabled, Status: status,
		ToolCount: snapshot.Count, ToolCountKnown: snapshot.Known, ServerVersion: snapshot.Version, LastError: snapshot.LastError, LastErrorCode: snapshot.LastErrorCode,
		Revision: fmt.Sprintf("%s:%d", cfg.revision, snapshot.Revision), OverrideSource: cfg.overrideSource, LastGoodAvailable: snapshot.Known && snapshot.LastError != "", CatalogState: catalogStateForSnapshot(cfg, snapshot)}
	if !snapshot.RefreshedAt.IsZero() {
		summary.RefreshedAt = snapshot.RefreshedAt.Format(time.RFC3339Nano)
	}
	return summary
}

func catalogEqual(left, right map[string]Tool) bool {
	if len(left) != len(right) {
		return false
	}
	for name, a := range left {
		b, found := right[name]
		if !found {
			return false
		}
		a.inputValidator, b.inputValidator = nil, nil
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}

func refreshStateLocked(ctx context.Context, cfg ServerConfig, state *serverState) (map[string]Tool, error) {
	candidate := &serverState{catalogChanged: state.catalogChanged}
	tools, err := initializeStateLocked(ctx, cfg, candidate)
	if err != nil {
		recordStateError(state, err)
		return nil, err
	}
	previous := state.client
	nextClientGeneration := state.clientGeneration + 1
	if nextClientGeneration == 0 {
		nextClientGeneration = 1
	}
	revision := state.indexRevision
	if !state.discovered || !catalogEqual(state.tools, candidate.tools) || state.serverVersion != candidate.serverVersion {
		revision++
	}
	state.client, state.tools = candidate.client, candidate.tools
	state.clientGeneration = nextClientGeneration
	state.discovered, state.serverVersion, state.indexRevision = true, candidate.serverVersion, revision
	state.lastError, state.lastErrorCode = "", ""
	state.refreshedAt = candidate.refreshedAt
	state.catalogGeneration = candidate.catalogGeneration
	publishStateLocked(state)
	// The caller already pins this state: an in-flight call finishes before refresh
	// takes the lock. A failed candidate never destroys the previous connection.
	if previous != nil {
		_ = previous.close()
	}
	return tools, nil
}

// Snapshots returns already-discovered metadata without connecting, reading files
// or expanding a Heavy plugin. It is safe on the tool-response presentation path.
func (m *Manager) Snapshots(names []string) []ServerSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := []ServerSummary{}
	for _, name := range names {
		if cfg, ok := m.servers[name]; ok {
			items = append(items, summaryFor(cfg, m.states[name]))
		}
	}
	return items
}

// DirectoryRevision reads only published in-memory catalog metadata. It never
// connects a transport or refreshes a third-party tool directory.
func (m *Manager) DirectoryRevision() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		item := summaryFor(m.servers[name], m.states[name])
		generation, clientGeneration := uint64(0), uint64(0)
		if state := m.states[name]; state != nil {
			if view := state.snapshot.Load(); view != nil {
				generation, clientGeneration = protocolGeneration(view.Client), view.ClientGeneration
			}
		}
		items = append(items, map[string]any{"name": name, "revision": item.Revision, "enabled": item.Enabled, "status": item.Status, "generation": generation, "client_generation": clientGeneration})
	}
	encoded, _ := json.Marshal(items)
	return fmt.Sprintf("mcprev_%x", sha256.Sum256(encoded))
}

// Freshness and connection availability are independent: retained schemas survive
// refresh failures, and a cold/lazy directory never proves a tool is absent.
func catalogStateForSnapshot(cfg ServerConfig, snapshot indexSnapshot) CatalogState {
	state := CatalogState{State: "stale", CatalogRevision: fmt.Sprintf("%s:%d", cfg.revision, snapshot.Revision), ServerRevision: cfg.revision, Lazy: !snapshot.Ready}
	if !snapshot.RefreshedAt.IsZero() {
		state.RefreshedAt = snapshot.RefreshedAt.Format(time.RFC3339Nano)
	}
	switch {
	case snapshot.LastErrorCode != "":
		state.RefreshErrorCode, state.RefreshAttempted = snapshot.LastErrorCode, true
		if snapshot.Known {
			state.State = "refresh_failed"
		} else {
			state.State = "unavailable"
		}
	case catalogFresh(&snapshot):
		state.State, state.AbsenceAuthoritative = "fresh", cfg.Enabled
	case snapshot.Known && !snapshot.Ready:
		state.State = "cached"
	}
	return state
}
