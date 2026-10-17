package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/config"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

func newContextRegressionRuntime(t *testing.T) *Runtime {
	t.Helper()
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestCommonContextCacheCopiesAndSameTimestampInvalidation(t *testing.T) {
	home := t.TempDir()
	setUserHomeForTest(t, home)
	root := filepath.Join(home, ".agents", "skills")
	writeCommonSkillForTest(t, root, "shared-test", "shared-test", "before")
	rt := newContextRegressionRuntime(t)
	first, _, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("first index: %#v %v", first, err)
	}
	first.Items[0].Description = "must not enter shared cache"
	second, info, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || !info.CacheHit || second.Items[0].Description != "before" {
		t.Fatalf("cache copy: %#v %#v %v", second, info, err)
	}
	path := filepath.Join(root, "shared-test", "SKILL.md")
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	revision := rt.contextSnapshots.commonFiles.Revision()
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "before", "after!", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for rt.contextSnapshots.commonFiles.Revision() == revision && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	updated, _, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || updated.Items[0].Description != "after!" {
		t.Fatalf("stale same-stamp index: %#v %v", updated, err)
	}
}

func TestRuntimeCloseReleasesAllContextCaches(t *testing.T) {
	rt := newContextRegressionRuntime(t)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.contextSnapshots.rules.Get(t.Context(), "closed", func(context.Context) (agentinstructions.Snapshot, error) {
		t.Fatal("closed cache started builder")
		return agentinstructions.Snapshot{}, nil
	}); err == nil {
		t.Fatal("rules cache remains open")
	}
	if _, _, err := rt.pluginStore.Snapshot(t.Context()); err == nil {
		t.Fatal("plugin cache remains open")
	}
}

func TestFilesystemContextScansRespectCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scanCommonFilesystemSkillsContext(ctx, t.TempDir(), nil); err == nil {
		t.Fatal("common scan ignored cancellation")
	}
	if _, err := scanWorkspaceFilesystemSkillsContext(ctx, t.TempDir()); err == nil {
		t.Fatal("workspace scan ignored cancellation")
	}
}

func TestContextCursorSurvivesCatalogAndGitWritesButRejectsChangedRules(t *testing.T) {
	rt := newContextRegressionRuntime(t)
	root := rt.cfg.AgentDockDefaultDir
	git := filepath.Join(root, ".git")
	if err := os.Mkdir(git, 0700); err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join(root, "AGENTS.md")
	rules := strings.Repeat("Retain this workspace rule and verify each completed edit.\n", 900)
	if err := os.WriteFile(rulesPath, []byte(rules), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithModelProjection(activity.WithSource(t.Context(), activity.Source{
		Principal: "context-cursor-test", Namespace: "mcp", HostConversationID: "context-cursor-boundary",
	}))
	args := map[string]any{"workdir": root, "max_response_bytes": 12000}
	first, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatal(err)
	}
	firstMeta := modelObject(first["structured_output"])
	cursor := stringArg(first, "next_cursor")
	snapshotID := stringArg(firstMeta, "snapshot_id")
	if cursor == "" || snapshotID == "" {
		t.Fatalf("large Context has no retained continuation: %#v", firstMeta)
	}
	beforeCatalog := rt.capabilityManager.DirectoryRevision()
	if _, err := rt.capabilityManager.Add(mcpclient.ServerConfig{
		Name: "context-cursor-disabled", Description: "Disabled catalog revision fixture", Transport: mcpclient.TransportStdio, Command: "must-not-start", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if rt.capabilityManager.DirectoryRevision() == beforeCatalog {
		t.Fatal("fixture did not change the published MCP directory")
	}
	gitInfo, err := os.Stat(git)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(git, "COMMIT_EDITMSG"), []byte("ordinary Git bookkeeping"), 0600); err != nil {
		t.Fatal(err)
	}
	// Force a distinct directory stamp even on filesystems with coarse mtimes.
	changedTime := gitInfo.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(git, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	args["cursor"] = cursor
	next, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatalf("catalog/Git bookkeeping invalidated immutable Context pages: %v", err)
	}
	nextMeta := modelObject(next["structured_output"])
	if stringArg(nextMeta, "snapshot_id") != snapshotID || integerArgument(nextMeta["page_index"]) != integerArgument(firstMeta["page_index"])+1 {
		t.Fatalf("continuation rebuilt or skipped the original snapshot: %#v", nextMeta)
	}
	if cursor = stringArg(next, "next_cursor"); cursor == "" {
		t.Fatal("fixture needs another retained page for rule invalidation")
	}
	if err := os.WriteFile(rulesPath, []byte(rules+"The workspace rules have changed.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args["cursor"] = cursor
	_, err = rt.Call(ctx, "agentdock_context", args)
	var failure *ToolError
	if !errors.As(err, &failure) || failure.Code != "CONTEXT_REVISION_CHANGED" {
		t.Fatalf("changed rules did not reject the retained cursor: %v", err)
	}
}
