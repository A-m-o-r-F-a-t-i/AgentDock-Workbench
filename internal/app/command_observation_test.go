package app

import (
	"errors"
	"testing"
)

func TestQuietCommandObservationRequiresNoOutputAndNoGap(t *testing.T) {
	for _, result := range []Result{{"status": "running"}, {"status": "exited", "exit_code": 0, "command_ok": true}} {
		if !quietCommandObservation(result, nil) {
			t.Fatal("quiet observation not classified")
		}
	}
	for _, extra := range []Result{{"stdout": "new output"}, {"stderr": "warning"}, {"source_complete": false}, {"stdout_truncated": true}, {"stderr_dropped_bytes": int64(2)}, {"command_ok": false}, {"outcome_unknown": true}, {"activity_warning": "journal unavailable"}} {
		result := Result{"status": "running"}
		for key, v := range extra {
			result[key] = v
		}
		if quietCommandObservation(result, nil) {
			t.Fatalf("important observation hidden: %v", result)
		}
	}
	if quietCommandObservation(Result{"status": "running"}, errors.New("failure")) {
		t.Fatal("error hidden")
	}
}
