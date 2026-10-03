//go:build windows

package desktopruntime

import (
	"context"
	"testing"
)

func TestRepairConfiguredTailscaleRefreshesWithoutRestartingCore(t *testing.T) {
	runtime, system := newPublicAccessTestSystem(t, "none")
	if err := system.configure(runtime, true); err != nil {
		t.Fatal(err)
	}
	runtime, err := loadTunnelRuntime(runtime.root)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := loadTailscaleState(runtime.root)
	if err != nil || beforeState == nil || beforeState.VerifiedAt == nil {
		t.Fatalf("missing verified setup state: %+v %v", beforeState, err)
	}
	hostPort := system.fake.node.DNSName + ":443"
	system.fake.config.Web[hostPort].Handlers["/other"] = &tailscaleHTTPHandler{Proxy: "http://127.0.0.1:9000"}
	beforeConfig := cloneTailscaleServe(system.fake.config)
	beforeWrites, beforeRestarts := system.fake.writeCount, system.restarts
	if err := repairConfiguredTailscale(t.Context(), runtime, runtime.manifest.TailscaleBinary, system.hooks); err != nil {
		t.Fatal(err)
	}
	if system.fake.writeCount != beforeWrites+2 || system.restarts != beforeRestarts {
		t.Fatalf("repair writes=%d restarts=%d", system.fake.writeCount-beforeWrites, system.restarts-beforeRestarts)
	}
	if !sameTailscaleServe(beforeConfig, system.fake.config) {
		t.Fatal("repair changed foreign or final Funnel configuration")
	}
	pending, err := loadTailscaleState(runtime.root)
	if err != nil || pending == nil || !pending.Pending || pending.VerifiedAt != nil || !pending.ConfiguredAt.After(beforeState.ConfiguredAt) {
		t.Fatalf("repair did not advance the verification generation safely: %+v %v", pending, err)
	}
	status, err := verifyConfiguredTailscale(t.Context(), runtime.root, system.hooks)
	if err != nil || !status.Ready || status.VerifiedAt == nil {
		t.Fatalf("repaired mapping could not be verified: %+v %v", status, err)
	}
}

func TestRepairConfiguredTailscaleFailsClosedBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"core_unhealthy", "foreign_root"} {
		t.Run(scenario, func(t *testing.T) {
			runtime, system := newPublicAccessTestSystem(t, "none")
			if err := system.configure(runtime, true); err != nil {
				t.Fatal(err)
			}
			runtime, err := loadTunnelRuntime(runtime.root)
			if err != nil {
				t.Fatal(err)
			}
			beforeState, err := loadTailscaleState(runtime.root)
			if err != nil || beforeState == nil {
				t.Fatal(err)
			}
			beforeWrites, beforeRestarts := system.fake.writeCount, system.restarts
			if scenario == "core_unhealthy" {
				system.core = false
			} else {
				hostPort := system.fake.node.DNSName + ":443"
				system.fake.config.Web[hostPort].Handlers["/"] = &tailscaleHTTPHandler{Proxy: "http://127.0.0.1:9000"}
			}
			if err := repairConfiguredTailscale(context.Background(), runtime, runtime.manifest.TailscaleBinary, system.hooks); err == nil {
				t.Fatal("unsafe repair unexpectedly succeeded")
			}
			if system.fake.writeCount != beforeWrites || system.restarts != beforeRestarts {
				t.Fatal("failed preflight changed network or Core state")
			}
			afterState, err := loadTailscaleState(runtime.root)
			if err != nil || afterState == nil || afterState.Pending != beforeState.Pending ||
				(afterState.VerifiedAt == nil) != (beforeState.VerifiedAt == nil) {
				t.Fatalf("failed preflight changed readiness state: %+v %v", afterState, err)
			}
		})
	}
}

func TestRepairConfiguredTailscaleInvalidatesInFlightVerification(t *testing.T) {
	runtime, system := newPublicAccessTestSystem(t, "none")
	if err := system.configure(runtime, true); err != nil {
		t.Fatal(err)
	}
	runtime, err := loadTunnelRuntime(runtime.root)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	system.hooks.verifyOrigin = func(context.Context, tunnelRuntime, string) error {
		close(started)
		<-release
		return nil
	}
	type result struct {
		status TunnelStatus
		err    error
	}
	done := make(chan result, 1)
	go func() {
		status, err := verifyConfiguredTailscale(context.Background(), runtime.root, system.hooks)
		done <- result{status: status, err: err}
	}()
	<-started
	if err := repairConfiguredTailscale(t.Context(), runtime, runtime.manifest.TailscaleBinary, system.hooks); err != nil {
		t.Fatal(err)
	}
	close(release)
	stale := <-done
	if stale.err != nil || stale.status.Ready || stale.status.DiagnosticCode != "mode_changed" {
		t.Fatalf("in-flight verification survived repair generation change: %+v %v", stale.status, stale.err)
	}
	pending, err := loadTailscaleState(runtime.root)
	if err != nil || pending == nil || !pending.Pending || pending.VerifiedAt != nil {
		t.Fatalf("stale verification changed repaired readiness: %+v %v", pending, err)
	}
	system.hooks.verifyOrigin = func(context.Context, tunnelRuntime, string) error { return nil }
	fresh, err := verifyConfiguredTailscale(t.Context(), runtime.root, system.hooks)
	if err != nil || !fresh.Ready {
		t.Fatalf("fresh verification did not publish repaired readiness: %+v %v", fresh, err)
	}
}
