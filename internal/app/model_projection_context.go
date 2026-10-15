package app

import "context"

type modelProjectionKey struct{}

// WithModelProjection is set by the trusted host response adapter. Device-side
// API consumers keep complete typed business results; only model delivery uses
// the bounded projection, immutable page sequence and compatibility receipts.
func WithModelProjection(ctx context.Context) context.Context {
	return context.WithValue(ctx, modelProjectionKey{}, true)
}

func modelProjectionRequested(ctx context.Context) bool {
	enabled, _ := ctx.Value(modelProjectionKey{}).(bool)
	return enabled
}
