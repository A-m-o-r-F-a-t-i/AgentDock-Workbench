package command

import (
	"context"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestCommandTerminalFuturePublishesAfterRootFact(t *testing.T) {
	svc, _ := newCommandTestService(t)
	store, err := activity.New(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetActivityStore(store)
	callID, err := activity.NewExecutionID("call_")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Exec(context.Background(), ExecRequest{
		Binding:       activity.Binding{CallID: callID},
		Cmd:           "echo terminal-future",
		ExecutionMode: "async",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := result["session_id"].(string)
	future, ok := svc.TakeTerminalFuture(sessionID)
	if !ok || future == nil {
		t.Fatalf("terminal future was not registered for %q", sessionID)
	}
	outcome, err := future.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != "succeeded" || outcome.CommandOK == nil || !*outcome.CommandOK {
		t.Fatalf("terminal outcome = %+v", outcome)
	}
	call, err := store.Call(context.Background(), callID)
	if err != nil {
		t.Fatal(err)
	}
	if call.Status != outcome.Status || !activity.CallTerminal(call.Status) || call.SessionID != sessionID {
		t.Fatalf("root fact = %+v, outcome = %+v", call, outcome)
	}
}
