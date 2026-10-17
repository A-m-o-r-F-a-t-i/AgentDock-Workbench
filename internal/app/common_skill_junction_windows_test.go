//go:build windows

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// createCommonSkillJunctionForTest creates only a directory junction beneath this
// test's owned TempDir. Paths are child-process environment values, quoted in a
// fixed cmd command; delayed expansion and AutoRun are disabled.
func createCommonSkillJunctionForTest(t *testing.T, owner, link, target string) {
	t.Helper()
	for _, path := range []string{link, target} {
		rel, err := filepath.Rel(owner, path)
		if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsAny(path, "\x00\r\n\"") {
			t.Fatalf("junction path is not an owned temporary child: %q", path)
		}
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("junction destination must be absent: %q, %v", link, err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("junction target must be an existing temporary directory: %q, %v", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			return
		} else if err != nil {
			t.Errorf("inspect temporary junction during cleanup: %v", err)
			return
		}
		if !isCommonSkillDirectoryJunctionForTest(t, link) {
			t.Errorf("temporary junction was replaced by another entry; refusing to remove %q", link)
			return
		}
		// os.Remove removes this reparse-point entry. Never RemoveAll(link),
		// walk through it, or delete its target as part of junction cleanup.
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove temporary junction: %v", err)
		}
	})
	commandPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if !filepath.IsAbs(commandPath) {
		t.Fatal("SystemRoot must identify the native Windows command interpreter")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, commandPath)
	cmd.Args = nil
	cmd.Env = append(os.Environ(), "AGENTDOCK_COMMON_JUNCTION_LINK="+link, "AGENTDOCK_COMMON_JUNCTION_TARGET="+target)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(commandPath) + ` /d /v:off /s /c "mklink /J "%AGENTDOCK_COMMON_JUNCTION_LINK%" "%AGENTDOCK_COMMON_JUNCTION_TARGET%""`}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create temporary directory junction: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	if !isCommonSkillDirectoryJunctionForTest(t, link) {
		t.Fatal("mklink /J did not create an NTFS mount-point reparse entry")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	resolved, resolveErr := filepath.EvalSymlinks(link)
	t.Logf("junction diagnostic: mode=%s EvalSymlinks=%q error=%v", info.Mode(), resolved, resolveErr)
}

func isCommonSkillDirectoryJunctionForTest(t *testing.T, path string) bool {
	t.Helper()
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var data syscall.Win32finddata
	handle, err := syscall.FindFirstFile(pointer, &data)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.FindClose(handle); err != nil {
		t.Fatal(err)
	}
	const mountPointTag = uint32(0xa0000003)
	return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && data.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 && data.Reserved0 == mountPointTag
}

// Common keeps its existing directory-junction read behavior under the default
// Go settings. The watcher must track the same target that the scanner reads.
func TestCommonContextCacheTracksJunctionReplacement(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(owner, "Common home & spaces")
	boundary := filepath.Join(owner, "external boundary with spaces")
	target := filepath.Join(boundary, "agents target A")
	retired := filepath.Join(boundary, "retired target A")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	setUserHomeForTest(t, home)
	writeCommonSkillForTest(t, filepath.Join(target, "skills"), "junction-skill", "junction-skill", "before")
	alias := filepath.Join(home, ".agents")
	createCommonSkillJunctionForTest(t, owner, alias, target)
	selected := filepath.Join(alias, "skills")
	resolved, resolveErr := filepath.EvalSymlinks(selected)
	t.Logf("Common selected-root diagnostic: EvalSymlinks=%q error=%v", resolved, resolveErr)
	rt := newContextRegressionRuntime(t)
	files := rt.contextSnapshots.commonFiles
	readIndex := func(stage, description string) {
		t.Helper()
		index, _, err := rt.cachedCommonSkillIndex(t.Context())
		if err != nil || index == nil || index.Total != 1 || len(index.Items) != 1 || index.Items[0].Description != description {
			t.Fatalf("%s Common index: %#v error=%v want=%q", stage, index, err, description)
		}
	}
	observeChange := func(before, stage string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for files.Revision() == before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if files.Revision() == before {
			t.Errorf("%s did not reach the Common watcher: revision=%s", stage, before)
		}
	}
	readIndex("initial external target", "before")
	rootBefore, err := os.Lstat(selected)
	if err != nil {
		t.Fatal(err)
	}
	before := files.Revision()
	document := filepath.Join(target, "skills", "junction-skill", "SKILL.md")
	rewriteRevalidationFixture(t, document, "before", "first!")
	rootAfter, err := os.Lstat(selected)
	if err != nil || rootAfter.Size() != rootBefore.Size() || rootAfter.ModTime() != rootBefore.ModTime() || rootAfter.Mode() != rootBefore.Mode() {
		t.Fatalf("in-place edit changed the selected root stamp: before=%#v after=%#v error=%v", rootBefore, rootAfter, err)
	}
	observeChange(before, "in-place same-size same-mtime target write")
	readIndex("in-place external target edit", "first!")
	before = files.Revision()
	if err := os.Rename(target, retired); err != nil {
		t.Fatalf("watch prevented external target rename: %v", err)
	}
	observeChange(before, "external target rename")
	before = files.Revision()
	writeCommonSkillForTest(t, filepath.Join(target, "skills"), "junction-skill", "junction-skill", "fresh!")
	observeChange(before, "external target reconstruction")
	readIndex("reconstructed external target", "fresh!")
	document = filepath.Join(target, "skills", "junction-skill", "SKILL.md")
	before = files.Revision()
	rewriteRevalidationFixture(t, document, "fresh!", "after!")
	observeChange(before, "same-size same-mtime target write")
	readIndex("same-stamp external target edit", "after!")
	if data, err := os.ReadFile(filepath.Join(retired, "skills", "junction-skill", "SKILL.md")); err != nil || !strings.Contains(string(data), "description: first!\n") {
		t.Fatalf("retired target changed while editing its replacement: %q %v", data, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatalf("remove only the temporary junction: %v", err)
	}
	if data, err := os.ReadFile(document); err != nil || !strings.Contains(string(data), "description: after!\n") {
		t.Fatalf("junction removal touched its external target: %q %v", data, err)
	}
}
