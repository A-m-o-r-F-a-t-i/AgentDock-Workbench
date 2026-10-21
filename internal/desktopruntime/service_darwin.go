//go:build darwin

package desktopruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchctlBinary() string {
	if configured := strings.TrimSpace(os.Getenv("AGENTDOCK_LAUNCHCTL_BIN")); configured != "" {
		return configured
	}
	return "/bin/launchctl"
}

func launchdTarget(label string) string { return launchdDomain() + "/" + label }

func launchdDescription(ctx context.Context, label string) (string, bool) {
	output, err := runCommand(ctx, launchctlBinary(), "print", launchdTarget(label))
	return output, err == nil
}

func launchdLoaded(ctx context.Context, label string) bool {
	_, loaded := launchdDescription(ctx, label)
	return loaded
}

func launchdPIDFromDescription(description string) int {
	for _, rawLine := range strings.Split(description, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "pid = ") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		if err == nil && pid > 1 {
			return pid
		}
	}
	return 0
}

func launchdPID(ctx context.Context, label string) int {
	description, loaded := launchdDescription(ctx, label)
	if !loaded {
		return 0
	}
	return launchdPIDFromDescription(description)
}

func kickstartRegisteredLaunchAgent(ctx context.Context, label, manager string) error {
	if !launchdLoaded(ctx, label) {
		if manager == "launchd" {
			return fmt.Errorf("CLI LaunchAgent %s 未加载；请先完成 agentdock install --register-service", label)
		}
		return fmt.Errorf("后台服务 %s 尚未由 AgentDock.app 注册", label)
	}
	_, err := runCommand(ctx, launchctlBinary(), "kickstart", "-k", launchdTarget(label))
	return err
}

func platformServiceStatus(ctx context.Context, runtimeRoot string) (ServiceStatus, error) {
	manifest, _, values, err := loadCoreEnvironment(runtimeRoot)
	if err != nil {
		return ServiceStatus{}, err
	}
	loaded := launchdLoaded(ctx, manifest.ServiceName)
	running := loaded && launchdPID(ctx, manifest.ServiceName) > 1
	return ServiceStatus{
		Running:        running,
		Healthy:        running && healthyVersion(ctx, values, buildinfo.Version),
		StartupEnabled: loaded,
	}, nil
}

func platformServiceAction(ctx context.Context, runtimeRoot, action string) error {
	manifest, _, values, err := loadCoreEnvironment(runtimeRoot)
	if err != nil {
		return err
	}

	// App Bundle：只 kickstart SMAppService 已经注册的 job。
	// CLI 安装：kickstart 用户 LaunchAgent（com.uvwt.agentdock），标签来自 desktop-runtime.json。
	switch action {
	case "start", "restart":
		if err := kickstartRegisteredLaunchAgent(ctx, manifest.ServiceName, manifest.ServiceManager); err != nil {
			return err
		}
		return waitHealthyVersion(ctx, values, buildinfo.Version, 30*time.Second)
	case "stop":
		if manifest.ServiceManager == "launchd" {
			_, err := runCommand(ctx, launchctlBinary(), "bootout", launchdTarget(manifest.ServiceName))
			return err
		}
		return errors.New("macOS 后台服务停用由 AgentDock.app 的 SMAppService 管理")
	default:
		return errors.New("不支持的 AgentDock 服务操作")
	}
}

func platformSetAutostart(context.Context, string, string, bool) error {
	return errors.New("macOS 后台服务启停由 AgentDock.app 的 SMAppService 管理")
}
