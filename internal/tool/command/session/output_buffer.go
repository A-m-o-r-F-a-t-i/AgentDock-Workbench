package session

import (
	"strings"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/textutil"
)

const maxSessionOutputBytes = 4 * 1024 * 1024
const initialSessionOutputBytes = 4 * 1024

// outputBuffer is a bounded, dynamically grown ring. Once it reaches its
// limit, appending a small chunk overwrites only the evicted bytes instead of
// copying the entire retained output.
type outputBuffer struct {
	data       []byte
	start      int
	size       int
	limitBytes int
}

func (b *outputBuffer) limit() int {
	if b.limitBytes > 0 {
		return b.limitBytes
	}
	return maxSessionOutputBytes
}

func (b *outputBuffer) Len() int { return b.size }

func (b *outputBuffer) Write(data []byte) (int, error) {
	n, _ := b.append(data)
	return n, nil
}

func (b *outputBuffer) WriteString(value string) (int, error) {
	return b.Write([]byte(value))
}

func (b *outputBuffer) append(data []byte) (int, int) {
	n := len(data)
	if n == 0 {
		return 0, 0
	}
	limit := b.limit()
	if n >= limit {
		dropped := b.size + n - limit
		if len(b.data) < limit {
			b.data = make([]byte, limit)
		}
		copy(b.data[:limit], data[n-limit:])
		b.start, b.size = 0, limit
		return n, dropped
	}

	retained := min(b.size+n, limit)
	b.ensureCapacity(retained)
	dropped := max(0, b.size+n-limit)
	if dropped > 0 {
		b.start = (b.start + dropped) % len(b.data)
		b.size -= dropped
	}
	end := (b.start + b.size) % len(b.data)
	first := min(n, len(b.data)-end)
	copy(b.data[end:end+first], data[:first])
	copy(b.data[:n-first], data[first:])
	b.size += n
	return n, dropped
}

func (b *outputBuffer) ensureCapacity(required int) {
	if required <= len(b.data) {
		return
	}
	limit := b.limit()
	next := min(initialSessionOutputBytes, limit)
	if next == 0 {
		next = required
	}
	for next < required {
		next = min(next*2, limit)
	}
	grown := make([]byte, next)
	b.copyRange(grown, 0, b.size)
	b.data, b.start = grown, 0
}

func (b *outputBuffer) copyRange(dst []byte, offset, count int) int {
	if count <= 0 || offset < 0 || offset >= b.size || len(b.data) == 0 {
		return 0
	}
	count = min(count, b.size-offset, len(dst))
	begin := (b.start + offset) % len(b.data)
	first := min(count, len(b.data)-begin)
	copy(dst[:first], b.data[begin:begin+first])
	copy(dst[first:count], b.data[:count-first])
	return count
}

// stringFrom returns retained bytes starting at a relative offset. A positive
// budget is applied at a UTF-8 boundary using the existing public contract.
func (b *outputBuffer) stringFrom(offset, maxBytes int) (string, int) {
	if offset < 0 {
		offset = 0
	}
	if offset >= b.size {
		return "", 0
	}
	available := b.size - offset
	readBytes := available
	if maxBytes > 0 && available > maxBytes {
		readBytes = maxBytes + min(available-maxBytes, utf8.UTFMax)
	}
	var builder strings.Builder
	builder.Grow(readBytes)
	begin := (b.start + offset) % len(b.data)
	first := min(readBytes, len(b.data)-begin)
	_, _ = builder.Write(b.data[begin : begin+first])
	if first < readBytes {
		_, _ = builder.Write(b.data[:readBytes-first])
	}
	value := builder.String()
	if maxBytes > 0 && available > maxBytes {
		value = textutil.SafeTruncateString(value, maxBytes).Text
	}
	return value, available
}

func (b *outputBuffer) String() string {
	value, _ := b.stringFrom(0, 0)
	return value
}
