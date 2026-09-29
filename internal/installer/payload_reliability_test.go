package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/updateengine"
)

func writePayloadFixture(t testing.TB, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func requirePayloadFixture(t testing.TB, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s: got %q, err=%v, want %q", path, got, err, want)
	}
}

func windowsPayloadFixture(t *testing.T) (Request, updateengine.WindowsLayout) {
	t.Helper()
	root := t.TempDir()
	request := Request{
		InstallRoot: filepath.Join(root, "runtime"), RuntimeRoot: filepath.Join(root, "runtime"),
		PayloadDir: filepath.Join(root, "payload"), Version: "v1.2.3", TunnelMode: "none",
	}
	for _, name := range []string{"agentdock.exe", "agentdock-tray.exe", "agentdock-arbiter.exe"} {
		writePayloadFixture(t, filepath.Join(request.PayloadDir, name), "new-"+name)
	}
	layout, err := updateengine.NewWindowsLayout(request.InstallRoot)
	if err != nil {
		t.Fatal(err)
	}
	return request, layout
}

func TestWindowsPublishDoesNotReuseInterruptedStaging(t *testing.T) {
	request, layout := windowsPayloadFixture(t)
	stale := filepath.Join(layout.VersionsDir(), ".bootstrap-"+request.Version)
	for _, name := range []string{"core-skills/removed.txt", "wsl-helper/removed.txt", "orphan.dll"} {
		writePayloadFixture(t, filepath.Join(stale, filepath.FromSlash(name)), "interrupted payload")
	}
	_, err := publishWindowsGeneration(request, newJournal(request.RuntimeRoot, "fresh-publish"), layout)
	if err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, layout.GenerationCore(request.Version), "new-agentdock.exe")
	for _, name := range []string{"core-skills", "wsl-helper", "orphan.dll"} {
		if _, err := os.Lstat(filepath.Join(layout.GenerationDir(request.Version), name)); !os.IsNotExist(err) {
			t.Errorf("interrupted staging contaminated generation: %s (err=%v)", name, err)
		}
	}
}

func TestWindowsActivationFailureRestoresStableLaunchers(t *testing.T) {
	request, layout := windowsPayloadFixture(t)
	writePayloadFixture(t, layout.CoreShim(), "old-core-shim")
	writePayloadFixture(t, layout.TrayShim(), "old-tray-shim")
	writePayloadFixture(t, filepath.Join(request.PayloadDir, "agentdock-shim.exe"), "new-core-shim")
	writePayloadFixture(t, filepath.Join(request.PayloadDir, "agentdock-tray-shim.exe"), "new-tray-shim")
	// Missing icon fails activation only after both stable launchers were copied.
	journal := newJournal(request.RuntimeRoot, "launcher-rollback")
	staged, err := publishWindowsGeneration(request, journal, layout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activateWindows(context.Background(), request, staged); err == nil {
		t.Fatal("missing icon did not fail activation")
	}
	if err := journal.Restore(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, layout.CoreShim(), "old-core-shim")
	requirePayloadFixture(t, layout.TrayShim(), "old-tray-shim")
}

func TestUnixSameVersionStageRollbackRestoresGeneration(t *testing.T) {
	root := t.TempDir()
	request := unixInstallRequest(filepath.Join(root, "app"), filepath.Join(root, "state"),
		writeUnixPayload(t, root, "payload", "replacement-core"), "v1.2.3")
	generation := filepath.Join(request.InstallRoot, "versions", request.Version)
	writePayloadFixture(t, filepath.Join(generation, "agentdock"), "known-good-core")
	writePayloadFixture(t, filepath.Join(generation, "core-skills", "old.txt"), "known-good-skill")
	journal := newJournal(request.RuntimeRoot, "same-version-rollback")
	if _, err := stageUnixPayload(request, journal); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, filepath.Join(generation, "agentdock"), "replacement-core")
	if err := journal.Restore(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, filepath.Join(generation, "agentdock"), "known-good-core")
	requirePayloadFixture(t, filepath.Join(generation, "core-skills", "old.txt"), "known-good-skill")
}

func TestUnixStageFailureLeavesExistingGenerationUntouched(t *testing.T) {
	root := t.TempDir()
	request := unixInstallRequest(filepath.Join(root, "app"), filepath.Join(root, "state"),
		writeUnixPayload(t, root, "payload", "replacement-core"), "v1.2.3")
	request.SkillBundle = filepath.Join(root, "missing-bundle")
	generation := filepath.Join(request.InstallRoot, "versions", request.Version)
	writePayloadFixture(t, filepath.Join(generation, "agentdock"), "known-good-core")
	journal := newJournal(request.RuntimeRoot, "partial-stage")
	if _, err := stageUnixPayload(request, journal); err == nil {
		t.Fatal("missing bundle was accepted")
	}
	requirePayloadFixture(t, filepath.Join(generation, "agentdock"), "known-good-core")
	if err := journal.Restore(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, filepath.Join(generation, "agentdock"), "known-good-core")
}

func TestUnixNoPayloadRepairKeepsInstalledBundle(t *testing.T) {
	root := t.TempDir()
	request := unixInstallRequest(filepath.Join(root, "app"), filepath.Join(root, "state"), "", "v1.2.3")
	request.Action = ActionRepair
	generation := filepath.Join(request.InstallRoot, "versions", request.Version)
	request.BinaryPath = filepath.Join(generation, "agentdock")
	writePayloadFixture(t, request.BinaryPath, "installed-core")
	writePayloadFixture(t, filepath.Join(generation, "core-skills", "installed.txt"), "installed-skill")
	staged, err := stageUnixPayload(request, newJournal(request.RuntimeRoot, "no-payload"))
	if err != nil {
		t.Fatal(err)
	}
	requirePayloadFixture(t, staged.Binary, "installed-core")
	requirePayloadFixture(t, filepath.Join(generation, "core-skills", "installed.txt"), "installed-skill")
	if staged.SkillBundle != "" {
		t.Fatal("no-payload repair unexpectedly requested skill bootstrap")
	}
}

func TestWindowsPublishFailureCleansOnlyOwnStaging(t *testing.T) {
	request, layout := windowsPayloadFixture(t)
	stale := filepath.Join(layout.VersionsDir(), ".bootstrap-"+request.Version, "orphan.txt")
	writePayloadFixture(t, stale, "unrelated interrupted attempt")
	if err := os.Remove(filepath.Join(request.PayloadDir, "agentdock-arbiter.exe")); err != nil {
		t.Fatal(err)
	}
	if _, err := publishWindowsGeneration(request, newJournal(request.RuntimeRoot, "copy-failure"), layout); err == nil {
		t.Fatal("incomplete payload was accepted")
	}
	requirePayloadFixture(t, stale, "unrelated interrupted attempt")
	temps, err := filepath.Glob(filepath.Join(layout.VersionsDir(), ".bootstrap-"+request.Version+"-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("unpublished staging not cleaned: %v, %v", temps, err)
	}
	if _, err := os.Stat(layout.GenerationDir(request.Version)); !os.IsNotExist(err) {
		t.Fatalf("failed payload was published: %v", err)
	}
}
