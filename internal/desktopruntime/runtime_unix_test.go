//go:build darwin || linux

package desktopruntime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDarwinExecutableFromAppBundle(t *testing.T) {
	if !darwinExecutableFromAppBundle("/Applications/AgentDock.app/Contents/Helpers/agentdock") {
		t.Fatal("App Helper must be treated as SMAppService install")
	}
	if !darwinExecutableFromAppBundle(`/Applications/AgentDock.app/Contents/MacOS/AgentDock`) {
		t.Fatal("App executable must be treated as App Bundle")
	}
	if darwinExecutableFromAppBundle(filepath.Join("/Users/me", ".local", "bin", "agentdock")) {
		t.Fatal("CLI binary must not be treated as App Bundle")
	}
}

func TestLoadUnixRuntimeCLIUsesLaunchAgentLabels(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("CLI vs App Bundle dispatch is Darwin-only")
	}
	root := t.TempDir()
	manifest, _, err := loadUnixRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ServiceManager != "launchd" {
		t.Fatalf("test binary is not an App Bundle, manager=%s", manifest.ServiceManager)
	}
	if manifest.ServiceName != "com.uvwt.agentdock" || manifest.TunnelServiceName != "com.uvwt.agentdock.cloudflared" {
		t.Fatalf("CLI labels=%s %s", manifest.ServiceName, manifest.TunnelServiceName)
	}
}

func TestHealthyRequiresStructuredOKPayload(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{name: "healthy", body: `{"ok":true,"version":"1.1.9"}`, want: true},
		{name: "explicit false", body: `{"ok":false,"version":"1.1.9"}`, want: false},
		{name: "plain 200", body: `ready`, want: false},
		{name: "missing ok", body: `{"version":"1.1.9"}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, closeServer := healthFixture(t, test.body)
			defer closeServer()
			if got := healthy(context.Background(), values); got != test.want {
				t.Fatalf("healthy=%v want %v", got, test.want)
			}
		})
	}
}

func TestHealthyVersionRejectsOlderCore(t *testing.T) {
	values, closeServer := healthFixture(t, `{"ok":true,"version":"v1.1.8"}`)
	defer closeServer()
	if !healthy(context.Background(), values) {
		t.Fatal("generic health should accept an explicitly healthy Core")
	}
	if healthyVersion(context.Background(), values, "1.1.9") {
		t.Fatal("version-bound health accepted an older Core")
	}
}

func TestWaitHealthyVersionReportsTarget(t *testing.T) {
	values, closeServer := healthFixture(t, `{"ok":true,"version":"1.1.8"}`)
	defer closeServer()
	err := waitHealthyVersion(context.Background(), values, "1.1.9", 20*time.Millisecond)
	if err == nil || !containsAll(err.Error(), "1.1.9", "未就绪") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func healthFixture(t *testing.T, body string) (map[string]string, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(response, body)
	}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return map[string]string{
		"AGENTDOCK_HOST": host,
		"AGENTDOCK_PORT": strconv.Itoa(port),
	}, server.Close
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
