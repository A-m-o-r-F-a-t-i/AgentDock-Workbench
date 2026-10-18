package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func registryFixture(t *testing.T) (*Registry, Record) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	registry, err := NewRegistry(home, root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Select(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return registry, record
}

func TestWorkspaceRoutingAllKindsAndExplicitExternalTarget(t *testing.T) {
	registry, record := registryFixture(t)
	ctx := context.Background()
	taskID := "tsk_1234567890abcdef"
	for _, test := range []struct{ kind, path, want string }{
		{"source", "src/main.go", filepath.Join(record.Root, "src", "main.go")},
		{"artifact", "release.exe", filepath.Join(record.ArtifactRoot, taskID, "release.exe")},
		{"scratch", "probe.log", filepath.Join(record.ScratchRoot, taskID, "probe.log")},
		{"cache", "dependency.zip", filepath.Join(record.CacheRoot, "dependency.zip")},
	} {
		target, err := ResolveTarget(record, TargetRequest{Kind: test.kind, TaskID: taskID, Path: test.path})
		if err != nil || target.ResolvedPath != test.want || target.WorkspaceID != record.ID {
			t.Fatalf("%s: %+v %v", test.kind, target, err)
		}
	}
	externalParent := t.TempDir()
	canonicalParent, err := filepath.EvalSymlinks(externalParent)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(externalParent, "explicit-report.txt")
	target, err := ResolveTarget(record, TargetRequest{Kind: "external", ExternalPath: external})
	if err != nil || target.ResolvedPath != filepath.Join(canonicalParent, "explicit-report.txt") {
		t.Fatalf("external %+v %v", target, err)
	}
	if _, err = ResolveTarget(record, TargetRequest{Kind: "external", Path: external}); err == nil {
		t.Fatal("implicit external target accepted")
	}
	if _, err = ResolveTarget(record, TargetRequest{Kind: "external", ExternalPath: external, Path: "other.txt"}); err == nil {
		t.Fatal("undeclared external target accepted")
	}
	selected, err := registry.Select(ctx, "", "")
	if err != nil || selected.ID != record.ID {
		t.Fatal("external routing changed the default workspace")
	}
}

func canonicalRegistryTestDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkspaceRegistryUsesSeparateDefaultRoots(t *testing.T) {
	home, root := t.TempDir(), canonicalRegistryTestDir(t)
	artifact, scratch, cache := filepath.Join(canonicalRegistryTestDir(t), "artifacts"), filepath.Join(canonicalRegistryTestDir(t), "scratch"), filepath.Join(canonicalRegistryTestDir(t), "cache")
	registry, err := NewRegistryWithRoots(home, root, DefaultRoots{ArtifactRoot: artifact, ScratchRoot: scratch, CacheRoot: cache})
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Select(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameNativePath(record.Root, root) || !sameNativePath(record.ArtifactRoot, filepath.Join(artifact, record.ID)) ||
		!sameNativePath(record.ScratchRoot, filepath.Join(scratch, record.ID)) || !sameNativePath(record.CacheRoot, cache) {
		t.Fatalf("separate roots were not preserved: expected root=%q artifact=%q scratch=%q cache=%q actual=%+v", root, artifact, scratch, cache, record)
	}
}

func TestWorkspaceRegistryMigratesLegacyDefaultWithoutChangingIdentity(t *testing.T) {
	home, legacyRoot, currentRoot := t.TempDir(), canonicalRegistryTestDir(t), canonicalRegistryTestDir(t)
	legacy, err := NewRegistry(home, legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := legacy.Select(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	artifact, scratch, cache := filepath.Join(canonicalRegistryTestDir(t), "artifacts"), filepath.Join(canonicalRegistryTestDir(t), "scratch"), filepath.Join(canonicalRegistryTestDir(t), "cache")
	migrated, err := NewRegistryWithRoots(home, currentRoot, DefaultRoots{
		ArtifactRoot: artifact,
		ScratchRoot:  scratch,
		CacheRoot:    cache,
		LegacyRoot:   legacyRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := migrated.Select(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != previous.ID || !sameNativePath(current.Root, currentRoot) || current.RulesRevision != previous.RulesRevision+1 {
		t.Fatalf("legacy workspace identity was not migrated in place: previous=%+v current=%+v", previous, current)
	}
	if !sameNativePath(current.ArtifactRoot, filepath.Join(artifact, current.ID)) ||
		!sameNativePath(current.ScratchRoot, filepath.Join(scratch, current.ID)) || !sameNativePath(current.CacheRoot, cache) {
		t.Fatalf("migrated workspace roots are incorrect: expected artifact=%q scratch=%q cache=%q actual=%+v", artifact, scratch, cache, current)
	}
	records, defaultID, err := migrated.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || defaultID != previous.ID {
		t.Fatalf("migration created duplicate workspace records: records=%+v default=%s", records, defaultID)
	}
}

func TestWorkspaceRejectsTraversalAndNoImplicitCWD(t *testing.T) {
	_, record := registryFixture(t)
	for _, request := range []TargetRequest{{Path: "../outside.txt"}, {Path: filepath.Join(t.TempDir(), "elsewhere.txt")}, {Kind: "artifact", Path: "file.exe"}, {Kind: "scratch", TaskID: "../escape"}, {Kind: "artifact", TaskID: "tsk_safe", Path: "../other-task/file"}} {
		if _, err := ResolveTarget(record, request); err == nil {
			t.Fatalf("invalid target accepted: %+v", request)
		}
	}
	for _, root := range []string{"", ".", "relative/root"} {
		if _, err := NewRegistry(t.TempDir(), root); !errors.Is(err, ErrWorkspaceRequired) {
			t.Fatalf("implicit root %q: %v", root, err)
		}
	}
	if runtime.GOOS == "windows" {
		if _, err := ResolveTarget(record, TargetRequest{Path: filepath.Join(record.Root, "file.txt:secret")}); err == nil {
			t.Fatal("alternate data stream accepted")
		}
	}
}

func TestResolveCommandDirectoryRejectsTraversalBeforeCreation(t *testing.T) {
	_, record := registryFixture(t)
	taskRoot := filepath.Join(record.ScratchRoot, "tsk_safe")
	escaped := filepath.Join(record.ScratchRoot, "escaped")
	_, err := ResolveCommandDirectory(record, TargetRequest{
		Kind:   "scratch",
		TaskID: "tsk_safe",
		Path:   filepath.Join("..", "escaped"),
	})
	if !errors.Is(err, ErrWorkspaceBoundary) {
		t.Fatalf("ResolveCommandDirectory() traversal error = %v", err)
	}
	for _, path := range []string{taskRoot, escaped} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rejected traversal created %q: %v", path, statErr)
		}
	}
}

func TestWorkspaceRegistryRevisionConcurrentUpdates(t *testing.T) {
	registry, record := registryFixture(t)
	ctx := context.Background()
	second, err := NewRegistry(registry.home, record.Root)
	if err != nil {
		t.Fatal(err)
	}
	revision := record.RulesRevision
	errorsSeen := make(chan error, 2)
	var wait sync.WaitGroup
	for _, writer := range []*Registry{registry, second} {
		wait.Add(1)
		go func(writer *Registry) {
			defer wait.Done()
			_, err := writer.Register(ctx, RegisterInput{WorkspaceID: record.ID, ExpectedRevision: &revision, Name: "updated"})
			errorsSeen <- err
		}(writer)
	}
	wait.Wait()
	close(errorsSeen)
	conflicts, successes := 0, 0
	for err := range errorsSeen {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrWorkspaceConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent overwrite: successes=%d conflicts=%d", successes, conflicts)
	}
	selected, err := second.Select(ctx, record.ID, "")
	if err != nil || selected.RulesRevision != revision+1 {
		t.Fatalf("revision %+v %v", selected, err)
	}
}

func TestWorkspaceContextSelectionNeverMutatesDefaultAndProjectMapping(t *testing.T) {
	registry, original := registryFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	record, err := registry.EnsureRoot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := registry.Select(ctx, "", "")
	if err != nil || selected.ID != original.ID {
		t.Fatal("context changed global default")
	}
	revision := record.RulesRevision
	updated, err := registry.Register(ctx, RegisterInput{WorkspaceID: record.ID, ExpectedRevision: &revision, Project: "firmware"})
	if err != nil {
		t.Fatal(err)
	}
	match, err := registry.Select(ctx, "", "firmware")
	if err != nil || match.ID != updated.ID {
		t.Fatalf("project mapping %+v %v", match, err)
	}
	reused, err := registry.EnsureRoot(ctx, root)
	if err != nil || reused.Project != "firmware" || reused.RulesRevision != updated.RulesRevision {
		t.Fatal("context rewrote registered rules")
	}
	other, err := registry.Register(ctx, RegisterInput{Root: t.TempDir(), Project: "firmware"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Select(ctx, "", "firmware"); err == nil {
		t.Fatal("ambiguous project guessed")
	}
	if exact, err := registry.Select(ctx, other.ID, "firmware"); err != nil || exact.ID != other.ID {
		t.Fatal("explicit workspace did not take priority")
	}
}

func TestWorkspaceSymlinkEscapeIsRejected(t *testing.T) {
	_, record := registryFixture(t)
	outside := t.TempDir()
	link := filepath.Join(record.Root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("creating symlink requires platform permission: %v", err)
	}
	if _, err := ResolveTarget(record, TargetRequest{Path: "escape/new.txt"}); !errors.Is(err, ErrWorkspaceBoundary) {
		t.Fatalf("symlink escaped: %v", err)
	}
}

func TestWorkspaceRegistrationValidationPrecedesCreatingDirectory(t *testing.T) {
	registry, _ := registryFixture(t)
	root := filepath.Join(t.TempDir(), "new-root")
	if _, err := registry.Register(context.Background(), RegisterInput{Root: root, CreateRoot: true, Kind: "invalid"}); err == nil {
		t.Fatal("invalid registration accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid registration created the source directory")
	}
	record, err := registry.Register(context.Background(), RegisterInput{Root: root, CreateRoot: true, Name: "created"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := ResolveCommandDirectory(record, TargetRequest{Kind: "scratch", TaskID: "tsk_test"})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target.ResolvedPath); err != nil || !info.IsDir() {
		t.Fatalf("scratch command directory missing: %v", err)
	}
}

func TestWorkspaceRegistryCorruptionIsNotSilentlyReplaced(t *testing.T) {
	registry, record := registryFixture(t)
	path := filepath.Join(registry.home, "workspaces.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state registryState
	if err = json.Unmarshal(before, &state); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = NewRegistry(registry.home, record.Root); err == nil {
		t.Fatal("corrupt registry silently reset")
	}
	after, _ := os.ReadFile(path)
	if string(after) != "corrupt" {
		t.Fatal("registry corruption was overwritten")
	}
}

func syntheticWorkspaceRecord(registry *Registry, id, root, project string, timestamp time.Time) Record {
	return Record{
		ID: id, Name: filepath.Base(root), Kind: "repository", Project: project,
		Runtime: HostRuntime(), Root: root, DefaultWorkdir: ".",
		ArtifactRoot:  filepath.Join(registry.artifactRoot, id),
		ScratchRoot:   filepath.Join(registry.scratchRoot, id),
		CacheRoot:     registry.cacheRoot,
		RulesRevision: 1, CreatedAt: timestamp, UpdatedAt: timestamp,
	}
}

func TestWorkspaceRegistryPrunesUnavailableImplicitContextRecords(t *testing.T) {
	registry, defaultRecord := registryFixture(t)
	state, err := registry.read()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	now := time.Now().UTC()
	explicitID := "wsp_0000000000000abc"
	explicit := syntheticWorkspaceRecord(registry, explicitID, filepath.Join(parent, "missing-explicit"), "", now)
	state.Records = append(state.Records, explicit)
	for index := 0; len(state.Records) < workspacePruneThreshold; index++ {
		id := fmt.Sprintf("wsp_%016x", 0x1000+index)
		root := filepath.Join(parent, fmt.Sprintf("missing-context-%03d", index))
		state.Records = append(state.Records, syntheticWorkspaceRecord(registry, id, root, "", now.Add(time.Duration(index)*time.Second)))
		markImplicitWorkspace(&state, id)
	}
	if err := registry.write(state); err != nil {
		t.Fatal(err)
	}

	newRoot := t.TempDir()
	registered, err := registry.EnsureRoot(context.Background(), newRoot)
	if err != nil {
		t.Fatal(err)
	}
	records, defaultID, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundDefault, foundExplicit, foundRegistered := false, false, false
	for _, record := range records {
		foundDefault = foundDefault || record.ID == defaultRecord.ID
		foundExplicit = foundExplicit || record.ID == explicitID
		foundRegistered = foundRegistered || record.ID == registered.ID
		if record.Project == "" && strings.HasPrefix(record.Name, "missing-context-") {
			t.Fatalf("unavailable implicit Context workspace was retained: %+v", record)
		}
	}
	if !foundDefault || !foundExplicit || !foundRegistered || defaultID != defaultRecord.ID {
		t.Fatalf("pruning removed a protected workspace: default=%v explicit=%v registered=%v records=%+v", foundDefault, foundExplicit, foundRegistered, records)
	}
}

func TestWorkspaceExplicitRegistrationPromotesImplicitRoot(t *testing.T) {
	registry, _ := registryFixture(t)
	root := t.TempDir()
	implicit, err := registry.EnsureRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := registry.read()
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, id := range state.ImplicitIDs {
		marked = marked || id == implicit.ID
	}
	if !marked {
		t.Fatalf("EnsureRoot did not mark the implicit workspace: %+v", state)
	}

	explicit, err := registry.Register(context.Background(), RegisterInput{Root: root, Name: "explicit-no-project"})
	if err != nil || explicit.ID != implicit.ID {
		t.Fatalf("explicit registration did not reuse the implicit root: %+v %v", explicit, err)
	}
	state, err = registry.read()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range state.ImplicitIDs {
		if id == implicit.ID {
			t.Fatalf("explicit registration retained the implicit cleanup marker: %+v", state)
		}
	}
}

func TestWorkspaceRegistryExceedsLegacyCapacityWithoutDroppingExplicitRecords(t *testing.T) {
	registry, _ := registryFixture(t)
	state, err := registry.read()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	now := time.Now().UTC()
	for index := 0; len(state.Records) <= workspacePruneThreshold; index++ {
		id := fmt.Sprintf("wsp_%016x", 0x2000+index)
		root := filepath.Join(parent, fmt.Sprintf("explicit-%03d", index))
		state.Records = append(state.Records, syntheticWorkspaceRecord(registry, id, root, fmt.Sprintf("project-%03d", index), now))
	}
	if err := registry.write(state); err != nil {
		t.Fatalf("registry rejected %d explicit records at the legacy limit: %v", len(state.Records), err)
	}
	records, _, err := registry.List(context.Background())
	if err != nil || len(records) != len(state.Records) {
		t.Fatalf("registry did not preserve explicit records above the legacy limit: got=%d want=%d err=%v", len(records), len(state.Records), err)
	}
}
