package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

type commandExecutionMode string

const (
	commandExecutionModeAuto  commandExecutionMode = "auto"
	commandExecutionModeSync  commandExecutionMode = "sync"
	commandExecutionModeAsync commandExecutionMode = "async"

	completedSessionRetention       = time.Hour
	sessionKillWait                 = 3 * time.Second
	defaultCommandYield             = 10 * time.Second
	maxCommandYield                 = 30 * time.Second
	minSessionObserveDelay          = 5 * time.Second
	intermediateSessionObserveDelay = 15 * time.Second
	maxSessionObserveDelay          = 60 * time.Second
	maxSessionObserveWaitMS         = 300000
	maxCommandTimeout               = 24 * time.Hour
	maxCommandOutputBytes           = 4 << 20
	maxConcurrentCommandSessions    = 32
	maxRetainedCommandSessions      = 128
)

func (svc *Service) Exec(ctx context.Context, request ExecRequest) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.SkillRef == "" {
		request.SkillRef = request.Skill
	}
	if request.Cmd == "" {
		return nil, toolError("INVALID_ARGUMENT", "cmd is required", "validation")
	}
	if (request.Backend == "termux_host" || request.Backend == "android_shizuku") && len(request.Stdin) > 16384 {
		return nil, toolError("ANDROID_STDIN_TOO_LARGE", "Initial Android stdin exceeds 16 KiB; no command was started", "validation")
	}
	if err := request.Binding.Validate(); err != nil {
		return nil, toolError("INVALID_ACTIVITY_BINDING", err.Error(), "validation")
	}
	timeout, err := commandTimeout(request.TimeoutMS)
	if err != nil {
		return nil, err
	}
	executionMode, err := commandExecutionModeArg(request.ExecutionMode)
	if err != nil {
		return nil, err
	}
	defaultYieldMS := int(defaultCommandYield / time.Millisecond)
	yieldMS := boundedInt(intValue(request.YieldTimeMS, defaultYieldMS), defaultYieldMS, 0, int(maxCommandYield/time.Millisecond))
	yield := time.Duration(yieldMS) * time.Millisecond
	maxBytes := commandOutputLimit(request.MaxOutputBytes)
	tty := request.TTY
	commandCtx, err := svc.commandContext()
	if err != nil {
		return nil, err
	}
	var journal *activity.AppendReservation
	if svc.activity != nil {
		journal, err = svc.activity.ReserveAppend(ctx, 2)
		if err != nil {
			return nil, toolErrorDetails("ACTIVITY_CAPACITY", "Command was not started because its activity lifecycle could not be reserved.", "resource_limit", map[string]any{"executed": false, "reason": err.Error()})
		}
	}
	journalTransferred := false
	defer func() {
		if !journalTransferred {
			journal.Close()
		}
	}()
	prepareCtx, prepareCancel := context.WithTimeout(ctx, timeout)
	var invocation commandInvocation
	if request.Backend != "" && request.Backend != "default" {
		invocation, err = svc.prepareAndroidInvocation(prepareCtx, request, timeout)
	} else {
		invocation, err = svc.prepareCommandInvocation(prepareCtx, request)
	}
	preparationErr := prepareCtx.Err()
	prepareCancel()
	if err != nil {
		return nil, err
	}
	skillReleaseHandled := false
	defer func() {
		if !skillReleaseHandled && invocation.skillRelease != nil {
			invocation.skillRelease()
		}
	}()
	// A resolver may return a usable lease concurrently with cancellation.
	// Reject it before dispatch while still releasing the acquired lease.
	if preparationErr != nil {
		return nil, preparationErr
	}

	if !svc.sessions.TryReserve(maxConcurrentCommandSessions) {
		if svc.sessions.Closing() {
			return nil, toolError("RUNTIME_CLOSING", "AgentDock runtime is shutting down", "runtime")
		}
		return nil, toolErrorDetails(
			"SESSION_LIMIT_REACHED",
			"too many command sessions are already running",
			"resource_limit",
			map[string]any{"max_running_sessions": maxConcurrentCommandSessions},
		)
	}
	reservationActive := true
	defer func() {
		if reservationActive {
			svc.sessions.ReleaseReservation()
		}
	}()

	// 这里故意不用请求 ctx 派生子进程生命周期。
	// 背景：exec_command 可能先返回 running，让模型后续通过 session_observe action=status 继续取结果；
	// 如果子进程绑定到单次 MCP 请求 ctx，请求结束时 git push / npm install 等长任务会被杀掉。
	// 因此长任务只受 timeout_ms 和 session_act action=kill/kill_all 控制。
	// Only detach the process lifetime after admitting an uncancelled request.
	if err := ctx.Err(); err != nil {
		svc.sessions.FinishStart()
		return nil, err
	}
	s, sandboxStatus, err := invocation.start(commandCtx, timeout, func(command *exec.Cmd) (func(), session.PreparationStatus) {
		// AgentDock 不额外过滤命令，实际权限边界由所选运行环境决定。
		privilegeWarning := "exec_command runs with the AgentDock process OS user privileges"
		if invocation.execution.Runtime == "wsl" {
			privilegeWarning = "runtime=wsl executes with the selected distribution's default Linux user privileges"
		}
		return func() {}, session.PreparationStatus{Enabled: false, Mode: "none", Policy: "no_command_content_filtering", Warnings: []string{privilegeWarning, "use Docker volumes, service users, file permissions, and network policy as the security boundary"}}
	})
	// 只有 invocation.start 完整返回后，runner、平台进程控制器以及 cmdCtx 取消监听才都已经建立。
	// Runtime.Close 会等待这个启动窗口排空，再取消 commandCtx，避免在半启动状态抢占进程。
	if err != nil {
		svc.sessions.FinishStart()
		return nil, err
	}
	if invocation.skillRelease != nil {
		skillReleaseHandled = true
		go func() { <-s.Done; invocation.skillRelease() }()
	}
	s.SetExecutionContext(invocation.execution)
	s.SetActivityBinding(request.Binding)
	activityDone := svc.trackCommandActivity(s, request, journal)
	journalTransferred = true
	svc.sessions.FinishStart()
	storeSession := func(reason string) Result {
		svc.storeReservedSession(s)
		reservationActive = false
		result := snapshotResult(s.Snapshot("running", maxBytes))
		result["sandbox"] = preparationStatusResult(sandboxStatus)
		result["session_reason"] = reason
		return result
	}

	preserveInputFailure := func() Result {
		result := storeSession("initial_stdin_unconfirmed")
		result["stdin_error"] = "Initial input was not acknowledged; cancellation was requested. Observe the original session and do not resend input."
		return result
	}
	if request.Stdin != "" {
		if err := s.Write(request.Stdin); err != nil {
			s.Kill()
			s.Cancel()
			if invocation.external != nil {
				return preserveInputFailure(), nil
			}
			return nil, fmt.Errorf("write command stdin: %w", err)
		}
	}
	if !tty {
		if err := s.CloseStdin(); err != nil && !errors.Is(err, os.ErrClosed) {
			s.Kill()
			s.Cancel()
			if invocation.external != nil {
				return preserveInputFailure(), nil
			}
			return nil, fmt.Errorf("close command stdin: %w", err)
		}
	}

	switch executionMode {
	case commandExecutionModeAsync:
		return storeSession("explicit_async"), nil
	case commandExecutionModeSync:
		select {
		case <-s.Done:
		case <-ctx.Done():
			return storeSession("request_cancelled"), nil
		}
	case commandExecutionModeAuto:
		timer := time.NewTimer(yield)
		defer timer.Stop()
		select {
		case <-s.Done:
		case <-timer.C:
			return storeSession("foreground_threshold_exceeded"), nil
		case <-ctx.Done():
			return storeSession("request_cancelled"), nil
		}
	}

	err = s.WaitError()
	s.Cancel()
	waitCommandActivity(activityDone)
	var final session.Snapshot
	if modelOutputProjection(ctx) {
		final = s.RetainedSnapshot()
	} else {
		final = s.Snapshot("exited", maxBytes)
	}
	result := snapshotResult(final)
	result["sandbox"] = preparationStatusResult(sandboxStatus)
	if s.TimedOut {
		result["status"] = "timeout"
	}
	if err != nil {
		result["command_error"] = err.Error()
	}
	return result, nil
}

