package client

import (
	"context"
	"time"
)

// Local Plugin runtime verification must not consume the transport timeout.
// Callers may extend this with an explicit larger server timeout, while their
// own cancellation or deadline always remains authoritative.
const minimumExternalPreparationTimeout = 2 * time.Minute

// Metadata providers are read-only. Filesystem preparation is reserved for the
// one selected service, never a side effect of context or metadata listing.
func (m *Manager) SetExternalServerPreparation(prepare func(context.Context, ServerConfig) error) {
	m.mu.Lock()
	m.externalPrepare = prepare
	m.mu.Unlock()
}

// SetExternalServerRuntimeLease binds a live connection to its owner's runtime
// files. The lease is acquired only when a protocol client is initialized and
// released after that client closes, including asynchronously retired states.
func (m *Manager) SetExternalServerRuntimeLease(acquire func(context.Context, ServerConfig) (func(), error)) {
	m.mu.Lock()
	m.externalRuntimeLease = acquire
	m.mu.Unlock()
}

func (m *Manager) acquireExternalServerRuntime(ctx context.Context, cfg ServerConfig) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.PluginRoot == "" {
		return nil, nil
	}
	m.mu.RLock()
	acquire := m.externalRuntimeLease
	m.mu.RUnlock()
	if acquire == nil {
		return nil, nil
	}
	return acquire(ctx, cfg)
}

func (m *Manager) prepareExternalServer(ctx context.Context, cfg ServerConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.PluginRoot == "" {
		return nil
	}
	m.mu.RLock()
	prepare := m.externalPrepare
	m.mu.RUnlock()
	if prepare != nil {
		if err := prepare(ctx, cfg); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func externalPreparationTimeout(cfg ServerConfig) time.Duration {
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout < minimumExternalPreparationTimeout {
		return minimumExternalPreparationTimeout
	}
	return timeout
}

func (m *Manager) prepareRuntimeConfig(ctx context.Context, cfg ServerConfig) (ServerConfig, error) {
	resolved, err := m.runtimeConfig(cfg)
	if err != nil {
		return ServerConfig{}, err
	}
	prepareCtx, cancel := context.WithTimeout(ctx, externalPreparationTimeout(resolved))
	defer cancel()
	if err := m.prepareExternalServer(prepareCtx, resolved); err != nil {
		return ServerConfig{}, err
	}
	return resolved, nil
}
