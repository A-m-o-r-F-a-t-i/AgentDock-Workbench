package app

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func importantSidebarIDs(t *testing.T, rows []ConversationItem) []string {
	t.Helper()
	ids := make([]string, 0, len(rows))
	seen := make(map[string]bool)
	for _, row := range rows {
		key, err := sidebarNavigationKey(row)
		if err != nil || seen[key] {
			t.Fatalf("invalid or repeated navigation row %q: %v", key, err)
		}
		seen[key] = true
		ids = append(ids, key)
	}
	return ids
}

func TestSidebarImportantRowsPreserveLegacyDefault(t *testing.T) {
	items := sidebarFixture(time.Now(), 35, 0)
	items[30].Pinned = true
	items[31].InFlight = true
	group := SidebarGroup{}
	projectSidebarHistoryRows(&group, items, items, nil, 20, false, items[32].ID)
	if len(group.Conversations) != 20 || !group.HasMore {
		t.Fatalf("legacy page changed: %+v", group)
	}
	if !reflect.DeepEqual(importantSidebarIDs(t, group.Conversations), importantSidebarIDs(t, items[:20])) {
		t.Fatal("legacy history ordering changed")
	}
}

func TestSidebarImportantRowsStayOutsideOrdinaryQuota(t *testing.T) {
	items := sidebarFixture(time.Now(), 35, 0)
	items[30].Pinned, items[30].InFlight = true, true
	items[31].InFlight = true
	group := SidebarGroup{}
	projectSidebarHistoryRows(&group, items, items, nil, 20, true, items[32].ID)
	want := append([]string{items[30].ID, items[31].ID, items[32].ID}, importantSidebarIDs(t, items[:20])...)
	if !reflect.DeepEqual(importantSidebarIDs(t, group.Conversations), want) || !group.HasMore {
		t.Fatalf("important tail rows missing or consumed history quota: %+v", group)
	}
	projectSidebarHistoryRows(&group, items, items, nil, 40, true, items[32].ID)
	if len(group.Conversations) != len(items) || group.HasMore {
		t.Fatal("last history page has wrong size or cursor")
	}
	importantSidebarIDs(t, group.Conversations)
}

func TestSidebarImportantRowsDeduplicateArrivalsAndPreserveUnattributed(t *testing.T) {
	now := time.Now()
	items := sidebarFixture(now, 30, 0)
	arrival := sidebarFixture(now, 2, 0)
	arrival[0].ID, arrival[0].Pinned = "conv_arrival_pin", true
	arrival[1].ID = "conv_arrival"
	unattributed := ConversationItem{IsUnattributed: true}
	ordered := append(append([]ConversationItem{}, items...), unattributed)
	current := append(append([]ConversationItem{}, arrival...), ordered...)
	group := SidebarGroup{}
	projectSidebarHistoryRows(&group, current, ordered, arrival, 40, true, "")
	ids := importantSidebarIDs(t, group.Conversations)
	if len(ids) != len(current) || ids[0] != arrival[0].ID || ids[1] != arrival[1].ID || group.HasMore {
		t.Fatalf("arrivals or unattributed navigation lost: %v", ids)
	}
}

func TestSidebarImportantRowsReflectLiveChangesInFrozenHistory(t *testing.T) {
	now := time.Now()
	items := sidebarFixture(now, 60, 0)
	cache := sidebarHistoryCache{}
	_, _, cursor, _, err := cache.order("fixture\x00active", "", items, now)
	if err != nil {
		t.Fatal(err)
	}
	items[50].InFlight = true
	items[51].Pinned = true
	ordered, arrivals, again, reset, err := cache.order("fixture\x00active", cursor, items, now.Add(time.Second))
	if err != nil || reset || again != cursor {
		t.Fatalf("frozen ordering lost: %v", err)
	}
	group := SidebarGroup{}
	projectSidebarHistoryRows(&group, items, ordered, arrivals, 20, true, "")
	want := append([]string{items[50].ID, items[51].ID}, importantSidebarIDs(t, items[:20])...)
	if !reflect.DeepEqual(importantSidebarIDs(t, group.Conversations), want) {
		t.Fatal("existing tail conversation did not become visible when it became important")
	}
	// Removing a row from the authoritative view must also remove it from the
	// frozen history; the opt-in must not restore trashed/filtered resources.
	current := append(append([]ConversationItem{}, items[:50]...), items[51:]...)
	ordered, arrivals, _, _, err = cache.order("fixture\x00active", cursor, current, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	projectSidebarHistoryRows(&group, current, ordered, arrivals, 20, true, "")
	for _, id := range importantSidebarIDs(t, group.Conversations) {
		if id == items[50].ID {
			t.Fatal("filtered record was restored")
		}
	}
}

func TestSidebarImportantRowsDoNotPromoteUnconfirmedExecution(t *testing.T) {
	now := time.Now()
	items := sidebarFixture(now, 40, 0)
	items[30].Statistics.Running = 1
	items[31].InFlight, items[31].TerminatedAt = true, &now
	items[32].InFlight, items[32].ArchivedAt = true, &now
	items[33].InFlight, items[33].TrashedAt = true, &now
	group := SidebarGroup{}
	projectSidebarHistoryRows(&group, items, items, nil, 20, true, "")
	if !reflect.DeepEqual(importantSidebarIDs(t, group.Conversations), importantSidebarIDs(t, items[:20])) {
		t.Fatal("inactive or unconfirmed execution was promoted")
	}
}

func TestSidebarImportantRowsJSONIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{{`{}`, false}, {`{"include_important":false}`, false}, {`{"include_important":true}`, true}} {
		var request SidebarRequest
		if err := json.Unmarshal([]byte(tc.input), &request); err != nil {
			t.Fatal(err)
		}
		if request.IncludeImportant != tc.want {
			t.Fatal("opt-in changed legacy defaults")
		}
	}
}
