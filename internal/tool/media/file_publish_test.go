package media

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/workspace"
)

func TestFilePublishSchemaAcceptsMountedAndConnectorFiles(t *testing.T) {
	schema, ok := InputSchema(ToolFilePublish)
	if !ok {
		t.Fatal("file_publish input schema missing")
	}
	properties := schema["properties"].(map[string]any)
	file := properties["file"].(map[string]any)
	branches := file["anyOf"].([]map[string]any)
	if len(branches) != 3 || branches[0]["type"] != "string" || branches[0]["format"] != "binary" {
		t.Fatalf("file anyOf = %#v", branches)
	}
	mounted := branches[1]
	if mounted["type"] != "object" {
		t.Fatalf("mounted file branch = %#v", mounted)
	}
	mountedProperties := mounted["properties"].(map[string]any)
	for _, key := range []string{"local_path", "file_path", "mount_path", "path", "filename"} {
		if _, exists := mountedProperties[key]; !exists {
			t.Fatalf("mounted file schema missing %q", key)
		}
	}
	object := branches[2]
	if object["type"] != "object" {
		t.Fatalf("connector file branch = %#v", object)
	}
	objectProperties := object["properties"].(map[string]any)
	for _, key := range []string{"download_url", "file_id", "file_name", "mime_type"} {
		if _, exists := objectProperties[key]; !exists {
			t.Fatalf("connector file schema missing %q", key)
		}
	}
	required := object["required"].([]string)
	for _, key := range []string{"download_url", "file_id"} {
		if !slices.Contains(required, key) {
			t.Fatalf("connector file required = %#v, missing %q", required, key)
		}
	}
	oneOf := schema["oneOf"].([]map[string]any)
	if len(oneOf) != 2 || !slices.Equal(oneOf[0]["required"].([]string), []string{"file"}) || !slices.Equal(oneOf[1]["required"].([]string), []string{"path"}) {
		t.Fatalf("file_publish source oneOf = %#v", oneOf)
	}
}

func TestFilePublishKeepsLocalSourceCompatibilityAndMountedPrecedence(t *testing.T) {
	service, root, _ := newFilePublishTestService(t)
	payload := []byte("local payload")
	if err := os.WriteFile(filepath.Join(root, "local.txt"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	service.connectorDownloadClient = func() *http.Client {
		t.Fatal("mounted path unexpectedly attempted a network download")
		return nil
	}

	requests := []FilePublishRequest{
		{File: "local.txt"},
		{File: map[string]any{
			"local_path":   "local.txt",
			"download_url": "file:///must-not-be-used",
			"file_id":      "file-mounted",
		}},
		{Path: "local.txt"},
	}
	for index, request := range requests {
		result, err := service.FilePublish(t.Context(), request)
		if err != nil {
			t.Fatalf("request %d: %v", index, err)
		}
		if stringValue(result["artifact_id"]) == "" || resultSize(result) != int64(len(payload)) {
			t.Fatalf("request %d result = %#v", index, result)
		}
	}

	_, err := service.FilePublish(t.Context(), FilePublishRequest{File: "local.txt", Path: "local.txt"})
	assertMediaToolErrorCode(t, err, "FILE_PUBLISH_SOURCE_CONFLICT")
}

func TestFilePublishDownloadsStandardConnectorObjectAndCleansTemporarySource(t *testing.T) {
	payload := []byte("connector file payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/download" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	serverURL := mustURL(t, server.URL)
	serverTransport := server.Client().Transport
	service, _, home := newFilePublishTestService(t)
	service.connectorDownloadClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			forwarded := request.Clone(request.Context())
			forwardedURL := *request.URL
			forwardedURL.Scheme = serverURL.Scheme
			forwardedURL.Host = serverURL.Host
			forwarded.URL = &forwardedURL
			return serverTransport.RoundTrip(forwarded)
		})}
	}
	result, err := service.FilePublish(t.Context(), FilePublishRequest{File: map[string]any{
		"download_url": "https://files.example/download",
		"file_id":      `C:\this-is-an-identity-not-a-path`,
		"file_name":    "../review.txt",
		"mime_type":    "text/plain",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(result["artifact_id"]) == "" || resultSize(result) != int64(len(payload)) || result["filename"] != "review.txt" {
		t.Fatalf("result = %#v", result)
	}
	assertNoFilePublishTempDirectories(t, home)
}

func TestDownloadConnectorFileUsesPrivateTemporaryFile(t *testing.T) {
	service, _, home := newFilePublishTestService(t)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return downloadResponse(request, -1, "private payload"), nil
	})}
	pathValue, cleanup, err := service.downloadConnectorFileWithLimit(t.Context(), connectorFileInput{
		DownloadURL: "https://files.example/download",
		FileID:      "file-private",
		FileName:    "private.txt",
	}, client, 1024)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(pathValue)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("temp file mode = %o, want 600", info.Mode().Perm())
	}
	cleanup()
	if _, err := os.Stat(pathValue); !os.IsNotExist(err) {
		t.Fatalf("temp file still exists after cleanup: %v", err)
	}
	assertNoFilePublishTempDirectories(t, home)
}