func commandExecutionModeArg(raw string) (commandExecutionMode, error) {
	if raw == "" {
		raw = string(commandExecutionModeAuto)
	}
	mode := commandExecutionMode(raw)
	switch mode {
	case commandExecutionModeAuto, commandExecutionModeSync, commandExecutionModeAsync:
		return mode, nil
	default:
		return "", toolErrorDetails(
			"INVALID_EXECUTION_MODE",
			"execution_mode must be auto, sync, or async",
			"validation",
			map[string]any{"execution_mode": mode, "allowed": []string{"auto", "sync", "async"}},
		)
	}
}

func commandTimeout(requested *int) (time.Duration, error) {
	timeoutMS := intValue(requested, 30000)
	if timeoutMS <= 0 {
		return 0, toolErrorDetails(
			"INVALID_TIMEOUT",
			"timeout_ms must be a positive integer",
			"validation",
			map[string]any{"timeout_ms": timeoutMS},
		)
	}
	maximumMS := int(maxCommandTimeout / time.Millisecond)
	if timeoutMS > maximumMS {
		timeoutMS = maximumMS
	}
	return time.Duration(timeoutMS) * time.Millisecond, nil
}

func commandOutputLimit(requested *int) int {
	return boundedInt(intValue(requested, 65536), 65536, 1, maxCommandOutputBytes)
}

