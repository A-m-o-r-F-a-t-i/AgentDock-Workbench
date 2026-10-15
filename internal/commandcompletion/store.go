package commandcompletion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/filelock"
)

type record struct {
	Owner        string    `json:"owner"`
	Conversation string    `json:"conversation"`
	Identity     Identity  `json:"identity"`
	Event        *Event    `json:"event,omitempty"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"created_at"`
	ReservedBy   string    `json:"reserved_by,omitempty"`
	ReservedPID  int       `json:"reserved_pid,omitempty"`
}

type ledger struct {
	Version int      `json:"version"`
	Records []record `json:"records"`
}

type Store struct {
	root  string
	now   func() time.Time
	alive func(int) bool
}

func New(root string, now func() time.Time) (*Store, error) {
	if root == "" {
		return nil, errors.New("completion store root is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Store{root: root, now: now, alive: filelock.ProcessAlive}, nil
}

func validTarget(owner, conversation string) bool {
	return owner != "" && len(owner) <= 1024 && conversationPattern.MatchString(conversation)
}

func (s *Store) Add(ctx context.Context, owner string, event Event) error {
	if !validTarget(owner, event.ConversationID) {
		return errors.New("completion requires authenticated target")
	}
	if err := event.validate(); err != nil {
		return err
	}
	event = Clone([]Event{event})[0]
	return s.update(ctx, func(state *ledger) (bool, error) {
		for _, item := range state.Records {
			if item.Identity.OriginalCallID != event.OriginalCallID {
				continue
			}
			if item.Owner != owner || item.Conversation != event.ConversationID || item.Identity.SessionID != event.SessionID {
				return false, ErrConflict
			}
			// A direct observation can create the consumed receipt before the
			// terminal settlement callback gets CPU time. Never requeue it.
			return false, nil
		}
		pending := 0
		for _, item := range state.Records {
			if item.Owner == owner && item.Conversation == event.ConversationID && (item.State == "pending" || item.State == "reserved") {
				pending++
			}
		}
		if pending >= MaxPendingPerConversation || len(state.Records) >= MaxRecords {
			return false, ErrCapacity
		}
		state.Records = append(state.Records, record{Owner: owner, Conversation: event.ConversationID, Identity: event.Identity, Event: &event, State: "pending", CreatedAt: s.now().UTC()})
		return true, nil
	})
}

// Reserve runs at the final root-response boundary. The admission timestamp
// limits selection to facts queued before this call; its own command is excluded.
func (s *Store) Reserve(ctx context.Context, owner, conversation, callID string, received time.Time) ([]Event, error) {
	if !validTarget(owner, conversation) || !callIDPattern.MatchString(callID) {
		return nil, errors.New("invalid completion recipient")
	}
	var events []Event
	err := s.update(ctx, func(state *ledger) (bool, error) {
		changed := false
		for i := range state.Records {
			item := &state.Records[i]
			if item.Owner != owner || item.Conversation != conversation || item.Event == nil || item.Identity.OriginalCallID == callID || item.CreatedAt.After(received) {
				continue
			}
			if item.State != "pending" && !(item.State == "reserved" && item.ReservedBy == callID && item.ReservedPID == os.Getpid()) {
				continue
			}
			candidate := append(append([]Event(nil), events...), *item.Event)
			if !batchFits(candidate) {
				break
			}
			events = candidate
			item.State, item.ReservedBy, item.ReservedPID = "reserved", callID, os.Getpid()
			changed = true
		}
		return changed, nil
	})
	if err != nil {
		return nil, err
	}
	return Clone(events), nil
}

// Finish commits at-most-once inclusion, not network delivery. Rollback is safe
// only before a final response can escape. Observed records suppress later enqueue.
func (s *Store) Finish(ctx context.Context, owner, conversation, callID string, commit bool, observed *Identity) error {
	if !validTarget(owner, conversation) || !callIDPattern.MatchString(callID) {
		return errors.New("invalid completion receipt")
	}
	if observed != nil && !observed.valid() {
		return errors.New("invalid observed command identity")
	}
	return s.update(ctx, func(state *ledger) (bool, error) {
		changed, foundObserved := false, false
		for i := range state.Records {
			item := &state.Records[i]
			if item.Owner != owner || item.Conversation != conversation {
				continue
			}
			if item.State == "reserved" && item.ReservedBy == callID && item.ReservedPID == os.Getpid() {
				if commit {
					item.State = "appended"
				} else {
					item.State, item.ReservedBy = "pending", ""
				}
				item.ReservedPID = 0
				changed = true
			}
			if observed != nil && item.Identity == *observed {
				foundObserved = true
				if commit && item.State == "pending" {
					item.State, item.ReservedBy = "appended", callID
					changed = true
				}
			}
		}
		if commit && observed != nil && !foundObserved {
			if len(state.Records) >= MaxRecords {
				return false, ErrCapacity
			}
			state.Records = append(state.Records, record{Owner: owner, Conversation: conversation, Identity: *observed, State: "appended", ReservedBy: callID, CreatedAt: s.now().UTC()})
			changed = true
		}
		return changed, nil
	})
}

func (s *Store) DeliveryUnknown(ctx context.Context, owner, conversation, callID string) error {
	return s.update(ctx, func(state *ledger) (bool, error) {
		changed := false
		for i := range state.Records {
			item := &state.Records[i]
			if item.Owner == owner && item.Conversation == conversation && item.ReservedBy == callID && item.State == "appended" {
				item.State = "delivery_unknown"
				changed = true
			}
		}
		return changed, nil
	})
}

func (s *Store) update(ctx context.Context, fn func(*ledger) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := filelock.Acquire(ctx, filepath.Join(s.root, ".delivery.lock"))
	if err != nil {
		return err
	}
	defer release()
	state, err := s.load()
	if err != nil {
		return err
	}
	changed := s.cleanup(&state)
	dirty, err := fn(&state)
	if err != nil {
		return err
	}
	if !changed && !dirty {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > MaxLedgerBytes || len(state.Records) > MaxRecords {
		return ErrCapacity
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.root, "delivery.json"), data, 0o600)
}

func (s *Store) load() (ledger, error) {
	state := ledger{Version: 1}
	path := filepath.Join(s.root, "delivery.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxLedgerBytes {
		return state, errors.New("invalid or oversized completion ledger")
	}
	file, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxLedgerBytes+1))
	if err != nil {
		return state, err
	}
	if len(data) > MaxLedgerBytes {
		return state, ErrCapacity
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("read completion ledger: %w", err)
	}
	if state.Version != 1 || len(state.Records) > MaxRecords {
		return state, errors.New("unsupported completion ledger")
	}
	seen := make(map[string]bool, len(state.Records))
	for _, item := range state.Records {
		if !validTarget(item.Owner, item.Conversation) || !item.Identity.valid() || item.CreatedAt.IsZero() || seen[item.Identity.OriginalCallID] {
			return state, errors.New("invalid completion ledger record")
		}
		seen[item.Identity.OriginalCallID] = true
		switch item.State {
		case "pending", "reserved":
			if item.Event == nil {
				return state, errors.New("pending completion has no fact")
			}
		case "appended", "delivery_unknown":
		default:
			return state, errors.New("invalid completion delivery state")
		}
		if item.Event != nil {
			if err := item.Event.validate(); err != nil {
				return state, err
			}
			if item.Event.Identity != item.Identity || item.Event.ConversationID != item.Conversation {
				return state, ErrConflict
			}
		}
		if item.State == "reserved" && (!callIDPattern.MatchString(item.ReservedBy) || item.ReservedPID <= 0) {
			return state, errors.New("invalid completion reservation")
		}
	}
	return state, nil
}

