package androidbridge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func paired(t *testing.T) (*Broker, Registration) {
	t.Helper()
	b := New("node-test")
	r, err := b.Register(RegisterRequest{Protocol: 1, WorkerID: strings.Repeat("a", 32), Backends: capabilities()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b, r
}
func capabilities() map[string]Capability {
	return map[string]Capability{"termux_host": {Ready: true, State: "verified"}}
}
func spec() Spec {
	return Spec{Backend: "termux_host", Command: "printf hi", Workdir: "/data/data/com.termux/files/home", TimeoutMS: 30000}
}
func poll(t *testing.T, b *Broker, r Registration, events ...Event) ExchangeResponse {
	t.Helper()
	v, err := b.Exchange(r.LeaseToken, ExchangeRequest{Protocol: 1, Backends: capabilities(), Events: events})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestLeaseCannotBeTakenOverOrUsedForAnotherWorker(t *testing.T) {
	b, r := paired(t)
	_, err := b.Register(RegisterRequest{Protocol: 1, WorkerID: strings.Repeat("b", 32), ResumeToken: r.LeaseToken, Backends: capabilities()})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	_, err = b.Exchange(strings.Repeat("0", 64), ExchangeRequest{Protocol: 1})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}
func TestFirstOfferIsNeverReplayedAsStart(t *testing.T) {
	b, r := paired(t)
	_, err := b.Start(context.Background(), "session-one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	first := poll(t, b, r)
	if first.Commands[0].Action != "start" {
		t.Fatal(first)
	}
	again := poll(t, b, r)
	if again.Commands[0].Action != "observe" || again.Commands[0].Spec != nil {
		t.Fatal(again)
	}
}
func TestRevisionDeduplicatesOutputAndCompletion(t *testing.T) {
	b, r := paired(t)
	var out bytes.Buffer
	j, err := b.Start(context.Background(), "session-one", spec(), &out, &bytes.Buffer{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	c := poll(t, b, r).Commands[0]
	event := Event{OperationID: c.OperationID, Revision: c.Revision, State: "running", Stdout: []byte("中")}
	poll(t, b, r, event)
	poll(t, b, r, event)
	if out.String() != "中" {
		t.Fatal(out.String())
	}
	code := 0
	event = Event{OperationID: c.OperationID, Revision: 2, State: "exited", StdoutOffset: 3, ExitCode: &code}
	poll(t, b, r, event)
	poll(t, b, r, event)
	got, e := j.Wait()
	if got != 0 || e != nil {
		t.Fatal(got, e)
	}
}
func TestWholeBatchValidationPrecedesOutputWrites(t *testing.T) {
	b, r := paired(t)
	var out bytes.Buffer
	_, _ = b.Start(context.Background(), "one", spec(), &out, &bytes.Buffer{}, func(string) {})
	_, _ = b.Start(context.Background(), "two", spec(), &out, &bytes.Buffer{}, func(string) {})
	poll(t, b, r)
	_, err := b.Exchange(r.LeaseToken, ExchangeRequest{Protocol: 1, Events: []Event{{OperationID: "one", Revision: 1, State: "running", Stdout: []byte("not written")}, {OperationID: "two", Revision: 99, State: "running"}}})
	if err == nil || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
}
func TestDisconnectPreservesOriginalForObservation(t *testing.T) {
	b, r := paired(t)
	_, _ = b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	poll(t, b, r)
	if err := b.Disconnect(r.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if b.Ready("termux_host") {
		t.Fatal("ready while disconnected")
	}
	resumed, err := b.Register(RegisterRequest{Protocol: 1, WorkerID: strings.Repeat("a", 32), ResumeToken: r.LeaseToken, Backends: capabilities()})
	if err != nil {
		t.Fatal(err)
	}
	c := poll(t, b, resumed).Commands[0]
	if c.Action != "observe" || !c.Cancel {
		t.Fatal(c)
	}
}
func TestShutdownReportsUnknownNotExitZero(t *testing.T) {
	b, _ := paired(t)
	j, _ := b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	b.Close()
	code, err := j.Wait()
	if code != -1 || !errors.Is(err, session.ErrOutcomeUnknown) {
		t.Fatal(code, err)
	}
}
func TestUnsentCancellationCannotLaunchLater(t *testing.T) {
	b, r := paired(t)
	j, _ := b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	if err := j.Kill(); err != nil {
		t.Fatal(err)
	}
	if len(poll(t, b, r).Commands) != 0 {
		t.Fatal("cancelled unoffered command dispatched")
	}
	_, err := j.Wait()
	if !errors.Is(err, session.ErrRemoteCancelled) {
		t.Fatal(err)
	}
}
func TestUnknownProviderResultDoesNotCompleteProcess(t *testing.T) {
	b, r := paired(t)
	var states []string
	process, _ := b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(s string) { states = append(states, s) })
	poll(t, b, r, Event{OperationID: "one", Revision: 1, State: "unknown"})
	j := process.(*job)
	select {
	case <-j.done:
		t.Fatal("unknown settled a process")
	default:
	}
	if states[0] != "unknown" {
		t.Fatal(states)
	}
}
func TestStaleLeaseCannotAcceptNewCommands(t *testing.T) {
	b, _ := paired(t)
	b.mu.Lock()
	b.lastSeen = time.Now().Add(-time.Minute)
	b.mu.Unlock()
	_, err := b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	if !errors.Is(err, ErrOffline) {
		t.Fatal(err)
	}
}
func TestAbsoluteBackendDirectoryAndRequestBounds(t *testing.T) {
	for _, dir := range []string{"", ".", "../x", "/a/../b", "/x\x00"} {
		s := spec()
		s.Workdir = dir
		if ValidateSpec(s) == nil {
			t.Fatal(dir)
		}
	}
	s := spec()
	s.Command = strings.Repeat("x", 16385)
	if ValidateSpec(s) == nil {
		t.Fatal("large command")
	}
	s = spec()
	s.Backend = "auto"
	if ValidateSpec(s) == nil {
		t.Fatal("implicit backend")
	}
}
func TestInputAcknowledgementIsRequiredAndCorrelated(t *testing.T) {
	b, r := paired(t)
	process, _ := b.Start(context.Background(), "one", spec(), &bytes.Buffer{}, &bytes.Buffer{}, func(string) {})
	poll(t, b, r)
	done := make(chan error, 1)
	go func() { _, err := process.Stdin().Write([]byte("hello")); done <- err }()
	var c Instruction
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c = poll(t, b, r).Commands[0]
		if len(c.Input) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(c.Input) != 1 || string(c.Input[0].Data) != "hello" {
		t.Fatal(c)
	}
	select {
	case <-done:
		t.Fatal("write completed without ack")
	default:
	}
	poll(t, b, r, Event{OperationID: "one", Revision: c.Revision, State: "running", InputApplied: 1})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
