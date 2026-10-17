//go:build windows

package scripts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/process"
	"golang.org/x/sys/windows"
)

func TestSetupRuntimeStopWaitsForLauncherChildren(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal("Windows PowerShell is required to verify Setup process stopping")
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
	for _, test := range []struct {
		name       string
		core       bool
		afterEmpty bool
	}{
		{name: "tray_after_empty_generation_scan", afterEmpty: true},
		{name: "tray_during_launcher_stop"},
		{name: "core_during_launcher_stop", core: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "installation with spaces")
			stable := filepath.Join(root, "bin")
			generation := filepath.Join(root, "versions", "v1.1.9")
			other := filepath.Join(t.TempDir(), "other installation")
			for _, directory := range []string{stable, generation, other} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			corePath, trayPath := filepath.Join(stable, "agentdock.exe"), filepath.Join(stable, "agentdock-tray.exe")
			generationCore, generationTray := filepath.Join(generation, "agentdock-core.exe"), filepath.Join(generation, "agentdock-tray.exe")
			launcher, child := trayPath, generationTray
			if test.core {
				launcher, child = corePath, generationCore
			}
			otherPath := filepath.Join(other, filepath.Base(child))
			for _, path := range []string{corePath, trayPath, generationCore, generationTray, otherPath} {
				if err := os.WriteFile(path, payload, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal(err)
			}
			prefix := `Local\AgentDock-Setup-Stop-` + hex.EncodeToString(nonce[:])
			events := make(map[string]windows.Handle)
			for _, name := range []string{"launch", "parent-ready", "child-ready", "other-ready", "finish"} {
				handle, err := windows.CreateEvent(nil, 1, 0, windows.StringToUTF16Ptr(prefix+"-"+name))
				if err != nil {
					t.Fatal(err)
				}
				events[name] = handle
				t.Cleanup(func() { _ = windows.CloseHandle(handle) })
			}
			childPIDPath := filepath.Join(root, "child.pid")
			t.Cleanup(func() {
				_ = windows.SetEvent(events["finish"])
				if data, err := os.ReadFile(childPIDPath); err == nil {
					pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
					if handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
						_, _ = windows.WaitForSingleObject(handle, 5000)
						_ = windows.CloseHandle(handle)
					}
				}
			})
			environment := append(os.Environ(),
				"AGENTDOCK_SETUP_STOP_EVENTS="+prefix,
				"AGENTDOCK_SETUP_STOP_CHILD="+child,
				"AGENTDOCK_SETUP_STOP_CHILD_PID="+childPIDPath,
			)
			start := func(path, role string) *exec.Cmd {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				command := exec.CommandContext(ctx, path, "-test.run=^TestSetupRuntimeStopHelperProcess$")
				command.Env = append(append([]string(nil), environment...), "AGENTDOCK_SETUP_STOP_ROLE="+role)
				process.Configure(command)
				if err := command.Start(); err != nil {
					cancel()
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- command.Wait() }()
				t.Cleanup(func() {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("fixture process did not exit")
					}
				})
				return command
			}
			parent := start(launcher, "parent")
			waitSetupStopEvent(t, events["parent-ready"])
			foreign := start(otherPath, "other")
			waitSetupStopEvent(t, events["other-ready"])
			triggerPath, triggerCount := launcher, 1
			if test.afterEmpty {
				// The old per-path stopper checks an empty generation twice, then
				// moves on to the stable launcher without scanning its child again.
				triggerPath, triggerCount = child, 2
			}
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
			program := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
. %s -LibraryOnly
$tokens = $null; $parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile(%s, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw 'Installer script has parse errors.' }
$stopBlocks = @($ast.FindAll({ param($node)
    $node -is [System.Management.Automation.Language.TryStatementAst] -and
    $node.Body.Statements.Count -gt 0 -and
    $node.Body.Statements[0].Extent.Text -eq '$agentDockStopAttempted = $true'
}, $true))
if ($stopBlocks.Count -ne 1) { throw 'Missing unique installer service-stop body.' }
$script:originalLookup = (Get-Command Get-ProcessesByPath).ScriptBlock
$script:scanCount = 0
$script:childLaunched = $false
$launch = [Threading.EventWaitHandle]::OpenExisting(%s)
$childReady = [Threading.EventWaitHandle]::OpenExisting(%s)
function Get-ProcessesByPath {
    param([string] $ProcessName, [string] $BinaryPath)
    $found = @(& $script:originalLookup -ProcessName $ProcessName -BinaryPath $BinaryPath)
    if ([string]::Equals($BinaryPath, %s, [StringComparison]::OrdinalIgnoreCase)) {
        $script:scanCount++
        if ($script:scanCount -eq %d) {
            $launcherProcess = Get-Process -Id %d -ErrorAction SilentlyContinue
            if ($null -ne $launcherProcess) {
                try {
                    if (-not $launcherProcess.HasExited) {
                        [void] $launch.Set()
                        if (-not $childReady.WaitOne(15000)) { throw 'Generation child did not acknowledge launch.' }
                        $script:childLaunched = $true
                    }
                } finally { $launcherProcess.Dispose() }
            }
        }
    }
    return $found
}
$destinationBinary = %s
$destinationTrayBinary = %s
$existingGenerationDirectory = %s
$existingGenerationCore = %s
$existingGenerationTray = %s
$generationLayoutDetected = $true
$processWasRunning = $false
$trayProcessWasRunning = $false
$binaryBackup = %s
$trayBackup = %s
$body = $stopBlocks[0].Body.Extent.Text
try {
    # Execute the actual production stop block, including its running-state
    # capture. No Setup, registry, task or global environment operation runs.
    . ([scriptblock]::Create($body.Substring(1, $body.Length - 2)))
    foreach ($path in @($destinationBinary, $destinationTrayBinary, $existingGenerationCore, $existingGenerationTray)) {
        $name = [IO.Path]::GetFileNameWithoutExtension($path)
        if (@(Get-ProcessesByPath -ProcessName $name -BinaryPath $path).Count -ne 0) {
            throw ('Installer stop left a process at ' + $path)
        }
    }
    # Rollback reuses this owner without replacing the original running snapshot.
    Stop-AgentDockRuntimeForUpgrade -StableCorePath $destinationBinary -StableTrayPath $destinationTrayBinary -GenerationDirectories @($existingGenerationDirectory)
    @{core=$processWasRunning; tray=$trayProcessWasRunning; child_launched=$script:childLaunched} | ConvertTo-Json -Compress
} finally {
    $launch.Dispose()
    $childReady.Dispose()
}
`, quote(script), quote(script), quote(prefix+"-launch"), quote(prefix+"-child-ready"), quote(triggerPath), triggerCount, parent.Process.Pid,
				quote(corePath), quote(trayPath), quote(generation), quote(generationCore), quote(generationTray),
				quote(filepath.Join(root, "core.backup")), quote(filepath.Join(root, "tray.backup")))
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", program)
			process.Configure(command)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("production installer stop: %v\n%s", err, output)
			}
			var result struct {
				Core          bool `json:"core"`
				Tray          bool `json:"tray"`
				ChildLaunched bool `json:"child_launched"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("stop receipt: %v\n%s", err, output)
			}
			if result.Core != test.core || result.Tray == test.core {
				t.Fatalf("observed running state was lost or mixed: %+v", result)
			}
			if result.ChildLaunched == test.afterEmpty {
				t.Fatalf("wrong controlled launch ordering: %+v", result)
			}
			t.Logf("controlled launch=%t; observed core=%t tray=%t", result.ChildLaunched, result.Core, result.Tray)
			if err := os.Remove(child); err != nil {
				t.Fatalf("generation executable remains mapped after stopping: %v", err)
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
		})
	}
}

