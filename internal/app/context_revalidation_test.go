package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/plugin"
)

type revalidationContextFiles struct {
	generation atomic.Uint64
	clock      atomic.Uint64
	drift      atomic.Bool
}

func (*revalidationContextFiles) Add(string)                     {}
func (*revalidationContextFiles) Close()                         {}
func (*revalidationContextFiles) Sync(ctx context.Context) error { return ctx.Err() }
func (f *revalidationContextFiles) Revision() string {
	if f.drift.Load() {
		f.generation.Add(1)
	}
	return fmt.Sprintf("%d-unhealthy", f.generation.Load())
}
func (f *revalidationContextFiles) Revisions() (source, cache string) {
	source = f.Revision()
	return source, fmt.Sprintf("%s-revalidate-%d", source, f.clock.Add(1))
}

func replaceContextTracker(t *testing.T, target *contextFiles) *revalidationContextFiles {
	t.Helper()
	(*target).Close()
	tracker := &revalidationContextFiles{}
	*target = tracker
	return tracker
}

func rewriteRevalidationFixture(t *testing.T, path, before, after string) {
	t.Helper()
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), before) {
		t.Fatalf("fixture missing %q", before)
	}
	updated := strings.Replace(string(data), before, after, 1)
	if len(updated) != len(data) {
		t.Fatal("fixture must preserve size")
	}
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func TestContextRevalidationRulesCrossClockAndRefresh(t *testing.T) {
	rt := newContextRegressionRuntime(t)
	tracker := replaceContextTracker(t, &rt.contextSnapshots.ruleFiles)
	home, work := t.TempDir(), t.TempDir()
	path := filepath.Join(home, "AGENTS.md")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := agentinstructions.Options{Home: home, GlobalFile: path, Workdir: work, DefaultDir: work}
	first, err := rt.cachedInstructions(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(first.Files), "before") {
		t.Fatal("initial rules missing")
	}
	rewriteRevalidationFixture(t, path, "before", "after!")
	second, err := rt.cachedInstructions(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(second.Files), "after!") {
		t.Fatal("stale rules after cache refresh")
	}
	tracker.drift.Store(true)
	if _, err := rt.cachedInstructions(t.Context(), opts); !errors.Is(err, errContextVersionChanged) {
		t.Fatalf("rules accepted source drift: %v", err)
	}
}

func TestContextRevalidationCommonSkillsCrossClockAndRefresh(t *testing.T) {
	home := t.TempDir()
	setUserHomeForTest(t, home)
	root := filepath.Join(home, ".agents", "skills")
	writeCommonSkillForTest(t, root, "shared-clock", "shared-clock", "before")
	rt := newContextRegressionRuntime(t)
	tracker := replaceContextTracker(t, &rt.contextSnapshots.commonFiles)
	first, firstInfo, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Description != "before" {
		t.Fatalf("initial index: %#v", first)
	}
	rewriteRevalidationFixture(t, filepath.Join(root, "shared-clock", "SKILL.md"), "before", "after!")
	next, nextInfo, err := rt.cachedCommonSkillIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].Description != "after!" || firstInfo.BuildID == nextInfo.BuildID {
		t.Fatalf("stale common index: %#v", next)
	}
	tracker.drift.Store(true)
	if _, _, err := rt.cachedCommonSkillIndex(t.Context()); !errors.Is(err, errContextVersionChanged) {
		t.Fatalf("common index accepted source drift: %v", err)
	}
}

func TestContextRevalidationManagedSkillsCrossClock(t *testing.T) {
	rt := newContextRegressionRuntime(t)
	tracker := replaceContextTracker(t, &rt.contextSnapshots.skillFiles)
	directory := &plugin.Directory{Revision: "plugin-generation-1"}
	first, firstInfo, err := rt.contextSkillIndex(t.Context(), directory, true)
	if err != nil {
		t.Fatal(err)
	}
	next, nextInfo, err := rt.contextSkillIndex(t.Context(), directory, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(next) || firstInfo.BuildID == nextInfo.BuildID {
		t.Fatal("managed index did not rebuild across refresh boundary")
	}
	tracker.drift.Store(true)
	if _, _, err := rt.contextSkillIndex(t.Context(), directory, true); !errors.Is(err, errContextVersionChanged) {
		t.Fatalf("managed index accepted source drift: %v", err)
	}
}

func TestContextRevalidationCancelledRequests(t *testing.T) {
	rt := newContextRegressionRuntime(t)
	replaceContextTracker(t, &rt.contextSnapshots.ruleFiles)
	replaceContextTracker(t, &rt.contextSnapshots.commonFiles)
	replaceContextTracker(t, &rt.contextSnapshots.skillFiles)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := rt.cachedInstructions(ctx, agentinstructions.Options{Home: t.TempDir(), Workdir: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("rules cancellation: %v", err)
	}
	if _, _, err := rt.cachedCommonSkillIndex(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("common cancellation: %v", err)
	}
	if _, _, err := rt.contextSkillIndex(ctx, &plugin.Directory{}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("skill cancellation: %v", err)
	}
}
