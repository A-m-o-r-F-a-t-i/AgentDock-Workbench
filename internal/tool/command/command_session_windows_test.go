//go:build windows

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsNativeChildReadyEnv = "AGENTDOCK_TEST_WINDOWS_NATIVE_CHILD_READY"

type windowsNativeChildReady struct {
	PID  int `json:"pid"`
	Port int `json:"port"`
}

func TestWindowsTTYKeepsPipeStdinWithoutVisibleConsole(t *testing.T) {
	service, _ := newCommandTestService(t)
	defer service.Close()
	started, err := service.execArgs(context.Background(), map[string]any{
		"cmd":            "Start-Sleep -Seconds 2",
		"tty":            true,
		"execution_mode": "async",
		"timeout_ms":     10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if terminal, _ := started["terminal"].(string); terminal != "pipes" {
		t.Fatalf("terminal = %q, want pipes", terminal)
	}
	sessionID, _ := started["session_id"].(string)
	stored, ok := service.sessions.Get(sessionID)
	if !ok || stored.Command == nil || stored.Command.Process == nil {
		t.Fatalf("running session is unavailable: %#v", started)
	}
	defer func() { _, _ = service.actArgs(map[string]any{"action": "kill", "session_id": sessionID}) }()

	rootPID := uint32(stored.Command.Process.Pid)
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		family, err := windowsProcessFamily(rootPID)
		if err != nil {
			t.Fatal(err)
		}
		visible, err := visibleWindowsForProcesses(family)
		if err != nil {
			t.Fatal(err)
		}
		if len(visible) != 0 {
			t.Fatalf("pipe-backed command created visible windows: %v", visible)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsProcessFamily(root uint32) (map[uint32]struct{}, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	children := make(map[uint32][]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		entry.Size = uint32(unsafe.Sizeof(windows.ProcessEntry32{}))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, err
		}
	}
	family := map[uint32]struct{}{root: {}}
	queue := []uint32{root}
	for len(queue) != 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if _, seen := family[child]; seen {
				continue
			}
			family[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return family, nil
}

func visibleWindowsForProcesses(processes map[uint32]struct{}) ([]uintptr, error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	enumWindows := user32.NewProc("EnumWindows")
	isWindowVisible := user32.NewProc("IsWindowVisible")
	getWindowThreadProcessID := user32.NewProc("GetWindowThreadProcessId")
	var handles []uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, selected := processes[pid]; selected {
			visible, _, _ := isWindowVisible.Call(hwnd)
			if visible != 0 {
				handles = append(handles, hwnd)
			}
		}
		return 1
	})
	ok, _, callErr := enumWindows.Call(callback, 0)
	if ok == 0 && callErr != windows.ERROR_SUCCESS {
		return nil, callErr
	}
	return handles, nil
}

func TestWindowsSessionActKillTerminatesNativeChildTree(t *testing.T) {
	service, cfg := newCommandTestService(t)
	root := cfg.AgentDockDefaultDir
	defer service.Close()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(root, "native-child.ready.json")
	command := fmt.Sprintf("& '%s' '-test.run=^TestWindowsSessionNativeChildHelper$'", strings.ReplaceAll(testBinary, "'", "''"))
	started, err := service.execArgs(context.Background(), map[string]any{
		"cmd": command, "execution_mode": "async", "timeout_ms": 60000,
		"env": map[string]any{windowsNativeChildReadyEnv: readyPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := started["session_id"].(string)
	ready := waitForWindowsNativeChild(t, service, sessionID, readyPath)
	childPID := ready.PID
	port := ready.Port
	defer func() {
		if process, findErr := os.FindProcess(childPID); findErr == nil {
			_ = process.Kill()
		}
	}()

	result, err := service.actArgs(map[string]any{"action": "kill", "session_id": sessionID})
	if err != nil {
		t.Fatalf("session_act(kill) error = %v", err)
	}
	if result["status"] != "cancelled" || result["command_ok"] != false || result["operation_status"] != "failed" {
		t.Fatalf("kill result = %#v", result)
	}
	if err := waitForWindowsPort(port, false, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSessionNativeChildHelper(t *testing.T) {
	readyPath := os.Getenv(windowsNativeChildReadyEnv)
	if readyPath == "" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready := windowsNativeChildReady{PID: os.Getpid(), Port: listener.Addr().(*net.TCPAddr).Port}
	data, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	tempReadyPath := readyPath + ".tmp"
	if err := os.WriteFile(tempReadyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempReadyPath, readyPath); err != nil {
		t.Fatal(err)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		_ = connection.Close()
	}
}

func waitForWindowsNativeChild(t *testing.T, service *Service, sessionID, readyPath string) windowsNativeChildReady {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(readyPath)
		if err == nil {
			var ready windowsNativeChildReady
			if json.Unmarshal(data, &ready) == nil && ready.PID > 0 && ready.Port > 0 {
				if err := waitForWindowsPort(ready.Port, true, 2*time.Second); err != nil {
					t.Fatalf("native child reported ready but its port is unavailable: %v", err)
				}
				return ready
			}
		}
		if stored, ok := service.sessions.Get(sessionID); ok {
			select {
			case <-stored.Done:
				status, observeErr := service.observeArgs(map[string]any{
					"action": "status", "session_id": sessionID, "max_output_bytes": 4096,
				})
				t.Fatalf("native child exited before readiness: status=%#v observe_err=%v", status, observeErr)
			default:
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	status, observeErr := service.observeArgs(map[string]any{
		"action": "status", "session_id": sessionID, "max_output_bytes": 4096,
	})
	t.Fatalf("native child did not start: status=%#v observe_err=%v", status, observeErr)
	return windowsNativeChildReady{}
}

func waitForWindowsPort(port int, wantOpen bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if connection != nil {
			_ = connection.Close()
		}
		if (err == nil) == wantOpen {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("port %d open state did not become %t", port, wantOpen)
}
