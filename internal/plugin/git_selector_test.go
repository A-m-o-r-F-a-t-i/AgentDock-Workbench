package plugin

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitSelectorGrammar(t *testing.T) {
	for _, value := range []string{"main", "release/1.1.7", "fix/a-b_c+d", "功能/修复", strings.Repeat("a", 1024)} {
		if !gitBranchSelectorPattern.MatchString(value) {
			t.Errorf("expected supported selector %q", value)
		}
	}
	for _, value := range []string{"-option", "a b", "a\nb", "a\x00b", "a;command", "$(command)", "a|b", "a&b", "a`b", "a\"b"} {
		if gitBranchSelectorPattern.MatchString(value) {
			t.Errorf("accepted unsafe selector %q", value)
		}
	}
	for _, value := range []string{"HEAD^{commit}", strings.Repeat("a", 40) + "^{commit}"} {
		if !gitCommitExpressionPattern.MatchString(value) {
			t.Errorf("expected pinned commit expression %q", value)
		}
	}
	for _, value := range []string{"HEAD", "main^{commit}", "HEAD^{commit};command", "--help", strings.Repeat("a", 39) + "^{commit}"} {
		if gitCommitExpressionPattern.MatchString(value) {
			t.Errorf("accepted unpinned expression %q", value)
		}
	}
}

func TestGitSelectorRejectionPrecedesProcessCreation(t *testing.T) {
	root := t.TempDir()
	for _, ref := range []string{"--upload-pack=other", "main;other", strings.Repeat("a", 1025)} {
		for _, shallow := range []bool{false, true} {
			_, err := clonePluginGit(context.Background(), root, filepath.Join(root, "target.git"), ref, shallow)
			if err == nil || !strings.Contains(err.Error(), "invalid Git branch selector") {
				t.Fatalf("selector %q reached a Git process: %v", ref, err)
			}
		}
	}
	for _, args := range [][]string{{"cat-file", "-e", "HEAD^{commit};other"}, {"rev-parse", "--help"}, {"fetch", "--depth=1", "origin", "--upload-pack=other"}} {
		if _, err := runPluginGit(context.Background(), root, args...); err == nil || !strings.Contains(err.Error(), "unsupported Plugin Git operation") {
			t.Fatalf("unvalidated Git arguments %q reached a process: %v", args, err)
		}
	}
}

func TestGitPinnedFetchUsesOperandBoundary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	runGitTest(t, root, "init")
	runGitTest(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "pinned fetch fixture")
	revision := strings.TrimSpace(runGitTest(t, root, "rev-parse", "HEAD"))
	destination := filepath.Join(t.TempDir(), "repository.git")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := clonePluginGit(ctx, root, destination, "", true); err != nil {
		t.Fatalf("clone fixture: %v: %s", err, output)
	}
	if output, err := runPluginGit(ctx, destination, "fetch", "--depth=1", "origin", revision); err != nil {
		t.Fatalf("fetch exact commit: %v: %s", err, output)
	}
	if output, err := runPluginGit(ctx, destination, "cat-file", "-e", revision+"^{commit}"); err != nil {
		t.Fatalf("verify fetched commit: %v: %s", err, output)
	}
}
