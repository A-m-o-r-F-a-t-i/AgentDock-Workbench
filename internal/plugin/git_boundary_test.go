package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecurityPluginGitRejectsUnspecifiedOperations(t *testing.T) {
	for _, args := range [][]string{{"config", "core.hooksPath", "elsewhere"}, {"fetch", "--depth=1", "origin", "--upload-pack=unexpected"}, {"rev-parse", "--show-toplevel"}, {"cat-file", "-e", "--help"}} {
		if _, err := runPluginGit(context.Background(), t.TempDir(), args...); err == nil {
			t.Fatalf("accepted unspecified operation: %v", args)
		}
	}
}
func TestSecurityPluginGitDiagnosticBound(t *testing.T) {
	var output pluginGitOutput
	data := []byte(strings.Repeat("x", 128<<10))
	for i := 0; i < 3; i++ {
		if n, err := output.Write(data); err != nil || n != len(data) {
			t.Fatal("output did not fully drain")
		}
	}
	if len(output.bytes) != 64<<10 || !output.truncated || !strings.Contains(output.String(), "truncated") {
		t.Fatal("diagnostic output not bounded/annotated")
	}
}
func TestSecurityGitArchiveRejectedEntryDoesNotWaitForCallerDeadline(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if err := os.Symlink("outside", filepath.Join(root, "00-link")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "zz-tail"), make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "init")
	runGitTest(t, root, "add", "--", "00-link", "zz-tail")
	runGitTest(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "archive fixture")
	revision := strings.TrimSpace(runGitTest(t, root, "rev-parse", "HEAD"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := extractGitArchive(ctx, filepath.Join(root, ".git"), revision, t.TempDir()); err == nil {
		t.Fatal("accepted unsupported archive symlink")
	}
	if ctx.Err() != nil {
		t.Fatalf("rejected archive only returned after the caller deadline: %v", ctx.Err())
	}
}
