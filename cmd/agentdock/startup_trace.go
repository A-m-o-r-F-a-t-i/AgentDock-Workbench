package main

import (
	"io"
	"log/slog"
	"time"
)

// traceStartupPhase is independent of the configured logger: configuration and
// directory initialization can stall before logx.Setup has been reached.
func traceStartupPhase(output io.Writer, phase string) func(error) {
	if output == nil {
		output = io.Discard
	}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	started := time.Now()
	logger.Info("core startup phase", "phase", phase, "state", "started")
	return func(err error) {
		state := "completed"
		if err != nil {
			state = "failed"
		}
		// The caller retains the original error. Do not duplicate possible
		// configuration secrets in this compact, early startup trace.
		logger.Info("core startup phase", "phase", phase, "state", state, "elapsed_ms", time.Since(started).Milliseconds())
	}
}
