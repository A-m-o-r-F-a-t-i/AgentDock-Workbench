// Package capabilityrouting defines model-visible activation boundaries for
// capabilities that must not participate in unrelated tool selection.
package capabilityrouting

import "strings"

const (
	ComputerUsePlugin = "computer-use"
	DesktopGUIIntent  = "desktop_gui"

	// ComputerUseRule is injected only when the Computer Use plugin is enabled.
	ComputerUseRule = "Computer Use 采用显式加载和按浏览器所有权路由：只有当前任务明确要求操作网页、Windows 原生界面或启动桌面应用时，才调用 plugin_load(\"computer-use\")。代码、文件、Git、终端、PCB、API、纯文本及普通 MCP 任务不得加载或调用 computer-use。用户个人浏览器、当前已打开并保留登录态的浏览器必须读取并执行 current-browser-control Skill；它是 Skill/CLI，不是动态 MCP Server，禁止把 computer-use 或 current-browser-control 传给 mcp_tool_list、mcp_tool_search、mcp_tool_inspect 或 mcp_tool_call。current-browser-control 暂时故障时按其恢复流程处理，不得改用 cua-driver 或原生 Computer Use 接管该浏览器。cua-driver 只用于 Windows 原生应用、系统界面，以及由 CUA 自行启动的隔离临时浏览器；CUA 浏览器工具不得连接现有浏览器 PID、用户 profile 或登录会话，禁止 existing_profile。chatgpt-native-computer-use 不得自动接管个人浏览器，仅在用户明确指定或非个人浏览器/原生 Windows 任务的最终回退中使用。调用 Computer Use 动态 MCP 工具必须传 interaction_intent=desktop_gui 和与用户任务直接对应的具体 reason。"
)

const (
	computerUsePluginPrefix       = "显式 GUI 控制插件。用户个人或当前已打开的浏览器必须执行 current-browser-control Skill；该名称是 Skill，不是 MCP Server，CUA 不得接管个人浏览器。cua-driver 仅用于 Windows 原生界面或 CUA 自建的隔离临时浏览器，禁止 existing_profile；原生 Computer Use 仅在用户明确指定或最终回退时使用。"
	currentBrowserPrefix          = "用户个人浏览器、当前已打开并保留登录态的浏览器页面必须使用本 Skill。它通过宿主 exec_command 绑定 plugin_load 返回的精确 skill_ref 运行，不是动态 MCP Server；禁止把 computer-use 或 current-browser-control 送入动态 MCP 发现或调用。Bridge 暂时故障时按本 Skill 的恢复流程处理，不得切换到 CUA 或原生 Computer Use 接管该浏览器。"
	cuaDriverPrefix               = "CUA 仅用于 Windows 原生应用、系统界面，以及由 CUA 自行启动的隔离临时浏览器。不得操作用户个人或当前浏览器的页面、窗口、profile 或登录会话；不得连接现有浏览器 PID，禁止 existing_profile。个人浏览器必须使用 current-browser-control，其故障不构成回退到 CUA 的条件。"
	nativeComputerUsePrefix       = "最终回退的原生 Computer Use；不得自动接管用户个人或当前浏览器。个人浏览器仍由 current-browser-control 负责；仅在用户明确指定，或非个人浏览器/原生 Windows 任务中 CUA 不可用、不兼容时使用。"
	computerUseToolPrefix         = "仅在当前任务明确需要 Windows GUI 交互时调用；通过 AgentDock 调用必须传 interaction_intent=desktop_gui 和具体 reason。不得借此接管用户个人或当前浏览器。"
	cuaDriverToolPrefix           = "CUA 工具仅用于 Windows 原生应用、系统界面，以及由 CUA 自行启动的隔离临时浏览器；不得选择或操作用户个人/当前浏览器。通过 AgentDock 调用必须传 interaction_intent=desktop_gui 和具体 reason。"
	nativeComputerUseToolPrefix   = "原生 Computer Use 工具不得自动接管用户个人/当前浏览器；仅在用户明确指定，或非个人浏览器/原生 Windows 任务的最终回退中调用。通过 AgentDock 调用必须传 interaction_intent=desktop_gui 和具体 reason。"
	cuaTemporaryBrowserToolPrefix = "CUA 的浏览器工具只允许操作由 CUA 自行启动的隔离临时浏览器。不得连接或控制用户个人/当前浏览器、现有浏览器 PID、用户 profile 或登录会话，禁止 existing_profile；个人浏览器必须执行 current-browser-control Skill，故障时按该 Skill 恢复而不切换后端。通过 AgentDock 调用必须传 interaction_intent=desktop_gui 和具体 reason。"
	launchAppPrefix               = "仅在当前任务明确要求启动尚未运行的 Windows 原生应用时调用；通过 AgentDock 调用必须传 interaction_intent=desktop_gui 和具体 reason。不得用它向用户个人浏览器打开 URL，也不得用于能力探测、会话恢复、查找已有窗口、获取 PID 或通用任务初始化；临时网页应由 CUA 创建隔离临时浏览器，禁止 existing_profile。"
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
	if normalizeName(server) == "cua-driver" {
		switch normalizedTool := normalizeName(tool); {
		case normalizedTool == "launch_app":
			prefix = launchAppPrefix
		case normalizedTool == "get_browser_state", strings.HasPrefix(normalizedTool, "browser_"):
			prefix = cuaTemporaryBrowserToolPrefix
		default:
			prefix = cuaDriverToolPrefix
		}
	} else if normalizeName(server) == "chatgpt-native-computer-use" {
		prefix = nativeComputerUseToolPrefix
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