func sessionObserveDelay(elapsedMS int64) time.Duration {
	if elapsedMS < (30 * time.Second).Milliseconds() {
		return minSessionObserveDelay
	}
	if elapsedMS < time.Minute.Milliseconds() {
		return intermediateSessionObserveDelay
	}
	return maxSessionObserveDelay
}

func sessionObserveWait(requested *int, elapsedMS int64) (time.Duration, error) {
	if requested == nil {
		return sessionObserveDelay(elapsedMS), nil
	}
	if *requested < 0 || *requested > maxSessionObserveWaitMS {
		return 0, toolErrorDetails(
			"INVALID_ARGUMENT",
			fmt.Sprintf("wait_ms must be between 0 and %d", maxSessionObserveWaitMS),
			"validation",
			map[string]any{"wait_ms": *requested, "minimum": 0, "maximum": maxSessionObserveWaitMS},
		)
	}
	return time.Duration(*requested) * time.Millisecond, nil
}

// snapshotResult 是 Command capability 向外部 Tool Result 的输出边界；Session 核心只暴露强类型快照。
func snapshotResult(snapshot session.Snapshot) Result {
	result := Result{
		"session_id": snapshot.SessionID, "status": publicCommandStatus(snapshot.Status),
		"outcome":    commandOutcome(snapshot),
		"elapsed_ms": snapshot.ElapsedMS, "terminal": snapshot.Terminal,
		"source_complete":      snapshot.StdoutDroppedBytes == 0 && snapshot.StderrDroppedBytes == 0,
		"displayed_bytes":      snapshot.StdoutOutputBytes + snapshot.StderrOutputBytes,
		"total_output_bytes":   snapshot.StdoutTotalBytes + snapshot.StderrTotalBytes,
		"retained_bytes":       max(0, snapshot.StdoutTotalBytes-snapshot.StdoutDroppedBytes) + max(0, snapshot.StderrTotalBytes-snapshot.StderrDroppedBytes),
		"dropped_before_bytes": snapshot.StdoutDroppedBytes + snapshot.StderrDroppedBytes,
		"offset":               map[string]any{"stdout": snapshot.StdoutOffsetBytes, "stderr": snapshot.StderrOffsetBytes},
		"next_offset":          map[string]any{"stdout": snapshot.StdoutNextOffset, "stderr": snapshot.StderrNextOffset},
		"offset_unit":          "utf8_byte",
	}
	if snapshot.Stdout == "" && snapshot.Stderr == "" {
		result["no_change"] = true
		if !snapshot.Completed {
			result["next_observe_after_ms"] = maxSessionObserveDelay.Milliseconds()
		}
	}
	addStreamSnapshotResult(result, "stdout", snapshot.Stdout, snapshot.StdoutOutputBytes, snapshot.StdoutTotalBytes, snapshot.StdoutDroppedBytes, snapshot.StdoutOmittedBytes, snapshot.StdoutOutputLines, snapshot.StdoutTruncated)
	addStreamSnapshotResult(result, "stderr", snapshot.Stderr, snapshot.StderrOutputBytes, snapshot.StderrTotalBytes, snapshot.StderrDroppedBytes, snapshot.StderrOmittedBytes, snapshot.StderrOutputLines, snapshot.StderrTruncated)
	if snapshot.TimedOut {
		result["timed_out"] = true
	}
	addBindingResult(result, snapshot.Binding)
	if snapshot.ActivityWarning != "" {
		result["activity_warning"] = snapshot.ActivityWarning
	}
	if snapshot.OutcomeUnknown {
		result["outcome_unknown"] = true
	}
	if snapshot.Completed && !snapshot.OutcomeUnknown {
		result["exit_code"] = snapshot.ExitCode
		result["command_ok"] = snapshot.CommandOK && snapshot.Status == "exited" && snapshot.ExitCode == 0
	}
	if snapshot.Runtime != "" {
		result["runtime"] = snapshot.Runtime
	}
	if snapshot.WSLDistribution != "" {
		result["wsl_distribution"] = snapshot.WSLDistribution
	}
	if snapshot.Workdir != "" {
		result["workdir"] = snapshot.Workdir
	}
	if snapshot.Status == "running" {
		result["observe_after_ms"] = sessionObserveDelay(snapshot.ElapsedMS).Milliseconds()
	}
	setCommandOperationStatus(result)
	return result
}

