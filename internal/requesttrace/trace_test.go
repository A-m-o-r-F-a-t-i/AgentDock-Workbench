package requesttrace

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestTraceValidatesOpaqueRequestID(t *testing.T) {
	for _, input := range []string{"", "x", "req_", strings.Repeat("a", 10000), "Bearer secret", "req_" + strings.Repeat("a", 31) + "\n", "req_" + strings.Repeat("A", 32)} {
		ctx, err := Ensure(t.Context(), input)
		if err != nil || !ValidID(ID(ctx)) || ID(ctx) == input {
			t.Fatalf("invalid candidate was accepted: length=%d err=%v", len(input), err)
		}
	}
	id := "req_" + strings.Repeat("a", 32)
	ctx, err := Ensure(t.Context(), id)
	if err != nil || ID(ctx) != id {
		t.Fatal("valid transport ID was not preserved")
	}
	again, err := Ensure(ctx, "req_"+strings.Repeat("b", 32))
	if err != nil || ID(again) != id {
		t.Fatal("nested context replaced request identity")
	}
}

func TestTraceRootIdentityAndParallelAccess(t *testing.T) {
	ctx, err := Ensure(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	BindCall(ctx, "call_root")
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			BindCall(ctx, "call_child")
			Dispatched(ctx, "call_child")
			_ = Read(ctx)
			Stage(ctx, "mcp_dispatch")
		})
	}
	wg.Wait()
	if trace := Read(ctx); trace.CallID != "call_root" || trace.HandlerDispatched {
		t.Fatalf("child corrupted root: %+v", trace)
	}
	Dispatched(ctx, "call_root")
	if trace := Read(ctx); !trace.HandlerDispatched || trace.Stage != "tool_execution" {
		t.Fatalf("dispatch not recorded: %+v", trace)
	}
	independent, _ := Ensure(context.Background(), "")
	if ID(independent) == ID(ctx) || Read(independent).CallID != "" {
		t.Fatal("cross-request state leaked")
	}
	if Read(context.Background()) != (Snapshot{}) {
		t.Fatal("untraced request fabricated metadata")
	}
}