func TestDownloadConnectorFileEnforcesDeclaredAndActualLimits(t *testing.T) {
	for _, test := range []struct {
		name          string
		contentLength int64
		body          string
	}{
		{name: "declared", contentLength: 5, body: "1234"},
		{name: "actual", contentLength: -1, body: "12345"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, home := newFilePublishTestService(t)
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return downloadResponse(request, test.contentLength, test.body), nil
			})}
			_, _, err := service.downloadConnectorFileWithLimit(t.Context(), connectorFileInput{
				DownloadURL: "https://files.example/download",
				FileID:      "file-limit",
			}, client, 4)
			assertMediaToolErrorCode(t, err, "FILE_PUBLISH_FILE_TOO_LARGE")
			assertNoFilePublishTempDirectories(t, home)
		})
	}
}

func TestConnectorDownloadNetworkPolicy(t *testing.T) {
	blocked := []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.0.2.1", "192.168.1.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "240.0.0.1", "::1", "fc00::1", "fe80::1", "2001:db8::1", "3fff::1",
	}
	for _, raw := range blocked {
		if !blockedConnectorDownloadIP(net.ParseIP(raw)) {
			t.Errorf("blockedConnectorDownloadIP(%q) = false", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedConnectorDownloadIP(net.ParseIP(raw)) {
			t.Errorf("blockedConnectorDownloadIP(%q) = true", raw)
		}
	}
	if err := validateConnectorResolvedAddresses([]net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}); err == nil {
		t.Fatal("mixed public/private DNS result was accepted")
	}
	if _, err := validateConnectorDownloadURL("https://127.0.0.1/file"); err == nil {
		t.Fatal("private literal download URL was accepted")
	}

	client := newConnectorDownloadClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("connector transport proxy = %#v", client.Transport)
	}
	redirectCases := []struct {
		name string
		url  string
		via  []*http.Request
	}{
		{name: "unsupported scheme", url: "file:///tmp/file"},
		{name: "private literal", url: "https://127.0.0.1/file"},
		{name: "HTTPS downgrade", url: "http://files.example/file", via: []*http.Request{{URL: mustURL(t, "https://files.example/start")}}},
		{name: "redirect limit", url: "https://files.example/file", via: repeatedRequests(t, maxConnectorRedirects)},
	}
	for _, test := range redirectCases {
		t.Run(test.name, func(t *testing.T) {
			if err := client.CheckRedirect(&http.Request{URL: mustURL(t, test.url)}, test.via); err == nil {
				t.Fatal("unsafe redirect was accepted")
			}
		})
	}
	if err := client.CheckRedirect(
		&http.Request{URL: mustURL(t, "https://cdn.example/file")},
		[]*http.Request{{URL: mustURL(t, "https://files.example/start")}},
	); err != nil {
		t.Fatalf("safe redirect rejected: %v", err)
	}
}

func newFilePublishTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "workspace")
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	return New(config.Config{AgentDockHome: home}, ws, nil), root, home
}

func resultSize(result Result) int64 {
	switch value := result["size_bytes"].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return -1
	}
}

func assertMediaToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func assertNoFilePublishTempDirectories(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "tmp"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "file-publish-") {
			t.Fatalf("temporary directory was not removed: %s", entry.Name())
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func downloadResponse(request *http.Request, contentLength int64, body string) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
		Request:       request,
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func repeatedRequests(t *testing.T, count int) []*http.Request {
	t.Helper()
	requests := make([]*http.Request, count)
	for index := range requests {
		requests[index] = &http.Request{URL: mustURL(t, "https://files.example/redirect")}
	}
	return requests
}
