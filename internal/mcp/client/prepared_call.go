package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PreparedMCPCall fixes the executable meaning of one MCP request without
// copying the complete schema into approval or activity records.
type PreparedMCPCall struct {
	QualifiedName         string
	ServerFingerprint     string
	ClientGeneration      uint64
	CatalogRevision       string
	ToolSchemaDigest      string
	ToolAnnotationsDigest string
	PreparedAt            time.Time

	server          string
	toolName        string
	arguments       map[string]any
	argumentsDigest string
	state           *serverState
	tool            Tool
}

// PreparedCallSummary is the bounded immutable identity persisted with approval
// and activity records. It intentionally excludes arguments and full schemas.
type PreparedCallSummary struct {
	QualifiedName         string    `json:"qualified_name"`
	ServerFingerprint     string    `json:"server_fingerprint"`
	ClientGeneration      uint64    `json:"client_generation"`
	CatalogRevision       string    `json:"catalog_revision"`
	ToolSchemaDigest      string    `json:"tool_schema_digest"`
	ToolAnnotationsDigest string    `json:"tool_annotations_digest"`
	PreparedAt            time.Time `json:"prepared_at"`
}

// Summary returns only the stable digests and revisions needed to identify the
// prepared request without exposing arguments, environment values, or schemas.
func (p *PreparedMCPCall) Summary() PreparedCallSummary {
	if p == nil {
		return PreparedCallSummary{}
	}
	return PreparedCallSummary{
		QualifiedName:         p.QualifiedName,
		ServerFingerprint:     p.ServerFingerprint,
		ClientGeneration:      p.ClientGeneration,
		CatalogRevision:       p.CatalogRevision,
		ToolSchemaDigest:      p.ToolSchemaDigest,
		ToolAnnotationsDigest: p.ToolAnnotationsDigest,
		PreparedAt:            p.PreparedAt,
	}
}

// Matches reports whether a service request is the exact request represented
// by this preparation. The retained arguments remain private.
func (p *PreparedMCPCall) Matches(qualifiedName string, arguments map[string]any) bool {
	if p == nil || p.QualifiedName != qualifiedName {
		return false
	}
	digest, err := digestJSON(normalizeCallArguments(arguments))
	return err == nil && digest == p.argumentsDigest
}

func normalizeCallArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	return cloneJSONMap(arguments)
}

func digestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func toolSchemaDigest(tool Tool) (string, error) {
	return digestJSON(struct {
		Input  map[string]any `json:"input"`
		Output map[string]any `json:"output"`
	}{Input: tool.InputSchema, Output: tool.OutputSchema})
}

func toolAnnotationsDigest(tool Tool) (string, error) {
	return digestJSON(tool.Annotations)
}

// The prepared catalog revision excludes registry presentation metadata. The
// server fingerprint covers executable configuration, while client generation
// covers connection replacement.
func preparedCatalogRevision(state *serverState) string {
	if state == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", state.indexRevision, state.catalogGeneration)
}

