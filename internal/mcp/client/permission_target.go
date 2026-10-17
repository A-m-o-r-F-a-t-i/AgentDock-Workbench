package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func definitionFingerprint(definition ServerConfig) (string, error) {
	definition.Description = ""
	definition.RecoverySkill, definition.Lazy = "", false
	raw, err := json.Marshal(struct {
		Config                                 ServerConfig
		PluginRoot, PluginData, PluginVersion  string
		PackageEnv, PackageHeaders, RuntimeEnv map[string]string
	}{definition, definition.PluginRoot, definition.PluginData, definition.PluginVersion, definition.PackageEnv, definition.PackageHeaders, definition.RuntimeEnv})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// ExecutionFingerprint checks the configured target without starting a server.
func (m *Manager) ExecutionFingerprint(ctx context.Context, name string) (string, error) {
	if err := m.syncRegistryContext(ctx); err != nil {
		return "", err
	}
	cfg, _, release, err := m.lockServerContext(ctx, name)
	if err != nil {
		return "", err
	}
	defer release()
	runtimeCfg, err := m.runtimeConfig(cfg)
	if err != nil {
		return "", err
	}
	return definitionFingerprint(runtimeCfg)
}
