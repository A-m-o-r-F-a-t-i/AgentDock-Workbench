package client

import "context"

type expectedMCPIdentityKey struct{}

// WithExpectedMCPIdentity is an internal immutable recovery constraint. It is
// never decoded from remote metadata or model-supplied tool parameters.
func WithExpectedMCPIdentity(ctx context.Context, identity PreparedCallSummary) context.Context {
	return context.WithValue(ctx, expectedMCPIdentityKey{}, identity)
}
func checkExpectedMCPIdentity(ctx context.Context, name, fingerprint, schema, annotations string) error {
	expected, ok := ctx.Value(expectedMCPIdentityKey{}).(PreparedCallSummary)
	if !ok {
		return nil
	}
	changed := expected.QualifiedName != "" && name != expected.QualifiedName || expected.ServerFingerprint == "" || expected.ServerFingerprint != fingerprint
	if schema != "" && expected.ToolSchemaDigest != "" && schema != expected.ToolSchemaDigest {
		changed = true
	}
	if annotations != "" && expected.ToolAnnotationsDigest != "" && annotations != expected.ToolAnnotationsDigest {
		changed = true
	}
	if changed {
		return newError("MCP_TOOL_CHANGED_AFTER_RECOVERY", "The original MCP target or schema changed; the original operation was not replayed", false, map[string]any{"executed": false, "execution_performed": false, "effect_state": "none", "tool": name, "next_action": "inspect_tool_again"}, nil)
	}
	return nil
}
func checkExpectedMCPRefresh(ctx context.Context, cfg ServerConfig) error {
	expected, ok := ctx.Value(expectedMCPIdentityKey{}).(PreparedCallSummary)
	if !ok {
		return nil
	}
	server, _, err := splitQualifiedToolName(expected.QualifiedName)
	if err != nil || server != cfg.Name {
		return newError("MCP_TOOL_CHANGED_AFTER_RECOVERY", "Recovery cannot change its original server", false, map[string]any{"executed": false}, err)
	}
	fingerprint, err := definitionFingerprint(cfg)
	if err != nil {
		return err
	}
	return checkExpectedMCPIdentity(ctx, expected.QualifiedName, fingerprint, "", "")
}
