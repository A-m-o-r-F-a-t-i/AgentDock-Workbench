// Package capabilityrouting defines model-visible activation boundaries for
// capabilities that must not participate in unrelated tool selection.
package capabilityrouting

import "strings"

const (
	ComputerUsePlugin = "computer-use"
	DesktopGUIIntent  = "desktop_gui"

	// ComputerUseRule is injected only when the Computer Use plugin is enabled.
	ComputerUseRule = "Computer Use 采用显式加载和显式意图：只有当前任务明确要求操作当前网页、Windows 原生界面或启动桌面应用时，才调用 plugin_load(\"computer-use\")。代码、文件、Git、终端、PCB、API、纯文本及普通 MCP 任务不得加载或调用 computer-use。网页正文优先 current-browser-control；只有其不适用时才使用 cua-driver；chatgpt-native-computer-use 仅作最终回退。调用 computer-use 的动态 MCP 工具必须传 interaction_intent=desktop_gui 和与用户任务直接对应的具体 reason。cua-driver:launch_app 仅用于明确要求启动尚未运行的 Windows 应用或打开 URL，不得用于能力探测、会话恢复、查找已有窗口、获取 PID 或通用任务初始化。"
)

const (
	computerUsePluginPrefix = "显式 GUI 控制能力；仅在当前任务明确要求操作网页或 Windows 桌面界面时加载。路由顺序：current-browser-control > cua-driver > chatgpt-native-computer-use。"
	currentBrowserPrefix    = "显式网页交互能力；仅在当前任务明确要求控制已打开的 Edge 页面时使用。"
	cuaDriverPrefix         = "显式 Windows GUI 控制能力；仅在 current-browser-control 不适用且当前任务明确需要原生界面时使用。"
	nativeComputerUsePrefix = "最终回退的原生 Computer Use；仅在 cua-driver 不可用、不兼容或用户明确指定时使用。"
	computerUseToolPrefix   = "仅在当前任务明确需要网页或 Windows GUI 交互时调用；通过 AgentDock 调用时必须传 interaction_intent=desktop_gui 和具体 reason。"
	launchAppPrefix         = "仅在当前任务明确要求启动尚未运行的 Windows 应用或打开 URL 时调用；通过 AgentDock 调用时必须传 interaction_intent=desktop_gui 和具体 reason；不得用于能力探测、会话恢复、查找已有窗口、获取 PID 或通用任务初始化。"
)

func RequiresExplicitPluginLoad(plugin string) bool {
	return equalName(plugin, ComputerUsePlugin)
}

func IsComputerUseServer(plugin, server string) bool {
	if RequiresExplicitPluginLoad(plugin) {
		return true
	}
	switch normalizeName(server) {
	case "cua-driver", "chatgpt-native-computer-use":
		return true
	default:
		return false
	}
}

func IsComputerUseSkill(plugin, skill string) bool {
	if RequiresExplicitPluginLoad(plugin) {
		return true
	}
	switch normalizeName(skill) {
	case "current-browser-control", "cua-driver-computer-use", "chatgpt-native-computer-use":
		return true
	default:
		return false
	}
}

func RequiresExplicitMCPSelection(plugin, server string) bool {
	return IsComputerUseServer(plugin, server)
}

func RequiresDesktopGUIIntent(plugin, qualifiedName string) bool {
	server, _, ok := strings.Cut(strings.TrimSpace(qualifiedName), ":")
	return ok && IsComputerUseServer(plugin, server)
}

func ValidDesktopGUIIntent(intent, reason string) bool {
	return normalizeName(intent) == DesktopGUIIntent && strings.TrimSpace(reason) != ""
}

func PluginDescription(plugin, description string) string {
	if !RequiresExplicitPluginLoad(plugin) {
		return strings.TrimSpace(description)
	}
	return prefixDescription(computerUsePluginPrefix, description)
}

func SkillDescription(plugin, skill, description string) string {
	if !IsComputerUseSkill(plugin, skill) {
		return strings.TrimSpace(description)
	}
	prefix := computerUseToolPrefix
	switch normalizeName(skill) {
	case "current-browser-control":
		prefix = currentBrowserPrefix
	case "cua-driver-computer-use":
		prefix = cuaDriverPrefix
	case "chatgpt-native-computer-use":
		prefix = nativeComputerUsePrefix
	}
	return prefixDescription(prefix, description)
}

func ServerDescription(plugin, server, description string) string {
	if !IsComputerUseServer(plugin, server) {
		return strings.TrimSpace(description)
	}
	prefix := computerUseToolPrefix
	switch normalizeName(server) {
	case "cua-driver":
		prefix = cuaDriverPrefix
	case "chatgpt-native-computer-use":
		prefix = nativeComputerUsePrefix
	}
	return prefixDescription(prefix, description)
}

func ToolDescription(plugin, qualifiedName, description string) string {
	server, tool, ok := strings.Cut(strings.TrimSpace(qualifiedName), ":")
	if !ok || !IsComputerUseServer(plugin, server) {
		return strings.TrimSpace(description)
	}
	prefix := computerUseToolPrefix
	if normalizeName(server) == "cua-driver" && normalizeName(tool) == "launch_app" {
		prefix = launchAppPrefix
	}
	return prefixDescription(prefix, description)
}

func prefixDescription(prefix, description string) string {
	prefix = strings.TrimSpace(prefix)
	description = strings.TrimSpace(description)
	if description == "" || description == prefix {
		return prefix
	}
	if strings.HasPrefix(description, prefix) {
		return description
	}
	return prefix + "\n\n" + description
}

func normalizeName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func equalName(left, right string) bool {
	return normalizeName(left) == normalizeName(right)
}
