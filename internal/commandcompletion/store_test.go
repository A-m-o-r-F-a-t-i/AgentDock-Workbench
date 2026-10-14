package commandcompletion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const testConversation = "conv_11111111111111111111111111111111"

func call(n int) string { return fmt.Sprintf("call_%032x", n) }
func fact(n int) Event {
	zero, yes := 0, true
	return Event{Identity: Identity{OriginalCallID: call(n), SessionID: fmt.Sprintf("session-%d", n)}, Type: "command_completion", Version: 1, EventID: ID(call(n)), ConversationID: testConversation, Status: "succeeded", ExitCode: &zero, CommandOK: &yes, CompletedAt: time.Unix(100, 0), Summary: "Command completed successfully."}
}
func testStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Unix(200, 0)
	s, err := New(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s, &now
}
func mustAdd(t *testing.T, s *Store, n int) {
	t.Helper()
	if err := s.Add(t.Context(), "owner", fact(n)); err != nil {
		t.Fatal(err)
	}
}
func reserve(t *testing.T, s *Store, n int) []Event {
	t.Helper()
	events, err := s.Reserve(t.Context(), "owner", testConversation, call(n), s.now())
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestDeliveryOnceAcrossRestartAndUnknownTransport(t *testing.T) {
	s, now := testStore(t)
	mustAdd(t, s, 1)
	mustAdd(t, s, 1)
	events := reserve(t, s, 10)
	if len(events) != 1 || events[0].EventID != ID(call(1)) {
		t.Fatalf("events: %#v", events)
	}
	if err := s.Finish(t.Context(), "owner", testConversation, call(10), true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryUnknown(t.Context(), "owner", testConversation, call(10)); err != nil {
		t.Fatal(err)
	}
	reopened, _ := New(s.root, func() time.Time { return *now })
	mustAdd(t, reopened, 1)
	if len(reserve(t, reopened, 11)) != 0 {
		t.Fatal("terminal event was redelivered")
	}
	state, err := reopened.load()
	if err != nil || len(state.Records) != 1 || state.Records[0].State != "delivery_unknown" {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestReservationScopeCutoffAndRollback(t *testing.T) {
	s, now := testStore(t)
	mustAdd(t, s, 1)
	for _, scope := range [][2]string{{"other", testConversation}, {"owner", "conv_22222222222222222222222222222222"}} {
		events, err := s.Reserve(t.Context(), scope[0], scope[1], call(10), *now)
		if err != nil || len(events) != 0 {
			t.Fatal("completion crossed authenticated scope", err, events)
		}
	}
	events, err := s.Reserve(t.Context(), "owner", testConversation, call(1), *now)
	if err != nil || len(events) != 0 {
		t.Fatal("command received its own notification")
	}
	events, err = s.Reserve(t.Context(), "owner", testConversation, call(10), now.Add(-time.Second))
	if err != nil || len(events) != 0 {
		t.Fatal("future event entered older root call")
	}
	if len(reserve(t, s, 10)) != 1 || len(reserve(t, s, 11)) != 0 {
		t.Fatal("reservation was not exclusive")
	}
	if err := s.Finish(t.Context(), "owner", testConversation, call(10), false, nil); err != nil {
		t.Fatal(err)
	}
	if len(reserve(t, s, 11)) != 1 {
		t.Fatal("failed encoding consumed event")
	}
}

func TestConcurrentStoreHandlesClaimOnce(t *testing.T) {
	s, _ := testStore(t)
	mustAdd(t, s, 1)
	other, _ := New(s.root, s.now)
	var wg sync.WaitGroup
	counts := make(chan int, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			selected := s
			if i%2 != 0 {
				selected = other
			}
			e, err := selected.Reserve(t.Context(), "owner", testConversation, call(100+i), s.now())
			counts <- len(e)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(counts)
	close(errs)
	total := 0
	for n := range counts {
		total += n
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 {
		t.Fatalf("claimed %d times", total)
	}
}

func TestDeadReservationRecoveryAndDirectObservation(t *testing.T) {
	s, _ := testStore(t)
	mustAdd(t, s, 1)
	if len(reserve(t, s, 10)) != 1 {
		t.Fatal("missing event")
	}
	s.alive = func(int) bool { return true }
	if len(reserve(t, s, 11)) != 0 {
		t.Fatal("live reservation stolen")
	}
	s.alive = func(int) bool { return false }
	if len(reserve(t, s, 11)) != 1 {
		t.Fatal("dead reservation not recovered")
	}
	// The replacement claimant is alive; only the preceding lease was dead.
	s.alive = func(int) bool { return true }
	if err := s.Finish(t.Context(), "owner", testConversation, call(11), true, nil); err != nil {
		t.Fatal(err)
	}
	id := fact(2).Identity
	if err := s.Finish(t.Context(), "owner", testConversation, call(12), true, &id); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, 2)
	if len(reserve(t, s, 13)) != 0 {
		t.Fatal("late callback resurrected directly observed terminal")
	}
}

func TestCapacityBudgetExpirationAndSnapshots(t *testing.T) {
	s, now := testStore(t)
	for i := 1; i <= MaxPendingPerConversation; i++ {
		mustAdd(t, s, i)
	}
	if err := s.Add(t.Context(), "owner", fact(99)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("limit=%v", err)
	}
	events := reserve(t, s, 100)
	if len(events) != MaxBatch || !batchFits(events) {
		t.Fatalf("batch=%d", len(events))
	}
	*events[0].ExitCode = 17
	again := reserve(t, s, 100)
	if *again[0].ExitCode != 0 {
		t.Fatal("returned event mutated ledger")
	}
	*now = now.Add(Retention + time.Second)
	if len(reserve(t, s, 101)) != 0 {
		t.Fatal("expired event was delivered")
	}
	mustAdd(t, s, 99)
}

func TestCorruptLedgerAndCancellationDoNotResetFacts(t *testing.T) {
	s, _ := testStore(t)
	mustAdd(t, s, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Add(ctx, "owner", fact(2)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(reserve(t, s, 10)) != 1 {
		t.Fatal("cancelled write changed facts")
	}
	path := filepath.Join(s.root, "delivery.json")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(t.Context(), "owner", fact(3)); err == nil {
		t.Fatal("corrupt state silently replaced")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "corrupt" {
		t.Fatal("corrupt state overwritten")
	}
}

func TestTerminalFactValidation(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled", "unknown"} {
		e := fact(1)
		e.Status = status
		if status != "succeeded" {
			e.ExitCode = nil
			e.CommandOK = nil
		}
		if err := e.validate(); err != nil {
			t.Fatal(status, err)
		}
	}
	e := fact(1)
	e.Status = "unknown"
	if e.validate() == nil {
		t.Fatal("unknown terminal claimed success")
	}
	e = fact(1)
	e.Status = "running"
	if e.validate() == nil {
		t.Fatal("running event accepted")
	}
	e = fact(1)
	e.SessionID = "../unsafe"
	if e.validate() == nil {
		t.Fatal("unsafe id accepted")
	}
}
