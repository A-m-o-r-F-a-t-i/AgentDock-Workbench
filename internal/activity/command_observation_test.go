package activity

import "testing"

func TestCommandObservationPreservesIndependentRoots(t *testing.T) {
	p := newCallProjection()
	for i, id := range []string{"call_original", "call_observation"} {
		p.apply(Event{Seq: uint64(i + 1), Binding: Binding{CallID: id, ConversationID: "conv_same"}, Kind: "call.created"})
	}
	p.apply(Event{Seq: 3, Binding: Binding{CallID: "call_observation", ConversationID: "conv_same"}, Kind: "call.completed", ToolName: "session_observe", Status: "succeeded", CommandObservation: CommandObservation{OriginCallID: "call_original", RoutineObservation: true}})
	row := p.calls["call_observation"]
	if row.OriginCallID != "call_original" || !row.RoutineObservation || row.ParentCallID != "" {
		t.Fatalf("invalid relation: %#v", row)
	}
	stats := callStatsAccumulator{}
	for _, call := range p.calls {
		stats.add(call)
	}
	if stats.result().Total != 2 {
		t.Fatal("folding changed root statistics")
	}
	p.apply(Event{Seq: 4, Binding: row.Binding, Kind: "call.completed", ToolName: "session_observe", Status: "succeeded", CommandObservation: CommandObservation{OriginCallID: "call_other", RoutineObservation: true}})
	if !row.HistoryIncomplete || row.OriginCallID != "call_original" {
		t.Fatal("conflicting relation accepted")
	}
}
