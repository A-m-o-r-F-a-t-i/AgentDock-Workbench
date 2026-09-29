package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Every read crosses a cache refresh boundary, without inventing file events.
// This models slow scans independently of scheduler speed and the host watcher.
type revalidationDirectoryFiles struct {
	generation atomic.Uint64
	clock      atomic.Uint64
	drift      atomic.Bool
}

func (*revalidationDirectoryFiles) Add(string)                     {}
func (*revalidationDirectoryFiles) Close()                         {}
func (f *revalidationDirectoryFiles) Invalidate()                  { f.generation.Add(1) }
func (*revalidationDirectoryFiles) Sync(ctx context.Context) error { return ctx.Err() }
func (f *revalidationDirectoryFiles) Revisions() (source, cache string) {
	if f.drift.Load() {
		f.generation.Add(1)
	}
	source = fmt.Sprintf("%d-unhealthy", f.generation.Load())
	return source, fmt.Sprintf("%s-revalidate-%d", source, f.clock.Add(1))
}

func newRevalidationPluginStore(t *testing.T) (*Store, *revalidationDirectoryFiles, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "plugins", "clock-plugin")
	skill := filepath.Join(root, "skills", "clock-skill")
	state := filepath.Join(home, "plugins", ".state", "clock-plugin.json")
	for _, dir := range []string{skill, filepath.Dir(state)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(root, "plugin.json"): `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"clock-plugin","version":"1.0.0","description":"Clock fixture","extensions":{"io.github.uvwt.agentdock":{"heavy":true}}}`,
		filepath.Join(skill, "SKILL.md"):   "---\nname: clock-skill\ndescription: Clock fixture\n---\nfixture\n",
		state:                              `{"enabled":true }`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	store.snapshots.files.Close()
	tracker := &revalidationDirectoryFiles{}
	store.snapshots.files = tracker
	store.Invalidate()
	return store, tracker, state
}

func TestPluginSnapshotRevalidationCrossesCacheBoundaries(t *testing.T) {
	store, _, _ := newRevalidationPluginStore(t)
	first, firstInfo, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	member, ok := first.SkillMembership("clock-skill")
	if !ok || !member.Enabled {
		t.Fatalf("missing enabled member: %#v, definitions=%#v", member, first.Definitions())
	}
	second, secondInfo, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.sourceRevision != second.sourceRevision {
		t.Fatal("clock-only refresh changed source identity")
	}
	if first.Revision == second.Revision || firstInfo.BuildID == secondInfo.BuildID {
		t.Fatal("periodic rebuild was not published to downstream caches")
	}
}

func TestPluginSnapshotRevalidationObservesDisabledMember(t *testing.T) {
	store, _, state := newRevalidationPluginStore(t)
	first, _, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(state, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	next, _, err := store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	member, ok := next.SkillMembership("clock-skill")
	if !ok || member.Enabled {
		t.Fatalf("stale enabled state survived refresh: %#v", member)
	}
	if first.Revision == next.Revision {
		t.Fatal("new member state reused published revision")
	}
}

func TestPluginSnapshotRevalidationRejectsSourceDrift(t *testing.T) {
	store, tracker, _ := newRevalidationPluginStore(t)
	tracker.drift.Store(true)
	if _, _, err := store.Snapshot(t.Context()); !errors.Is(err, errDirectoryChanged) {
		t.Fatalf("real source changes were not rejected: %v", err)
	}
	tracker.drift.Store(false)
	if _, _, err := store.Snapshot(t.Context()); err != nil {
		t.Fatalf("stable retry: %v", err)
	}
}

func TestPluginSnapshotRevalidationRespectsCancellation(t *testing.T) {
	store, _, _ := newRevalidationPluginStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := store.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request returned %v", err)
	}
}
