package file

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditNegativeSearchIsLiteralArgument(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		if os.Getenv("AGENTDOCK_REQUIRE_RG") == "1" {
			t.Fatal("native ripgrep regression is required")
		}
		t.Skip("ripgrep is not installed")
	}
	rt, root := newCodeToolsRuntime(t)
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("angle=-140.90\n-Werror\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := rt.ws.ResolveExisting(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"-140.90", "-Werror"} {
		result, available, err := rt.searchTextRG(t.Context(), path, SearchOptions{Query: query, CaseSensitive: true, MaxResults: 10})
		if err != nil || !available || result["total_matches"] != 1 {
			t.Fatalf("negative query was interpreted as an rg option: %#v %v", result, err)
		}
	}
}

func TestAuditExactSearchSkipsUnrelatedTreeBeforeReading(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	if err := os.Mkdir(filepath.Join(root, "a-cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if err := os.WriteFile(filepath.Join(root, "a-cache", fmt.Sprintf("%d.txt", i)), []byte("unrelated"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Preferences"), []byte("needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := rt.ws.ResolveExisting(".")
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.searchTextGoWithLimits(t.Context(), path, SearchOptions{
		Query: "needle", CaseSensitive: true, IncludeGlobs: []string{"Preferences"}, MaxResults: 10,
	}, searchFallbackLimits{MaxEntries: 1, MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 1024, Timeout: time.Second})
	if err != nil || result["total_matches"] != 1 || result["files_scanned"] != 1 {
		t.Fatalf("exact filename search traversed unrelated paths: %#v %v", result, err)
	}
	result, err = rt.searchTextGoWithLimits(t.Context(), path, SearchOptions{
		Query: "needle", CaseSensitive: true, ExcludeGlobs: []string{"a-cache/**"}, MaxResults: 10,
	}, searchFallbackLimits{MaxEntries: 3, MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 1024, Timeout: time.Second})
	if err != nil || result["total_matches"] != 1 {
		t.Fatalf("excluded subtree was traversed: %#v %v", result, err)
	}
}

func TestAuditExactSearchPreservesHiddenAndIgnoredParents(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	for _, name := range []string{".hidden/value.txt", "node_modules/value.txt", "ignored/value.txt"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("needle"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := rt.SearchText(t.Context(), SearchRequest{Path: ".", Query: "needle", IncludeGlobs: []string{".hidden/value.txt", "node_modules/value.txt", "ignored/value.txt"}})
	if err != nil || result["total_matches"] != 0 {
		t.Fatalf("exact scope widened hidden or ignored access: %#v %v", result, err)
	}
}

func TestAuditExactSearchDoesNotFollowSymlinkParents(t *testing.T) {
	rt, root := newCodeToolsRuntime(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "value.txt"), []byte("private needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := rt.SearchText(t.Context(), SearchRequest{Path: ".", Query: "needle", IncludeGlobs: []string{"link/value.txt"}})
	if err != nil || result["total_matches"] != 0 {
		t.Fatalf("exact scope followed a symlink parent: %#v %v", result, err)
	}
}

func TestAuditDirectoryExclusionKeepsPartialPatterns(t *testing.T) {
	if excludedSearchDirectory("cache", []string{"cache/*"}) || excludedSearchDirectory("cache/nested", []string{"cache/*"}) {
		t.Fatal("single-level glob pruned deeper descendants")
	}
	for _, pattern := range []string{"cache/**", "cache/**/*", "**/cache/**"} {
		if !excludedSearchDirectory("cache", []string{pattern}) {
			t.Fatalf("full subtree %q was not pruned", pattern)
		}
	}
}

type auditReaderOnly struct{ io.Reader }

func TestAuditSearchCaptureIsBoundedEvenThroughIOCopy(t *testing.T) {
	cancels := 0
	capture := &searchOutputCapture{limit: 8, cancel: func() { cancels++ }}
	written, err := io.Copy(capture, auditReaderOnly{strings.NewReader(strings.Repeat("x", 4096))})
	if err != nil || written != 4096 || capture.Len() != 8 || !capture.exceeded || cancels != 1 {
		t.Fatalf("capture bypassed its bound: written=%d bytes=%d exceeded=%t cancels=%d err=%v", written, capture.Len(), capture.exceeded, cancels, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := walkSearchScope(ctx, t.TempDir(), SearchOptions{IncludeGlobs: []string{"missing.txt"}}, func(string, os.DirEntry, error) error { return nil }); err != context.Canceled {
		t.Fatalf("exact scope lost cancellation: %v", err)
	}
}
