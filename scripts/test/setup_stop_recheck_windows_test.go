//go:build windows

package scripts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/process"
	"golang.org/x/sys/windows"
)

func TestSetupStopRechecksProcessAfterEmptyScan(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal("Windows PowerShell is required to verify the Setup stop recheck")
	}
	script := os.Getenv("AGENTDOCK_TEST_INSTALLER_SCRIPT")
	if script == "" {
		script = filepath.Join("..", "install", "install.ps1")
	}
	script, err = filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	ownedPath := filepath.Join(t.TempDir(), "target installation", "agentdock.exe")
	foreignPath := filepath.Join(t.TempDir(), "other installation", "agentdock.exe")
	for _, path := range []string{ownedPath, foreignPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, payload, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	prefix := `Local\AgentDock-Stop-Recheck-` + hex.EncodeToString(nonce[:])
	events := make(map[string]windows.Handle)
	for _, name := range []string{"owned", "foreign", "finish"} {
		handle, err := windows.CreateEvent(nil, 1, 0, windows.StringToUTF16Ptr(prefix+"-"+name))
		if err != nil {
			t.Fatal(err)
		}
		events[name] = handle
		t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	foreign := exec.CommandContext(ctx, foreignPath, "-test.run=^TestSetupStopRecheckHelperProcess$")
	foreign.Env = append(os.Environ(), "AGENTDOCK_SETUP_RECHECK_EVENTS="+prefix, "AGENTDOCK_SETUP_RECHECK_ROLE=foreign")
	process.Configure(foreign)
	if err := foreign.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- foreign.Wait() }()
	t.Cleanup(func() {
		_ = windows.SetEvent(events["finish"])
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("foreign fixture process did not exit")
		}
	})
	waitSetupStopEvent(t, events["foreign"])
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	program := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
. %s -LibraryOnly
$script:originalLookup = (Get-Command Get-ProcessesByPath).ScriptBlock
$script:scanCount = 0
$script:spawned = $null
$ready = [Threading.EventWaitHandle]::OpenExisting(%s)
function Get-ProcessesByPath {
    param([string] $ProcessName, [string] $BinaryPath)
    $script:scanCount++
    if ($script:scanCount -eq 2) {
        $script:spawned = Start-Process -FilePath %s -ArgumentList '-test.run=^TestSetupStopRecheckHelperProcess$' -PassThru
        if (-not $ready.WaitOne(15000)) { throw 'Late process did not acknowledge startup.' }
    }
    return @(& $script:originalLookup -ProcessName $ProcessName -BinaryPath $BinaryPath)
}
try {
    $observed = $false
    $stopped = Stop-ProcessesForUpgrade -ProcessName 'agentdock' -BinaryPath %s -ObservedRunning ([ref]$observed) -TimeoutMilliseconds 10000
    if ($null -eq $script:spawned) { throw 'The empty process scan was not rechecked.' }
    if (-not $stopped -or -not $observed) { throw 'The late running process was not recorded.' }
    if (-not $script:spawned.WaitForExit(1000)) { throw 'The late process was left running.' }
} finally {
    if ($null -ne $script:spawned) {
        if (-not $script:spawned.HasExited) { $script:spawned.Kill() }
        [void] $script:spawned.WaitForExit(5000)
        $script:spawned.Dispose()
    }
    $ready.Dispose()
}
`, quote(script), quote(prefix+"-owned"), quote(ownedPath), quote(ownedPath))
	command := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", program)
	command.Env = append(os.Environ(), "AGENTDOCK_SETUP_RECHECK_EVENTS="+prefix, "AGENTDOCK_SETUP_RECHECK_ROLE=owned")
	process.Configure(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stop after an initially empty process scan: %v\n%s", err, output)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(foreign.Process.Pid))
	if err != nil {
		t.Fatalf("other installation process disappeared: %v", err)
	}
	state, waitErr := windows.WaitForSingleObject(handle, 0)
	_ = windows.CloseHandle(handle)
	if waitErr != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("other installation was stopped: state=%d err=%v", state, waitErr)
	}
}

func TestSetupStopRecheckHelperProcess(t *testing.T) {
	role := os.Getenv("AGENTDOCK_SETUP_RECHECK_ROLE")
	if role == "" {
		return
	}
	prefix := os.Getenv("AGENTDOCK_SETUP_RECHECK_EVENTS")
	open := func(name string) windows.Handle {
		t.Helper()
		handle, err := windows.OpenEvent(windows.SYNCHRONIZE|windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(prefix+"-"+name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = windows.CloseHandle(handle) })
		return handle
	}
	ready, finish := open(role), open("finish")
	if err := windows.SetEvent(ready); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(finish, 45000); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("fixture cleanup did not complete: state=%d err=%v", state, err)
	}
}
