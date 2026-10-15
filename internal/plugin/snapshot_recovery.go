package plugin

import (
	"context"
	"errors"
	"maps"
	"runtime"
	"syscall"
)

func transientPluginRead(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}
	var code syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &code) && (code == 32 || code == 33 || code == 170)
}

func (e *Error) DiagnosticDetails() map[string]any {
	details := maps.Clone(e.Details)
	if details == nil {
		details = map[string]any{}
	}
	details["error_code"] = e.Code
	if e.Cause != nil {
		details["cause"] = e.Cause.Error()
	}
	return details
}

// Revalidation is mandatory even when the failed cache generation had the same
// key. A changed or unverifiable source identity can never restore old tools.
func (s *Store) lastVerifiedDirectory(ctx context.Context, revision string, failure error) *Directory {
	if ctx.Err() != nil || !transientPluginRead(failure) {
		return nil
	}
	current, _, err := s.revision(ctx, true)
	if err != nil || current != revision {
		return nil
	}
	s.snapshots.keyMu.Lock()
	defer s.snapshots.keyMu.Unlock()
	previous := s.snapshots.lastVerified
	if previous == nil || !previous.verified || previous.sourceRevision != revision {
		return nil
	}
	copy := *previous
	copy.SnapshotFreshness = "last_verified"
	copy.DegradedReason = "PLUGIN_REGISTRY_TEMPORARILY_UNAVAILABLE"
	return &copy
}