func commandOutcome(snapshot session.Snapshot) string {
	if snapshot.OutcomeUnknown {
		return "unknown"
	}
	if !snapshot.Completed {
		return "running"
	}
	if snapshot.TimedOut {
		return "timed_out"
	}
	if snapshot.CommandOK && snapshot.Status == "exited" && snapshot.ExitCode == 0 {
		return "command_succeeded"
	}
	return "command_failed"
}

func addStreamSnapshotResult(result Result, name, text string, outputBytes, totalBytes, droppedBytes, omittedBytes, outputLines int, truncated bool) {
	if text != "" {
		result[name] = text
	}
	if outputBytes > 0 {
		result[name+"_output_bytes"] = outputBytes
	}
	if totalBytes > 0 {
		result[name+"_total_bytes"] = totalBytes
	}
	if droppedBytes > 0 {
		result[name+"_dropped_bytes"] = droppedBytes
	}
	if omittedBytes > 0 {
		result[name+"_omitted_bytes"] = omittedBytes
	}
	if outputLines > 0 {
		result[name+"_output_lines"] = outputLines
	}
	if truncated {
		result[name+"_truncated"] = true
	}
}

func preparationStatusResult(status session.PreparationStatus) map[string]any {
	result := map[string]any{"enabled": status.Enabled}
	if status.Mode != "" {
		result["mode"] = status.Mode
	}
	if status.Policy != "" {
		result["policy"] = status.Policy
	}
	if len(status.Warnings) > 0 {
		result["warnings"] = append([]string(nil), status.Warnings...)
	}
	return result
}

