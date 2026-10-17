package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/activity"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
)

func fileToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "read_file", Contract: fileToolContract, Title: "Read file", Description: "Read a bounded UTF-8 source slice or an exact host-issued skill:// or activity:// reference returned by agentdock_context or workspace_context. do not construct Skill URIs. Continue retained output with its returned byte cursor.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("read_file", func(ctx context.Context, r *Runtime, request toolfile.ReadRequest) (Result, error) {
			request.Binding = activity.FromContext(ctx)
			return r.readFileWithOutputSource(ctx, request)
		})},
		{Name: "list_dir", Contract: fileToolContract, Title: "List directory", Description: "List directory entries, by default one level, with relative glob and entry-type filters. Follow continuation for large directories.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("list_dir", func(ctx context.Context, r *Runtime, request toolfile.ListRequest) (Result, error) {
			request.Binding = activity.FromContext(ctx)
			return r.files.ListDir(ctx, request)
		})},
		{Name: "search_text", Contract: fileToolContract, Title: "Search text", Description: "Search UTF-8 files for literal or regex matches, returning paths, line numbers and short excerpts. No matches is a successful search.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("search_text", func(ctx context.Context, r *Runtime, request toolfile.SearchRequest) (Result, error) {
			request.Binding = activity.FromContext(ctx)
			return r.files.SearchText(ctx, request)
		})},
		{Name: "apply_patch", Contract: fileToolContract, Title: "Apply patch", Description: "Apply a Codex V4A text patch to add, update, delete or move files. Preflight conflicts write no targets. Application failures report actual effects without rollback or replay; inspect partial or unknown effects before another mutation. Success returns the actual written diff.", Annotations: mutatingToolAnnotations(true, false), Handler: typedToolHandler("apply_patch", func(ctx context.Context, r *Runtime, request toolfile.ApplyPatchRequest) (Result, error) {
			request.Binding = activity.FromContext(ctx)
			return r.files.ApplyPatch(ctx, request)
		})},
	}
}
