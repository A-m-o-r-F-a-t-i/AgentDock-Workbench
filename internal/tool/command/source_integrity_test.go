package command

import (
	"encoding/json"
	"testing"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func TestSnapshotResultUsesSparseEmptyObservation(t *testing.T) {
	result := snapshotResult(session.Snapshot{
		SessionID: "session-empty",
		Status:    "running",
		ElapsedMS: 125,
		Terminal:  "pipes",
	})

	for _, key := range []string{
		"stdout", "stderr", "timed_out",
		"stdout_output_bytes", "stderr_output_bytes",
		"stdout_total_bytes", "stderr_total_bytes",
		"stdout_dropped_bytes", "stderr_dropped_bytes",
		"stdout_omitted_bytes", "stderr_omitted_bytes",
		"stdout_output_lines", "stderr_output_lines",
		"stdout_truncated", "stderr_truncated",
	} {
		if _, exists := result[key]; exists {
			t.Fatalf("empty observation returned %q: %#v", key, result[key])
		}
	}
	if result["source_complete"] != true || result["displayed_bytes"] != 0 || result["total_output_bytes"] != 0 || result["retained_bytes"] != 0 || result["dropped_before_bytes"] != 0 {
		t.Fatalf("empty source accounting = %#v", result)
	}
	assertStreamOffset(t, result["offset"], 0, 0)
	assertStreamOffset(t, result["next_offset"], 0, 0)
	if result["offset_unit"] != "utf8_byte" {
		t.Fatalf("offset_unit = %#v", result["offset_unit"])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 1024 {
		t.Fatalf("empty business response = %d bytes, want <= 1024: %s", len(encoded), encoded)
	}
}

func TestSnapshotResultReportsRetainedAndDroppedRanges(t *testing.T) {
	result := snapshotResult(session.Snapshot{
		SessionID:          "session-partial",
		Status:             "running",
		ElapsedMS:          250,
		Terminal:           "pipes",
		Stdout:             "中a",
		Stderr:             "xy",
		StdoutOutputBytes:  len([]byte("中a")),
		StderrOutputBytes:  2,
		StdoutTotalBytes:   100,
		StderrTotalBytes:   50,
		StdoutDroppedBytes: 20,
		StderrDroppedBytes: 10,
		StdoutOmittedBytes: 76,
		StderrOmittedBytes: 38,
		StdoutOutputLines:  1,
		StderrOutputLines:  1,
		StdoutTruncated:    true,
		StderrTruncated:    true,
		StdoutOffsetBytes:  30,
		StderrOffsetBytes:  15,
		StdoutNextOffset:   34,
		StderrNextOffset:   17,
	})

	if result["source_complete"] != false {
		t.Fatalf("source_complete = %#v", result["source_complete"])
	}
	if result["displayed_bytes"] != 6 || result["total_output_bytes"] != 150 || result["retained_bytes"] != 120 || result["dropped_before_bytes"] != 30 {
		t.Fatalf("source accounting = %#v", result)
	}
	assertStreamOffset(t, result["offset"], 30, 15)
	assertStreamOffset(t, result["next_offset"], 34, 17)
	if result["stdout"] != "中a" || result["stderr"] != "xy" || result["stdout_truncated"] != true || result["stderr_truncated"] != true {
		t.Fatalf("stream result = %#v", result)
	}
}

func assertStreamOffset(t *testing.T, value any, stdout, stderr int) {
	t.Helper()
	offsets, ok := value.(map[string]any)
	if !ok || offsets["stdout"] != stdout || offsets["stderr"] != stderr {
		t.Fatalf("offsets = %#v, want stdout=%d stderr=%d", value, stdout, stderr)
	}
}
