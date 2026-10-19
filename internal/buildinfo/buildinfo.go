package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/uvwt/agentdock/internal/executioncompat"
)

const Version = "1.1.9"

// ProductName is the display identity from 1.1.7 onward; machine IDs remain stable.
const ProductName = "AgentDock Workbench"

var (
	Commit    string
	BuildDate string
)

type Info struct {
	ProductName            string `json:"product_name"`
	ExecutionPolicyVersion int    `json:"execution_policy_version"`
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	BuildDate              string `json:"build_date"`
	GoVersion              string `json:"go_version"`
	Platform               string `json:"platform"`
}

func Current() Info {
	commit, buildDate := buildIdentity()
	shortCommit := commit
	if len(shortCommit) > 12 {
		shortCommit = shortCommit[:12]
	}
	return Info{
		ProductName:            ProductName,
		ExecutionPolicyVersion: executioncompat.PolicyVersion,
		Version:                strings.TrimSpace(Version),
		Commit:                 shortCommit,
		BuildDate:              buildDate,
		GoVersion:              runtime.Version(),
		Platform:               runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// FullCommit returns the immutable build revision without display truncation.
func FullCommit() string {
	commit, _ := buildIdentity()
	return commit
}

func buildIdentity() (string, string) {
	commit := strings.TrimSpace(Commit)
	buildDate := strings.TrimSpace(BuildDate)
	build, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "" {
					commit = strings.TrimSpace(setting.Value)
				}
			case "vcs.time":
				if buildDate == "" {
					buildDate = strings.TrimSpace(setting.Value)
				}
			}
		}
	}
	if commit == "" {
		commit = "unknown"
	}
	if buildDate == "" {
		buildDate = "unknown"
	}
	return commit, buildDate
}
