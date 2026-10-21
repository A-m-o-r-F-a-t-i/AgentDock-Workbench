//go:build windows

package desktopruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCoreServerCommandLineExcludesSiblingCLI(t *testing.T) {
	for _, test := range []struct {
		line string
		want bool
	}{
		{`"C:\Agent Dock\agentdock-core.exe" service launch-core --runtime-root "C:\Agent Dock"`, true},
		{`"C:\service status\agentdock-core.exe" service launch-core --runtime-root "C:\Agent Dock"`, true},
		{`agentdock-core.exe`, true},
		{`agentdock-core.exe -port 8765`, true},
		{`agentdock-core.exe --stdio`, false},
		{`agentdock-core.exe -stdio=true`, false},
		{`agentdock-core.exe --stdio=false`, true},
		{`agentdock-core.exe --stdio=true --stdio=false`, true},
		{`agentdock-core.exe -auth-token stdio`, true},
		{`agentdock-core.exe version --json`, false},
		{`agentdock-core.exe --version`, false},
		{`agentdock-core.exe --help`, false},
		{`agentdock-core.exe -help`, false},
		{`agentdock-core.exe config runtime-get --runtime-root C:\AgentDock`, false},
		{`agentdock-core.exe service status --runtime-root C:\AgentDock`, false},
		{`agentdock-core.exe service start --runtime-root C:\AgentDock`, false},
		{`agentdock-core.exe install inspect --runtime-root C:\AgentDock`, false},
		{`agentdock-core.exe tunnel launch --runtime-root C:\AgentDock`, false},
		{`agentdock-core.exe update --progress-json`, false},
	} {
		t.Run(test.line, func(t *testing.T) {
			got, err := isCoreServerCommandLine(test.line)
			if err != nil || got != test.want {
				t.Fatalf("server=%v err=%v; want %v", got, err, test.want)
			}
		})
	}
}

func TestQueryProcessCommandLineNative(t *testing.T) {
	line, err := queryProcessCommandLine(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	args, err := windows.DecomposeCommandLine(line)
	if err != nil || !reflect.DeepEqual(args, os.Args) {
		t.Fatalf("native command line roundtrip: args=%q expected=%q err=%v", args, os.Args, err)
	}
}

func TestCoreStartFailureKeepsCauseBeforeBoundedLogTail(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	log := strings.Repeat("old recovery information\n", 500) + "startup failed: configured executable is missing\n"
	if err := os.WriteFile(filepath.Join(root, "logs", "agentdock.err.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("Core exited before health became ready")
	err := coreStartFailure(root, cause)
	if !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), cause.Error()) || !strings.Contains(err.Error(), "configured executable is missing") || len(err.Error()) > 4600 {
		t.Fatalf("lost startup failure or unbounded log: %v", err)
	}
}

func TestBinaryProcessStopConfirmsSameHandle(t *testing.T) {
	invalidHandle := windows.ERROR_INVALID_HANDLE
	for _, test := range []struct {
		name      string
		states    []uint32
		waitError error
		errorAt   int
		terminate error
		wantError bool
		wantCause error
		wantKill  bool
	}{
		{name: "already exited", states: []uint32{windows.WAIT_OBJECT_0}},
		{name: "normal termination", states: []uint32{uint32(windows.WAIT_TIMEOUT), windows.WAIT_OBJECT_0}, wantKill: true},
		{name: "concurrent supervisor exit", states: []uint32{uint32(windows.WAIT_TIMEOUT), windows.WAIT_OBJECT_0}, terminate: windows.ERROR_ACCESS_DENIED, wantKill: true},
		{name: "real access denial remains failure", states: []uint32{uint32(windows.WAIT_TIMEOUT), uint32(windows.WAIT_TIMEOUT)}, terminate: windows.ERROR_ACCESS_DENIED, wantKill: true, wantError: true, wantCause: windows.ERROR_ACCESS_DENIED},
		{name: "termination success without exit is failure", states: []uint32{uint32(windows.WAIT_TIMEOUT), uint32(windows.WAIT_TIMEOUT)}, wantKill: true, wantError: true},
		{name: "initial observation fails", states: []uint32{windows.WAIT_FAILED}, waitError: invalidHandle, errorAt: 0, wantError: true, wantCause: invalidHandle},
		{name: "final observation fails", states: []uint32{uint32(windows.WAIT_TIMEOUT), windows.WAIT_FAILED}, waitError: invalidHandle, errorAt: 1, wantKill: true, wantError: true, wantCause: invalidHandle},
		{name: "unknown wait state is not success", states: []uint32{windows.WAIT_ABANDONED}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const handle = windows.Handle(123)
			waitCalls, killCalls := 0, 0
			terminate := func(actual windows.Handle, code uint32) error {
				if actual != handle || code != 0 {
					t.Fatalf("termination changed handle or exit code: %d, %d", actual, code)
				}
				killCalls++
				return test.terminate
			}
			wait := func(actual windows.Handle, timeout uint32) (uint32, error) {
				if actual != handle || waitCalls >= len(test.states) {
					t.Fatalf("unexpected observation: handle=%d call=%d", actual, waitCalls)
				}
				wantTimeout := uint32(0)
				if waitCalls > 0 {
					wantTimeout = 5000
				}
				if timeout != wantTimeout {
					t.Fatalf("wait is not bounded as expected: got %d want %d", timeout, wantTimeout)
				}
				state := test.states[waitCalls]
				var err error
				if waitCalls == test.errorAt {
					err = test.waitError
				}
				waitCalls++
				return state, err
			}
			err := terminateBinaryProcessHandle(handle, terminate, wait)
			if (err != nil) != test.wantError || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("unexpected result: %v", err)
			}
			if waitCalls != len(test.states) || (killCalls == 1) != test.wantKill || killCalls > 1 {
				t.Fatalf("unexpected operations: wait=%d terminate=%d", waitCalls, killCalls)
			}
		})
	}
}

