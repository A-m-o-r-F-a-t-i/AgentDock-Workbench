package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRegistryCanonicalizesSeparateRootsThroughAlias(t *testing.T) {
	base := canonicalRegistryTestDir(t)
	root := filepath.Join(base, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Skipf("creating symlink requires platform permission: %v", err)
	}
	registry, err := NewRegistryWithRoots(t.TempDir(), filepath.Join(alias, "source"), DefaultRoots{
		ArtifactRoot: filepath.Join(alias, "artifacts"),
		ScratchRoot:  filepath.Join(alias, "scratch"),
		CacheRoot:    filepath.Join(alias, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Select(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ name, got, want string }{
		{"source", record.Root, root},
		{"artifact", record.ArtifactRoot, filepath.Join(base, "artifacts", record.ID)},
		{"scratch", record.ScratchRoot, filepath.Join(base, "scratch", record.ID)},
		{"cache", record.CacheRoot, filepath.Join(base, "cache")},
	} {
		if !sameNativePath(check.got, check.want) {
			t.Errorf("%s canonical root = %q, want %q", check.name, check.got, check.want)
		}
	}
	reused, err := registry.EnsureRoot(context.Background(), root)
	if err != nil || reused.ID != record.ID || reused.RulesRevision != record.RulesRevision {
		t.Fatalf("canonical path changed alias workspace identity: before=%+v after=%+v err=%v", record, reused, err)
	}
}
