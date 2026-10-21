package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsInstallerStopsAllGenerationsBeforePortPreflightAndEngine(t *testing.T) {
	data, err := os.ReadFile("../install/install.ps1")
	if err != nil {
		t.Fatalf("read install.ps1: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"function Get-AgentDockGenerationDirectories",
		"function Stop-AgentDockManagedPortOwners",
		"function Assert-AgentDockPortAvailable",
		"Get-NetTCPConnection -State Listen -LocalPort $Port",
		"Get-CimInstance Win32_Process -Filter \"ProcessId=$ownerID\"",
		"$stopGenerationDirectories = @(Get-AgentDockGenerationDirectories -VersionsDirectory $versionsDir)",
		"$rollbackGenerationDirectories = @(Get-AgentDockGenerationDirectories -VersionsDirectory $versionsDir)",
		"Stop-AgentDockManagedPortOwners -Port $Port -StableCorePath $destinationBinary",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("install.ps1 is missing %q", required)
		}
	}
	stop := strings.Index(script, "Stop-AgentDockRuntimeForUpgrade -StableCorePath $destinationBinary")
	preflight := -1
	if stop >= 0 {
		if relative := strings.Index(script[stop:], "Assert-AgentDockPortAvailable -Port $Port"); relative >= 0 {
			preflight = stop + relative
		}
	}
	engine := strings.Index(script, "$engineJson = (Invoke-SetupRuntimeProcess -FilePath $sourceBinary")
	if stop < 0 || preflight <= stop || engine <= preflight {
		t.Fatalf("installer order must be stop (%d), port preflight (%d), engine mutation (%d)", stop, preflight, engine)
	}
}