func TestBinaryProcessStopNativeConcurrentExit(t *testing.T) {
	handle, input := startBinaryProcessStopChild(t, windows.PROCESS_TERMINATE|windows.SYNCHRONIZE)
	var nativeError error
	terminate := func(actual windows.Handle, code uint32) error {
		// Deterministically reproduce a supervisor exiting the child after the
		// initial live observation but before our own TerminateProcess call.
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
		if state, err := windows.WaitForSingleObject(actual, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("child did not exit before competing termination: state=%d err=%v", state, err)
		}
		nativeError = windows.TerminateProcess(actual, code)
		return nativeError
	}
	if err := terminateBinaryProcessHandle(handle, terminate, windows.WaitForSingleObject); err != nil {
		t.Fatalf("confirmed concurrent exit was reported as failure: %v", err)
	}
	if !errors.Is(nativeError, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("native race was not reproduced: %v", nativeError)
	}
	t.Log("Native TerminateProcess returned access denied after child exit; pinned handle confirmed termination")
	// Completed process handles remain safe to observe repeatedly.
	for i := 0; i < 2; i++ {
		if err := terminateBinaryProcessHandle(handle, windows.TerminateProcess, windows.WaitForSingleObject); err != nil {
			t.Fatalf("repeat stop failed: %v", err)
		}
	}
}

func TestBinaryProcessStopNativeAccessDenial(t *testing.T) {
	// A real live process handle deliberately lacks PROCESS_TERMINATE.
	handle, _ := startBinaryProcessStopChild(t, windows.SYNCHRONIZE)
	instantWait := func(actual windows.Handle, _ uint32) (uint32, error) {
		return windows.WaitForSingleObject(actual, 0)
	}
	err := terminateBinaryProcessHandle(handle, windows.TerminateProcess, instantWait)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("live process access denial was suppressed: %v", err)
	}
	if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("access-denied child should still be alive: state=%d err=%v", state, err)
	}
}

func startBinaryProcessStopChild(t *testing.T, access uint32) (windows.Handle, io.WriteCloser) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBinaryProcessStopChild$")
	cmd.Env = append(os.Environ(), "AGENTDOCK_PROCESS_STOP_TEST_CHILD=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child did not become ready: %q %v", line, err)
	}
	handle, err := windows.OpenProcess(access, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	return handle, input
}

func TestBinaryProcessStopChild(t *testing.T) {
	if os.Getenv("AGENTDOCK_PROCESS_STOP_TEST_CHILD") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
