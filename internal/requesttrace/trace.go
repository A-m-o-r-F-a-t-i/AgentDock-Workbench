// Package requesttrace carries transport diagnostics, never authorization or
// conversation identity. A trace belongs to one external request and its children.
package requesttrace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
)

const Header = "X-AgentDock-Request-Id"

type key struct{}
type Trace struct {
	mu       sync.Mutex
	snapshot Snapshot
}

type Snapshot struct {
	RequestID         string `json:"request_id,omitempty"`
	CallID            string `json:"call_id,omitempty"`
	Stage             string `json:"stage"`
	HandlerDispatched bool   `json:"handler_dispatched"`
}

// ValidID deliberately accepts only opaque IDs, not arbitrary header text,
// credentials, URLs, control characters or model-provided business arguments.
func ValidID(id string) bool {
	if len(id) != 36 || id[:4] != "req_" {
		return false
	}
	for _, c := range id[4:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func Ensure(ctx context.Context, candidate string) (context.Context, error) {
	if _, ok := ctx.Value(key{}).(*Trace); ok {
		return ctx, nil
	}
	if !ValidID(candidate) {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return ctx, err
		}
		candidate = "req_" + hex.EncodeToString(raw[:])
	}
	return context.WithValue(ctx, key{}, &Trace{snapshot: Snapshot{RequestID: candidate, Stage: "ingress"}}), nil
}

func Read(ctx context.Context) Snapshot {
	trace, _ := ctx.Value(key{}).(*Trace)
	if trace == nil {
		return Snapshot{}
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.snapshot
}
func ID(ctx context.Context) string { return Read(ctx).RequestID }
func Stage(ctx context.Context, stage string) {
	if trace, ok := ctx.Value(key{}).(*Trace); ok {
		trace.mu.Lock()
		trace.snapshot.Stage = stage
		trace.mu.Unlock()
	}
}

// BindCall is called only for the external root. Child calls cannot replace it.
func BindCall(ctx context.Context, id string) {
	if trace, ok := ctx.Value(key{}).(*Trace); ok {
		trace.mu.Lock()
		if trace.snapshot.CallID == "" {
			trace.snapshot.CallID = id
		}
		trace.mu.Unlock()
	}
}
func Dispatched(ctx context.Context, id string) {
	if trace, ok := ctx.Value(key{}).(*Trace); ok {
		trace.mu.Lock()
		if trace.snapshot.CallID == id {
			trace.snapshot.HandlerDispatched = true
			trace.snapshot.Stage = "tool_execution"
		}
		trace.mu.Unlock()
	}
}
