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

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/plugin"
)

type revalidationContextFiles struct {
	generation atomic.Uint64
	clock      atomic.Uint64
	drift      atomic.Bool
	holdClock  atomic.Bool
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
	clock := f.clock.Load()
	if !f.holdClock.Load() {
		clock = f.clock.Add(1)
	}
	return source, fmt.Sprintf("%s-revalidate-%d", source, clock)
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

func TestContextRevalidationRefreshesDeliveredContext(t *testing.T) {
	for _, component := range []string{"rules", "common"} {
		for _, deliveredReference := range []bool{false, true} {
			name := fmt.Sprintf("%s/reference=%t", component, deliveredReference)
			t.Run(name, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				setUserHomeForTest(t, root)
				cfg := config.Config{AgentDockHome: filepath.Join(root, "home"), AgentDockDefaultDir: filepath.Join(root, "workspace")}
				if err := cfg.Normalize(); err != nil {
					t.Fatal(err)
				}
				path, before, after := filepath.Join(cfg.AgentDockDefaultDir, "AGENTS.md"), "rule-before", "rule-after!"
				if component == "common" {
					commonRoot := filepath.Join(root, ".agents", "skills")
					before, after = "common-before", "common-after!"
					writeCommonSkillForTest(t, commonRoot, "shared-refresh", "shared-refresh", before)
					path = filepath.Join(commonRoot, "shared-refresh", "SKILL.md")
				} else if err := os.WriteFile(path, []byte(before), 0600); err != nil {
					t.Fatal(err)
				}
				rt, err := newUnrestrictedTestRuntime(cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = rt.Close() })
				trackers := map[string]*revalidationContextFiles{
					"rules":  replaceContextTracker(t, &rt.contextSnapshots.ruleFiles),
					"skills": replaceContextTracker(t, &rt.contextSnapshots.skillFiles),
					"common": replaceContextTracker(t, &rt.contextSnapshots.commonFiles),
				}
				for _, tracker := range trackers {
					tracker.holdClock.Store(true)
				}
				source := activity.Source{Principal: "context-revalidation", Namespace: "mcp"}
				if deliveredReference {
					source.HostConversationID = "context-revalidation-reference"
				}
				ctx := WithModelProjection(activity.WithSource(t.Context(), source))
				args := map[string]any{"workdir": cfg.AgentDockDefaultDir, "max_response_bytes": 32000}
				readComplete := func() (Result, Result) {
					t.Helper()
					first, err := rt.Call(ctx, "agentdock_context", args)
					if err != nil {
						t.Fatal(err)
					}
					if first["has_more"] != true {
						return first, first
					}
					continuation := modelObject(modelObject(first["structured_output"])["continue_read"])
					var raw strings.Builder
					for pageIndex := 0; pageIndex < 16; pageIndex++ {
						if continuation["tool"] != "read_file" || modelObject(continuation["arguments"]) == nil {
							t.Fatalf("Context did not expose its exact source continuation: %#v", first)
						}
						page, err := rt.Call(ctx, "read_file", modelObject(continuation["arguments"]))
						if err != nil {
							t.Fatal(err)
						}
						raw.WriteString(stringArg(page, "content"))
						if page["has_more"] == false {
							if modelObject(page["delivery"])["sequence_complete"] != true {
								t.Fatal("Context source was not fully delivered")
							}
							canonical, err := decodeStructuredResult([]byte(raw.String()))
							if err != nil {
								t.Fatal(err)
							}
							return first, canonical
						}
						continuation = modelObject(page["continue_read"])
					}
					t.Fatal("Context source did not finish within its page bound")
					return nil, nil
				}
				first, canonical := readComplete()
				field := "instruction_files"
				if component == "common" {
					field = "common_skills"
				}
				if !strings.Contains(fmt.Sprint(canonical[field]), before) {
					t.Fatalf("initial complete Context omitted %s: %#v", before, canonical[field])
				}
				if deliveredReference {
					reference, err := rt.Call(ctx, "agentdock_context", args)
					if err != nil || modelObject(reference["projection"])["mode"] != "reference" {
						t.Fatalf("unchanged delivered Context did not become a reference: %#v %v", reference, err)
					}
				}
				stable, err := rt.contextSourceStamp(ctx, cfg.AgentDockDefaultDir, activity.Binding{})
				if err != nil {
					t.Fatal(err)
				}
				builds := rt.contextSnapshots.complete.Stats().Builds
				rewriteRevalidationFixture(t, path, before, after)
				trackers[component].clock.Add(1)
				nextStable, err := rt.contextSourceStamp(ctx, cfg.AgentDockDefaultDir, activity.Binding{})
				if err != nil || stable != nextStable {
					t.Fatalf("fixture must change only the revalidation epoch, not source metadata: %q => %q (%v)", stable, nextStable, err)
				}
				next, refreshed := readComplete()
				if next["unchanged"] == true || modelObject(next["projection"])["mode"] == "reference" || !strings.Contains(fmt.Sprint(refreshed[field]), after) {
					t.Fatalf("expired Context reused stale content/reference: component=%s projection=%#v content=%#v", component, next["projection"], refreshed[field])
				}
				if rt.contextSnapshots.complete.Stats().Builds <= builds || next["context_revision"] == first["context_revision"] {
					t.Fatal("Context refresh did not rebuild content and its revision")
				}
				trackers[component].clock.Add(1)
				unchanged, current := readComplete()
				if unchanged["context_revision"] != next["context_revision"] || !strings.Contains(fmt.Sprint(current[field]), after) {
					t.Fatal("clock-only revalidation changed the immutable Context content identity")
				}
			})
		}
	}
}
