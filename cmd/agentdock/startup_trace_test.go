package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStartupTraceReportsStartedAndOutcomeWithoutErrorContents(t *testing.T) {
	for _, failure := range []error{nil, errors.New("private-config-value")} {
		var output bytes.Buffer
		complete := traceStartupPhase(&output, "configuration")
		if !strings.Contains(output.String(), `"state":"started"`) {
			t.Fatal("phase start was not emitted before initialization")
		}
		complete(failure)
		decoder := json.NewDecoder(&output)
		var started, finished map[string]any
		if err := decoder.Decode(&started); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&finished); err != nil {
			t.Fatal(err)
		}
		want := "completed"
		if failure != nil {
			want = "failed"
		}
		if started["phase"] != "configuration" || started["state"] != "started" || finished["phase"] != "configuration" || finished["state"] != want {
			t.Fatalf("startup trace mismatch: %v %v", started, finished)
		}
		elapsed, ok := finished["elapsed_ms"].(float64)
		if !ok || elapsed < 0 {
			t.Fatalf("missing monotonic phase duration: %v", finished)
		}
		if _, exists := finished["error"]; exists {
			t.Fatal("raw error contents leaked into phase trace")
		}
	}
	traceStartupPhase(nil, "discard")(nil)
}
