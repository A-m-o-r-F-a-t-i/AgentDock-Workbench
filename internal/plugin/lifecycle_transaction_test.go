package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleRecoveryUsesFormalStateToResolveInterruptedActivation(t *testing.T) {
	for _, test := range []struct {
		name          string
		switchVisible bool
		commitState   bool
		wantVersion   string
	}{
		{name: "candidate staged", wantVersion: "1.0.0"},
		{name: "visible switched", switchVisible: true, wantVersion: "1.0.0"},
		{name: "formal state committed", switchVisible: true, commitState: true, wantVersion: "2.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, store, activation, transaction := preparePluginUpdateTransaction(t)
			if test.switchVisible {
				switchLifecycleVisibleForTest(t, store, &transaction)
			}
			if test.commitState {
				if err := store.writeNamedState(transaction.Name, *transaction.Candidate); err != nil {
					t.Fatal(err)
				}
				// Deliberately leave the journal at visible_switched. Recovery must
				// use formal-state CAS rather than guessing from a stale phase.
			}
			store.Close()

			recovered, err := New(home)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			definition, err := recovered.Get(activation.Name)
			if err != nil {
				t.Fatal(err)
			}
			if definition.Version != test.wantVersion {
				t.Fatalf("version = %q, want %q", definition.Version, test.wantVersion)
			}
			transactionPath, err := recovered.transactionPath(activation.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(transactionPath); !os.IsNotExist(err) {
				t.Fatalf("lifecycle journal remains: %v", err)
			}
			work, err := recovered.resolveLifecyclePath(transaction.WorkPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Fatalf("lifecycle work remains: %v", err)
			}
		})
	}
}

