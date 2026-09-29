package client

import (
	"context"
	"sort"
	"strings"
)

const maxSearchCatalogSummaries = 100

// SearchResult separates an empty match set from an undiscovered or stale
// directory. Broad discovery never connects to unrelated servers.
type SearchResult struct {
	Tools     []ToolSummary
	Catalogs  []map[string]any
	Complete  bool
	Truncated bool
	CacheOnly bool
}

// SearchCatalogsFiltered searches cached directories when no service is selected.
// Selecting a server explicitly retains lazy discovery and its original errors.
// The visibility predicate runs before reading any member catalog.
func (m *Manager) SearchCatalogsFiltered(ctx context.Context, query, server string, limit int, allow func(string) bool) (SearchResult, error) {
	result := SearchResult{Tools: []ToolSummary{}, Catalogs: []map[string]any{}, Complete: true}
	query, server = strings.ToLower(strings.TrimSpace(query)), strings.TrimSpace(server)
	result.CacheOnly = server == ""
	if query == "" {
		return result, newError("MCP_QUERY_REQUIRED", "MCP tool search query is required", false, nil, nil)
	}
	if err := m.syncRegistryContext(ctx); err != nil {
		return result, err
	}
	configs, err := m.searchServers(server)
	if err != nil {
		return result, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	type scoredTool struct {
		score int
		item  ToolSummary
	}
	matches := []scoredTool{}
	visible := 0
	for _, cfg := range configs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if allow != nil && !allow(cfg.Name) {
			continue
		}
		visible++
		if visible > maxSearchCatalogSummaries {
			result.Truncated, result.Complete = true, false
			break
		}
		catalog, fresh, err := m.catalogSnapshot(cfg.Name)
		if err != nil {
			return result, err
		}
		if !result.CacheOnly {
			catalog, err = m.Catalog(ctx, cfg.Name)
			if err != nil {
				return result, err
			}
			fresh = true
		}
		entry := map[string]any{
			"name": cfg.Name, "server": cfg.Name,
			"description":      OneLineDescription(cfg.Description),
			"catalog_revision": catalog.Revision, "tool_count_known": catalog.Complete,
			"stale": !fresh, "total": len(catalog.Tools),
		}
		if !catalog.Complete || !fresh {
			result.Complete = false
			entry["next_action"] = "mcp_tool_list"
			entry["arguments"] = map[string]any{"server": cfg.Name}
		}
		// Copy only error codes, not remote messages or credential-bearing config.
		for _, summary := range m.Snapshots([]string{cfg.Name}) {
			entry["status"] = summary.Status
			if summary.LastErrorCode != "" {
				entry["last_error_code"] = summary.LastErrorCode
				result.Complete = false
				entry["next_action"] = "mcp_manage"
				entry["arguments"] = map[string]any{"action": "inspect", "name": cfg.Name}
			}
		}
		result.Catalogs = append(result.Catalogs, entry)
		for _, tool := range catalog.Tools {
			if score := toolMatchScore(query, tool); score > 0 {
				matches = append(matches, scoredTool{score, toolSummaryForConfig(cfg, tool)})
			}
		}
	}
	if server != "" && visible == 0 {
		return result, newError("MCP_SERVER_HIDDEN", "dynamic MCP server is hidden by its capability container", false, map[string]any{"server": server}, nil)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].item.QualifiedName < matches[j].item.QualifiedName
	})
	if len(matches) > limit {
		matches = matches[:limit]
		result.Truncated, result.Complete = true, false
	}
	for _, match := range matches {
		result.Tools = append(result.Tools, match.item)
	}
	return result, nil
}
