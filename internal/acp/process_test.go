package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMissingOptionalAgentFailsOnlyWhenUsed(t *testing.T) {
	root := t.TempDir()
	command := filepath.Join(root, "removed-agent.exe")
	manager, err := NewManager(Options{
		Home: root, DefaultCWD: root,
		Agent: AgentSpec{Name: "helper", Command: command},
	})
	if err != nil {
		t.Fatalf("missing optional executable prevented manager creation: %v", err)
	}
	defer manager.Close()
	_, err = manager.AgentInfo(context.Background())
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "ACP_START_FAILED" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing agent did not fail explicitly at use: %v", err)
	}
}

func TestACPExecutionRetainsFileTypeAndPermissionChecks(t *testing.T) {
	for _, kind := range []string{"directory", "non-executable"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "non-executable" && runtime.GOOS == "windows" {
				t.Skip("Windows does not use POSIX executable mode bits")
			}
			root := t.TempDir()
			command := filepath.Join(root, "agent")
			want := "not a regular file"
			if kind == "directory" {
				if err := os.Mkdir(command, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(command, []byte("unused"), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "not executable"
			}
			manager, err := NewManager(Options{Home: root, DefaultCWD: root, Agent: AgentSpec{Name: "helper", Command: command}})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			_, err = manager.AgentInfo(context.Background())
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != "ACP_START_FAILED" || !strings.Contains(err.Error(), want) {
				t.Fatalf("execution check was lost: %v", err)
			}
		})
	}
}
