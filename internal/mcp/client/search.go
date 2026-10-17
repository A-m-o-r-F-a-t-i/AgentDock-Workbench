package client

import (
	"context"
	"errors"
	"sort"
	"strings"
)

const maxSearchCatalogSummaries = 100

// SearchResult keeps directory completeness separate from candidate truncation.
type SearchResult struct {
	Tools                []ToolSummary
	Catalogs             []map[string]any
	Query                string
	Server               string
	MatchKind            string
	Complete             bool
	Truncated            bool
	CacheOnly            bool
	MatchCount           int
	ExactCount           int
	Selected             map[string]any
	AbsenceAuthoritative bool
	ArgumentsComplete    bool
}

func (m *Manager) SearchCatalogsFiltered(ctx context.Context, query, server string, limit int, allow func(string) bool) (SearchResult, error) {
	return m.SearchCatalogs(ctx, query, server, limit, false, allow)
}

// ResolveSearchSelector recognizes a dotted qualifier only against registered
// server names, preserving periods in ordinary canonical tool identifiers.
func (m *Manager) ResolveSearchSelector(ctx context.Context, query, server string) (string, string, error) {
	query, server, err := NormalizeSearchSelector(query, server)
	if err != nil {
		return query, server, err
	}
	if err := m.syncRegistryContext(ctx); err != nil {
		return query, server, err
	}
	if server == "" {
		m.mu.RLock()
		for name := range m.servers {
			if strings.HasPrefix(query, strings.ToLower(name)+".") && len(name) > len(server) {
				server = name
			}
		}
		m.mu.RUnlock()
		if server != "" {
			query = query[len(server)+1:]
		}
	} else if strings.HasPrefix(query, strings.ToLower(server)+".") {
		query = query[len(server)+1:]
	}
	return query, server, nil
}

