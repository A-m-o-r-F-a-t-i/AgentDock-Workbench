package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func TestObservationCancellationLeavesCommandAndOtherWaiterAlive(t *testing.T) {
	svc := &Service{sessions: session.NewStore()}
	s := &session.Session{ID: "session-cancel-wait", StartedAt: time.Now(), Done: make(chan struct{})}
	svc.sessions.Add(s)
	defer close(s.Done)
	wait := MaxSessionObserveWaitMS
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := svc.ObserveContext(ctx, SessionObserveRequest{Action: "status", SessionID: s.ID, WaitMS: &wait})
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter remained blocked")
	}
	select {
	case <-s.Done:
		t.Fatal("wait cancellation killed process")
	default:
	}
	short := 10
	got, err := svc.ObserveContext(t.Context(), SessionObserveRequest{Action: "status", SessionID: s.ID, WaitMS: &short})
	if err != nil || got["status"] != "running" {
		t.Fatal("independent observer failed", got, err)
	}
}

func TestObservationWaitPolicyAndBounds(t *testing.T) {
	for _, tc := range []struct{ age, wait time.Duration }{{0, 5 * time.Second}, {29 * time.Second, 5 * time.Second}, {30 * time.Second, 15 * time.Second}, {59 * time.Second, 15 * time.Second}, {time.Minute, time.Minute}, {time.Hour, time.Minute}} {
		if got := sessionObserveDelay(tc.age.Milliseconds()); got != tc.wait {
			t.Fatalf("%v -> %v", tc.age, got)
		}
	}
	max := 300000
	if got, err := sessionObserveWait(&max, 0); err != nil || got != 5*time.Minute {
		t.Fatal(got, err)
	}
	if defaultCommandYield != 10*time.Second {
		t.Fatal("unexpected initial wait")
	}
}
