package capabilityrouting

import (
	"strings"
	"testing"
)

func TestComputerUseCapabilitiesRequireExplicitSelection(t *testing.T) {
	if !RequiresExplicitPluginLoad("computer-use") || RequiresExplicitPluginLoad("github") {
		t.Fatal("wrong explicit-load classification")
	}
	if !RequiresExplicitMCPSelection("", "cua-driver") || !RequiresExplicitMCPSelection("computer-use", "other") {
		t.Fatal("Computer Use MCP should require explicit selection")
	}
	if RequiresExplicitMCPSelection("github", "github") {
		t.Fatal("ordinary MCP was classified as Computer Use")
	}
	if !RequiresDesktopGUIIntent("computer-use", "cua-driver:launch_app") {
		t.Fatal("launch_app should require desktop GUI intent")
	}
	if ValidDesktopGUIIntent("desktop_gui", "") || !ValidDesktopGUIIntent("desktop_gui", "Open the user-requested Windows Settings page") {
		t.Fatal("desktop GUI intent validation is incorrect")
	}
}

func TestComputerUseDescriptionsRoutePersonalAndTemporaryBrowsers(t *testing.T) {
	for _, want := range []string{"个人浏览器", "current-browser-control Skill", "不是动态 MCP Server", "禁止 existing_profile", "不得改用 cua-driver"} {
		if !strings.Contains(ComputerUseRule, want) {
			t.Fatalf("ComputerUseRule missing %q: %s", want, ComputerUseRule)
		}
	}

	plugin := PluginDescription("computer-use", "Windows browser and desktop control.")
	for _, want := range []string{"个人或当前已打开的浏览器", "不是 MCP Server", "CUA 不得接管个人浏览器", "隔离临时浏览器", "禁止 existing_profile"} {
		if !strings.Contains(plugin, want) {
			t.Fatalf("plugin description missing %q: %s", want, plugin)
		}
	}

	current := SkillDescription("computer-use", "current-browser-control", "Control the current browser.")
	for _, want := range []string{"必须使用本 Skill", "exec_command", "skill_ref", "不是动态 MCP Server", "不得切换到 CUA"} {
		if !strings.Contains(current, want) {
			t.Fatalf("current-browser-control description missing %q: %s", want, current)
		}
	}

	cua := ServerDescription("computer-use", "cua-driver", "Control Windows.")
	for _, want := range []string{"隔离临时浏览器", "不得操作用户个人或当前浏览器", "不得连接现有浏览器 PID", "禁止 existing_profile"} {
		if !strings.Contains(cua, want) {
			t.Fatalf("cua-driver description missing %q: %s", want, cua)
		}
	}

	prepare := ToolDescription("computer-use", "cua-driver:browser_prepare", "Prepare a browser.")
	for _, want := range []string{"只允许操作由 CUA 自行启动的隔离临时浏览器", "禁止 existing_profile", "个人浏览器必须执行 current-browser-control Skill"} {
		if !strings.Contains(prepare, want) {
			t.Fatalf("browser_prepare description missing %q: %s", want, prepare)
		}
	}

	state := ToolDescription("computer-use", "cua-driver:get_browser_state", "Inspect a browser.")
	if !strings.Contains(state, "隔离临时浏览器") || !strings.Contains(state, "不得连接或控制用户个人/当前浏览器") {
		t.Fatalf("get_browser_state did not inherit temporary-browser boundary: %s", state)
	}

	desktop := ToolDescription("computer-use", "cua-driver:click", "Click a native control.")
	if !strings.Contains(desktop, "不得选择或操作用户个人/当前浏览器") || !strings.Contains(desktop, "interaction_intent=desktop_gui") {
		t.Fatalf("ordinary CUA tool lost routing or intent boundary: %s", desktop)
	}

	native := ToolDescription("computer-use", "chatgpt-native-computer-use:click", "Click through the native backend.")
	if !strings.Contains(native, "不得自动接管用户个人/当前浏览器") || !strings.Contains(native, "interaction_intent=desktop_gui") {
		t.Fatalf("native Computer Use tool lost routing or intent boundary: %s", native)
	}
}

func TestComputerUseDescriptionsLeadWithRoutingBoundary(t *testing.T) {
	launch := ToolDescription("computer-use", "cua-driver:launch_app", "Launch a Windows app.")
	for _, want := range []string{"尚未运行", "interaction_intent=desktop_gui", "不得用它向用户个人浏览器打开 URL", "不得用于能力探测", "禁止 existing_profile", "Launch a Windows app"} {
		if !strings.Contains(launch, want) {
			t.Fatalf("launch_app description missing %q: %s", want, launch)
		}
	}
	ordinary := ToolDescription("github", "github:get_me", "Get the current user.")
	if ordinary != "Get the current user." {
		t.Fatalf("ordinary tool description changed: %q", ordinary)
	}
	decorated := ToolDescription("computer-use", "cua-driver:launch_app", launch)
	if decorated != launch {
		t.Fatal("description decoration is not idempotent")
	}
}
