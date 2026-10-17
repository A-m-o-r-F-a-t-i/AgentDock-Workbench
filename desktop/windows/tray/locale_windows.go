//go:build windows

package tray

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const localeNameMaxLength = 85

var procGetUserDefaultLocaleName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

type trayText struct {
	TooltipStopped       string
	TooltipRunning       string
	TooltipUnavailable   string
	StatusStopped        string
	StatusRunning        string
	StatusUnavailable    string
	CopyLocalMCP         string
	CopyPublicMCP        string
	RefreshQuickURL      string
	StartAgentDock       string
	RestartAgentDock     string
	InstallUpdate        string
	OpenRuntimeFolder    string
	OpenDocumentation    string
	ExitTray             string
	CopyLocalFailed      string
	CopyLocalSucceeded   string
	CopyPublicFailed     string
	CopyPublicSucceeded  string
	RefreshQuickFailed   string
	RefreshQuickStarted  string
	StartFailed          string
	StartStarted         string
	RestartFailed        string
	RestartStarted       string
	UpdateFailed         string
	ClipboardNoAddress   string
	ClipboardAllocFailed string
	ClipboardLockFailed  string
	ClipboardBusy        string
	ClipboardClearFailed string
	ClipboardWriteFailed string
	RuntimeIncomplete    string
	CoreUnavailablePlain string
	CoreUnavailable      string
	NativeCommandFailed  string
	QuickTunnelNotActive string
	UpdateProgramFailed  string
}

var trayTextEnglish = trayText{
	TooltipStopped:       "ACTOVO: Stopped",
	TooltipRunning:       "ACTOVO: Running",
	TooltipUnavailable:   "ACTOVO: Status unavailable",
	StatusStopped:        "Status: Stopped",
	StatusRunning:        "Status: Running",
	StatusUnavailable:    "Status: Unavailable",
	CopyLocalMCP:         "Copy local MCP address",
	CopyPublicMCP:        "Copy public MCP address",
	RefreshQuickURL:      "Regenerate temporary public address",
	StartAgentDock:       "Start ACTOVO",
	RestartAgentDock:     "Restart ACTOVO",
	InstallUpdate:        "Check for and install updates",
	OpenRuntimeFolder:    "Open runtime folder",
	OpenDocumentation:    "Open documentation",
	ExitTray:             "Exit tray",
	CopyLocalFailed:      "Failed to copy local MCP address: %v",
	CopyLocalSucceeded:   "Local MCP address copied.",
	CopyPublicFailed:     "Failed to copy public MCP address: %v",
	CopyPublicSucceeded:  "Public MCP address copied.",
	RefreshQuickFailed:   "Failed to regenerate temporary address: %v",
	RefreshQuickStarted:  "Generating a new temporary public address. The tray will update automatically when it is ready.",
	StartFailed:          "Failed to start ACTOVO: %v",
	StartStarted:         "Starting ACTOVO.",
	RestartFailed:        "Failed to restart ACTOVO: %v",
	RestartStarted:       "Restarting ACTOVO.",
	UpdateFailed:         "Failed to launch updater: %v",
	ClipboardNoAddress:   "No address is available to copy",
	ClipboardAllocFailed: "failed to allocate clipboard memory: %v",
	ClipboardLockFailed:  "failed to lock clipboard memory: %v",
	ClipboardBusy:        "The clipboard is being used by another application",
	ClipboardClearFailed: "failed to clear clipboard: %v",
	ClipboardWriteFailed: "failed to write clipboard: %v",
	RuntimeIncomplete:    "ACTOVO runtime information is incomplete",
	CoreUnavailablePlain: "ACTOVO core is unavailable",
	CoreUnavailable:      "ACTOVO core is unavailable: %v",
	NativeCommandFailed:  "native control command failed: %v: %s",
	QuickTunnelNotActive: "A temporary public address is not currently in use",
	UpdateProgramFailed:  "failed to launch updater; ShellExecute error code %d",
}

