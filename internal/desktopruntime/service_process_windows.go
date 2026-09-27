//go:build windows

package desktopruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// BinaryProcessRunning 只按完整可执行文件路径判断进程是否正在运行。
func BinaryProcessRunning(binaryPath string) (bool, error) {
	processes, err := processIDsAtPath(binaryPath)
	if err != nil {
		return false, err
	}
	return len(processes) > 0, nil
}

func processRunningAtPath(binaryPath string) (bool, error) {
	return BinaryProcessRunning(binaryPath)
}

// StopBinaryProcesses 只终止可执行文件路径与 binaryPath 完全一致的进程。
func StopBinaryProcesses(ctx context.Context, binaryPath string, timeout time.Duration) error {
	return stopBinaryProcessesExcept(ctx, binaryPath, nil, timeout)
}

func stopBinaryProcessesExcept(ctx context.Context, binaryPath string, excluded map[uint32]struct{}, timeout time.Duration) error {
	if err := terminateProcessesAtPathExcept(binaryPath, excluded); err != nil {
		return err
	}
	stopped, err := waitBinaryStoppedExcept(ctx, binaryPath, excluded, timeout)
	if err != nil {
		return err
	}
	if !stopped {
		return fmt.Errorf("进程未在 %s 内退出: %s", timeout, binaryPath)
	}
	return nil
}

// WaitBinaryStopped waits until no process with the exact binary path remains.
func WaitBinaryStopped(ctx context.Context, binaryPath string, timeout time.Duration) (bool, error) {
	return waitBinaryStoppedExcept(ctx, binaryPath, nil, timeout)
}

func waitBinaryStoppedExcept(ctx context.Context, binaryPath string, excluded map[uint32]struct{}, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		processes, err := processIDsAtPathExcept(binaryPath, excluded)
		if err != nil {
			return false, err
		}
		if len(processes) == 0 {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return false, nil
}

func waitBinaryStopped(ctx context.Context, binaryPath string, timeout time.Duration) (bool, error) {
	return WaitBinaryStopped(ctx, binaryPath, timeout)
}

func terminateProcessesAtPath(binaryPath string) error {
	return terminateProcessesAtPathExcept(binaryPath, nil)
}

func terminateProcessesAtPathExcept(binaryPath string, excluded map[uint32]struct{}) error {
	processIDs, err := processIDsAtPathExcept(binaryPath, excluded)
	if err != nil {
		return err
	}
	var failures []string
	for _, processID := range processIDs {
		process, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, processID)
		if openErr != nil {
			// The process may exit between enumeration and opening its handle.
			if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			failures = append(failures, fmt.Sprintf("PID %d: %v", processID, openErr))
			continue
		}
		if terminateErr := terminateBinaryProcessHandle(process, windows.TerminateProcess, windows.WaitForSingleObject); terminateErr != nil {
			failures = append(failures, fmt.Sprintf("PID %d: %v", processID, terminateErr))
		}
		_ = windows.CloseHandle(process)
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// terminateBinaryProcessHandle confirms the terminal state of the same pinned
// handle. A tunnel supervisor may concurrently terminate its child, in which
// case TerminateProcess can return ERROR_ACCESS_DENIED after the child exits.
// Access denial alone is never evidence of success; only a signaled handle is.
func terminateBinaryProcessHandle(process windows.Handle, terminate func(windows.Handle, uint32) error, wait func(windows.Handle, uint32) (uint32, error)) error {
	state, err := wait(process, 0)
	if err != nil {
		return fmt.Errorf("inspect process before termination: %w", err)
	}
	if state == windows.WAIT_OBJECT_0 {
		return nil
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("unexpected process wait state before termination: %#x", state)
	}

	terminateErr := terminate(process, 0)
	state, waitErr := wait(process, 5000)
	if waitErr == nil && state == windows.WAIT_OBJECT_0 {
		return nil
	}
	if waitErr != nil {
		return errors.Join(terminateErr, fmt.Errorf("confirm process termination: %w", waitErr))
	}
	return errors.Join(terminateErr, fmt.Errorf("process termination was not confirmed within 5s (wait state %#x)", state))
}

func processIDsAtPathExcept(binaryPath string, excluded map[uint32]struct{}) ([]uint32, error) {
	processIDs, err := processIDsAtPath(binaryPath)
	if err != nil {
		return nil, err
	}
	if len(excluded) == 0 {
		return processIDs, nil
	}
	filtered := processIDs[:0]
	for _, processID := range processIDs {
		if _, keep := excluded[processID]; keep {
			continue
		}
		filtered = append(filtered, processID)
	}
	return filtered, nil
}

func processIDsAtPath(binaryPath string) ([]uint32, error) {
	target, err := filepath.Abs(binaryPath)
	if err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("创建 Windows 进程快照失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("读取 Windows 进程快照失败: %w", err)
	}
	currentPID := uint32(os.Getpid())
	var processIDs []uint32
	for {
		if entry.ProcessID != currentPID {
			if processPath, pathErr := queryProcessPath(entry.ProcessID); pathErr == nil && samePath(processPath, target) {
				processIDs = append(processIDs, entry.ProcessID)
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("继续读取 Windows 进程快照失败: %w", err)
		}
	}
	return processIDs, nil
}

// ancestorProcessIDsAtPath returns ancestor processes whose executable path exactly
// matches binaryPath. A live generation update runs as:
// stable shim -> source generation updater -> source Arbiter -> stable shim -> service stop.
// The stop helper must terminate the long-running source Core, but it must not kill the
// source updater that is synchronously waiting for the Arbiter's terminal result. Recovery
// Arbiters do not have that updater ancestor, so crash recovery still stops every source Core.
func ancestorProcessIDsAtPath(binaryPath string) (map[uint32]struct{}, error) {
	target, err := filepath.Abs(binaryPath)
	if err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("创建 Windows 进程快照失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	parents := make(map[uint32]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("读取 Windows 进程快照失败: %w", err)
	}
	for {
		parents[entry.ProcessID] = entry.ParentProcessID
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("继续读取 Windows 进程快照失败: %w", err)
		}
	}

	excluded := map[uint32]struct{}{}
	seen := map[uint32]struct{}{}
	processID := uint32(os.Getpid())
	for {
		parentID := parents[processID]
		if parentID == 0 {
			break
		}
		if _, ok := seen[parentID]; ok {
			break
		}
		seen[parentID] = struct{}{}
		if processPath, pathErr := queryProcessPath(parentID); pathErr == nil && samePath(processPath, target) {
			excluded[parentID] = struct{}{}
		}
		processID = parentID
	}
	return excluded, nil
}

func queryProcessPath(processID uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}
