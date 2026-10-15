package mcp

import (
	"context"

	"github.com/uvwt/agentdock/internal/contextguide"
)

type knownContextRevisionKey struct{}

func withContextRevisionRequest(ctx context.Context, name string, args map[string]any) context.Context {
	if name != "agentdock_context" && name != "workspace_context" {
		return ctx
	}
	if cursor, _ := args["cursor"].(string); cursor != "" {
		return context.WithValue(ctx, knownContextRevisionKey{}, "")
	}
	revision, _ := args["known_context_revision"].(string)
	if revision == "" {
		return ctx
	}
	return context.WithValue(ctx, knownContextRevisionKey{}, revision)
}

func projectContextReference(ctx context.Context, name string, result any, err error) any {
	if (name != "agentdock_context" && name != "workspace_context") || err != nil {
		return result
	}
	known, _ := ctx.Value(knownContextRevisionKey{}).(string)
	return contextguide.ProjectReference(name, known, asMap(result))
}
