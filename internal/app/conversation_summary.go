package app

import (
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

// ConversationSummary counts real conversations, independently of root-call
// statistics. Total includes archived/trash records, matching the all view.
type ConversationSummary struct {
	RecentlyActive int   `json:"recently_active"`
	Total          int   `json:"total"`
	WindowMS       int64 `json:"recent_interaction_window_ms"`
}

func summarizeConversations(items []activity.Conversation, stats map[string]activity.CallStats, now time.Time) ConversationSummary {
	result := ConversationSummary{WindowMS: SidebarRecentWindow.Milliseconds()}
	for _, item := range items {
		if item.ID == "" || item.DeletedAt != nil {
			continue
		}
		result.Total++
		_, _, recent := projectedConversationInteraction(stats[item.ID], now,
			item.TerminatedAt != nil || item.TrashedAt != nil || item.ArchivedAt != nil)
		if recent {
			result.RecentlyActive++
		}
	}
	return result
}
