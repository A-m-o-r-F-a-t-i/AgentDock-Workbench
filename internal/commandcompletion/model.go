// Package commandcompletion stores bounded delivery receipts for command facts.
// It does not own processes, task state, user messages, or model scheduling.
package commandcompletion

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxBatch                  = 8
	MaxBatchBytes             = 8 << 10
	MaxPendingPerConversation = 64
	MaxRecords                = 4096
	MaxLedgerBytes            = 4 << 20
	Retention                 = 24 * time.Hour
)

var (
	ErrCapacity         = errors.New("command completion delivery capacity reached; terminal facts remain in activity")
	ErrConflict         = errors.New("command completion identity conflict")
	callIDPattern       = regexp.MustCompile(`^call_[a-f0-9]{32}$`)
	conversationPattern = regexp.MustCompile(`^conv_[a-f0-9]{32}$`)
	identifierPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
)

type Identity struct {
	OriginalCallID string `json:"original_call_id"`
	SessionID      string `json:"session_id"`
}

// Event contains only host-owned, persisted facts. Output and command text stay
// in the original activity record; reading a notification consumes no log cursor.
type Event struct {
	Identity
	Type            string    `json:"type"`
	Version         int       `json:"version"`
	EventID         string    `json:"event_id"`
	ConversationID  string    `json:"conversation_id"`
	TaskID          string    `json:"task_id,omitempty"`
	ThreadID        string    `json:"thread_id,omitempty"`
	WorkspaceID     string    `json:"workspace_id,omitempty"`
	Status          string    `json:"status"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	CommandOK       *bool     `json:"command_ok,omitempty"`
	TimedOut        bool      `json:"timed_out,omitempty"`
	CompletedAt     time.Time `json:"completed_at"`
	ActivityEventID string    `json:"activity_event_id,omitempty"`
	Summary         string    `json:"summary"`
}

func ID(originalCallID string) string { return "cmp_" + strings.TrimPrefix(originalCallID, "call_") }

func (i Identity) valid() bool {
	return callIDPattern.MatchString(i.OriginalCallID) && identifierPattern.MatchString(i.SessionID)
}

func (e Event) validate() error {
	if !e.Identity.valid() || e.EventID != ID(e.OriginalCallID) || !conversationPattern.MatchString(e.ConversationID) || e.Type != "command_completion" || e.Version != 1 || e.CompletedAt.IsZero() || len(e.Summary) > 256 {
		return errors.New("invalid command completion fact")
	}
	for _, value := range []string{e.TaskID, e.ThreadID, e.WorkspaceID, e.ActivityEventID} {
		if value != "" && !identifierPattern.MatchString(value) {
			return errors.New("invalid command completion scope")
		}
	}
	if e.ThreadID != "" && e.TaskID == "" {
		return errors.New("completion thread requires task")
	}
	switch e.Status {
	case "succeeded":
		if e.ExitCode == nil || *e.ExitCode != 0 || e.CommandOK == nil || !*e.CommandOK || e.TimedOut {
			return errors.New("inconsistent successful terminal fact")
		}
	case "failed", "cancelled":
		if e.CommandOK != nil && *e.CommandOK {
			return errors.New("inconsistent unsuccessful terminal fact")
		}
	case "unknown":
		if e.ExitCode != nil || e.CommandOK != nil {
			return errors.New("unknown outcome cannot claim an exit code or success")
		}
	default:
		return errors.New("completion requires terminal status")
	}
	return nil
}

func Clone(events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	result := append([]Event(nil), events...)
	for i := range result {
		if p := result[i].ExitCode; p != nil {
			value := *p
			result[i].ExitCode = &value
		}
		if p := result[i].CommandOK; p != nil {
			value := *p
			result[i].CommandOK = &value
		}
	}
	return result
}

// Text is a compact compatibility view of the same events, never a user instruction.
func Text(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("[AgentDock command completion]")
	for _, e := range events {
		fmt.Fprintf(&out, "\n%s %s %s", e.EventID, e.SessionID, e.Status)
		if e.ExitCode != nil {
			fmt.Fprintf(&out, " exit=%d", *e.ExitCode)
		}
	}
	return out.String()
}

func batchFits(events []Event) bool {
	// Budget both model-visible representations, including JSON escaping.
	encoded, err := json.Marshal(map[string]any{
		"agentdock_guidance": map[string]any{"command_completions": events},
		"content":            []map[string]string{{"type": "text", "text": Text(events)}},
	})
	return err == nil && len(events) <= MaxBatch && len(encoded) <= MaxBatchBytes
}
