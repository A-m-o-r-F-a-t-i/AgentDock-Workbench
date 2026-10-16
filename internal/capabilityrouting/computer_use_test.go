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

func TestComputerUseDescriptionsLeadWithRoutingBoundary(t *testing.T) {
	launch := ToolDescription("computer-use", "cua-driver:launch_app", "Launch a Windows app.")
	for _, want := range []string{"尚未运行", "interaction_intent=desktop_gui", "不得用于能力探测", "Launch a Windows app"} {
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
