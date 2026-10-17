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
	Query     string
	Server    string
	MatchKind string
	Complete  bool
	Truncated bool
	CacheOnly bool
}

// SearchCatalogsFiltered searches cached directories when no service is selected.
// Selecting a server explicitly retains lazy discovery and its original errors.
// The visibility predicate runs before reading any member catalog.
func (m *Manager) SearchCatalogsFiltered(ctx context.Context, query, server string, limit int, allow func(string) bool) (SearchResult, error) {
	query, server, err := NormalizeSearchSelector(query, server)
	result := SearchResult{Tools: []ToolSummary{}, Catalogs: []map[string]any{}, Query: query, Server: server, MatchKind: "none", Complete: true}
	if err != nil {
		return result, err
	}
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
		kind  string
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
			"server_fingerprint": catalog.ServerFingerprint,
			"stale":              !fresh, "total": len(catalog.Tools),
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
			if score, kind := toolMatch(query, tool); score > 0 {
				matches = append(matches, scoredTool{score: score, kind: kind, item: toolSummaryForCatalog(cfg, catalog, tool)})
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
	if len(matches) > 0 {
		result.MatchKind = matches[0].kind
	}
	return result, nil
}

// NormalizeSearchSelector accepts either a capability query or an exact
// <server>:<tool> selector. Exact selectors select their server before catalog
// visibility is evaluated, so qualified names behave the same as an explicit
// server plus a bare tool name.
func NormalizeSearchSelector(query, server string) (string, string, error) {
	rawQuery := strings.TrimSpace(query)
	query = strings.ToLower(rawQuery)
	server = strings.TrimSpace(server)
	qualifiedServer, tool, qualified := strings.Cut(rawQuery, ":")
	qualifiedServer, tool = strings.TrimSpace(qualifiedServer), strings.TrimSpace(tool)
	if !qualified || qualifiedServer == "" || tool == "" || strings.ContainsAny(qualifiedServer+tool, " \t\r\n") {
		return query, server, nil
	}
	if server != "" && !strings.EqualFold(server, qualifiedServer) {
		return query, server, newError(
			"MCP_SERVER_QUERY_MISMATCH",
			"qualified MCP tool query conflicts with the selected server",
			false,
			map[string]any{"server": server, "query_server": qualifiedServer},
			nil,
		)
	}
	if server == "" {
		server = qualifiedServer
	}
	return strings.ToLower(tool), server, nil
}