func TestLifecycleOwnerCASAndDigestMismatchPreserveRecoveryMaterial(t *testing.T) {
	t.Run("owner and formal state CAS", func(t *testing.T) {
		_, store, activation, transaction := preparePluginUpdateTransaction(t)
		defer store.Close()
		if _, err := store.FinalizeActivation(activation.Name, strings.Repeat("0", 32)); pluginErrorCode(err) != "PLUGIN_ACTIVATION_OWNER_MISMATCH" {
			t.Fatalf("wrong owner error = %v", err)
		}
		mutated := *transaction.Previous
		mutated.Enabled = !mutated.Enabled
		if err := store.writeNamedState(transaction.Name, mutated); err != nil {
			t.Fatal(err)
		}
		if _, err := store.FinalizeActivation(activation.Name, activation.OwnerID); pluginErrorCode(err) != "PLUGIN_STATE_CHANGED" {
			t.Fatalf("formal-state CAS error = %v", err)
		}
		if err := store.writeNamedState(transaction.Name, *transaction.Previous); err != nil {
			t.Fatal(err)
		}
		if err := store.AbortActivation(activation.Name, activation.OwnerID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("candidate digest mismatch", func(t *testing.T) {
		home, store, activation, transaction := preparePluginUpdateTransaction(t)
		generationPath, err := store.generationPath(activation.Name, activation.Generation)
		if err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(generationPath, ManifestFilename)
		file, err := os.OpenFile(manifest, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(" \n"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		store.Close()
		if _, err := New(home); pluginErrorCode(err) != "PLUGIN_RECOVERY_REQUIRED" {
			t.Fatalf("digest mismatch recovery error = %v", err)
		}
		transactionPath := filepath.Join(home, "plugins", ".state", "transactions", activation.Name+".json")
		if _, err := os.Stat(transactionPath); err != nil {
			t.Fatalf("recovery journal was not retained: %v", err)
		}
		if _, err := os.Stat(generationPath); err != nil {
			t.Fatalf("changed candidate generation was not retained: %v", err)
		}
		work := filepath.Join(home, "plugins", filepath.FromSlash(transaction.WorkPath))
		if _, err := os.Stat(work); err != nil {
			t.Fatalf("recovery work was not retained: %v", err)
		}
	})
}

func TestLifecycleWriterSerializesPluginOwnership(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := writePluginPackage(t, t.TempDir(), "first-owner", "1.0.0", []string{"shared-skill"}, nil)
	second := writePluginPackage(t, t.TempDir(), "second-owner", "1.0.0", []string{"shared-skill"}, nil)
	firstCandidate, err := store.PrepareSource(t.Context(), legacyLocalSourceRequest(first))
	if err != nil {
		t.Fatal(err)
	}
	defer firstCandidate.Cleanup()
	secondCandidate, err := store.PrepareSource(t.Context(), legacyLocalSourceRequest(second))
	if err != nil {
		t.Fatal(err)
	}
	defer secondCandidate.Cleanup()
	activation, err := store.BeginPreparedActivation(t.Context(), firstCandidate, firstCandidate.Review.ReviewToken, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginPreparedActivation(t.Context(), secondCandidate, secondCandidate.Review.ReviewToken, false, false); pluginErrorCode(err) != "PLUGIN_ACTIVATION_IN_PROGRESS" {
		t.Fatalf("second Plugin started while first ownership was pending: %v", err)
	}
	if _, err := store.FinalizeActivation(activation.Name, activation.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginPreparedActivation(t.Context(), secondCandidate, secondCandidate.Review.ReviewToken, false, false); pluginErrorCode(err) != "PLUGIN_MEMBER_CONFLICT" {
		t.Fatalf("second Plugin did not recheck committed ownership: %v", err)
	}
	if _, err := store.SetHeavy(activation.Name, false); err != nil {
		t.Fatalf("failed Begin retained its writer claim: %v", err)
	}
}

func TestLifecycleStartupWaitsForLiveOwner(t *testing.T) {
	home, store, activation, _ := preparePluginUpdateTransaction(t)
	defer store.Close()
	opened := make(chan error, 1)
	go func() {
		peer, err := New(home)
		if peer != nil {
			peer.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("another Store recovered a live lifecycle: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := store.FinalizeActivation(activation.Name, activation.OwnerID); err != nil {
		t.Fatalf("live owner could not finalize after another Store opened: %v", err)
	}
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another Store did not continue after the lifecycle completed")
	}
}

func TestLifecycleRollbackRetainsCandidateReaders(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "abort"
		if restart {
			name = "startup recovery"
		}
		t.Run(name, func(t *testing.T) {
			home, store, activation, _ := preparePluginUpdateTransaction(t)
			defer store.Close()
			pkg, release, err := store.AcquireActivationRuntime(t.Context(), activation.Name, activation.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if restart {
				store.Close()
				store.Close()
				recovered, err := New(home)
				if err != nil {
					t.Fatal(err)
				}
				defer recovered.Close()
			} else if err := store.AbortActivation(activation.Name, activation.OwnerID); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(pkg.Root, "skills", "transaction-skill", "candidate.txt"))
			if err != nil || string(content) != "candidate" {
				t.Fatalf("rollback removed a leased candidate: content=%q error=%v", content, err)
			}
			if _, err := store.loadLifecycleTransaction(activation.Name); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("resolved lifecycle remains pending: %v", err)
			}
			release()
			if _, err := os.Stat(pkg.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("abandoned candidate remains after its last reader: %v", err)
			}
		})
	}
}

func TestPluginSkillLeasePinsPreviousGenerationAcrossUpdate(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v1 := writePluginPackage(t, t.TempDir(), "lease-demo", "1.0.0", []string{"leased-skill"}, nil)
	if _, err := store.Install(v1, false); err != nil {
		t.Fatal(err)
	}
	previous, err := store.readNamedState("lease-demo")
	if err != nil {
		t.Fatal(err)
	}
	member, release, err := store.AcquireSkillMember(context.Background(), "lease-demo", "leased-skill")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	v2 := writePluginPackage(t, t.TempDir(), "lease-demo", "2.0.0", []string{"leased-skill"}, nil)
	if err := os.WriteFile(filepath.Join(v2, "skills", "leased-skill", "marker.txt"), []byte("new generation"), 0o600); err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := store.Install(v2, true); err != nil {
		release()
		t.Fatal(err)
	}
	previousPath, err := store.generationPath("lease-demo", previous.Generation)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := os.Stat(previousPath); err != nil {
		release()
		t.Fatalf("previous generation was removed while leased: %v", err)
	}
	if _, err := os.Stat(member.Path); err != nil {
		release()
		t.Fatalf("leased Skill path became unavailable: %v", err)
	}
	commandRoot, releaseCommand, err := store.CreateSkillRuntime(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCommand()
	if sameFilesystemPath(commandRoot, member.Path) {
		t.Fatal("Skill command uses its immutable generation as its working directory")
	}
	if _, err := os.Stat(filepath.Join(commandRoot, "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("Skill command switched to the newly installed generation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandRoot, "command-cache.txt"), []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if digest, exists, err := packageDigestAt(previousPath); err != nil || !exists || digest != previous.PackageDigest {
		t.Fatalf("Skill command changed its immutable generation: digest=%q exists=%v error=%v", digest, exists, err)
	}
	release()
	if _, err := os.Stat(previousPath); err != nil {
		t.Fatalf("command lease did not retain its selected generation: %v", err)
	}
	releaseCommand()
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Fatalf("previous generation remains after final reader release: %v", err)
	}
	definition, err := store.Get("lease-demo")
	if err != nil {
		t.Fatal(err)
	}
	if definition.Version != "2.0.0" {
		t.Fatalf("active version = %q", definition.Version)
	}
}

func TestStdioRuntimeGenerationCannotMutateInstalledDigest(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := writePluginPackage(t, t.TempDir(), "runtime-demo", "1.0.0", nil, []string{"runtime-mcp"})
	if _, err := store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	state, err := store.readNamedState("runtime-demo")
	if err != nil {
		t.Fatal(err)
	}
	generationPath, err := store.generationPath("runtime-demo", state.Generation)
	if err != nil {
		t.Fatal(err)
	}
	before, exists, err := packageDigestAt(generationPath)
	if err != nil || !exists {
		t.Fatalf("generation digest before runtime = %q %v", before, err)
	}
	pkg, release, err := store.RuntimeMCPPackage(context.Background(), "runtime-demo")
	if err != nil {
		t.Fatal(err)
	}
	if sameFilesystemPath(pkg.Root, generationPath) || !strings.Contains(filepath.ToSlash(pkg.Root), "/.runtime/runtime-demo/generation-") {
		release()
		t.Fatalf("stdio runtime root = %q", pkg.Root)
	}
	if err := os.WriteFile(filepath.Join(pkg.Root, "runtime-write.txt"), []byte("runtime mutation"), 0o600); err != nil {
		release()
		t.Fatal(err)
	}
	if err := store.PrepareMCPData(t.Context(), pkg.MCPServers["runtime-mcp"]); err != nil {
		release()
		t.Fatalf("runtime-generated file prevented MCP preparation: %v", err)
	}
	for _, root := range []string{pkg.Root, generationPath} {
		manifestPath := filepath.Join(root, ManifestFilename)
		original, err := os.ReadFile(manifestPath)
		if err != nil {
			release()
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, append(append([]byte(nil), original...), '\n'), 0o600); err != nil {
			release()
			t.Fatal(err)
		}
		prepareErr := store.PrepareMCPData(t.Context(), pkg.MCPServers["runtime-mcp"])
		restoreErr := os.WriteFile(manifestPath, original, 0o600)
		if prepareErr == nil || restoreErr != nil {
			release()
			t.Fatalf("changed package accepted or restore failed: root=%q prepare=%v restore=%v", root, prepareErr, restoreErr)
		}
	}
	after, exists, err := packageDigestAt(generationPath)
	if err != nil || !exists {
		release()
		t.Fatalf("generation digest after runtime = %q %v", after, err)
	}
	if after != before || after != state.PackageDigest {
		release()
		t.Fatalf("installed digest changed: before=%q after=%q state=%q", before, after, state.PackageDigest)
	}
	runtimeRoot := pkg.Root
	retained, releaseRetained, err := store.RuntimeMCPPackage(t.Context(), "runtime-demo")
	if err != nil {
		release()
		t.Fatal(err)
	}
	if retained.Root != runtimeRoot {
		release()
		releaseRetained()
		t.Fatalf("unchanged generation moved runtime root: %q != %q", retained.Root, runtimeRoot)
	}
	release()
	if _, err := os.Stat(runtimeRoot); err != nil {
		releaseRetained()
		t.Fatalf("runtime generation disappeared while retained: %v", err)
	}
	releaseRetained()
	if _, err := os.Stat(runtimeRoot); err != nil {
		t.Fatalf("active runtime cache disappeared after final reader release: %v", err)
	}
	store.Close()
	reopened, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reused, releaseReused, err := reopened.RuntimeMCPPackage(t.Context(), "runtime-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseReused()
	if reused.Root != runtimeRoot {
		t.Fatalf("active runtime cache was copied again: %q != %q", reused.Root, runtimeRoot)
	}
	if content, err := os.ReadFile(filepath.Join(reused.Root, "runtime-write.txt")); err != nil || string(content) != "runtime mutation" {
		t.Fatalf("runtime-generated cache was not reused: content=%q error=%v", content, err)
	}
}

func preparePluginUpdateTransaction(t *testing.T) (string, *Store, ActivationCandidate, LifecycleTransaction) {
	t.Helper()
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	v1 := writePluginPackage(t, t.TempDir(), "transaction-demo", "1.0.0", []string{"transaction-skill"}, nil)
	if _, err := store.Install(v1, false); err != nil {
		store.Close()
		t.Fatal(err)
	}
	v2 := writePluginPackage(t, t.TempDir(), "transaction-demo", "2.0.0", []string{"transaction-skill"}, nil)
	if err := os.WriteFile(filepath.Join(v2, "skills", "transaction-skill", "candidate.txt"), []byte("candidate"), 0o600); err != nil {
		store.Close()
		t.Fatal(err)
	}
	candidate, err := store.PrepareSource(context.Background(), legacyLocalSourceRequest(v2))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	activation, err := store.BeginPreparedActivation(context.Background(), candidate, candidate.Review.ReviewToken, true, false)
	candidate.Cleanup()
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	transaction, err := store.loadLifecycleTransaction(activation.Name)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return home, store, activation, transaction
}

func switchLifecycleVisibleForTest(t *testing.T, store *Store, transaction *LifecycleTransaction) {
	t.Helper()
	work, err := store.resolveLifecyclePath(transaction.WorkPath)
	if err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(store.root, transaction.Name)
	backup := filepath.Join(work, "previous")
	staged := filepath.Join(work, "visible")
	if err := os.Rename(visible, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, visible); err != nil {
		t.Fatal(err)
	}
	transaction.Phase = lifecyclePhaseVisibleSwitched
	if err := store.saveLifecycleTransaction(*transaction); err != nil {
		t.Fatal(err)
	}
}

func pluginErrorCode(err error) string {
	var pluginErr *Error
	if errors.As(err, &pluginErr) {
		return pluginErr.Code
	}
	return ""
}

func TestLifecycleRecoveryResolvesInterruptedRemoval(t *testing.T) {
	for _, test := range []struct {
		name        string
		moveVisible bool
		commitState bool
		wantPresent bool
	}{
		{name: "removal prepared", wantPresent: true},
		{name: "visible removal staged", moveVisible: true, wantPresent: true},
		{name: "formal removal committed", moveVisible: true, commitState: true, wantPresent: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			store, err := New(home)
			if err != nil {
				t.Fatal(err)
			}
			source := writePluginPackage(t, t.TempDir(), "remove-demo", "1.0.0", []string{"remove-skill"}, nil)
			if _, err := store.Install(source, false); err != nil {
				store.Close()
				t.Fatal(err)
			}
			activation, err := store.BeginRemoval(context.Background(), "remove-demo")
			if err != nil {
				store.Close()
				t.Fatal(err)
			}
			transaction, err := store.loadLifecycleTransaction(activation.Name)
			if err != nil {
				store.Close()
				t.Fatal(err)
			}
			if test.moveVisible {
				work, err := store.resolveLifecyclePath(transaction.WorkPath)
				if err != nil {
					store.Close()
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(store.root, activation.Name), filepath.Join(work, "previous")); err != nil {
					store.Close()
					t.Fatal(err)
				}
				transaction.Phase = lifecyclePhaseVisibleSwitched
				if err := store.saveLifecycleTransaction(transaction); err != nil {
					store.Close()
					t.Fatal(err)
				}
			}
			if test.commitState {
				if err := os.Remove(filepath.Join(store.root, ".state", activation.Name+".json")); err != nil {
					store.Close()
					t.Fatal(err)
				}
			}
			store.Close()

			recovered, err := New(home)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			_, getErr := recovered.Get(activation.Name)
			if test.wantPresent && getErr != nil {
				t.Fatalf("Plugin should have been restored: %v", getErr)
			}
			if !test.wantPresent && pluginErrorCode(getErr) != "PLUGIN_NOT_FOUND" {
				t.Fatalf("removed Plugin lookup error = %v", getErr)
			}
			transactionPath, err := recovered.transactionPath(activation.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(transactionPath); !os.IsNotExist(err) {
				t.Fatalf("removal transaction remains: %v", err)
			}
		})
	}
}

func TestPluginSkillLeasePinsGenerationAcrossRemoval(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := writePluginPackage(t, t.TempDir(), "remove-lease-demo", "1.0.0", []string{"remove-lease-skill"}, nil)
	if _, err := store.Install(source, false); err != nil {
		t.Fatal(err)
	}
	state, err := store.readNamedState("remove-lease-demo")
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := store.AcquireSkillMember(context.Background(), "remove-lease-demo", "remove-lease-skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("remove-lease-demo"); err != nil {
		release()
		t.Fatal(err)
	}
	generationPath, err := store.generationPath("remove-lease-demo", state.Generation)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := os.Stat(generationPath); err != nil {
		release()
		t.Fatalf("removed Plugin generation disappeared while leased: %v", err)
	}
	release()
	if _, err := os.Stat(generationPath); !os.IsNotExist(err) {
		t.Fatalf("removed Plugin generation remains after release: %v", err)
	}
}

func TestPluginMCPLeasePinsPreviousGenerationAcrossUpdate(t *testing.T) {
	home := t.TempDir()
	store, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	v1 := writePluginPackage(t, t.TempDir(), "mcp-lease-demo", "1.0.0", nil, []string{"leased-mcp"})
	if _, err := store.Install(v1, false); err != nil {
		t.Fatal(err)
	}
	previous, err := store.readNamedState("mcp-lease-demo")
	if err != nil {
		t.Fatal(err)
	}
	pkg, release, err := store.RuntimeMCPPackage(context.Background(), "mcp-lease-demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pkg.MCPServers["leased-mcp"]; !ok {
		release()
		t.Fatalf("runtime MCP package = %#v", pkg.MCPServers)
	}
	v2 := writePluginPackage(t, t.TempDir(), "mcp-lease-demo", "2.0.0", nil, []string{"leased-mcp"})
	if err := os.WriteFile(filepath.Join(v2, "mcp-generation.txt"), []byte("new generation"), 0o600); err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := store.Install(v2, true); err != nil {
		release()
		t.Fatal(err)
	}
	previousPath, err := store.generationPath("mcp-lease-demo", previous.Generation)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if _, err := os.Stat(previousPath); err != nil {
		release()
		t.Fatalf("previous MCP generation was removed while leased: %v", err)
	}
	runtimeRoot := pkg.Root
	release()
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Fatalf("previous MCP generation remains after release: %v", err)
	}
	if _, err := os.Stat(runtimeRoot); !os.IsNotExist(err) {
		t.Fatalf("old MCP runtime generation remains after release: %v", err)
	}
}
