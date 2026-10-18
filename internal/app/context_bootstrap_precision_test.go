package app

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/config"
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
	// Activity storage rejects symlink ancestors; resolve platform temp aliases
	// before constructing the fixture's Home as well as its workspace.
	testRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setUserHomeForTest(t, testRoot)
	server := newPluginTestMCPServer(t)
	t.Cleanup(server.Close)
	cfg := config.Config{AgentDockHome: filepath.Join(testRoot, "home"), AgentDockDefaultDir: filepath.Join(testRoot, "workspace")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	// Always page a real Context, regardless of platform-specific Bootstrap size.
	workdir := rt.ws.Root()
	rulesPath := filepath.Join(workdir, "AGENTS.md")
	rules := strings.Repeat("Preserve each Context page and exact Bootstrap counts. 💡\n", 200) + "BOOTSTRAP_COUNTS_RULES_END\n"
	if err := os.WriteFile(rulesPath, []byte(rules), 0600); err != nil {
		t.Fatal(err)
	}
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
	args := map[string]any{"workdir": workdir}
	full, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatal(err)
	}
	fullSummary := modelObject(full["dynamic_mcp_summary"])
	if toolcore.IntArg(fullSummary, "tool_count", -1) != 1 || fullSummary["tool_count_known"] != true || toolcore.IntArg(fullSummary, "server_count", -1) != 1 {
		t.Fatalf("full Context lost the discovered tool count: %#v", fullSummary)
	}
	meta := modelObject(full["structured_output"])
	snapshotID := stringArg(meta, "snapshot_id")
	revision := stringArg(full, "context_revision")
	if snapshotID == "" || revision == "" || full["has_more"] != true || toolcore.IntArg(meta, "page_count", 0) < 2 || modelObject(full["delivery"])["sequence_complete"] != false {
		t.Fatalf("fixture did not leave Context details pending: %#v", full)
	}
	if full["bootstrap_complete"] != true || full["context_ready"] != true || full["source_complete"] != true || meta["source_complete"] != true {
		t.Fatalf("paging changed Context preparation or source completeness: %#v", full)
	}
	origin, err := rt.activity.Call(ctx, stringArg(full, "call_id"))
	if err != nil {
		t.Fatal(err)
	}
	source := stringArg(meta, "source")
	if origin.SourceOwnerKey != activity.SourceOwnerKey(ctx) || origin.ConversationID == "" || origin.ConversationID != stringArg(full, "conversation_id") || origin.OutputSource == nil || origin.OutputSource.State != "complete" || source == "" || source != activity.SourceReference(origin.CallID, origin.OutputSource) {
		t.Fatalf("Context source lost its original owner or payload version: %#v", origin)
	}
	// An indivisible semantic unit may omit next_cursor while details remain.
	// Consume the published source continuation; readiness alone is not delivery.
	continuation := modelObject(meta["continue_read"])
	var assembled strings.Builder
	for pageIndex := 0; ; pageIndex++ {
		if pageIndex >= 32 {
			t.Fatal("Context source continuation did not terminate")
		}
		readArgs := modelObject(continuation["arguments"])
		if continuation["tool"] != "read_file" || stringArg(readArgs, "path") != source || toolcore.IntArg(readArgs, "offset", -1) != assembled.Len() {
			t.Fatalf("Context source lost its exact byte continuation: %#v", continuation)
		}
		page, err := rt.Call(ctx, "read_file", readArgs)
		if err != nil {
			t.Fatal(err)
		}
		// Normalize the native int64 byte offsets through the same JSON boundary.
		if err := remarshal(page, &page); err != nil {
			t.Fatal(err)
		}
		delivery := modelObject(page["source_delivery"])
		if stringArg(page, "path") != source || stringArg(delivery, "snapshot_id") != snapshotID || stringArg(page, "conversation_id") != origin.ConversationID || toolcore.IntArg(page, "offset", -1) != assembled.Len() || toolcore.IntArg(delivery, "byte_start", -1) != assembled.Len() {
			t.Fatalf("Context source changed snapshot, owner scope or byte range: %#v", page)
		}
		content := stringArg(page, "content")
		if content == "" || page["source_complete"] != true || modelObject(page["capture"])["complete"] != true {
			t.Fatalf("Context source page is missing retained content: %#v", page)
		}
		assembled.WriteString(content)
		if toolcore.IntArg(page, "next_offset", -1) != assembled.Len() || toolcore.IntArg(delivery, "byte_end", -1) != assembled.Len() {
			t.Fatalf("Context source skipped bytes: %#v", page)
		}
		if page["has_more"] == false {
			if pageIndex == 0 || page["context_ready"] != true || delivery["has_more"] != false || modelObject(page["delivery"])["sequence_complete"] != true || int64(assembled.Len()) != origin.OutputSource.Bytes {
				t.Fatalf("Context source did not complete its retained sequence: %#v", page)
			}
			break
		}
		// Source reads acknowledge complete Context only at EOF, unlike Bootstrap.
		if page["has_more"] != true || page["context_ready"] != false || delivery["has_more"] != true || modelObject(page["delivery"])["sequence_complete"] != false {
			t.Fatalf("Context source acknowledged delivery before EOF: %#v", page)
		}
		continuation = modelObject(page["continue_read"])
	}
	var canonical Result
	if err := toolcore.DecodeJSON(strings.NewReader(assembled.String()), &canonical); err != nil {
		t.Fatal(err)
	}
	if stringArg(canonical, "context_revision") != revision {
		t.Fatalf("source changed Context revision: %#v", canonical["context_revision"])
	}
	var instructions agentinstructions.Snapshot
	if err := remarshal(canonical["instruction_files"], &instructions); err != nil {
		t.Fatal(err)
	}
	foundRules := false
	for _, file := range instructions.Files {
		if sameExistingTestPath(file.Path, rulesPath) {
			foundRules = true
			if file.Status != "loaded" || file.Content != strings.TrimSpace(rules) {
				t.Fatalf("Context source lost the complete workspace rules: %#v", file)
			}
		}
	}
	if !foundRules {
		t.Fatal("Context source omitted the workspace rules")
	}
	reference, err := rt.Call(ctx, "agentdock_context", args)
	if err != nil {
		t.Fatal(err)
	}
	if modelObject(reference["projection"])["mode"] != "reference" {
		t.Fatalf("delivered Context was not reused as a reference: %#v", reference["projection"])
	}
	if stringArg(reference, "context_revision") != revision || reference["unchanged"] != true || reference["source_complete"] != true || modelObject(reference["delivery"])["sequence_complete"] != true {
		t.Fatalf("Context reference changed its revision or delivery completeness: %#v", reference)
	}
	sourceSummary := modelObject(canonical["dynamic_mcp_summary"])
	referenceSummary := modelObject(reference["dynamic_mcp_summary"])
	for _, key := range []string{"tool_count", "server_count"} {
		if toolcore.IntArg(sourceSummary, key, -1) != toolcore.IntArg(fullSummary, key, -1) || toolcore.IntArg(referenceSummary, key, -1) != toolcore.IntArg(fullSummary, key, -1) {
			t.Fatalf("source or reference changed %s: full=%#v source=%#v reference=%#v", key, fullSummary, sourceSummary, referenceSummary)
		}
	}
	for _, key := range []string{"tool_count_known", "state", "catalog_revision", "absence_authoritative"} {
		if sourceSummary[key] != fullSummary[key] || referenceSummary[key] != fullSummary[key] {
			t.Fatalf("source or reference changed %s: full=%#v source=%#v reference=%#v", key, fullSummary, sourceSummary, referenceSummary)
		}
	}
}
