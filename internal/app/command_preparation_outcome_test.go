package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
)

func TestCommandSkillPreparationFailureReportsNoExecution(t *testing.T) {
	runtimes := []string{"native"}
	if runtime.GOOS == "windows" {
		runtimes = append(runtimes, "wsl")
	}
	for _, runtimeName := range runtimes {
		t.Run(runtimeName, func(t *testing.T) {
			r := completionRuntime(t)
			envs, err := envstore.New(r.cfg.AgentDockHome)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.command.Close(); err != nil {
				t.Fatal(err)
			}
			var preparationCause error
			r.command = toolcommand.New(func() config.Config { return r.cfg }, r.ws, envs,
				func(ctx context.Context, _ string) (toolcommand.SkillLease, error) {
					<-ctx.Done()
					preparationCause = fmt.Errorf("Plugin runtime file verification interrupted: %w", ctx.Err())
					return toolcommand.SkillLease{}, preparationCause
				}, r.commandExecutionContext)
			r.command.SetActivityStore(r.activity)
			command := "printf dispatched > preparation-marker"
			if runtime.GOOS == "windows" && runtimeName == "native" {
				command = "Set-Content -LiteralPath 'preparation-marker' -Value 'dispatched'"
			}
			args := map[string]any{"cmd": command, "skill_ref": "preparation-fixture", "timeout_ms": 30, "execution_mode": "sync"}
			if runtimeName == "wsl" {
				args["runtime"], args["wsl_distribution"] = "wsl", "Ubuntu"
			}
			result, err := r.Call(scopeHost("preparation-"+runtimeName), "exec_command", args)
			if preparationCause == nil || !errors.Is(err, preparationCause) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Runtime lost the actual preparation cause: result=%v err=%v cause=%v", result, err, preparationCause)
			}
			var failure *ToolError
			if !errors.As(err, &failure) {
				t.Fatalf("missing structured failure: %v", err)
			}
			if failure.Details["execution_performed"] != false || failure.Details["effect_state"] != "none" || failure.Details["retry_safe"] != false {
				t.Errorf("preparation failure misreported business execution: code=%s details=%+v", failure.Code, failure.Details)
			}
			callID := stringArg(failure.Details, "call_id")
			call, err := r.activity.Call(t.Context(), callID)
			if err != nil {
				t.Fatal(err)
			}
			detail := call.ToolExecution
			if call.Status != "failed" || call.SessionID != "" || detail == nil || detail.ExecutionPerformed == nil || *detail.ExecutionPerformed || detail.EffectState != "none" || detail.RetrySafe == nil || *detail.RetrySafe {
				t.Errorf("persisted preparation outcome disagrees: call=%+v details=%+v", call, detail)
			}
			page, err := r.activity.Query(t.Context(), activity.Query{CallID: callID})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Events {
				if event.Kind == "command.started" || event.SessionID != "" {
					t.Errorf("failed Skill preparation dispatched a command: %+v", event)
				}
			}
			if _, err := os.Stat(filepath.Join(r.cfg.AgentDockDefaultDir, "preparation-marker")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("failed preparation executed its command: %v", err)
			}
		})
	}
}

func TestCommandFailureAfterStartRetainsExecutedOutcome(t *testing.T) {
	r := completionRuntime(t)
	command := "printf dispatched > started-marker; exit 7"
	if runtime.GOOS == "windows" {
		command = "Set-Content -LiteralPath 'started-marker' -Value 'dispatched'; exit 7"
	}
	result, err := r.Call(scopeHost("failure-after-start"), "exec_command", map[string]any{
		"cmd": command, "execution_mode": "sync", "timeout_ms": 15000,
	})
	if err != nil || result["command_ok"] != false || result["exit_code"] != 7 || result["operation_status"] != "failed" || stringArg(result, "session_id") == "" {
		t.Fatalf("started command lost its real failure: result=%+v err=%v", result, err)
	}
	if result["execution_performed"] == false || result["effect_state"] == "none" || result["retry_safe"] == true {
		t.Fatalf("started command was classified as safe non-execution: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(r.cfg.AgentDockDefaultDir, "started-marker")); err != nil {
		t.Fatalf("command did not reach its actual side effect: %v", err)
	}
	r.commandSettlementWG.Wait()
	call, err := r.activity.Call(t.Context(), stringArg(result, "call_id"))
	if err != nil || call.Status != "failed" || call.SessionID != stringArg(result, "session_id") || call.ExitCode == nil || *call.ExitCode != 7 {
		t.Fatalf("started command lost its persisted terminal: call=%+v err=%v", call, err)
	}
	if detail := call.ToolExecution; detail != nil && (detail.ExecutionPerformed != nil && !*detail.ExecutionPerformed || detail.EffectState == "none" || detail.RetrySafe != nil && *detail.RetrySafe) {
		t.Fatalf("persisted started command was classified as non-execution: %+v", detail)
	}
}
