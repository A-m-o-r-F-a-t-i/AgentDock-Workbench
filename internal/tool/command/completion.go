package command

import (
	"fmt"
	"time"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func (svc *Service) registerTerminalFuture(command *session.Session) session.TerminalFuture {
	future := command.TerminalFuture()
	svc.terminalMu.Lock()
	svc.terminals[command.ID] = future
	svc.terminalMu.Unlock()
	return future
}

func commandTerminalOutcome(snapshot session.Snapshot) session.TerminalOutcome {
	completedAt := snapshot.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	outcome := session.TerminalOutcome{
		SessionID:   snapshot.SessionID,
		TimedOut:    snapshot.TimedOut,
		CompletedAt: completedAt,
	}
	if snapshot.OutcomeUnknown {
		outcome.Status = "unknown"
		outcome.Summary = "Command outcome could not be confirmed. Verify the original operation before retrying."
		return outcome
	}

	exitCode, commandOK := snapshot.ExitCode, snapshot.CommandOK
	outcome.ExitCode, outcome.CommandOK = &exitCode, &commandOK
	switch {
	case snapshot.CommandOK:
		outcome.Status = "succeeded"
		outcome.Summary = "Command completed successfully."
	case snapshot.Status == "killed":
		outcome.Status = "cancelled"
		outcome.Summary = "Command was stopped."
	case snapshot.TimedOut || snapshot.Status == "timeout":
		outcome.Status = "failed"
		outcome.Summary = "Command timed out."
	default:
		outcome.Status = "failed"
		outcome.Summary = fmt.Sprintf("Command exited with code %d.", snapshot.ExitCode)
	}
	return outcome
}

func unconfirmedCommandTerminal(sessionID, summary string) session.TerminalOutcome {
	if summary == "" {
		summary = "Command outcome could not be confirmed. Verify the original operation before retrying."
	}
	return session.TerminalOutcome{
		SessionID:   sessionID,
		Status:      "unknown",
		Summary:     summary,
		CompletedAt: time.Now(),
	}
}