var trayTextChinese = trayText{
	TooltipStopped:       "ACTOVO: 未运行",
	TooltipRunning:       "ACTOVO: 运行中",
	TooltipUnavailable:   "ACTOVO: 状态不可用",
	StatusStopped:        "状态：未运行",
	StatusRunning:        "状态：运行中",
	StatusUnavailable:    "状态：不可用",
	CopyLocalMCP:         "复制本地 MCP 地址",
	CopyPublicMCP:        "复制公网 MCP 地址",
	RefreshQuickURL:      "重新生成临时公网地址",
	StartAgentDock:       "启动 ACTOVO",
	RestartAgentDock:     "重启 ACTOVO",
	InstallUpdate:        "检查并安装更新",
	OpenRuntimeFolder:    "打开运行目录",
	OpenDocumentation:    "打开使用文档",
	ExitTray:             "退出托盘",
	CopyLocalFailed:      "复制本地 MCP 地址失败：%v",
	CopyLocalSucceeded:   "本地 MCP 地址已复制。",
	CopyPublicFailed:     "复制公网 MCP 地址失败：%v",
	CopyPublicSucceeded:  "公网 MCP 地址已复制。",
	RefreshQuickFailed:   "重新生成临时地址失败：%v",
	RefreshQuickStarted:  "正在生成新的临时公网地址，完成后托盘会自动更新。",
	StartFailed:          "启动 ACTOVO 失败：%v",
	StartStarted:         "正在启动 ACTOVO。",
	RestartFailed:        "重启 ACTOVO 失败：%v",
	RestartStarted:       "正在重启 ACTOVO。",
	UpdateFailed:         "启动更新失败：%v",
	ClipboardNoAddress:   "没有可复制的地址",
	ClipboardAllocFailed: "分配剪贴板内存失败: %v",
	ClipboardLockFailed:  "锁定剪贴板内存失败: %v",
	ClipboardBusy:        "剪贴板正被其他程序占用",
	ClipboardClearFailed: "清空剪贴板失败: %v",
	ClipboardWriteFailed: "写入剪贴板失败: %v",
	RuntimeIncomplete:    "ACTOVO 运行信息不完整",
	CoreUnavailablePlain: "ACTOVO 核心不可用",
	CoreUnavailable:      "ACTOVO 核心不可用: %v",
	NativeCommandFailed:  "原生控制命令失败: %v: %s",
	QuickTunnelNotActive: "当前未使用临时公网地址",
	UpdateProgramFailed:  "启动更新程序失败，ShellExecute 错误码 %d",
}

func currentTrayText() trayText {
	var localeName [localeNameMaxLength]uint16
	result, _, _ := procGetUserDefaultLocaleName.Call(
		uintptr(unsafe.Pointer(&localeName[0])),
		uintptr(len(localeName)),
	)
	var systemLocale string
	if result != 0 {
		systemLocale = windows.UTF16ToString(localeName[:])
	}
	if resolveTrayLocale(readTrayLanguagePreference(), systemLocale) == "zh-CN" {
		return trayTextChinese
	}
	return trayTextEnglish
}

func readTrayLanguagePreference() string {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "system"
	}
	data, err := os.ReadFile(filepath.Join(localAppData, "AgentDock", "ui-language"))
	if err != nil {
		return "system"
	}
	return strings.TrimSpace(string(data))
}

func resolveTrayLocale(preference, systemLocale string) string {
	switch strings.TrimSpace(preference) {
	case "en":
		return "en"
	case "zh-CN":
		return "zh-CN"
	}
	if isSimplifiedChineseLocale(systemLocale) {
		return "zh-CN"
	}
	return "en"
}

func isSimplifiedChineseLocale(value string) bool {
	locale := strings.ToLower(strings.TrimSpace(value))
	return locale == "zh" || locale == "zh-cn" || locale == "zh-sg" || locale == "zh-hans" || strings.HasPrefix(locale, "zh-hans-")
}
