//go:build windows

package desktopruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"golang.org/x/sys/windows"
)

const windowsCoreStartTimeout = 180 * time.Second

func platformServiceStatus(ctx context.Context, runtimeRoot string) (ServiceStatus, error) {
	manifest, _, err := loadDesktopManifest(runtimeRoot)
	if err != nil {
		return ServiceStatus{}, err
	}
	coreBinary := ActiveCoreBinary(runtimeRoot, manifest)
	running, err := coreProcessRunning(runtimeRoot, coreBinary)
	if err != nil {
		return ServiceStatus{}, err
	}
	healthy := running && testHealthVersion(ctx, manifest.HealthURL(), buildinfo.Version)
	startupEnabled, err := coreAutostartEnabled(ctx, manifest)
	if err != nil {
		return ServiceStatus{}, fmt.Errorf("读取 AgentDock 开机启动状态失败: %w", err)
	}
	return ServiceStatus{Running: running || healthy, Healthy: healthy, StartupEnabled: startupEnabled}, nil
}

func platformServiceAction(ctx context.Context, runtimeRoot, action string) error {
	manifest, root, err := loadDesktopManifest(runtimeRoot)
	if err != nil {
		return err
	}

	switch action {
	case "start":
		return startCore(ctx, manifest, root)
	case "stop":
		return stopCore(ctx, manifest, root)
	case "restart":
		if err := stopCore(ctx, manifest, root); err != nil {
			return err
		}
		return startCore(ctx, manifest, root)
	default:
		return fmt.Errorf("不支持的 Windows 服务操作：%s", action)
	}
}

func loadDesktopManifest(runtimeRoot string) (Manifest, string, error) {
	root, err := filepath.Abs(strings.TrimSpace(runtimeRoot))
	if err != nil {
		return Manifest{}, "", fmt.Errorf("解析 Windows 运行目录失败: %w", err)
	}
	manifest, err := Load(filepath.Join(root, "runtime.json"))
	if err != nil {
		return Manifest{}, "", fmt.Errorf("读取 Windows 运行清单失败: %w", err)
	}
	return manifest, root, nil
}

func startCore(ctx context.Context, manifest Manifest, runtimeRoot string) error {
	coreBinary := ActiveCoreBinary(runtimeRoot, manifest)
	if err := CheckExecutionCompatibility(ctx, runtimeRoot, coreBinary); err != nil {
		return err
	}
	// Health alone is insufficient during an upgrade: an older Core or another
	// installation can own the same port. Require the active generation process
	// and the exact build version before treating the service as already ready.
	running, err := coreProcessRunning(runtimeRoot, coreBinary)
	if err != nil {
		return fmt.Errorf("识别已启动的 AgentDock Core 失败: %w", err)
	}
	if running {
		if testHealthVersion(ctx, manifest.HealthURL(), buildinfo.Version) {
			return nil
		}
		return waitForCoreHealth(ctx, manifest, runtimeRoot, buildinfo.Version, windowsCoreStartTimeout, true)
	}
	if err := ensureCoreListenAddressAvailable(manifest); err != nil {
		return err
	}
	recoverAbandonedCoreLocks(manifest, runtimeRoot)
	if manifest.UsesScheduledTask() {
		if err := StartInteractiveScheduledTask(ctx, runtimeRoot, manifest.AgentDockTaskName); err != nil {
			return err
		}
	} else if err := startDetachedCore(manifest, runtimeRoot); err != nil {
		return err
	}
	return waitForCoreHealth(ctx, manifest, runtimeRoot, buildinfo.Version, windowsCoreStartTimeout, !manifest.UsesScheduledTask())
}

func ensureCoreListenAddressAvailable(manifest Manifest) error {
	address := net.JoinHostPort(manifest.Host, strconv.Itoa(manifest.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("AgentDock Core 无法使用监听地址 %s；该地址已被其他进程占用: %w", address, err)
	}
	if err := listener.Close(); err != nil {
		return fmt.Errorf("释放 AgentDock Core 监听地址探针 %s 失败: %w", address, err)
	}
	return nil
}

// coreProcessRunning requires a server entry point, in addition to excluding
// the Tunnel supervisor and same-binary command ancestors.
func coreProcessRunning(runtimeRoot, coreBinary string) (bool, error) {
	excluded, err := ancestorProcessIDsAtPath(coreBinary)
	if err != nil {
		return false, err
	}
	supervisorPID, err := activeTunnelSupervisorPID(runtimeRoot, coreBinary)
	if err != nil {
		return false, err
	}
	if supervisorPID != 0 {
		excluded[supervisorPID] = struct{}{}
	}
	processes, err := processIDsAtPathExcept(coreBinary, excluded)
	if err != nil {
		return false, err
	}
	for _, processID := range processes {
		commandLine, err := queryProcessCommandLine(processID)
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.STATUS_PROCESS_IS_TERMINATING) || errors.Is(err, windows.STATUS_INVALID_CID) {
			// A finite CLI probe can exit between the snapshot and this query.
			continue
		}
		if err != nil {
			return false, fmt.Errorf("读取 AgentDock 进程 PID %d 启动参数失败: %w", processID, err)
		}
		server, err := isCoreServerCommandLine(commandLine)
		if err != nil {
			return false, fmt.Errorf("识别 AgentDock 进程 PID %d 角色失败: %w", processID, err)
		}
		if server {
			return true, nil
		}
	}
	return false, nil
}

