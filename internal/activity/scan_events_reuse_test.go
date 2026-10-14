package activity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScanEventsDecodeContainerKeepsRetainedSnapshotsIndependent(t *testing.T) {
	firstCode, secondCode := 17, 23
	firstOK, secondOK := false, true
	first := Event{
		SchemaVersion: SchemaVersion, Seq: 1, Kind: "call.completed",
		CreatedAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
		Binding:   Binding{CallID: "call_first", TaskID: "tsk_first", ThreadID: "main"},
		ExitCode:  &firstCode, CommandOK: &firstOK, Request: &Payload{},
		OutputPreview: "retained first output", StderrPreview: "retained error",
		TimedOut: true, StdoutTruncated: true,
	}
	second := Event{
		SchemaVersion: SchemaVersion, Seq: 3, Kind: "call.completed",
		Binding:  Binding{CallID: "call_second"},
		ExitCode: &secondCode, CommandOK: &secondOK, Request: &Payload{},
	}
	sparse := Event{SchemaVersion: SchemaVersion, Seq: 4, Kind: "call.created"}
	want := []Event{first, second, sparse}
	var journal bytes.Buffer
	encoder := json.NewEncoder(&journal)
	for index, event := range want {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			// A partially decoded damaged record must not leak into the next one.
			journal.WriteString("{\"schema_version\":1,\"seq\":2,\"kind\":\"call.completed\",\"output_preview\":\"damaged\",\"exit_code\":\"invalid\"}\n")
		}
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, journal.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var got []Event
	warning := false
	if err := scanEvents(path, func(event Event) bool {
		got = append(got, event)
		return true
	}, &warning); err != nil {
		t.Fatal(err)
	}
	if !warning || !reflect.DeepEqual(got, want) {
		t.Fatalf("retained snapshots changed: warning=%v\ngot=%+v\nwant=%+v", warning, got, want)
	}
	if got[0].ExitCode == got[1].ExitCode || got[0].Request == got[1].Request {
		t.Fatal("decoded events share mutable nested state")
	}
	visited := 0
	if err := scanEvents(path, func(Event) bool { visited++; return false }, nil); err != nil || visited != 1 {
		t.Fatalf("early-stop contract changed: visited=%d err=%v", visited, err)
	}
}
