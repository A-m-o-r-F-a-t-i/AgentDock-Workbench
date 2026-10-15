package session

import "strings"

// RetainedSnapshot does not consume a model observer's cursor. Its byte ranges
// describe the retained ring, including an explicit gap if collection evicted data.
func (s *Session) RetainedSnapshot() Snapshot {
	result := s.ActivitySnapshot(&OutputCursor{})
	result.StdoutOutputBytes, result.StderrOutputBytes = len(result.Stdout), len(result.Stderr)
	result.StdoutOffsetBytes, result.StderrOffsetBytes = result.StdoutDroppedBytes, result.StderrDroppedBytes
	result.StdoutNextOffset, result.StderrNextOffset = result.StdoutTotalBytes, result.StderrTotalBytes
	if result.Stdout != "" {
		result.StdoutOutputLines = strings.Count(result.Stdout, "\n") + 1
	}
	if result.Stderr != "" {
		result.StderrOutputLines = strings.Count(result.Stderr, "\n") + 1
	}
	return result
}
