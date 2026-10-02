package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/permission"
)

func fakeAndroidDevice(t *testing.T) (*Runtime, context.Context, androidbridge.Registration, map[string]androidbridge.Capability) {
	t.Helper()
	r, ctx := profileRuntime(t, permission.DefaultSettings(), permission.Full)
	uid := 2000
	caps := map[string]androidbridge.Capability{"android_shizuku": {Ready: true, State: "verified", UID: &uid}}
	lease, err := r.androidExecutor.Register(androidbridge.RegisterRequest{Protocol: 1, WorkerID: strings.Repeat("b", 32), Backends: caps})
	if err != nil {
		t.Fatal(err)
	}
	return r, ctx, lease, caps
}
func deviceExchange(t *testing.T, r *Runtime, lease androidbridge.Registration, caps map[string]androidbridge.Capability, events ...androidbridge.Event) androidbridge.ExchangeResponse {
	t.Helper()
	result, err := r.androidExecutor.Exchange(lease.LeaseToken, androidbridge.ExchangeRequest{Protocol: 1, Backends: caps, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func waitDeviceJournal(t *testing.T, r *Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.command.WaitActivity(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAndroidDeviceReadKeepsOriginalRootAndSessionOwnership(t *testing.T) {
	r, ctx, lease, caps := fakeAndroidDevice(t)
	result, err := r.Call(ctx, "android_device_read", map[string]any{"action": "packages", "execution_mode": "async"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "android_device_read", result)
	id, sessionID := stringArg(result, "call_id"), stringArg(result, "session_id")
	if id == "" || sessionID == "" {
		t.Fatal(result)
	}
	call, err := r.activity.Call(ctx, id)
	if err != nil || activity.CallTerminal(call.Status) || call.ToolName != "android_device_read" {
		t.Fatal(call, err)
	}
	first := deviceExchange(t, r, lease, caps)
	if len(first.Commands) != 1 || first.Commands[0].Spec == nil {
		t.Fatal(first)
	}
	command := first.Commands[0]
	if command.Spec.Backend != "android_shizuku" || command.Spec.Workdir != "/" || command.Spec.Command != "cmd package list packages" {
		t.Fatal(command)
	}
	if _, err = r.Call(scopeHost("different-device-conversation"), "session_observe", map[string]any{"action": "status", "session_id": sessionID}); err == nil {
		t.Fatal("cross-conversation session admitted")
	}
	code := 0
	deviceExchange(t, r, lease, caps, androidbridge.Event{OperationID: command.OperationID, Revision: command.Revision, State: "exited", ExitCode: &code, Stdout: []byte("package:com.example\n")})
	waitDeviceJournal(t, r)
	call, err = r.activity.Call(ctx, id)
	if err != nil || !activity.CallTerminal(call.Status) || call.ToolName != "android_device_read" {
		t.Fatal(call, err)
	}
	if call.ExitCode == nil || *call.ExitCode != 0 {
		t.Fatal(call)
	}
}
func TestAndroidDeviceCapturePermissionCannotBypassReadOnlyProfile(t *testing.T) {
	settings := permission.DefaultSettings()
	settings.Profile.Filesystem = permission.FileRead
	r, ctx := profileRuntime(t, settings, permission.Full)
	result, err := r.Call(ctx, "android_device_read", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "android_device_read", result)
	if _, err = r.Call(ctx, "android_device_read", map[string]any{"action": "screenshot", "capture_dir": "/sdcard/Captures"}); err == nil {
		t.Fatal("read-only file policy admitted capture")
	}
	if r.androidExecutor.Status()["inflight"] != 0 {
		t.Fatal("denied operation reached broker")
	}
}
func TestAndroidDeviceStopWaitsForProviderCancellationReceipt(t *testing.T) {
	r, ctx, lease, caps := fakeAndroidDevice(t)
	result, err := r.Call(ctx, "android_device_act", map[string]any{"action": "keyevent", "keycode": 4, "execution_mode": "async"})
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultMatchestestOutputSchema(t, "android_device_act", result)
	first := deviceExchange(t, r, lease, caps)
	if len(first.Commands) != 1 {
		t.Fatal(first)
	}
	stop := make(chan error, 1)
	go func() { _, e := r.RuntimeCallStop(ctx, stringArg(result, "call_id")); stop <- e }()
	deadline := time.Now().Add(2 * time.Second)
	var pending androidbridge.Instruction
	for time.Now().Before(deadline) {
		batch := deviceExchange(t, r, lease, caps)
		if len(batch.Commands) == 1 {
			pending = batch.Commands[0]
			if pending.Cancel {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !pending.Cancel || pending.Action != "observe" {
		t.Fatal(pending)
	}
	select {
	case e := <-stop:
		t.Fatalf("stop returned before provider receipt: %v", e)
	default:
	}
	code := 137
	deviceExchange(t, r, lease, caps, androidbridge.Event{OperationID: pending.OperationID, Revision: pending.Revision, State: "cancelled", ExitCode: &code})
	select {
	case e := <-stop:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not observe receipt")
	}
	waitDeviceJournal(t, r)
	call, err := r.activity.Call(ctx, stringArg(result, "call_id"))
	if err != nil || call.Status != "cancelled" || call.ToolName != "android_device_act" {
		t.Fatal(call, err)
	}
}
