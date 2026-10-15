package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
)

func TestReadFileDescriptionUsesHostIssuedSkillURI(t *testing.T) {
	definition, ok := toolDefinitionForConfig("read_file", config.Config{})
	if !ok {
		t.Fatal("read_file definition is missing")
	}
	for _, want := range []string{"host-issued skill://", "agentdock_context", "workspace_context", "do not construct"} {
		if !strings.Contains(definition.Description, want) {
			t.Fatalf("read_file description missing %q: %s", want, definition.Description)
		}
	}
	for _, stale := range []string{"skill://<name>", "active Skill version"} {
		if strings.Contains(definition.Description, stale) {
			t.Fatalf("read_file description still contains stale Skill lifecycle text %q: %s", stale, definition.Description)
		}
	}
}

func TestReadFileSupportsSkillURI(t *testing.T) {
	cfg := config.Config{
		AgentDockDefaultDir: t.TempDir(),
		AgentDockHome:       filepath.Join(t.TempDir(), ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	packageDir := installDocumentSkillForTest(t, rt, "demo-skill", "1.0.0", "Read Skill resources in tests.")
	if err := os.MkdirAll(filepath.Join(packageDir, "references"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "references", "guide.md"), []byte("# Guide\n\nUse the safe workflow.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := rt.Call(context.Background(), "read_file", map[string]any{
		"path": "skill://managed/demo-skill/references/guide.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["path"] != "skill://managed/demo-skill/references/guide.md" {
		t.Fatalf("unexpected logical path: %#v", result["path"])
	}
	content, _ := result["content"].(string)
	if !strings.Contains(content, "safe workflow") {
		t.Fatalf("unexpected Skill resource content: %q", content)
	}
}

func TestSkillDocumentPagesThenReusesCompletedRevision(t *testing.T) {
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), ".agentdock")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	packageDir := installDocumentSkillForTest(t, rt, "demo-skill", "1.0.0", "Page and reuse Skill documents.")
	path := filepath.Join(packageDir, "SKILL.md")
	body := "---\nname: demo-skill\ndescription: Page and reuse Skill documents.\n---\n\n# Contract\n\n" + strings.Repeat("完整契约段落💡\n", 3200)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := WithModelProjection(activity.WithSource(context.Background(), activity.Source{
		Principal: "skill-contract-test", Namespace: "mcp", HostConversationID: "skill-contract-revision",
	}))
	args := map[string]any{"path": "skill://managed/demo-skill/SKILL.md"}
	var assembled strings.Builder
	revision := ""
	for pageIndex := 0; pageIndex < 100; pageIndex++ {
		page, err := rt.Call(ctx, "read_file", args)
		if err != nil {
			t.Fatal(err)
		}
		assembled.WriteString(stringArg(page, "content"))
		current := stringArg(page, "source_revision")
		if revision == "" {
			revision = current
		} else if current != revision {
			t.Fatal("Skill source revision changed during immutable paging")
		}
		call, err := rt.activity.Call(context.Background(), stringArg(page, "call_id"))
		if err != nil {
			t.Fatal(err)
		}
		rt.commitStructuredDelivery(call.Binding, page)
		if page["has_more"] != true {
			break
		}
		args = modelObject(modelObject(page["continue_read"])["arguments"])
		if args == nil {
			t.Fatal("Skill page lost its exact byte continuation")
		}
	}
	if assembled.String() != body {
		t.Fatalf("Skill contract paging changed content: %d/%d bytes", assembled.Len(), len(body))
	}

	reference, err := rt.Call(ctx, "read_file", map[string]any{"path": "skill://managed/demo-skill/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if reference["unchanged"] != true || stringArg(reference, "content") != "" || stringArg(reference, "source_revision") != revision {
		t.Fatalf("completed Skill revision was resent: %#v", reference)
	}
	if modelObject(reference["projection"])["mode"] != "reference" || modelObject(reference["delivery"])["sequence_complete"] != true {
		t.Fatalf("Skill revision reference state = %#v", reference)
	}

	changed := body + "\n## New revision\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := rt.Call(ctx, "read_file", map[string]any{"path": "skill://managed/demo-skill/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh["unchanged"] == true || stringArg(fresh, "source_revision") == revision || stringArg(fresh, "content") == "" {
		t.Fatalf("changed Skill reused stale revision: %#v", fresh)
	}
}

func TestReadFileRejectsSkillURITraversalAndSymlinkEscape(t *testing.T) {
	cfg := config.Config{
		AgentDockDefaultDir: t.TempDir(),
		AgentDockHome:       filepath.Join(t.TempDir(), ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	rt, err := newUnrestrictedTestRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	packageDir := installDocumentSkillForTest(t, rt, "demo-skill", "1.0.0", "Reject escaping Skill resources.")

	_, err = rt.Call(context.Background(), "read_file", map[string]any{
		"path": "skill://managed/demo-skill/../outside.txt",
	})
	assertToolErrorCode(t, err, "INVALID_SKILL_URI")

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(packageDir, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = rt.Call(context.Background(), "read_file", map[string]any{
		"path": "skill://managed/demo-skill/escape.txt",
	})
	assertToolErrorCode(t, err, "SKILL_PATH_ESCAPE")
}

func assertToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolError %s, got %T: %v", code, err, err)
	}
	if toolErr.Code != code {
		t.Fatalf("ToolError code = %s, want %s: %v", toolErr.Code, code, err)
	}
}