func (s *Store) cleanup(state *ledger) bool {
	now, changed := s.now().UTC(), false
	kept := state.Records[:0]
	for _, item := range state.Records {
		if now.Sub(item.CreatedAt) >= Retention {
			changed = true
			continue
		}
		if item.State == "reserved" && !s.alive(item.ReservedPID) {
			item.State, item.ReservedBy, item.ReservedPID = "pending", "", 0
			changed = true
		}
		kept = append(kept, item)
	}
	state.Records = kept
	return changed
}

// Delivered consults the existing durable receipt, including explicit terminal
// observations that settled before the asynchronous completion callback.
func (s *Store) Delivered(ctx context.Context, owner, conversation string, identity Identity) (bool, error) {
	if !validTarget(owner, conversation) || !identity.valid() {
		return false, errors.New("invalid completion identity")
	}
	delivered := false
	err := s.update(ctx, func(state *ledger) (bool, error) {
		for _, item := range state.Records {
			if item.Owner == owner && item.Conversation == conversation && item.Identity == identity {
				delivered = item.State == "appended"
				break
			}
		}
		return false, nil
	})
	return delivered, err
}

func (s *Store) PendingCount(ctx context.Context, owner, conversation string) (int, error) {
	if !validTarget(owner, conversation) {
		return 0, nil
	}
	count := 0
	err := s.update(ctx, func(state *ledger) (bool, error) {
		for _, item := range state.Records {
			if item.Owner == owner && item.Conversation == conversation && (item.State == "pending" || item.State == "reserved") {
				count++
			}
		}
		return false, nil
	})
	return count, err
}
