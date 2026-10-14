package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTerminalFutureSupportsConcurrentAndLateObservers(t *testing.T) {
	command := &Session{ID: "session-terminal-observers"}
	future := command.TerminalFuture()
	completedAt := time.Now().UTC()
	exitCode, commandOK := 0, true
	want := TerminalOutcome{
		SessionID:   command.ID,
		Status:      "succeeded",
		ExitCode:    &exitCode,
		CommandOK:   &commandOK,
		Summary:     "completed",
		CompletedAt: completedAt,
	}

	const observers = 3
	results := make(chan TerminalOutcome, observers)
	errorsSeen := make(chan error, observers)
	var ready sync.WaitGroup
	ready.Add(observers)
	for index := 0; index < observers; index++ {
		go func() {
			ready.Done()
			outcome, err := future.Wait(context.Background())
			results <- outcome
			errorsSeen <- err
		}()
	}
	ready.Wait()
	if !command.PublishTerminal(want, nil) {
		t.Fatal("first terminal publication was rejected")
	}
	for index := 0; index < observers; index++ {
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
		assertTerminalOutcome(t, <-results, want)
	}

	late, err := future.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertTerminalOutcome(t, late, want)
	late.ExitCode = nil
	stored, ok := future.Outcome()
	if !ok {
		t.Fatal("published outcome is unavailable")
	}
	assertTerminalOutcome(t, stored, want)
}

func TestTerminalFutureWaitCancellationDoesNotCancelPublication(t *testing.T) {
	command := &Session{ID: "session-terminal-cancel"}
	future := command.TerminalFuture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := future.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait error = %v", err)
	}

	want := TerminalOutcome{SessionID: command.ID, Status: "unknown", Summary: "unknown", CompletedAt: time.Now().UTC()}
	if !command.PublishTerminal(want, nil) {
		t.Fatal("terminal publication was affected by one cancelled waiter")
	}
	got, err := future.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertTerminalOutcome(t, got, want)
}

func TestTerminalFutureCompletedOutcomeWinsCancelledLateContext(t *testing.T) {
	command := &Session{ID: "session-terminal-late-cancelled"}
	want := TerminalOutcome{SessionID: command.ID, Status: "succeeded", Summary: "done", CompletedAt: time.Now().UTC()}
	if !command.PublishTerminal(want, nil) {
		t.Fatal("terminal publication was rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := command.TerminalFuture().Wait(ctx)
	if err != nil {
		t.Fatalf("completed future returned cancelled context: %v", err)
	}
	assertTerminalOutcome(t, got, want)
}

func TestTerminalFuturePublishesOnlyOnce(t *testing.T) {
	command := &Session{ID: "session-terminal-once"}
	first := TerminalOutcome{SessionID: command.ID, Status: "failed", Summary: "first", CompletedAt: time.Now().UTC()}
	second := TerminalOutcome{SessionID: command.ID, Status: "succeeded", Summary: "second", CompletedAt: time.Now().UTC()}
	if !command.PublishTerminal(first, nil) {
		t.Fatal("first publication was rejected")
	}
	if command.PublishTerminal(second, nil) {
		t.Fatal("second publication replaced the authoritative outcome")
	}
	got, err := command.TerminalFuture().Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertTerminalOutcome(t, got, first)
}

func assertTerminalOutcome(t *testing.T, got, want TerminalOutcome) {
	t.Helper()
	if got.SessionID != want.SessionID || got.Status != want.Status || got.TimedOut != want.TimedOut || got.Summary != want.Summary || !got.CompletedAt.Equal(want.CompletedAt) {
		t.Fatalf("terminal outcome = %+v, want %+v", got, want)
	}
	if (got.ExitCode == nil) != (want.ExitCode == nil) || got.ExitCode != nil && *got.ExitCode != *want.ExitCode {
		t.Fatalf("exit code = %v, want %v", got.ExitCode, want.ExitCode)
	}
	if (got.CommandOK == nil) != (want.CommandOK == nil) || got.CommandOK != nil && *got.CommandOK != *want.CommandOK {
		t.Fatalf("command_ok = %v, want %v", got.CommandOK, want.CommandOK)
	}
}