func (m *Manager) PrepareCall(ctx context.Context, qualifiedName string, arguments map[string]any) (*PreparedMCPCall, error) {
	if err := m.syncRegistryContext(ctx); err != nil {
		return nil, err
	}
	server, name, err := splitQualifiedToolName(qualifiedName)
	if err != nil {
		return nil, newError("MCP_TOOL_NAME_INVALID", err.Error(), false, map[string]any{"tool": qualifiedName}, err)
	}
	cfg, state, release, err := m.lockServerContext(ctx, server)
	if err != nil {
		return nil, err
	}
	defer release()
	if !cfg.Enabled {
		return nil, newError("MCP_SERVER_DISABLED", "dynamic MCP server is disabled", false, map[string]any{"server": server}, nil)
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	var runtimeCfg ServerConfig
	if state.client == nil || !state.discovered {
		runtimeCfg, err = m.prepareRuntimeConfig(callCtx, cfg)
		if err != nil {
			recordStateError(state, err)
			return nil, err
		}
		if _, err = refreshStateLocked(callCtx, runtimeCfg, state); err != nil {
			return nil, err
		}
	} else {
		runtimeCfg, err = m.runtimeConfig(cfg)
		if err != nil {
			return nil, err
		}
	}
	if !catalogFresh(state.snapshot.Load()) {
		if _, err = reloadCatalogLocked(callCtx, cfg, state); err != nil {
			return nil, err
		}
	}
	tool, exists := state.tools[name]
	if !exists {
		return nil, newError("MCP_TOOL_NOT_FOUND", "MCP tool not found", false, map[string]any{"tool": qualifiedName}, nil)
	}
	fixedArguments := normalizeCallArguments(arguments)
	if err = validateToolArguments(tool, fixedArguments); err != nil {
		return nil, err
	}
	fingerprint, err := definitionFingerprint(runtimeCfg)
	if err != nil {
		return nil, err
	}
	schemaDigest, err := toolSchemaDigest(tool)
	if err != nil {
		return nil, err
	}
	annotationsDigest, err := toolAnnotationsDigest(tool)
	if err != nil {
		return nil, err
	}
	argumentsDigest, err := digestJSON(fixedArguments)
	if err != nil {
		return nil, newError("MCP_ARGUMENT_INVALID", "MCP tool arguments must be JSON-compatible", false, map[string]any{"tool": qualifiedName}, err)
	}
	if state.client == nil || state.clientGeneration == 0 {
		return nil, newError("MCP_CLIENT_UNAVAILABLE", "MCP client generation is unavailable", true, map[string]any{"server": server}, nil)
	}
	return &PreparedMCPCall{
		QualifiedName:         qualifiedName,
		ServerFingerprint:     fingerprint,
		ClientGeneration:      state.clientGeneration,
		CatalogRevision:       preparedCatalogRevision(state),
		ToolSchemaDigest:      schemaDigest,
		ToolAnnotationsDigest: annotationsDigest,
		PreparedAt:            time.Now().UTC(),
		server:                server,
		toolName:              name,
		arguments:             fixedArguments,
		argumentsDigest:       argumentsDigest,
		state:                 state,
		tool:                  cloneTool(tool),
	}, nil
}

func preparedCallChanged(prepared *PreparedMCPCall, currentRevision, reason string, cause error) error {
	details := map[string]any{
		"executed":                  false,
		"next_action":               "inspect_tool_again",
		"prepared_catalog_revision": "",
		"current_catalog_revision":  currentRevision,
		"reason":                    reason,
	}
	if prepared != nil {
		details["tool"] = prepared.QualifiedName
		details["prepared_catalog_revision"] = prepared.CatalogRevision
		details["prepared_client_generation"] = prepared.ClientGeneration
	}
	return newError(
		"MCP_TOOL_CHANGED_AFTER_APPROVAL",
		"MCP tool definition changed after request preparation; the fixed request was not dispatched",
		false,
		details,
		cause,
	)
}

func preparedCallRevalidationFailed(prepared *PreparedMCPCall, reason string, cause error) error {
	details := map[string]any{
		"executed":    false,
		"next_action": "inspect_tool_again",
		"reason":      reason,
	}
	if prepared != nil {
		details["tool"] = prepared.QualifiedName
		details["prepared_catalog_revision"] = prepared.CatalogRevision
	}
	return newError(
		"MCP_PREPARED_CALL_REVALIDATION_FAILED",
		"The prepared MCP call could not be revalidated and was not dispatched",
		true,
		details,
		cause,
	)
}

func changedAfterPreparationError(err error) bool {
	var mcpErr *Error
	return errors.As(err, &mcpErr) && (mcpErr.Code == "MCP_SERVER_NOT_FOUND" || mcpErr.Code == "MCP_SERVER_DISABLED")
}

func (m *Manager) lockPreparedCall(ctx context.Context, prepared *PreparedMCPCall) (context.Context, *serverState, func(), error) {
	if prepared == nil {
		return nil, nil, nil, newError("MCP_PREPARED_CALL_REQUIRED", "prepared MCP call is required", false, map[string]any{"executed": false}, nil)
	}
	if err := m.syncRegistryContext(ctx); err != nil {
		if changedAfterPreparationError(err) {
			return nil, nil, nil, preparedCallChanged(prepared, "", err.Error(), err)
		}
		return nil, nil, nil, err
	}
	cfg, state, releaseState, err := m.lockServerContext(ctx, prepared.server)
	if err != nil {
		if changedAfterPreparationError(err) {
			return nil, nil, nil, preparedCallChanged(prepared, "", err.Error(), err)
		}
		return nil, nil, nil, err
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		releaseState()
	}
	fail := func(failure error) (context.Context, *serverState, func(), error) {
		release()
		return nil, nil, nil, failure
	}
	currentRevision := preparedCatalogRevision(state)
	if !cfg.Enabled {
		return fail(preparedCallChanged(prepared, currentRevision, "MCP server was disabled", nil))
	}
	if state != prepared.state {
		return fail(preparedCallChanged(prepared, currentRevision, "MCP client state was replaced", nil))
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	combinedRelease := func() {
		cancel()
		release()
	}
	failCombined := func(failure error) (context.Context, *serverState, func(), error) {
		combinedRelease()
		return nil, nil, nil, failure
	}
	if state.client == nil || state.clientGeneration != prepared.ClientGeneration {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP client generation changed", nil))
	}
	if !catalogFresh(state.snapshot.Load()) {
		if _, err = reloadCatalogLocked(callCtx, cfg, state); err != nil {
			return failCombined(preparedCallRevalidationFailed(prepared, "MCP catalog refresh failed", err))
		}
	}
	currentRevision = preparedCatalogRevision(state)
	runtimeCfg, err := m.runtimeConfig(cfg)
	if err != nil {
		return failCombined(preparedCallRevalidationFailed(prepared, "MCP server environment could not be read", err))
	}
	fingerprint, err := definitionFingerprint(runtimeCfg)
	if err != nil {
		return failCombined(preparedCallRevalidationFailed(prepared, "MCP server configuration could not be fingerprinted", err))
	}
	if fingerprint != prepared.ServerFingerprint {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP server execution configuration changed", nil))
	}
	if currentRevision != prepared.CatalogRevision {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP catalog revision changed", nil))
	}
	tool, exists := state.tools[prepared.toolName]
	if !exists {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP tool was removed", nil))
	}
	schemaDigest, digestErr := toolSchemaDigest(tool)
	if digestErr != nil {
		return failCombined(preparedCallRevalidationFailed(prepared, "MCP tool schema could not be fingerprinted", digestErr))
	}
	annotationsDigest, digestErr := toolAnnotationsDigest(tool)
	if digestErr != nil {
		return failCombined(preparedCallRevalidationFailed(prepared, "MCP tool annotations could not be fingerprinted", digestErr))
	}
	if schemaDigest != prepared.ToolSchemaDigest {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP tool schema changed", nil))
	}
	if annotationsDigest != prepared.ToolAnnotationsDigest {
		return failCombined(preparedCallChanged(prepared, currentRevision, "MCP tool annotations changed", nil))
	}
	if err = validateToolArguments(prepared.tool, prepared.arguments); err != nil {
		return failCombined(preparedCallChanged(prepared, currentRevision, "Prepared arguments no longer match the fixed schema", err))
	}
	return callCtx, state, combinedRelease, nil
}

// ValidatePreparedCall performs a no-dispatch consistency check. CallPrepared
// repeats the same check while retaining the server-state pin for tools/call.
func (m *Manager) ValidatePreparedCall(ctx context.Context, prepared *PreparedMCPCall) error {
	_, _, release, err := m.lockPreparedCall(ctx, prepared)
	if release != nil {
		release()
	}
	return err
}

func (m *Manager) CallPrepared(ctx context.Context, prepared *PreparedMCPCall) (map[string]any, error) {
	callCtx, state, release, err := m.lockPreparedCall(ctx, prepared)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = callCtx.Err(); err != nil {
		return nil, err
	}
	observer := m.callObserver.Load()
	dispatch := func(dispatchCtx context.Context) (map[string]any, error) {
		return state.client.callTool(dispatchCtx, prepared.toolName, cloneJSONMap(prepared.arguments))
	}
	if observer != nil {
		return observer.call(callCtx, prepared.QualifiedName, dispatch)
	}
	return dispatch(callCtx)
}
