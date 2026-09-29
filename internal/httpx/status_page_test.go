package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusPageRendersConnectionAndResourceLinks(t *testing.T) {
	cfg := testConfig(t)
	cfg.OAuthServerURL = "https://agentdock.example.com"
	cfg.OAuthEnabled = true
	cfg.ACPEnabled = true
	cfg.BrowserEnabled = true
	cfg.NexusEndpoint = "http://127.0.0.1:18777"

	response := httptest.NewRecorder()
	statusPageHandler(nil, cfg).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	for header, expected := range map[string]string{
		"Cache-Control":           "no-store",
		"Content-Security-Policy": statusPageCSP,
		"Referrer-Policy":         "no-referrer",
		"Vary":                    "Accept-Language",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	} {
		if got := response.Header().Get(header); got != expected {
			t.Fatalf("%s = %q, want %q", header, got, expected)
		}
	}
	body := response.Body.String()
	for _, expected := range []string{
		"AgentDock Workbench",
		"AI Agent task and execution workbench",
		"Workbench instance is ready.",
		"https://agentdock.example.com/mcp",
		"https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench",
		"https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases",
		"https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/tree/main/docs",
		`class="state-enabled"`,
		`class="state-auth"`,
		`class="resource resource-releases"`,
		`class="resource resource-documentation"`,
		">OAuth<",
		">Enabled<",
		"navigator.clipboard.writeText",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("status page missing %q", expected)
		}
	}
	for _, unexpected := range []string{
		"github.com/uvwt/agentdock",
		"uvwt.github.io/agentdock-docs",
		"1081337019",
		"QQ Group",
	} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("status page unexpectedly contains upstream-only resource %q", unexpected)
		}
	}
}

func TestStatusPageUsesChineseForChineseBrowserLanguage(t *testing.T) {
	cfg := testConfig(t)
	request := httptest.NewRequest(http.MethodGet, "https://dock.example/", nil)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	response := httptest.NewRecorder()

	statusPageHandler(nil, cfg).ServeHTTP(response, request)

	body := response.Body.String()
	for _, expected := range []string{
		`<html lang="zh-CN">`,
		"AI Agent 任务与执行工作台",
		"工作台实例已就绪。",
		"MCP 端点",
		`data-copied="已复制"`,
		"GitHub 仓库",
		"版本发布",
		"下载安装包并查看版本说明。",
		"查看本分支的架构、权限、安装与开发说明。",
		`href="https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases"`,
		`href="https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/tree/main/docs"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Chinese status page missing %q", expected)
		}
	}
}

func TestAuthLabelCoversConfiguredAuthModes(t *testing.T) {
	tests := []struct {
		name    string
		oauth   bool
		token   string
		english string
		chinese string
	}{
		{name: "none", english: "None", chinese: "无"},
		{name: "token", token: "secret", english: "Token", chinese: "访问令牌"},
		{name: "oauth", oauth: true, english: "OAuth", chinese: "OAuth"},
		{name: "oauth and token", oauth: true, token: "secret", english: "OAuth + Token", chinese: "OAuth + 访问令牌"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.OAuthEnabled = test.oauth
			cfg.AuthToken = test.token
			if got := authLabel(statusPageEnglish, cfg); got != test.english {
				t.Fatalf("English auth label = %q, want %q", got, test.english)
			}
			if got := authLabel(statusPageChinese, cfg); got != test.chinese {
				t.Fatalf("Chinese auth label = %q, want %q", got, test.chinese)
			}
		})
	}
}

func TestPreferredStatusPageTextRespectsLanguageQuality(t *testing.T) {
	tests := []struct {
		header string
		lang   string
	}{
		{header: "zh-CN,zh;q=0.9,en;q=0.8", lang: "zh-CN"},
		{header: "en-US,en;q=0.9,zh;q=0.8", lang: "en"},
		{header: "fr-FR,zh;q=0.8,en;q=0.7", lang: "zh-CN"},
		{header: "fr-FR", lang: "en"},
		{header: "zh;q=0,en;q=0.5", lang: "en"},
		{header: "en;q=0.4,zh;q=0.9", lang: "zh-CN"},
		{header: "zh;q=invalid,en;q=0.5", lang: "en"},
	}

	for _, test := range tests {
		t.Run(test.header, func(t *testing.T) {
			if got := preferredStatusPageText(test.header).Lang; got != test.lang {
				t.Fatalf("preferredStatusPageText(%q).Lang = %q, want %q", test.header, got, test.lang)
			}
		})
	}
}

func TestStatusPageUsesRequestOriginWithoutConfiguredPublicURL(t *testing.T) {
	cfg := testConfig(t)
	request := httptest.NewRequest(http.MethodGet, "https://dock.example/", nil)
	response := httptest.NewRecorder()

	statusPageHandler(nil, cfg).ServeHTTP(response, request)

	if !strings.Contains(response.Body.String(), "https://dock.example/mcp") {
		t.Fatalf("status page endpoint = %s", response.Body.String())
	}
}

func TestStatusPageOnlyHandlesExactRoot(t *testing.T) {
	cfg := testConfig(t)
	response := httptest.NewRecorder()

	statusPageHandler(nil, cfg).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestStatusPageRejectsUnsupportedMethods(t *testing.T) {
	cfg := testConfig(t)
	response := httptest.NewRecorder()

	statusPageHandler(nil, cfg).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
}