func (svc *Service) writeStdin(request SessionActRequest) (Result, error) {
	s, ok := svc.sessions.Get(request.SessionID)
	if !ok {
		return nil, toolError("SESSION_NOT_FOUND", "session not found", "not_found")
	}
	maxBytes := commandOutputLimit(request.MaxOutputBytes)
	select {
	case <-s.Done:
		return svc.completedSessionResult(s, maxBytes, false), nil
	default:
	}

	if request.Chars != "" {
		if err := s.Write(request.Chars); err != nil {
			select {
			case <-s.Done:
				return svc.completedSessionResult(s, maxBytes, false), nil
			default:
			}
			return nil, fmt.Errorf("write session stdin: %w", err)
		}
	}
	select {
	case <-s.Done:
		return svc.completedSessionResult(s, maxBytes, false), nil
	default:
		return snapshotResult(s.Peek("running", maxBytes)), nil
	}
}

func (svc *Service) completedSessionResult(s *session.Session, maxBytes int, consume bool) Result {
	err := s.WaitError()
	s.Cancel()
	var snapshot session.Snapshot
	if consume {
		snapshot = s.Snapshot("exited", maxBytes)
	} else {
		snapshot = s.Peek("exited", maxBytes)
	}
	result := snapshotResult(snapshot)
	if err != nil {
		result["command_error"] = err.Error()
	}
	return result
}

func (svc *Service) killSession(request SessionActRequest) (Result, error) {
	started := time.Now()
	s, ok := svc.sessions.Get(request.SessionID)
	if !ok {
		return nil, toolError("SESSION_NOT_FOUND", "session not found", "not_found")
	}
	select {
	case <-s.Done:
		return svc.completedSessionResult(s, commandOutputLimit(request.MaxOutputBytes), false), nil
	default:
	}
	_, killErr := s.Kill()
	if killErr != nil {
		return nil, toolErrorDetails(
			"SESSION_KILL_FAILED",
			"failed to terminate the session process tree",
			"runtime",
			map[string]any{"session_id": s.ID, "reason": killErr.Error()},
		)
	}
	if !waitForSessionCompletion(s, sessionKillWait) {
		return nil, toolErrorDetails(
			"SESSION_KILL_TIMEOUT",
			"session did not stop after kill request",
			"runtime",
			map[string]any{"session_id": s.ID, "wait_ms": sessionKillWait.Milliseconds()},
		)
	}
	result := snapshotResult(s.Peek("killed", commandOutputLimit(request.MaxOutputBytes)))
	if err := s.WaitError(); err != nil {
		result["command_error"] = err.Error()
	}
	result["kill_operation_ms"] = time.Since(started).Milliseconds()
	return result, nil
}

func (svc *Service) killAll() (Result, error) {
	sessions := svc.sessions.List()
	running := make([]*session.Session, 0, len(sessions))
	items := make([]map[string]any, 0, len(sessions))
	killFailures := make([]map[string]any, 0)
	for _, s := range sessions {
		select {
		case <-s.Done:
			summary := s.Summary()
			s.Cancel()
			items = append(items, map[string]any{"session_id": s.ID, "status": publicCommandStatus(summary.Status)})
		default:
			_, killErr := s.Kill()
			if killErr != nil {
				killFailures = append(killFailures, map[string]any{"session_id": s.ID, "reason": killErr.Error()})
			}
			running = append(running, s)
		}
	}
	completed, timedOut := waitForSessionsCompletion(running, sessionKillWait)
	for _, s := range completed {
		items = append(items, map[string]any{"session_id": s.ID, "status": publicCommandStatus(s.Summary().Status)})
	}
	if len(killFailures) > 0 {
		details := map[string]any{"sessions": killFailures}
		if len(timedOut) > 0 {
			details["timed_out_session_ids"] = timedOut
			details["wait_ms"] = sessionKillWait.Milliseconds()
		}
		return nil, toolErrorDetails(
			"SESSION_KILL_FAILED",
			"failed to terminate one or more session process trees",
			"runtime",
			details,
		)
	}
	if len(timedOut) > 0 {
		return nil, toolErrorDetails(
			"SESSION_KILL_TIMEOUT",
			"one or more sessions did not stop after kill request",
			"runtime",
			map[string]any{"session_ids": timedOut, "wait_ms": sessionKillWait.Milliseconds()},
		)
	}
	return Result{"sessions": items, "count": len(items)}, nil
}

