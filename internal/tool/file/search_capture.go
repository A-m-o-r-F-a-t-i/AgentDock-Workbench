package file

import (
	"bytes"
	"context"
)

const (
	maxRGOutputBytes = 16 << 20
	maxRGErrorBytes  = 4096
)

// Each stream has one os/exec copy goroutine. Read the capture only after Wait.
// Overflow cancels the process while continuing to drain bounded data, so a
// writer error cannot leave a child blocked on an unread pipe.
type searchOutputCapture struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *searchOutputCapture) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining < 0 {
		remaining = 0
	}
	kept := min(len(data), remaining)
	if kept > 0 {
		_, _ = b.buffer.Write(data[:kept])
	}
	if kept < len(data) && !b.exceeded {
		b.exceeded = true
		if b.cancel != nil {
			b.cancel()
		}
	}
	return len(data), nil
}

func (b *searchOutputCapture) Len() int       { return b.buffer.Len() }
func (b *searchOutputCapture) Bytes() []byte  { return b.buffer.Bytes() }
func (b *searchOutputCapture) String() string { return b.buffer.String() }
