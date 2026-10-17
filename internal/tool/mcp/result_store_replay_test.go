package mcp

import (
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestResultReplayKeepsOriginalResultIdentity(t *testing.T) {
	now := time.Unix(1000, 0)
	binding := activity.Binding{SourceOwnerKey: "owner", ConversationID: "conversation", CallID: "call_original"}
	store := resultStore{
		now: func() time.Time { return now },
		entries: map[string]*storedMCPResult{
			"call_original": {
				CallID: "call_original", Binding: binding, Pages: []resultPage{{}, {}},
				NextPage: 2, LastCursor: "hostcursor_replay", LastPage: 1, NextCursor: "hostcursor_next",
				CreatedAt: now, TouchedAt: now,
			},
		},
		cursors: map[string]resultCursorState{
			"hostcursor_replay": {CallID: "call_original", PageIndex: 1},
		},
		latestScope: map[string]string{},
		rootBudgets: map[string]resultRootBudget{},
	}

	result, err := store.continueResult(binding, "call_original", "hostcursor_replay")
	if err != nil {
		t.Fatal(err)
	}
	if result["result_call_id"] != "call_original" || result["replayed"] != true || result["content_returned"] != false {
		t.Fatalf("replay receipt lost its result identity: %#v", result)
	}
}
