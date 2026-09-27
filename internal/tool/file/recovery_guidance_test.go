package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathFailuresProvideTypedRecoveryGuidance(t *testing.T) {
	svc, root := newFileTestService(t)
	ctx := context.Background()

	_, err := svc.ReadFile(ctx, ReadRequest{Path: "missing/file.txt"})
	missing := requireFileToolError(t, err, "PATH_NOT_FOUND")
	if missing.Details["failure_class"] != "AGENT_INPUT_INVALID" || missing.Details["next_action"] != ToolListDir || missing.Details["retryable"] != false {
		t.Fatalf("missing path guidance = %#v", missing.Details)
	}
	if retry, ok := missing.Details["retry_arguments"].(map[string]any); !ok || retry["path"] != "." {
		t.Fatalf("missing path retry arguments = %#v", missing.Details["retry_arguments"])
	}

	if err := os.Mkdir(filepath.Join(root, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReadFile(ctx, ReadRequest{Path: "folder"})
	directory := requireFileToolError(t, err, "IS_DIRECTORY")
	if directory.Details["actual_type"] != "directory" || directory.Details["next_action"] != ToolListDir {
		t.Fatalf("directory read guidance = %#v", directory.Details)
	}

	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ListDir(ctx, ListRequest{Path: "plain.txt"})
	file := requireFileToolError(t, err, "NOT_A_DIRECTORY")
	if file.Details["actual_type"] != "file" || file.Details["next_action"] != ToolReadFile {
		t.Fatalf("file listing guidance = %#v", file.Details)
	}
}

func TestInvalidRegexIsRejectedBeforeSelectingSearchRuntime(t *testing.T) {
	svc, _ := newFileTestService(t)
	_, err := svc.SearchText(context.Background(), SearchRequest{Path: ".", Query: "(", Regex: true})
	toolErr := requireFileToolError(t, err, "INVALID_REGEX")
	if toolErr.Details["failure_class"] != "AGENT_INPUT_INVALID" || toolErr.Details["next_action"] != ToolSearchText || toolErr.Details["retryable"] != false {
		t.Fatalf("invalid regex guidance = %#v", toolErr.Details)
	}
	arguments, ok := toolErr.Details["recommended_arguments"].(map[string]any)
	if !ok || arguments["regex"] != false {
		t.Fatalf("invalid regex recommended arguments = %#v", toolErr.Details["recommended_arguments"])
	}
}

func TestSearchResourceLimitProvidesNarrowingGuidance(t *testing.T) {
	svc, root := newFileTestService(t)
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := svc.ws.ResolveExisting(".")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.searchTextGoWithLimits(context.Background(), resolved, SearchOptions{
		Query:      "needle",
		MaxResults: 10,
	}, searchFallbackLimits{
		MaxEntries:    1,
		MaxFiles:      10,
		MaxFileBytes:  1 << 20,
		MaxTotalBytes: 1 << 20,
		Timeout:       time.Second,
	})
	toolErr := requireFileToolError(t, err, "RESOURCE_LIMIT")
	if toolErr.Details["failure_class"] != "AGENT_REQUEST_TOO_BROAD" || toolErr.Details["next_action"] != ToolSearchText || toolErr.Details["retryable"] != false {
		t.Fatalf("resource limit guidance = %#v", toolErr.Details)
	}
	adjustments, ok := toolErr.Details["recommended_adjustments"].([]string)
	if !ok || len(adjustments) < 4 {
		t.Fatalf("resource limit adjustments = %#v", toolErr.Details["recommended_adjustments"])
	}
}

func TestPathResolutionErrorPreservesUnrelatedErrors(t *testing.T) {
	original := errors.New("permission denied")
	if got := pathResolutionError(original, "file.txt", "."); !errors.Is(got, original) {
		t.Fatalf("pathResolutionError() = %v, want original error", got)
	}
}

func requireFileToolError(t *testing.T, err error, code string) *ToolError {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
	return toolErr
}
