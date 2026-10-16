package client

import (
	"context"
	"errors"
	"time"
)

const (
	catalogNotificationDebounce = 25 * time.Millisecond
	catalogNotificationTimeout  = 30 * time.Second
)

type catalogChangeObserver struct{ notify func() }

func (m *Manager) SetCatalogChangeObserver(changed func()) {
	if changed == nil {
		m.catalogObserver.Store(nil)
		return
	}
	m.catalogObserver.Store(&catalogChangeObserver{notify: changed})
}

func (m *Manager) bindCatalogNotifications(name string, state *serverState) {
	state.catalogChanged = func(client protocolClient) { m.queueCatalogRefresh(name, state, client) }
	state.catalogPublished = m.queueCatalogNotice
}

// Notifications pin one server state and client. Retired connections cannot
// refresh a replacement generation, and a burst has only one pending worker.
func (m *Manager) queueCatalogRefresh(name string, state *serverState, client protocolClient) {
	m.mu.RLock()
	active := m.states[name] == state && m.servers[name].Enabled
	m.mu.RUnlock()
	// Do not hold catalogMu while reading the registry. Publishers may enqueue
	// notices under mu; the worker revalidates identity after acquiring state.mu.
	m.catalogMu.Lock()
	if m.closed.Load() || !active || !state.catalogRefreshQueued.CompareAndSwap(false, true) {
		m.catalogMu.Unlock()
		return
	}
	m.catalogWG.Add(1)
	m.catalogMu.Unlock()
	go m.refreshNotifiedCatalog(name, state, client)
}

func (m *Manager) refreshNotifiedCatalog(name string, state *serverState, client protocolClient) {
	defer m.catalogWG.Done()
	completed := false
	defer func() {
		state.catalogRefreshQueued.Store(false)
		// Catch a notification received while the worker was publishing. Errors
		// stay stale until a later notification or an explicit selected read.
		if !m.closed.Load() {
			view := state.snapshot.Load()
			if view != nil && view.Ready && (completed || view.Client != client) && protocolGeneration(view.Client) != view.Generation {
				m.queueCatalogRefresh(name, state, view.Client)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(m.catalogContext, catalogNotificationTimeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		if !waitCatalogNotification(ctx) {
			return
		}
		err := m.reloadNotifiedCatalog(ctx, name, state, client)
		if err == nil {
			completed = true
			return
		}
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != "MCP_CATALOG_CHANGED" {
			return
		}
	}
}

func (m *Manager) reloadNotifiedCatalog(ctx context.Context, name string, state *serverState, client protocolClient) error {
	if err := lockWithContext(ctx, &state.mu); err != nil {
		return err
	}
	defer state.mu.Unlock()
	m.mu.RLock()
	cfg, exists := m.servers[name]
	current := m.states[name]
	m.mu.RUnlock()
	if m.closed.Load() || !exists || !cfg.Enabled || current != state || state.client != client || !state.discovered {
		return nil
	}
	if protocolGeneration(client) == state.catalogGeneration {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	_, err := reloadCatalogLocked(ctx, cfg, state)
	return err
}

// Publish notifications outside all registry/client locks. Multiple provider
// changes share a bounded notification worker and are drained during Close.
func (m *Manager) queueCatalogNotice() {
	if m.catalogObserver.Load() == nil {
		return
	}
	m.catalogNoticeGeneration.Add(1)
	m.catalogMu.Lock()
	if m.closed.Load() || !m.catalogNoticeQueued.CompareAndSwap(false, true) {
		m.catalogMu.Unlock()
		return
	}
	m.catalogWG.Add(1)
	m.catalogMu.Unlock()
	go func() {
		defer m.catalogWG.Done()
		observed := uint64(0)
		defer func() {
			m.catalogNoticeQueued.Store(false)
			if observed != 0 && observed != m.catalogNoticeGeneration.Load() {
				m.queueCatalogNotice()
			}
		}()
		if !waitCatalogNotification(m.catalogContext) || m.closed.Load() {
			return
		}
		observed = m.catalogNoticeGeneration.Load()
		if observer := m.catalogObserver.Load(); observer != nil {
			observer.notify()
		}
	}()
}

func waitCatalogNotification(ctx context.Context) bool {
	timer := time.NewTimer(catalogNotificationDebounce)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