func waitForSessionCompletion(s *session.Session, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-s.Done:
			return true
		default:
			return false
		}
	}
	select {
	case <-s.Done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func waitForSessionsCompletion(sessions []*session.Session, timeout time.Duration) ([]*session.Session, []string) {
	deadline := time.Now().Add(timeout)
	completed := make([]*session.Session, 0, len(sessions))
	timedOut := make([]string, 0)
	for _, s := range sessions {
		remaining := time.Until(deadline)
		if waitForSessionCompletion(s, remaining) {
			completed = append(completed, s)
			continue
		}
		timedOut = append(timedOut, s.ID)
	}
	return completed, timedOut
}

func (svc *Service) sessionStatus(request SessionObserveRequest) (Result, error) {
	return svc.sessionStatusContext(context.Background(), request)
}

func (svc *Service) sessionStatusContext(ctx context.Context, request SessionObserveRequest) (result Result, failure error) {
	started := time.Now()
	defer func() {
		if result == nil || failure != nil {
			return
		}
		result["waited_ms"] = time.Since(started).Milliseconds()
		result["changed"] = result["status"] != "running" || result["stdout"] != nil || result["stderr"] != nil
		if result["changed"] == false {
			delete(result, "observe_after_ms")
			delete(result, "next_observe_after_ms")
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, ok := svc.sessions.Get(request.SessionID)
	if !ok {
		return svc.persistedSessionResult(ctx, request.SessionID)
	}
	read := func() (Result, error) {
		if request.Offset == nil {
			if s.Completed() {
				return svc.completedSessionResult(s, commandOutputLimit(request.MaxOutputBytes), true), nil
			}
			return snapshotResult(s.Snapshot("running", commandOutputLimit(request.MaxOutputBytes))), nil
		}
		snapshot, err := s.SnapshotAt("running", commandOutputLimit(request.MaxOutputBytes), *request.Offset)
		if err != nil {
			return nil, toolErrorDetails("INVALID_OUTPUT_CURSOR", err.Error(), "validation", map[string]any{"session_id": request.SessionID})
		}
		value := snapshotResult(snapshot)
		if snapshot.Completed {
			if err := s.WaitError(); err != nil {
				value["command_error"] = err.Error()
			}
		}
		return value, nil
	}
	select {
	case <-s.Done:
		return read()
	default:
	}
	// Validate an explicit cursor before waiting; invalid requests never consume time or output.
	if request.Offset != nil {
		if _, err := s.SnapshotAt("running", 1, *request.Offset); err != nil {
			return nil, toolError("INVALID_OUTPUT_CURSOR", err.Error(), "validation")
		}
	}
	wait, err := sessionObserveWait(request.WaitMS, s.Summary().ElapsedMS)
	if err != nil {
		return nil, err
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-s.Done:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return read()
}

func publicCommandStatus(status string) string {
	switch status {
	case "killed":
		return "cancelled"
	case "unknown":
		return "outcome_unknown"
	default:
		return status
	}
}

func (svc *Service) storeReservedSession(s *session.Session) {
	svc.sessions.PruneCompletedBefore(time.Now().Add(-completedSessionRetention))
	svc.sessions.AddReserved(s)
	svc.sessions.PruneCompletedToLimit(maxRetainedCommandSessions)
}

func (svc *Service) listSessions() (Result, error) {
	// list 是只读观察入口，不能消费刚完成命令的最终输出。完成结果保留一小时，
	// status 可重复读取终态；过期或超过容量的旧结果在这里统一淘汰。
	svc.sessions.PruneCompletedBefore(time.Now().Add(-completedSessionRetention))
	items := make([]map[string]any, 0)
	for _, s := range svc.sessions.List() {
		summary := s.Summary()
		item := map[string]any{"session_id": summary.ID, "status": publicCommandStatus(summary.Status), "elapsed_ms": summary.ElapsedMS, "timed_out": summary.TimedOut}
		addBindingResult(item, summary.Binding)
		if summary.Runtime != "" {
			item["runtime"] = summary.Runtime
		}
		if summary.Distribution != "" {
			item["wsl_distribution"] = summary.Distribution
		}
		if summary.Workdir != "" {
			item["workdir"] = summary.Workdir
		}
		items = append(items, item)
	}
	return Result{"sessions": items, "count": len(items)}, nil
}

func (svc *Service) commandEnv(skillName string, extra map[string]string) ([]string, error) {
	return svc.commandEnvWithRuntime(skillName, extra, nil)
}

func (svc *Service) commandEnvWithRuntime(skillName string, extra, runtime map[string]string) ([]string, error) {
	env, err := svc.baseCommandEnv()
	if err != nil {
		return nil, err
	}
	overrides, err := svc.commandEnvOverrides(skillName, extra)
	if err != nil {
		return nil, err
	}
	for key, value := range overrides {
		setPlatformCommandEnv(env, key, value)
	}
	// 运行时保留变量最后写入，确保 Skill env、请求 env 和宿主映射都不能覆盖。
	for key, value := range runtime {
		setPlatformCommandEnv(env, key, value)
	}
	return formatCommandEnv(env), nil
}

func (svc *Service) internalCommandEnv(extra map[string]string) ([]string, error) {
	env, err := svc.baseCommandEnv()
	if err != nil {
		return nil, err
	}
	for key, value := range extra {
		setPlatformCommandEnv(env, key, value)
	}
	return formatCommandEnv(env), nil
}

func (svc *Service) baseCommandEnv() (map[string]string, error) {
	env := map[string]string{}
	keys := append([]string{"PATH", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR", "TEMP", "TMP"}, platformCommandEnvKeys()...)
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	repairPlatformCommandEnv(env)
	env["AGENTDOCK_HOME"] = svc.config().AgentDockHome
	env["AGENTDOCK_DEFAULT_DIR"] = svc.config().AgentDockDefaultDir
	hostHome := ""
	if resolvedHome, err := os.UserHomeDir(); err == nil && resolvedHome != "" {
		hostHome = resolvedHome
		env["HOME"] = resolvedHome
	}
	// macOS 的 launchd 默认只提供系统 PATH。命令工具需要补齐常见用户级可执行目录，
	// 否则 Homebrew、~/.local/bin 中已安装的 CLI 在桌面服务里会表现为“未安装”。
	if commandPath := platformCommandPath(env["PATH"], hostHome); commandPath != "" {
		env["PATH"] = commandPath
	}
	commandTempDir := filepath.Join(svc.config().AgentDockHome, "tmp")
	env["TMPDIR"] = commandTempDir
	configurePlatformCommandTempEnv(env, commandTempDir)
	if err := os.MkdirAll(commandTempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create command temp directory: %w", err)
	}
	svc.applyHostEnvMapping(env)
	return env, nil
}

// applyHostEnvMapping 只复制部署者显式声明的宿主变量；未配置的宿主环境继续保持隔离。
func (svc *Service) applyHostEnvMapping(env map[string]string) {
	for childKey, hostKey := range svc.config().CommandEnvFromEnv {
		if config.IsReservedCommandEnvironmentKey(childKey) {
			continue
		}
		if value, ok := os.LookupEnv(hostKey); ok {
			setPlatformCommandEnv(env, childKey, value)
		}
	}
}

func formatCommandEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}
