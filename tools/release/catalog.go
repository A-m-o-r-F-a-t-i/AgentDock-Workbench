package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/uvwt/agentdock/internal/buildinfo"
)

// Artifact 描述完整验证目录中的跨平台资产。PublicContract 标记公开 Release 资产；
// 除用户安装包外，内置更新器依赖的不可变归档及其 checksum 也必须公开。
type Artifact struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Platform       string `json:"platform,omitempty"`
	Arch           string `json:"arch,omitempty"`
	Required       bool   `json:"required"`
	PublicContract bool   `json:"public_contract"`
}

func verifyDist(dir string, stdout io.Writer) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			present[entry.Name()] = true
		}
	}
	var missing []string
	for _, artifact := range ReleaseCatalog() {
		if !artifact.Required {
			continue
		}
		if !present[artifact.Name] {
			missing = append(missing, artifact.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("dist missing required artifacts: %s", strings.Join(missing, ", "))
	}
	required := 0
	for _, artifact := range ReleaseCatalog() {
		if artifact.Required {
			required++
		}
	}
	fmt.Fprintf(stdout, "release dist verified: %d required artifacts present\n", required)
	return nil
}

func ReleaseCatalog() []Artifact {
	payloads := []Artifact{
		{Name: "AgentDock-Workbench-" + buildinfo.Version + "-Android.apk", Kind: "android-package", Platform: "android", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "agentdock_linux_amd64.tar.gz", Kind: "binary-archive", Platform: "linux", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "agentdock_linux_arm64.tar.gz", Kind: "binary-archive", Platform: "linux", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "agentdock_darwin_amd64.tar.gz", Kind: "binary-archive", Platform: "darwin", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "agentdock_darwin_arm64.tar.gz", Kind: "binary-archive", Platform: "darwin", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "agentdock_windows_amd64.zip", Kind: "binary-archive", Platform: "windows", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "agentdock_windows_arm64.zip", Kind: "binary-archive", Platform: "windows", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "AgentDock-macos-universal.dmg", Kind: "disk-image", Platform: "darwin", Arch: "universal", Required: true, PublicContract: true},
		{Name: "AgentDock-macos-universal.zip", Kind: "desktop-update", Platform: "darwin", Arch: "universal", Required: true, PublicContract: true},
		{Name: "agentdock-workbench_" + buildinfo.Version + "_amd64.deb", Kind: "debian-package", Platform: "linux", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "agentdock-workbench_" + buildinfo.Version + "_arm64.deb", Kind: "debian-package", Platform: "linux", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "agentdock-workbench-" + buildinfo.Version + "-1.x86_64.rpm", Kind: "rpm-package", Platform: "linux", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "agentdock-workbench-" + buildinfo.Version + "-1.aarch64.rpm", Kind: "rpm-package", Platform: "linux", Arch: "arm64", Required: true, PublicContract: true},
		{Name: "AgentDock-Workbench-Setup-amd64.exe", Kind: "setup", Platform: "windows", Arch: "amd64", Required: true, PublicContract: true},
		{Name: "AgentDock-Workbench-Setup-arm64.exe", Kind: "setup", Platform: "windows", Arch: "arm64", Required: true, PublicContract: true},
	}
	scripts := []Artifact{
		{Name: "install.sh", Kind: "bootstrap", Platform: "unix", Required: true},
		{Name: "install.ps1", Kind: "bootstrap", Platform: "windows", Required: true},
	}
	var catalog []Artifact
	catalog = append(catalog, payloads...)
	for _, artifact := range payloads {
		catalog = append(catalog, Artifact{
			Name:           artifact.Name + ".sha256",
			Kind:           "checksum",
			Platform:       artifact.Platform,
			Arch:           artifact.Arch,
			Required:       artifact.Required,
			PublicContract: artifact.Kind == "binary-archive" || artifact.Kind == "desktop-update",
		})
	}
	catalog = append(catalog, scripts...)
	return catalog
}
