//go:build !windows

package securepath

import (
	"os"
	"syscall"
)

// A concurrently substituted FIFO must not turn a bounded file read into a wait.
const regularReadFlags = os.O_RDONLY | syscall.O_NONBLOCK