// Search uses one immutable ToolSpec generation. Exact matches form a separate
// partition before ranking, and unique results carry schemas without an inspect.
func (m *Manager) SearchCatalogs(ctx context.Context, query, server string, limit int, refresh bool, allow func(string) bool) (SearchResult, error) {
	rawQuery := strings.TrimSpace(query)
	query, server, err := m.ResolveSearchSelector(ctx, query, server)
	qualified := server != "" && (strings.EqualFold(rawQuery, server+":"+query) || strings.EqualFold(rawQuery, server+"."+query))
	result := SearchResult{Tools: []ToolSummary{}, Catalogs: []map[string]any{}, Query: query, Server: server, MatchKind: "none", Complete: true, CacheOnly: server == ""}
	if err != nil {
		return result, err
	}
	if query == "" {
		return result, newError("MCP_QUERY_REQUIRED", "MCP tool search query is required", false, nil, nil)
	}
	if refresh && server == "" {
		return result, newError("MCP_SERVER_REQUIRED", "select one server before refreshing its catalog", false, nil, nil)
	}
	configs, err := m.searchServers(server)
	if err != nil {
		return result, err
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 100 {
		limit = 100
	}
	type scoredTool struct {
		score   int
		kind    string
		exact   bool
		item    ToolSummary
		tool    Tool
		catalog Catalog
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
		if server != "" && !refresh && catalog.cachedCallable() {
			result.CacheOnly = true
		}
		attempted := !result.CacheOnly
		if attempted {
			var current Catalog
			if refresh {
				current, err = m.RefreshCatalog(ctx, cfg.Name)
			} else {
				current, err = m.Catalog(ctx, cfg.Name)
			}
			if err == nil {
				catalog, fresh = current, current.State.State == "fresh"
			} else {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				catalog, _, _ = m.catalogSnapshot(cfg.Name)
				fresh, result.Complete = false, false
				catalog.State.RefreshErrorCode = "MCP_CATALOG_REFRESH_FAILED"
				var typed *Error
				if errors.As(err, &typed) {
					catalog.State.RefreshErrorCode = typed.Code
				}
				catalog.State.State = "unavailable"
				if catalog.Complete {
					catalog.State.State = "refresh_failed"
				}
			}
		}
		catalog.State.RefreshAttempted = attempted || catalog.State.RefreshAttempted
		catalog.State.AbsenceAuthoritative = fresh && catalog.Complete && !result.CacheOnly && catalog.State.RefreshErrorCode == ""
		entry := map[string]any{
			"name": cfg.Name, "server": cfg.Name,
			"description":      OneLineDescription(cfg.Description),
			"catalog_revision": catalog.Revision, "catalog_state": catalog.State,
			"server_fingerprint": catalog.ServerFingerprint,
			"tool_count_known":   catalog.Complete, "stale": !fresh, "total": len(catalog.Tools),
		}
		if !catalog.Complete || !fresh {
			result.Complete = false
		}
		result.Catalogs = append(result.Catalogs, entry)
		for _, tool := range catalog.Tools {
			score, kind := toolMatch(query, tool)
			exact := strings.EqualFold(query, tool.Name)
			if !exact {
				for _, alias := range registeredToolAliases(tool) {
					if strings.EqualFold(query, alias) {
						score, kind, exact = 1000, "exact_alias", true
						break
					}
				}
			}
			if score <= 0 {
				continue
			}
			if exact {
				result.ExactCount++
				if qualified && kind == "exact_name" {
					kind = "exact_qualified_name"
				}
			}
			matches = append(matches, scoredTool{score: score, kind: kind, exact: exact, item: toolSummaryForCatalog(cfg, catalog, tool), tool: tool, catalog: catalog})
		}
	}
	if server != "" && visible == 0 {
		return result, newError("MCP_SERVER_HIDDEN", "dynamic MCP server is hidden by its capability container", false, map[string]any{"server": server}, nil)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].exact != matches[j].exact {
			return matches[i].exact
		}
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].item.QualifiedName < matches[j].item.QualifiedName
	})
	result.MatchCount = len(matches)
	result.AbsenceAuthoritative = result.Complete && !result.CacheOnly && visible > 0
	if len(matches) == 1 || result.ExactCount == 1 {
		chosen := matches[0]
		result.ArgumentsComplete = validateToolArguments(chosen.tool, map[string]any{}) == nil
		result.Selected = map[string]any{
			"server": chosen.item.Server, "tool_name": chosen.tool.Name,
			"qualified_name": chosen.item.QualifiedName, "title": chosen.item.Title,
			"description": chosen.tool.Description, "input_schema": cloneJSONMap(chosen.tool.InputSchema),
			"catalog_revision": chosen.catalog.Revision, "catalog_state": chosen.catalog.State,
			"server_fingerprint": chosen.item.ServerFingerprint,
			"schema_digest":      chosen.item.SchemaDigest, "annotations_digest": chosen.item.AnnotationsDigest,
			"annotations": cloneJSONMap(chosen.tool.Annotations),
		}
	}
	if limit < result.ExactCount {
		limit = result.ExactCount
	}
	if len(matches) > limit {
		matches = matches[:limit]
		result.Truncated = true
	}
	for _, match := range matches {
		result.Tools = append(result.Tools, match.item)
	}
	if len(matches) > 0 {
		result.MatchKind = matches[0].kind
	}
	return result, nil
}

// Aliases are metadata on an already registered ToolSpec. The returned callable
// always uses that spec's canonical name, never an alias as a command or endpoint.
func registeredToolAliases(tool Tool) []string {
	result := []string{}
	appendAliases := func(value any) {
		switch values := value.(type) {
		case []string:
			result = append(result, values...)
		case []any:
			for _, value := range values {
				if alias, ok := value.(string); ok {
					result = append(result, alias)
				}
			}
		}
	}
	appendAliases(tool.StandardMetadata["aliases"])
	if metadata, ok := tool.StandardMetadata["_meta"].(map[string]any); ok {
		appendAliases(metadata["agentdock/aliases"])
	}
	return result
}

func NormalizeSearchSelector(query, server string) (string, string, error) {
	rawQuery := strings.TrimSpace(query)
	query, server = strings.ToLower(rawQuery), strings.TrimSpace(server)
	qualifiedServer, tool, qualified := strings.Cut(rawQuery, ":")
	qualifiedServer, tool = strings.TrimSpace(qualifiedServer), strings.TrimSpace(tool)
	if !qualified || qualifiedServer == "" || tool == "" || strings.ContainsAny(qualifiedServer+tool, " \t\r\n") {
		return query, server, nil
	}
	if server != "" && !strings.EqualFold(server, qualifiedServer) {
		return query, server, newError("MCP_SERVER_QUERY_MISMATCH", "qualified MCP tool query conflicts with the selected server", false, map[string]any{"server": server, "query_server": qualifiedServer}, nil)
	}
	if server == "" {
		server = qualifiedServer
	}
	return strings.ToLower(tool), server, nil
}
