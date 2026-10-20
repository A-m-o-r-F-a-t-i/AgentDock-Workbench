//go:build windows

package process

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestConfigureHidesConsoleWindow(t *testing.T) {
	cmd := exec.Command("cmd.exe")

	Configure(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("Configure did not initialize Windows process attributes")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("Configure did not hide the child process window")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestConfigurePreservesExistingCreationFlags(t *testing.T) {
	existingFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: existingFlags}

	Configure(cmd)

	wantFlags := existingFlags | uint32(windows.CREATE_NO_WINDOW)
	if cmd.SysProcAttr.CreationFlags != wantFlags {
		t.Fatalf("creation flags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, wantFlags)
	}
}

func TestConfigureConsoleTreeUsesHiddenConsole(t *testing.T) {
	existingFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS)
	cmd := exec.Command("git.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: existingFlags}

	ConfigureConsoleTree(cmd)

	wantFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NEW_CONSOLE)
	if cmd.SysProcAttr.CreationFlags != wantFlags {
		t.Fatalf("creation flags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, wantFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("ConfigureConsoleTree did not hide the console")
	}
}

func TestConfigureStdioCommandPromptUsesHiddenConsole(t *testing.T) {
	existingFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: existingFlags}

	ConfigureStdio(cmd)

	wantFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NEW_CONSOLE)
	if cmd.SysProcAttr.CreationFlags != wantFlags {
		t.Fatalf("creation flags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, wantFlags)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("ConfigureStdio did not hide the command prompt console")
	}
}

func TestConfigureStdioDirectExecutableUsesNoWindow(t *testing.T) {
	cmd := exec.Command("powershell.exe")

	ConfigureStdio(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("ConfigureStdio did not initialize Windows process attributes")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE != 0 {
		t.Fatalf("creation flags = %#x, direct executable must not create a console", cmd.SysProcAttr.CreationFlags)
	}
}

func TestConfigureStdioPreservesCommandScriptChildPipes(t *testing.T) {
	t.Setenv("GO_WANT_STDIO_BATCH_HELPER", "1")
	t.Setenv("TEST_EXECUTABLE", os.Args[0])
	directory := t.TempDir()
	script := "@echo off\r\n\"%TEST_EXECUTABLE%\" -test.run=TestStdioBatchHelperProcess\r\n"
	if err := os.WriteFile(filepath.Join(directory, "run-server.cmd"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe", "/d", "/s", "/c", "call", "run-server.cmd")
	cmd.Dir = directory
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader("stdio-child-ok\r\n")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	ConfigureStdio(cmd)

	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "stdio-child-ok" {
		t.Fatalf("stdout = %q, want stdio-child-ok", stdout.String())
	}
}

func TestStdioBatchHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_STDIO_BATCH_HELPER") != "1" {
		return
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, line)
	os.Exit(0)
}

func TestWindowsJobObjectTerminatesAttachedProcess(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	controller, err := Attach(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err := controller.Terminate(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("terminated process exited successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Windows Job Object did not terminate the process")
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsJobObjectCloseTerminatesAttachedProcess(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	controller, err := Attach(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE only guarantees termination. Windows may
		// report a zero process exit code, so the contract here is bounded exit, not status.
	case <-time.After(5 * time.Second):
		t.Fatal("closing Windows Job Object did not terminate the process")
	}
}
