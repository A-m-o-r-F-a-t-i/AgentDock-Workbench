package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func definitionFingerprint(definition ServerConfig) (string, error) {
	definition.Description = ""
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