func waitSetupStopEvent(t *testing.T, handle windows.Handle) {
	t.Helper()
	state, err := windows.WaitForSingleObject(handle, 15000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("fixture event did not complete: state=%d err=%v", state, err)
	}
}

func TestSetupRuntimeStopHelperProcess(t *testing.T) {
	role := os.Getenv("AGENTDOCK_SETUP_STOP_ROLE")
	if role == "" {
		return
	}
	prefix := os.Getenv("AGENTDOCK_SETUP_STOP_EVENTS")
	open := func(name string) windows.Handle {
		t.Helper()
		handle, err := windows.OpenEvent(windows.SYNCHRONIZE|windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(prefix+"-"+name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = windows.CloseHandle(handle) })
		return handle
	}
	if role == "parent" {
		ready, launch := open("parent-ready"), open("launch")
		if err := windows.SetEvent(ready); err != nil {
			t.Fatal(err)
		}
		if state, err := windows.WaitForSingleObject(launch, 45000); err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("fixture launch was not released: state=%d err=%v", state, err)
		}
		child := exec.Command(os.Getenv("AGENTDOCK_SETUP_STOP_CHILD"), "-test.run=^TestSetupRuntimeStopHelperProcess$")
		child.Env = append(os.Environ(), "AGENTDOCK_SETUP_STOP_ROLE=child")
		process.Configure(child)
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if err := child.Process.Release(); err != nil {
			t.Fatal(err)
		}
		return
	}
	readyName := "other-ready"
	if role == "child" {
		readyName = "child-ready"
		if err := os.WriteFile(os.Getenv("AGENTDOCK_SETUP_STOP_CHILD_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ready, finish := open(readyName), open("finish")
	if err := windows.SetEvent(ready); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(finish, 60000); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("fixture cleanup did not complete: state=%d err=%v", state, err)
	}
}
