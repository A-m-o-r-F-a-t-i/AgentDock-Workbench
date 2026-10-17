package app

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	toolcore "github.com/uvwt/agentdock/internal/tool/core"
)

func TestContextCatalogSummaryPreservesRemarshalCounts(t *testing.T) {
	known := true
	items := []capabilityDynamicMCPItem{
		{Name: "first", ToolCount: 7, ToolCountKnown: &known, CatalogState: &mcpclient.CatalogState{State: "fresh"}},
		{Name: "second", ToolCount: 12, ToolCountKnown: &known, CatalogState: &mcpclient.CatalogState{State: "cached"}},
	}
	var servers []any
	if err := remarshal(items, &servers); err != nil {
		t.Fatal(err)
	}
	if modelObject(servers[0])["tool_count"] != json.Number("7") {
		t.Fatalf("fixture did not cross the canonical JSON boundary: %#v", servers)
	}
	summary := contextCatalogSummary(servers, nil, "catalog-test")
	if summary["tool_count"] != 19 || summary["tool_count_known"] != true || summary["server_count"] != 2 || summary["state"] != "cached" || summary["catalog_revision"] != "catalog-test" || summary["absence_authoritative"] != false {
		t.Fatalf("remarshaled directory counts or freshness changed: %#v", summary)
	}
}

func TestContextCatalogSummaryRejectsInvalidCounts(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  int
		known bool
	}{
		{"zero", 0, 0, true},
		{"native integer", 7, 7, true},
		{"integral float", float64(7), 7, true},
		{"JSON integer", json.Number("7"), 7, true},
		{"maximum integer", json.Number(strconv.Itoa(math.MaxInt)), math.MaxInt, true},
		{"fractional float", 7.5, 0, false},
		{"fractional JSON", json.Number("7.5"), 0, false},
		{"negative integer", -1, 0, false},
		{"negative JSON", json.Number("-1"), 0, false},
		{"overflowing float", math.Ldexp(1, strconv.IntSize-1), 0, false},
		{"overflowing JSON", json.Number("18446744073709551615"), 0, false},
		{"numeric string", "7", 0, false},
		{"missing", nil, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := contextCatalogSummary([]any{map[string]any{"tool_count": test.value, "tool_count_known": true}}, nil, "catalog-test")
			if summary["tool_count"] != test.want || summary["tool_count_known"] != test.known {
				t.Fatalf("count %T(%v) became %#v; want total=%d known=%t", test.value, test.value, summary, test.want, test.known)
			}
		})
	}
	t.Run("aggregate overflow", func(t *testing.T) {
		summary := contextCatalogSummary([]any{
			map[string]any{"tool_count": json.Number(strconv.Itoa(math.MaxInt)), "tool_count_known": true},
			map[string]any{"tool_count": json.Number("1"), "tool_count_known": true},
		}, nil, "catalog-test")
		if summary["tool_count"] != math.MaxInt || summary["tool_count_known"] != false {
			t.Fatalf("overflowing aggregate was reported as an exact count: %#v", summary)
		}
	})
}

func TestContextBootstrapCountsMatchFullAndReference(t *testing.T) {
	setUserHomeForTest(t, t.TempDir())
	server := newPluginTestMCPServer(t)
	t.Cleanup(server.Close)
	rt := newContextRegressionRuntime(t)
	if _, err := rt.capabilityManager.Add(mcpclient.ServerConfig{
		Name: "bootstrap-counts", Description: "Bootstrap count fixture", Transport: mcpclient.TransportStreamableHTTP, URL: server.URL, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Call(t.Context(), "mcp_tool_list", map[string]any{"server": "bootstrap-counts"}); err != nil {
		t.Fatal(err)
	}
	ctx := WithModelProjection(activity.WithSource(t.Context(), activity.Source{
		Principal: "bootstrap-count-test", Namespace: "mcp", HostConversationID: "bootstrap-counts",
	}))
	args := map[string]any{"workdir": rt.cfg.AgentDockDefaultDir, "max_response_bytes": 32000}
	full, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatal(err)
	}
	fullSummary := modelObject(full["dynamic_mcp_summary"])
	if toolcore.IntArg(fullSummary, "tool_count", -1) != 1 || fullSummary["tool_count_known"] != true || toolcore.IntArg(fullSummary, "server_count", -1) != 1 {
		t.Fatalf("full Context lost the discovered tool count: %#v", fullSummary)
	}
	page := full
	for attempts := 0; stringArg(page, "next_cursor") != ""; attempts++ {
		if attempts >= 16 {
			t.Fatal("Context continuation did not terminate")
		}
		args["cursor"] = page["next_cursor"]
		page, err = rt.Call(ctx, "agentdock_context", args)
		if err != nil {
			t.Fatal(err)
		}
	}
	delete(args, "cursor")
	reference, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatal(err)
	}
	if modelObject(reference["projection"])["mode"] != "reference" {
		t.Fatalf("delivered Context was not reused as a reference: %#v", reference["projection"])
	}
	referenceSummary := modelObject(reference["dynamic_mcp_summary"])
	for _, key := range []string{"tool_count", "server_count"} {
		if toolcore.IntArg(referenceSummary, key, -1) != toolcore.IntArg(fullSummary, key, -1) {
			t.Fatalf("reference changed %s: full=%#v reference=%#v", key, fullSummary, referenceSummary)
		}
	}
	for _, key := range []string{"tool_count_known", "state", "catalog_revision", "absence_authoritative"} {
		if referenceSummary[key] != fullSummary[key] {
			t.Fatalf("reference changed %s: full=%#v reference=%#v", key, fullSummary, referenceSummary)
		}
	}
}
