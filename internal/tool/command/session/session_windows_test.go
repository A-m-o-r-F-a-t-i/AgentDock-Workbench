//go:build windows

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/config"
)

const windowsNativeChildReadyEnv = "AGENTDOCK_TEST_WINDOWS_NATIVE_CHILD_READY"

type windowsNativeChildReady struct {
	PID  int `json:"pid"`
	Port int `json:"port"`
}

func TestWindowsShellSelectionBuildsExpectedCommands(t *testing.T) {
	lookup := func(name string) (string, error) {
		switch name {
		case "pwsh.exe":
			return `C:\Tools\pwsh.exe`, nil
		case "cmd.exe":
			return `C:\Windows\System32\cmd.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	gitBash := func() string { return `C:\Program Files\Git\bin\bash.exe` }
	for _, test := range []struct {
		name  string
		shell string
		want  []string
	}{
		{name: "PowerShell", shell: config.WindowsCommandShellPwsh, want: []string{`C:\Tools\pwsh.exe`, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", powershellUTF8Prefix + "echo hello"}},
		{name: "Command Prompt", shell: config.WindowsCommandShellCmd, want: []string{`C:\Windows\System32\cmd.exe`, "/D", "/S", "/C", "echo hello"}},
		{name: "Git Bash", shell: config.WindowsCommandShellGitBash, want: []string{`C:\Program Files\Git\bin\bash.exe`, "--noprofile", "--norc", "-c", "echo hello"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := windowsShellCommand(context.Background(), test.shell, "echo hello", lookup, gitBash)
			if got, want := strings.Join(cmd.Args, "\x00"), strings.Join(test.want, "\x00"); got != want {
				t.Fatalf("command args = %#v, want %#v", cmd.Args, test.want)
			}
		})
	}
}

func TestWindowsPowerShellFallsBackToCommandPrompt(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "cmd.exe" {
			return `C:\Windows\System32\cmd.exe`, nil
		}
		return "", errors.New("not found")
	}
	cmd := windowsShellCommand(context.Background(), config.WindowsCommandShellPwsh, "echo fallback", lookup, func() string { return "unused" })
	if got, want := strings.Join(cmd.Args, "\x00"), strings.Join([]string{`C:\Windows\System32\cmd.exe`, "/D", "/S", "/C", "echo fallback"}, "\x00"); got != want {
		t.Fatalf("fallback command args = %#v", cmd.Args)
	}
}

func TestGitBashCandidatesFollowGitForWindowsInstall(t *testing.T) {
	env := map[string]string{
		"ProgramFiles": `C:\Program Files`,
		"LocalAppData": `C:\Users\tester\AppData\Local`,
	}
	candidates := gitBashCandidates(`C:\Program Files\Git\cmd\git.exe`, func(name string) string { return env[name] })
	joined := strings.ToLower(strings.Join(candidates, "\n"))
	for _, expected := range []string{
		`c:\program files\git\bin\bash.exe`,
		`c:\users\tester\appdata\local\programs\git\bin\bash.exe`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Git Bash candidates %#v do not contain %q", candidates, expected)
		}
	}
}

func TestWindowsPowerShellOutputIsUTF8(t *testing.T) {
	s, _, err := Start(
		context.Background(),
		"[Console]::Out.Write('中文输出')",
		t.TempDir(),
		os.Environ(),
		5*time.Second,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	<-s.Done
	if err := s.WaitError(); err != nil {
		t.Fatal(err)
	}
	result := s.Snapshot("exited", 4096)
	if result.Stdout != "中文输出" {
		t.Fatalf("stdout = %#v", result.Stdout)
	}
}

func TestWindowsTTYUsesConPTYAndAcceptsInput(t *testing.T) {
	s, _, err := StartWithTTY(
		context.Background(),
		"$line=[Console]::In.ReadLine(); [Console]::Out.Write(\"received:$line\")",
		t.TempDir(),
		os.Environ(),
		10*time.Second,
		true,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if s.Terminal != "conpty" {
		t.Fatalf("terminal = %q, want conpty", s.Terminal)
	}
	if err := s.Write("hello\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("ConPTY command did not finish")
	}
	if err := s.WaitError(); err != nil {
		t.Fatal(err)
	}
	result := s.Snapshot("exited", 4096)
	stdout := result.Stdout
	if !strings.Contains(stdout, "received:hello") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestWindowsKillTerminatesPowerShellNativeChildTree(t *testing.T) {
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	readyPath := filepath.Join(root, "native-child.ready.json")
	command := fmt.Sprintf("& '%s' '-test.run=^TestWindowsNativeChildHelper$'", strings.ReplaceAll(testBinary, "'", "''"))
	childEnv := append(os.Environ(), windowsNativeChildReadyEnv+"="+readyPath)

	s, _, err := Start(context.Background(), command, root, childEnv, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	defer func() {
		if s.Command != nil && s.Command.Process != nil {
			_ = s.Command.Process.Kill()
		}
	}()
	ready := waitForWindowsNativeChild(t, s, readyPath)
	childPID := ready.PID
	port := ready.Port
	defer func() {
		if process, findErr := os.FindProcess(childPID); findErr == nil {
			_ = process.Kill()
		}
	}()

	killed, err := s.Kill()
	if err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	if !killed {
		t.Fatal("Kill() did not report a running session")
	}
	select {
	case <-s.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish after killing its process tree")
	}
	if err := waitForWindowsTestPort(port, false, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNativeChildHelper(t *testing.T) {
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

func waitForWindowsNativeChild(t *testing.T, s *Session, readyPath string) windowsNativeChildReady {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(readyPath)
		if err == nil {
			var ready windowsNativeChildReady
			if json.Unmarshal(data, &ready) == nil && ready.PID > 0 && ready.Port > 0 {
				if err := waitForWindowsTestPort(ready.Port, true, 2*time.Second); err != nil {
					t.Fatalf("native child reported ready but its port is unavailable: %v", err)
				}
				return ready
			}
		}
		select {
		case <-s.Done:
			status := s.Peek("exited", 4096)
			t.Fatalf("native child exited before readiness: stdout=%q stderr=%q command_error=%v", status.Stdout, status.Stderr, s.WaitError())
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	status := s.Peek("running", 4096)
	t.Fatalf("native child did not start: stdout=%q stderr=%q", status.Stdout, status.Stderr)
	return windowsNativeChildReady{}
}

func waitForWindowsTestPort(port int, wantOpen bool, timeout time.Duration) error {
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
