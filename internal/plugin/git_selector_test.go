package plugin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
