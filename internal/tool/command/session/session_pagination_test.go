package session

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSnapshotPaginationPreservesBothStreams(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%t", completed), func(t *testing.T) {
			s := &Session{StartedAt: time.Now(), FinishedAt: time.Now(), completed: completed}
			stdout := strings.Repeat("a", 8192) + "\r\n"
			stderr := strings.Repeat("error\n", 301)
			_, _ = (sessionOutputWriter{session: s}).Write([]byte(stdout))
			_, _ = (sessionOutputWriter{session: s, stderr: true}).Write([]byte(stderr))
			var gotOut, gotErr strings.Builder
			for page := 0; page < 100; page++ {
				got := s.Snapshot("running", 512)
				gotOut.WriteString(got.Stdout)
				gotErr.WriteString(got.Stderr)
				if got.StdoutOutputBytes != len(got.Stdout) || got.StderrOutputBytes != len(got.Stderr) {
					t.Fatal("output byte counts differ from delivered output")
				}
				if got.StdoutOmittedBytes != len(stdout)-gotOut.Len() || got.StderrOmittedBytes != len(stderr)-gotErr.Len() {
					t.Fatalf("page %d unread bytes = %d/%d; delivered = %d/%d", page, got.StdoutOmittedBytes, got.StderrOmittedBytes, gotOut.Len(), gotErr.Len())
				}
				if s.stdoutCursor != gotOut.Len() || s.stderrCursor != gotErr.Len() {
					t.Fatalf("page %d cursor = %d/%d; delivered = %d/%d", page, s.stdoutCursor, s.stderrCursor, gotOut.Len(), gotErr.Len())
				}
				if !got.StdoutTruncated && !got.StderrTruncated {
					break
				}
				if got.Stdout == "" && got.Stderr == "" {
					t.Fatal("unread output made no progress")
				}
			}
			if gotOut.String() != stdout || gotErr.String() != stderr {
				t.Fatal("pagination lost or repeated bytes")
			}
			last := s.Snapshot("running", 16384)
			if last.Stdout != "" || last.Stderr != "" || last.StdoutTruncated || last.StderrTruncated {
				t.Fatal("drained output repeated")
			}
			if last.StdoutTotalBytes != len(stdout) || last.StderrTotalBytes != len(stderr) {
				t.Fatal("stream totals changed while paging")
			}
		})
	}
}

func TestSnapshotPaginationUTF8AndSmallBudget(t *testing.T) {
	s := &Session{}
	text := strings.Repeat("中🙂ab", 17)
	_, _ = (sessionOutputWriter{session: s}).Write([]byte(text))
	first := s.Snapshot("running", 1)
	if first.Stdout != "" || s.stdoutCursor != 0 || first.StdoutOmittedBytes != len(text) {
		t.Fatal("small budget consumed an undelivered rune")
	}
	var got strings.Builder
	for page := 0; page < len(text); page++ {
		part := s.Snapshot("running", 7)
		if !utf8.ValidString(part.Stdout) || len(part.Stdout) > 7 {
			t.Fatal("UTF-8 page exceeded its boundary or byte budget")
		}
		got.WriteString(part.Stdout)
		if s.stdoutCursor != got.Len() {
			t.Fatal("cursor counted characters instead of bytes")
		}
		if !part.StdoutTruncated {
			break
		}
	}
	if got.String() != text {
		t.Fatal("UTF-8 pagination lost bytes")
	}
}

func TestSnapshotPaginationPeekAndAppendedOutput(t *testing.T) {
	s := &Session{}
	_, _ = (sessionOutputWriter{session: s}).Write([]byte("abcdefghi"))
	for i := 0; i < 2; i++ {
		peek := s.Peek("running", 3)
		if peek.Stdout != "abc" || s.stdoutCursor != 0 {
			t.Fatal("Peek consumed a truncated page")
		}
	}
	first := s.Snapshot("running", 3)
	if first.Stdout != "abc" {
		t.Fatal("Snapshot did not return peeked page")
	}
	_, _ = (sessionOutputWriter{session: s}).Write([]byte("XYZ"))
	remaining := s.Snapshot("running", 0)
	if remaining.Stdout != "defghiXYZ" || remaining.StdoutTruncated {
		t.Fatalf("remaining = %q", remaining.Stdout)
	}
}

func TestSnapshotPaginationAfterBufferEviction(t *testing.T) {
	const limit = 4 * 1024 * 1024
	const dropped = 2048
	for _, consumed := range []int{512, 4096} {
		t.Run(fmt.Sprintf("consumed=%d", consumed), func(t *testing.T) {
			s := &Session{}
			writer := sessionOutputWriter{session: s}
			_, _ = writer.Write([]byte(strings.Repeat("a", limit)))
			first := s.Snapshot("running", consumed)
			if len(first.Stdout) != consumed {
				t.Fatal("first page length")
			}
			_, _ = writer.Write([]byte(strings.Repeat("b", dropped)))
			want := strings.Repeat("a", limit-max(consumed, dropped)) + strings.Repeat("b", dropped)
			var got strings.Builder
			for page := 0; page < 100; page++ {
				part := s.Snapshot("running", 65536)
				got.WriteString(part.Stdout)
				if part.StdoutDroppedBytes != dropped || part.StdoutTotalBytes != limit+dropped {
					t.Fatal("buffer eviction accounting changed")
				}
				if !part.StdoutTruncated {
					break
				}
			}
			if got.String() != want {
				t.Fatalf("retained output length = %d, want %d", got.Len(), len(want))
			}
		})
	}
}

func TestSnapshotPaginationConcurrentObservers(t *testing.T) {
	s := &Session{}
	var text strings.Builder
	var want []string
	for i := 0; i < 100; i++ {
		line := fmt.Sprintf("%06d\n", i)
		text.WriteString(line)
		want = append(want, line)
	}
	_, _ = (sessionOutputWriter{session: s}).Write([]byte(text.String()))
	pages := make(chan string, len(want))
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				part := s.Snapshot("running", 7)
				if part.Stdout == "" {
					return
				}
				pages <- part.Stdout
			}
		}()
	}
	workers.Wait()
	close(pages)
	var got []string
	for page := range pages {
		got = append(got, page)
	}
	sort.Strings(got)
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Fatalf("concurrent pages = %d, want %d", len(got), len(want))
	}
}
