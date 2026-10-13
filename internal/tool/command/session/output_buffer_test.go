package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutputBufferRetainsNewestBytesAcrossWrap(t *testing.T) {
	buffer := outputBuffer{limitBytes: 8}
	if n, dropped := buffer.append([]byte("abcdef")); n != 6 || dropped != 0 {
		t.Fatalf("first append = %d/%d", n, dropped)
	}
	if n, dropped := buffer.append([]byte("WXYZ")); n != 4 || dropped != 2 {
		t.Fatalf("wrapped append = %d/%d", n, dropped)
	}
	if got := buffer.String(); got != "cdefWXYZ" {
		t.Fatalf("retained output = %q", got)
	}
	if got, available := buffer.stringFrom(3, 0); got != "fWXYZ" || available != 5 {
		t.Fatalf("range = %q/%d", got, available)
	}
}

func TestOutputBufferLargeWriteReplacesRetainedData(t *testing.T) {
	buffer := outputBuffer{limitBytes: 8}
	_, _ = buffer.append([]byte("old"))
	if n, dropped := buffer.append([]byte("0123456789")); n != 10 || dropped != 5 {
		t.Fatalf("large append = %d/%d", n, dropped)
	}
	if got := buffer.String(); got != "23456789" {
		t.Fatalf("retained output = %q", got)
	}
}

func TestOutputBufferBudgetPreservesUTF8BoundaryAcrossWrap(t *testing.T) {
	buffer := outputBuffer{limitBytes: 32}
	_, _ = buffer.append([]byte(strings.Repeat("x", 28)))
	_, _ = buffer.append([]byte("中🙂ab"))
	value, available := buffer.stringFrom(20, 7)
	if available <= 7 || len(value) > 7 || !utf8.ValidString(value) {
		t.Fatalf("UTF-8 page = %q available=%d", value, available)
	}
}

func TestFullOutputBufferAppendDoesNotAllocate(t *testing.T) {
	var buffer outputBuffer
	_, _ = buffer.append(make([]byte, maxSessionOutputBytes))
	chunk := make([]byte, 4096)
	allocations := testing.AllocsPerRun(100, func() {
		if n, dropped := buffer.append(chunk); n != len(chunk) || dropped != len(chunk) {
			t.Fatalf("steady append = %d/%d", n, dropped)
		}
	})
	if allocations != 0 {
		t.Fatalf("steady full-buffer append allocated %.2f objects", allocations)
	}
}

func BenchmarkSessionOutputWriterFullBufferAppend(b *testing.B) {
	s := &Session{}
	writer := sessionOutputWriter{session: s}
	_, _ = writer.Write(make([]byte, maxSessionOutputBytes))
	chunk := make([]byte, 4096)
	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for range b.N {
		if _, err := writer.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
}
