package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GitSnapshot contains local observation only. Its revision never invalidates
// workspace instructions or capability-directory context revisions.
type GitSnapshot struct {
	Available         bool   `json:"available"`
	RepositoryRoot    string `json:"repository_root,omitempty"`
	Branch            string `json:"branch,omitempty"`
	Head              string `json:"head,omitempty"`
	Dirty             bool   `json:"dirty,omitempty"`
	Conflicts         int    `json:"conflicts,omitempty"`
	ChangedPathsTotal int    `json:"changed_paths_total,omitempty"`
	Revision          string `json:"revision,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
}

type gitSnapshotBuffer struct {
	bytes.Buffer
	maximum   int
	truncated bool
}

func (b *gitSnapshotBuffer) Write(value []byte) (int, error) {
	count := len(value)
	remaining := max(0, b.maximum-b.Len())
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(value)
	return count, nil
}

func ReadGitSnapshot(parent context.Context, root string) GitSnapshot {
	// Git only needs cancellation and a deadline, not request payloads or other
	// business values attached to the caller's context.
	deadline := time.Now().Add(2 * time.Second)
	if inherited, ok := parent.Deadline(); ok && inherited.Before(deadline) {
		deadline = inherited
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	stopCancellation := context.AfterFunc(parent, cancel)
	defer stopCancellation()
	defer cancel()
	if parent.Err() != nil {
		cancel()
	}
	result := GitSnapshot{}
	command := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false"}, args...)...)
		cmd.Dir = root
		// Optional index/cache writes are disabled; this observer never fetches,
		// changes branches, rewrites user config or requests credentials.
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		stdout := &gitSnapshotBuffer{maximum: 4 << 20}
		stderr := &gitSnapshotBuffer{maximum: 4096}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			return stderr.String(), err
		}
		if stdout.truncated {
			return "", errors.New("git snapshot output budget exceeded")
		}
		return stdout.String(), nil
	}
	repository, err := command("rev-parse", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			result.ErrorCode = "GIT_SNAPSHOT_TIMEOUT"
		} else if !strings.Contains(repository, "not a git repository") {
			result.ErrorCode = "GIT_UNAVAILABLE"
		}
		return result
	}
	result.Available, result.RepositoryRoot = true, strings.TrimSpace(repository)
	branch, branchErr := command("symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr == nil {
		result.Branch = strings.TrimSpace(branch)
	} else {
		result.Branch = "HEAD"
	}
	head, headErr := command("rev-parse", "--verify", "HEAD")
	if headErr == nil {
		result.Head = strings.TrimSpace(head)
	}
	status, err := command("status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=none")
	if err != nil {
		result.ErrorCode = "GIT_STATUS_UNAVAILABLE"
		if ctx.Err() != nil {
			result.ErrorCode = "GIT_SNAPSHOT_TIMEOUT"
		}
		return result
	}
	records := strings.Split(status, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		if record == "" {
			continue
		}
		if len(record) < 3 || record[2] != ' ' {
			result.ErrorCode = "GIT_STATUS_INVALID"
			return result
		}
		state := record[:2]
		result.ChangedPathsTotal++
		if strings.Contains(state, "U") || state == "AA" || state == "DD" {
			result.Conflicts++
		}
		if strings.ContainsAny(state, "RC") {
			index++
		}
	}
	result.Dirty = result.ChangedPathsTotal > 0
	encoded, _ := json.Marshal(result)
	result.Revision = fmt.Sprintf("gitrev_%x", sha256.Sum256(append(encoded, []byte(status)...)))
	return result
}