func stopCore(ctx context.Context, manifest Manifest, runtimeRoot string) error {
	coreBinary := ActiveCoreBinary(runtimeRoot, manifest)
	excluded := map[uint32]struct{}{}
	ancestorPIDs, err := ancestorProcessIDsAtPath(coreBinary)
	if err != nil {
		return fmt.Errorf("识别 AgentDock Core 调用链失败: %w", err)
	}
	for processID := range ancestorPIDs {
		excluded[processID] = struct{}{}
	}
	supervisorPID, err := activeTunnelSupervisorPID(runtimeRoot, coreBinary)
	if err != nil {
		return fmt.Errorf("识别 Tunnel supervisor 失败: %w", err)
	}
	if supervisorPID != 0 {
		// Core 与 Tunnel supervisor 共用 agentdock.exe。停止 Core 时必须保留 supervisor，
		// 否则一次普通 Core 重启就会悄悄丢失 Tunnel 的后续自恢复能力。
		excluded[supervisorPID] = struct{}{}
	}

	if manifest.UsesScheduledTask() {
		// 先让任务计划程序正常结束最高权限进程，避免普通托盘立即申请 PROCESS_TERMINATE。
		_ = runScheduledTaskCommand(ctx, "/End", "/TN", scheduledTaskPath(manifest.AgentDockTaskName))
		stopped, waitErr := waitBinaryStoppedExcept(ctx, coreBinary, excluded, 5*time.Second)
		if waitErr != nil {
			return waitErr
		}
		if stopped {
			return nil
		}
	}
	if err := stopBinaryProcessesExcept(ctx, coreBinary, excluded, 15*time.Second); err != nil {
		return fmt.Errorf("停止 AgentDock 核心失败: %w", err)
	}
	return nil
}

func startDetachedCore(manifest Manifest, runtimeRoot string) error {
	coreBinary := ActiveCoreBinary(runtimeRoot, manifest)
	if info, err := os.Stat(coreBinary); err != nil || info.IsDir() {
		return fmt.Errorf("找不到 AgentDock 核心程序: %s", coreBinary)
	}
	command := exec.Command(coreBinary, "service", "launch-core", "--runtime-root", runtimeRoot)
	// launch-core 会自行把运行日志写入受限轮转文件；父进程不再持有同一路径的追加句柄。
	command.Dir = manifest.AgentDockDefaultDir
	if command.Dir == "" {
		command.Dir = defaultWindowsWorkDir()
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 AgentDock 核心失败: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("释放 AgentDock 后台进程句柄失败: %w", err)
	}
	return nil
}

func defaultWindowsWorkDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	workDir := filepath.Join(home, "AgentDock")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return home
	}
	return workDir
}

func waitForCoreHealth(ctx context.Context, manifest Manifest, runtimeRoot, expectedVersion string, timeout time.Duration, observedCore bool) error {
	url := manifest.HealthURL()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if testHealthVersion(ctx, url, expectedVersion) {
			return nil
		}
		running, err := coreProcessRunning(runtimeRoot, ActiveCoreBinary(runtimeRoot, manifest))
		if err != nil {
			return fmt.Errorf("检查 AgentDock Core 启动状态失败: %w", err)
		}
		if observedCore && !running {
			return coreStartFailure(runtimeRoot, fmt.Errorf("AgentDock Core 在健康检查就绪前退出: %s", url))
		}
		observedCore = observedCore || running
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return coreStartFailure(runtimeRoot, fmt.Errorf("AgentDock Core 在 %s 内未通过健康检查: %s", timeout, url))
}

func coreStartFailure(runtimeRoot string, cause error) error {
	logPath := filepath.Join(runtimeRoot, "logs", "agentdock.err.log")
	tail := strings.TrimSpace(setupLogTail(logPath, 4096))
	if tail == "" {
		return fmt.Errorf("%w; Core 日志: %s", cause, logPath)
	}
	return fmt.Errorf("%w; Core 最近日志 (%s): %s", cause, logPath, tail)
}

type coreHealthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

func readCoreHealth(ctx context.Context, url string) (coreHealthResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return coreHealthResponse{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return coreHealthResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return coreHealthResponse{}, fmt.Errorf("%s returned HTTP %d", url, response.StatusCode)
	}
	var health coreHealthResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil {
		return coreHealthResponse{}, err
	}
	if !health.OK {
		return coreHealthResponse{}, fmt.Errorf("%s returned ok=false", url)
	}
	return health, nil
}

func testHealth(ctx context.Context, url string) bool {
	_, err := readCoreHealth(ctx, url)
	return err == nil
}

func testHealthVersion(ctx context.Context, url, expectedVersion string) bool {
	health, err := readCoreHealth(ctx, url)
	if err != nil {
		return false
	}
	expected := strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	actual := strings.TrimPrefix(strings.TrimSpace(health.Version), "v")
	return expected != "" && actual == expected
}
