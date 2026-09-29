package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/updateengine"
)

type stagedInstall struct {
	Binary        string
	LiveBinary    string
	SkillBundle   string
	WindowsLayout *updateengine.WindowsLayout
	GenerationDir string
	Journal       *rollbackJournal
}

func payloadVersion(request Request) (string, error) {
	if request.Version != "" {
		return request.Version, nil
	}
	return updateengine.NormalizeVersion(buildinfo.Version), nil
}

func stagePayload(request Request, journal *rollbackJournal) (stagedInstall, error) {
	switch runtime.GOOS {
	case "windows":
		return stageWindowsPayload(request, journal)
	default:
		return stageUnixPayload(request, journal)
	}
}

func unixLiveBinary(request Request) string {
	if live := strings.TrimSpace(request.LiveBinary); live != "" {
		// 调用方给出的生产路径必须原样进 plist/unit，禁止 Abs/Clean。
		return request.LiveBinary
	}
	nested := filepath.Join(request.InstallRoot, "bin", "agentdock")
	direct := filepath.Join(request.InstallRoot, "agentdock")
	binaryPath := strings.TrimSpace(request.BinaryPath)
	if binaryPath != "" && filepath.Clean(binaryPath) == filepath.Clean(direct) {
		return binaryPath
	}
	return nested
}

func stageUnixPayload(request Request, journal *rollbackJournal) (stagedInstall, error) {
	staged := stagedInstall{Journal: journal, LiveBinary: unixLiveBinary(request)}
	source := strings.TrimSpace(request.BinaryPath)
	if source == "" && request.PayloadDir != "" {
		source = firstExisting(filepath.Join(request.PayloadDir, "bin", "agentdock"), filepath.Join(request.PayloadDir, "agentdock"))
	}
	if source == "" {
		if fileExists(staged.LiveBinary) {
			staged.Binary = staged.LiveBinary
			return staged, nil
		}
		return staged, fmt.Errorf("找不到 AgentDock 二进制")
	}
	generation := filepath.Join(request.InstallRoot, "versions", request.Version)
	if err := os.MkdirAll(filepath.Dir(generation), 0o755); err != nil {
		return staged, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(generation), ".install-"+request.Version+"-")
	if err != nil {
		return staged, err
	}
	defer os.RemoveAll(staging)
	// A no-payload repair retains the installed bundle and other generation
	// contents. Read them before replacing a same-version source directory.
	if request.PayloadDir == "" && dirExists(generation) {
		if err := copyTree(generation, staging, 0); err != nil {
			return staged, err
		}
	}
	stagedBinary := filepath.Join(staging, "agentdock")
	if err := copyTree(source, stagedBinary, 0o755); err != nil {
		return staged, err
	}
	if err := os.Chmod(stagedBinary, 0o755); err != nil {
		return staged, err
	}
	bundle := strings.TrimSpace(request.SkillBundle)
	if bundle == "" && request.PayloadDir != "" {
		candidate := filepath.Join(request.PayloadDir, "share", "agentdock", "core-skills")
		if dirExists(candidate) {
			bundle = candidate
		}
	}
	if bundle != "" {
		if err := copyTree(bundle, filepath.Join(staging, "core-skills"), 0o644); err != nil {
			return staged, err
		}
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return staged, err
	}
	// Existing same-version content belongs to the rollback snapshot, never
	// the created-path deletion list. Publish only after all input is copied.
	if err := journal.Snapshot(generation); err != nil {
		return staged, err
	}
	if err := os.RemoveAll(generation); err != nil {
		return staged, err
	}
	if err := os.Rename(staging, generation); err != nil {
		return staged, err
	}
	staged.Binary = filepath.Join(generation, "agentdock")
	staged.GenerationDir = generation
	if bundle != "" {
		staged.SkillBundle = filepath.Join(generation, "core-skills")
	}
	return staged, nil
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if fileExists(path) {
			return path
		}
	}
	return ""
}
