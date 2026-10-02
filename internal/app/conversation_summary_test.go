package app

import (
	"context"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

func TestConversationSummaryMatchesRecentIndicator(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	items := []activity.Conversation{{ID: "finished"}, {ID: "many-calls"}, {ID: "old-live"}, {ID: "unknown-time"},
		{ID: "terminated", TerminatedAt: &now}, {ID: "archived", Management: activity.Management{ArchivedAt: &now}},
		{ID: "trash", Management: activity.Management{TrashedAt: &now}}, {ID: "deleted", DeletedAt: &now}, {}}
	stats := map[string]activity.CallStats{}
	for _, item := range items {
		stats[item.ID] = activity.CallStats{Total: 8, Running: 4, LastInteractionAt: &now}
	}
	stats["finished"] = activity.CallStats{Total: 20, LastInteractionAt: &now}
	old := now.Add(-time.Hour)
	stats["old-live"] = activity.CallStats{Running: 9, Pending: 2, LastInteractionAt: &old, LastActivityAt: &now}
	stats["unknown-time"] = activity.CallStats{Running: 2, LastActivityAt: &now}
	got := summarizeConversations(items, stats, now)
	if got.RecentlyActive != 2 || got.Total != 7 || got.WindowMS != 120000 {
		t.Fatalf("unexpected conversation summary: %+v", got)
	}
	if stats["old-live"].Running != 9 || stats["old-live"].Pending != 2 {
		t.Fatal("presentation changed execution statistics")
	}
}

func TestConversationSummaryHalfOpenWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age  time.Duration
		want int
	}{
		{-time.Millisecond, 0}, {0, 1}, {119999 * time.Millisecond, 1}, {120 * time.Second, 0}, {121 * time.Second, 0},
	} {
		at := now.Add(-tc.age)
		stats := map[string]activity.CallStats{"conversation": {LastInteractionAt: &at, Running: 3}}
		got := summarizeConversations([]activity.Conversation{{ID: "conversation"}}, stats, now)
		if got.RecentlyActive != tc.want {
			t.Fatalf("age=%v summary=%+v", tc.age, got)
		}
	}
	if got := summarizeConversations(nil, nil, now); got.Total != 0 || got.RecentlyActive != 0 {
		t.Fatalf("empty summary=%+v", got)
	}
}

func TestExecutionOverviewUsesRealTasklessConversationSummary(t *testing.T) {
	r := executionTestRuntime(t)
	for _, name := range []string{"summary-a", "summary-a", "summary-b"} {
		if _, err := r.Call(scopeHost(name), "list_dir", map[string]any{"path": ".", "max_entries": 1}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	overview, err := r.RuntimeExecutionOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	summary := overview["conversation_summary"].(ConversationSummary)
	if summary.RecentlyActive != 2 || summary.Total != 2 {
		t.Fatalf("summary=%+v", summary)
	}
	if stats := overview["statistics"].(activity.CallStats); stats.Running != 0 {
		t.Fatalf("completed calls=%+v", stats)
	}
	page, err := r.RuntimeConversations(ctx, ExecutionListQuery{View: "all"})
	if err != nil {
		t.Fatal(err)
	}
	recent := 0
	for _, row := range page.Conversations {
		if row.RecentlyActive {
			recent++
		}
	}
	if recent != summary.RecentlyActive {
		t.Fatalf("summary=%+v sidebar=%d", summary, recent)
	}
}
