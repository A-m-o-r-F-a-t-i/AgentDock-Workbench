package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSameFilesystemPathResolvesPlatformAliases(t *testing.T) {
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		if !sameFilesystemPath(strings.ToUpper(root), strings.ToLower(root)) {
			t.Fatalf("case-insensitive aliases were treated as different: %q", root)
		}
		return
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if !sameFilesystemPath(filepath.Join(alias, "missing"), filepath.Join(root, "missing")) {
		t.Fatalf("filesystem-resolved aliases were treated as different: %q and %q", alias, root)
	}
	store, err := New(filepath.Join(alias, "home"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := writePluginPackage(t, t.TempDir(), "alias-demo", "1.0.0", []string{"alias-skill"}, nil)
	if _, err := store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	member, releaseRead, err := store.AcquireSkillMember(t.Context(), "alias-demo", "alias-skill")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRead()
	commandRoot, releaseCommand, err := store.CreateSkillRuntime(t.Context(), member)
	if err != nil {
		t.Fatalf("Skill generation with a filesystem alias could not execute: %v", err)
	}
	defer releaseCommand()
	if sameFilesystemPath(commandRoot, member.Path) {
		t.Fatal("Skill command uses its immutable resource directory")
	}
	if _, err := os.Stat(filepath.Join(commandRoot, "SKILL.md")); err != nil {
		t.Fatalf("Skill command directory lost its selected document: %v", err)
	}
}
