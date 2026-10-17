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

func TestCommonContextCacheTracksOnlySelectedDirectories(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setUserHomeForTest(t, home)
	root := filepath.Join(home, ".agents", "skills")
	rt := newContextRegressionRuntime(t)
	first, firstInfo, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || len(first.Items) != 0 {
		t.Fatalf("missing root index: %#v %v", first, err)
	}
	files := rt.contextSnapshots.commonFiles
	revision := files.Revision()
	// A missing Common root is watched through its existing ancestor. Core's
	// own Home and a sibling with the same prefix do not belong to that source.
	for _, dir := range []string{filepath.Join(home, "home"), filepath.Join(home, ".agents-backup")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(100 * time.Millisecond); time.Now().Before(deadline); {
		cached, info, err := rt.cachedCommonSkillIndex(t.Context())
		if err != nil || !info.CacheHit || info.BuildID != firstInfo.BuildID || len(cached.Items) != 0 || files.Revision() != revision {
			t.Fatalf("unrelated Home write invalidated Common: index=%#v info=%#v revision=%s before=%s err=%v", cached, info, files.Revision(), revision, err)
		}
		time.Sleep(time.Millisecond)
	}
	waitForChange := func(before string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for files.Revision() == before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if files.Revision() == before {
			t.Fatal("selected Common directory change was not observed")
		}
	}
	// Discover the missing ancestor first; the next read must then watch it so
	// later root creation and actual document edits cannot remain cached empty.
	if err := os.Mkdir(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	waitForChange(revision)
	if index, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(index.Items) != 0 {
		t.Fatalf("missing child root index: %#v %v", index, err)
	}
	revision = files.Revision()
	if err := os.Rename(filepath.Dir(root), filepath.Join(home, ".agents-retired")); err != nil {
		t.Fatal(err)
	}
	waitForChange(revision)
	if index, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(index.Items) != 0 {
		t.Fatalf("removed ancestor index: %#v %v", index, err)
	}
	revision = files.Revision()
	if err := os.Mkdir(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	waitForChange(revision)
	if index, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(index.Items) != 0 {
		t.Fatalf("replacement ancestor index: %#v %v", index, err)
	}
	revision = files.Revision()
	writeCommonSkillForTest(t, root, "created-skill", "created-skill", "before")
	waitForChange(revision)
	created, createdInfo, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || len(created.Items) != 1 || created.Items[0].Description != "before" || createdInfo.BuildID == firstInfo.BuildID {
		t.Fatalf("created root remained cached empty: %#v %#v %v", created, createdInfo, err)
	}
	revision = files.Revision()
	rewriteRevalidationFixture(t, filepath.Join(root, "created-skill", "SKILL.md"), "before", "after!")
	waitForChange(revision)
	updated, _, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil || len(updated.Items) != 1 || updated.Items[0].Description != "after!" {
		t.Fatalf("same-stamp edit after root creation remained cached: %#v %v", updated, err)
	}
}

func TestCommonContextCacheTracksSymlinkTargets(t *testing.T) {
	for _, link := range []string{"ancestor", "root", "package", "document"} {
		t.Run(link, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			setUserHomeForTest(t, home)
			root := filepath.Join(home, ".agents", "skills")
			external := t.TempDir()
			if link == "ancestor" {
				external = filepath.Join(external, "skills")
			}
			writeCommonSkillForTest(t, external, "linked-skill", "linked-skill", "before")
			path, target := root, external
			if link == "ancestor" {
				path, target = filepath.Dir(root), filepath.Dir(external)
			} else if link != "root" {
				path, target = filepath.Join(root, "linked-skill"), filepath.Join(external, "linked-skill")
			}
			if link == "document" {
				path, target = filepath.Join(path, "SKILL.md"), filepath.Join(target, "SKILL.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			rt := newContextRegressionRuntime(t)
			first, _, err := rt.cachedCommonSkillIndex(t.Context())
			if err != nil || len(first.Items) != 1 || first.Items[0].Description != "before" {
				t.Fatalf("initial linked Common index: %#v %v", first, err)
			}
			files := rt.contextSnapshots.commonFiles
			revision := files.Revision()
			document := filepath.Join(external, "linked-skill", "SKILL.md")
			rewriteRevalidationFixture(t, document, "before", "after!")
			deadline := time.Now().Add(time.Second)
			for files.Revision() == revision && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if files.Revision() == revision {
				t.Fatal("linked Common document edit was not observed")
			}
			updated, _, err := rt.cachedCommonSkillIndex(t.Context())
			if err != nil || len(updated.Items) != 1 || updated.Items[0].Description != "after!" {
				t.Fatalf("stale linked Common document: %#v %v", updated, err)
			}
			// Retarget the lexical link as well as editing through its target.
			// Keep the exposed root stamp unchanged where the root is a real
			// directory, so the notification revision must carry this change.
			rootInfo, err := os.Stat(root)
			if err != nil {
				t.Fatal(err)
			}
			replacement := t.TempDir()
			if link == "ancestor" {
				replacement = filepath.Join(replacement, "skills")
			}
			writeCommonSkillForTest(t, replacement, "linked-skill", "linked-skill", "fresh!")
			if err := os.Chtimes(replacement, rootInfo.ModTime(), rootInfo.ModTime()); err != nil {
				t.Fatal(err)
			}
			nextTarget := replacement
			if link == "ancestor" {
				nextTarget = filepath.Dir(replacement)
			} else if link != "root" {
				nextTarget = filepath.Join(replacement, "linked-skill")
			}
			if link == "document" {
				nextTarget = filepath.Join(nextTarget, "SKILL.md")
			}
			revision = files.Revision()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(nextTarget, path); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(time.Second)
			for files.Revision() == revision && time.Now().Before(deadline) {
				if err := files.Sync(t.Context()); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Millisecond)
			}
			if files.Revision() == revision {
				t.Fatal("retargeted Common link was not observed")
			}
			retargeted, _, err := rt.cachedCommonSkillIndex(t.Context())
			if err != nil || len(retargeted.Items) != 1 || retargeted.Items[0].Description != "fresh!" {
				t.Fatalf("retargeted Common link remained cached: %#v %v", retargeted, err)
			}
			document = filepath.Join(replacement, "linked-skill", "SKILL.md")
			revision = files.Revision()
			if err := os.Remove(document); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(time.Second)
			for files.Revision() == revision && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if files.Revision() == revision {
				t.Fatal("deleted linked Common document was not observed")
			}
			removed, _, err := rt.cachedCommonSkillIndex(t.Context())
			if err != nil || len(removed.Items) != 0 {
				t.Fatalf("removed linked Common document remained cached: %#v %v", removed, err)
			}
		})
	}
}

func TestCommonContextCacheRearmsAfterAncestorReplacement(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setUserHomeForTest(t, home)
	root := filepath.Join(home, ".agents", "skills")
	writeCommonSkillForTest(t, root, "replaced-skill", "replaced-skill", "before")
	rt := newContextRegressionRuntime(t)
	if first, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(first.Items) != 1 {
		t.Fatalf("initial Common index: %#v %v", first, err)
	}
	files := rt.contextSnapshots.commonFiles
	waitForChange := func(before string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for files.Revision() == before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if files.Revision() == before {
			t.Fatal("Common source change was not observed")
		}
	}
	revision := files.Revision()
	if err := os.Rename(filepath.Dir(root), filepath.Join(home, ".agents-retired")); err != nil {
		t.Fatal(err)
	}
	waitForChange(revision)
	writeCommonSkillForTest(t, root, "replaced-skill", "replaced-skill", "fresh!")
	if replaced, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(replaced.Items) != 1 || replaced.Items[0].Description != "fresh!" {
		t.Fatalf("replacement Common index: %#v %v", replaced, err)
	}
	revision = files.Revision()
	rewriteRevalidationFixture(t, filepath.Join(root, "replaced-skill", "SKILL.md"), "fresh!", "after!")
	waitForChange(revision)
	if updated, _, err := rt.cachedCommonSkillIndex(t.Context()); err != nil || len(updated.Items) != 1 || updated.Items[0].Description != "after!" {
		t.Fatalf("replacement kept watching the retired directory: %#v %v", updated, err)
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
	checkMarker := func(t *testing.T, change func() error, wantConflict bool) {
		t.Helper()
		first, err := rt.Call(ctx, "agentdock_context", map[string]any{
			"workdir": root, "max_response_bytes": 12000, "force_refresh": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		cursor := stringArg(first, "next_cursor")
		if cursor == "" {
			t.Fatal("fixture needs a retained page for marker validation")
		}
		if err := change(); err != nil {
			t.Fatal(err)
		}
		next, err := rt.Call(ctx, "agentdock_context", map[string]any{
			"workdir": root, "max_response_bytes": 12000, "cursor": cursor,
		})
		if wantConflict {
			var failure *ToolError
			if !errors.As(err, &failure) || failure.Code != "CONTEXT_REVISION_CHANGED" {
				t.Fatalf("changed marker boundary did not reject the retained cursor: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("unchanged marker boundary invalidated the retained cursor: %v", err)
		}
		firstMeta, nextMeta := modelObject(first["structured_output"]), modelObject(next["structured_output"])
		if stringArg(nextMeta, "snapshot_id") != stringArg(firstMeta, "snapshot_id") || integerArgument(nextMeta["page_index"]) != integerArgument(firstMeta["page_index"])+1 {
			t.Fatalf("marker bookkeeping rebuilt or skipped the original snapshot: %#v", nextMeta)
		}
	}
	for _, test := range []struct {
		name         string
		initial      string
		change       func() error
		wantConflict bool
	}{
		{"marker_removed", "directory", func() error { return os.RemoveAll(git) }, true},
		{"marker_created", "", func() error { return os.WriteFile(git, []byte("gitdir: worktree-before"), 0600) }, true},
		{"gitfile_bookkeeping", "file", func() error { return os.WriteFile(git, []byte("gitdir: worktree-after-with-a-different-size"), 0600) }, false},
		{"marker_type_changed", "file", func() error {
			if err := os.Remove(git); err != nil {
				return err
			}
			return os.Mkdir(git, 0700)
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.RemoveAll(git); err != nil {
				t.Fatal(err)
			}
			var err error
			switch test.initial {
			case "directory":
				err = os.Mkdir(git, 0700)
			case "file":
				err = os.WriteFile(git, []byte("gitdir: worktree-before"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			checkMarker(t, test.change, test.wantConflict)
		})
	}
	t.Run("marker_symlink_target", func(t *testing.T) {
		// kqueue can follow a symlink while watching directory members; deleting
		// its target may invalidate watcher health. That must reject old cursors.
		// Keep the source fixed here to isolate the marker's own Lstat identity,
		// then verify source invalidation separately through the same paging path.
		tracker := replaceContextTracker(t, &rt.contextSnapshots.ruleFiles)
		if err := os.RemoveAll(git); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		if err := os.Symlink(target, git); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		// The marker is still a symlink after its unrelated target disappears.
		checkMarker(t, func() error { return os.Remove(target) }, false)
		checkMarker(t, func() error {
			tracker.generation.Add(1)
			return nil
		}, true)
	})
}
