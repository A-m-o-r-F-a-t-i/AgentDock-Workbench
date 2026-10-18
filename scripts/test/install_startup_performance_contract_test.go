package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsSetupUsesFastModernInstallPaths(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		path := filepath.Join(append([]string{"..", ".."}, parts...)...)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}

	inno := read("packaging", "windows", "includes", "code.iss")
	loadStart := strings.Index(inno, "procedure LoadExistingSettings();")
	loadEnd := strings.Index(inno[loadStart:], "procedure ApplyExistingInstallPresentation();")
	if loadStart < 0 || loadEnd < 0 {
		t.Fatal("Setup existing-install settings procedure is missing")
	}
	loadBlock := inno[loadStart : loadStart+loadEnd]
	for _, required := range []string{
		"TryRuntimePrivilegeMode", "LegacyTask := False",
		"if not TryRuntimePrivilegeMode(PrivilegeMode) then",
		"PrivilegeMode := ''",
		"LegacyTask := LegacyAgentDockScheduledTaskExists()",
	} {
		if !strings.Contains(loadBlock, required) {
			t.Fatalf("modern install detection is missing %q", required)
		}
	}
	if strings.Count(loadBlock, "LegacyAgentDockScheduledTaskExists()") != 1 {
		t.Fatal("legacy scheduled-task fallback is evaluated more than once")
	}
	for _, required := range []string{
		"LegacyTaskDetectionComplete", "LegacyTaskDetected",
		"Exec(", "SW_HIDE", "ewWaitUntilTerminated",
	} {
		if !strings.Contains(inno, required) {
			t.Fatalf("legacy task probe is missing %q", required)
		}
	}

	legacyStart := strings.Index(inno, "function LegacyAgentDockScheduledTaskExists()")
	if legacyStart < 0 {
		t.Fatal("legacy scheduled-task probe is missing")
	}
	legacyEnd := strings.Index(inno[legacyStart:], "function TryRuntimePrivilegeMode")
	if legacyEnd < 0 {
		t.Fatal("legacy scheduled-task probe end is missing")
	}
	if strings.Contains(inno[legacyStart:legacyStart+legacyEnd], "NativeSetupCommand(") {
		t.Fatal("legacy scheduled-task detection still uses the Setup launch broker")
	}

	launcher := read("internal", "desktopruntime", "setup_launcher_windows.go")
	direct := strings.Index(launcher, "if !request.WaitForExit {")
	broker := strings.Index(launcher, "return runSetupLaunchBroker(requestPath, request)")
	if direct < 0 || broker < 0 || direct > broker || !strings.Contains(launcher[direct:broker], "runSetupProcess(requestPath, request)") {
		t.Fatal("no-wait Setup launch still requires the Task Scheduler broker")
	}
}
