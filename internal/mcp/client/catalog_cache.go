package client

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

const catalogCacheMaxAge = 24 * time.Hour
const catalogCacheMaxBytes = 8 << 20
const catalogCacheMaxFiles = 32
const catalogCacheReadBudget = 2 * time.Second

type retainedCatalog struct {
	Version       int       `json:"version"`
	Server        string    `json:"server"`
	Identity      string    `json:"identity"`
	RefreshedAt   time.Time `json:"refreshed_at"`
	ServerVersion string    `json:"server_version,omitempty"`
	Tools         []Tool    `json:"tools"`
}

func catalogCacheIdentity(cfg ServerConfig) (string, error) {
	// Timeouts and lazy/recovery presentation do not change a tool schema. Actual
	// credentials and the live schema are revalidated before every business call.
	cfg.TimeoutMS = 0
	cfg.RuntimeEnv = nil
	return definitionFingerprint(cfg)
}
func (catalog Catalog) cachedCallable() bool {
	age := time.Since(catalog.RefreshedAt)
	return catalog.Complete && catalog.State.State == "cached" && catalog.State.Lazy && age >= 0 && age < catalogCacheMaxAge
}
func (m *Manager) newCatalogState(cfg ServerConfig) *serverState {
	state := &serverState{}
	m.bindCatalogNotifications(cfg.Name, state)
	identity, err := catalogCacheIdentity(cfg)
	if err != nil {
		return state
	}
	root := filepath.Join(filepath.Dir(m.store.path), "catalog-cache")
	name := cfg.Name + ".json"
	var lastRevision uint64
	var lastSaved time.Time
	if cfg.Lazy && cfg.Enabled {
		ctx, cancel := context.WithTimeout(m.catalogContext, catalogCacheReadBudget)
		m.restoreCatalogCache(ctx, root, name, identity, cfg, state)
		cancel()
		if state.discovered {
			lastRevision, lastSaved = state.indexRevision, state.refreshedAt
		}
	}
	state.catalogRetained = func(snapshot indexSnapshot) {
		if !snapshot.Ready || !snapshot.Known || snapshot.LastErrorCode != "" || m.closed.Load() {
			return
		}
		if lastRevision == snapshot.Revision && !lastSaved.IsZero() && time.Since(lastSaved) < catalogCacheMaxAge/2 {
			return
		}
		tools := make([]Tool, 0, len(snapshot.Tools))
		for _, tool := range snapshot.Tools {
			tools = append(tools, tool)
		}
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		retained := retainedCatalog{Version: 1, Server: cfg.Name, Identity: identity, RefreshedAt: snapshot.RefreshedAt, ServerVersion: snapshot.Version, Tools: tools}
		data, err := json.Marshal(retained)
		if err != nil || len(data) > catalogCacheMaxBytes {
			return
		}
		if err = m.writeCatalogCache(root, name, data); err != nil {
			if !m.closed.Load() {
				slog.Warn("MCP catalog cache could not be retained", "server", cfg.Name, "error", err)
			}
			return
		}
		lastRevision, lastSaved = snapshot.Revision, snapshot.RefreshedAt
	}
	return state
}
func (m *Manager) restoreCatalogCache(ctx context.Context, root, name, identity string, cfg ServerConfig, state *serverState) {
	if err := lockWithContext(ctx, &m.catalogCacheMu); err != nil {
		return
	}
	data, err := securepath.ReadRegular(root, name, catalogCacheMaxBytes)
	m.catalogCacheMu.Unlock()
	if err != nil || ctx.Err() != nil {
		return
	}
	var cached retainedCatalog
	if json.Unmarshal(data, &cached) != nil || cached.Version != 1 || cached.Server != cfg.Name || cached.Identity != identity {
		return
	}
	age := time.Since(cached.RefreshedAt)
	if age < 0 || age >= catalogCacheMaxAge {
		return
	}
	tools, err := compileCatalog(ctx, cfg, cached.Tools)
	if err != nil {
		return
	}
	state.tools, state.discovered, state.indexRevision = tools, true, 1
	state.refreshedAt, state.serverVersion = cached.RefreshedAt, cached.ServerVersion
	snapshot := stateSnapshotLocked(state)
	state.snapshot.Store(&snapshot)
}
func (m *Manager) writeCatalogCache(root, name string, data []byte) error {
	if err := lockWithContext(m.catalogContext, &m.catalogCacheMu); err != nil {
		return err
	}
	defer m.catalogCacheMu.Unlock()
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(root, name), data, 0600); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	type cachedFile struct {
		name     string
		modified time.Time
	}
	files := []cachedFile{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			files = append(files, cachedFile{entry.Name(), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.Before(files[j].modified) })
	remaining := len(files)
	for _, file := range files {
		if remaining <= catalogCacheMaxFiles {
			break
		}
		if file.name == name {
			continue
		}
		if os.Remove(filepath.Join(root, file.name)) == nil {
			remaining--
		}
	}
	return nil
}
