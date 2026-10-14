# AgentDock development rules

This repository is the user's extension fork. Keep changes on the current feature
branch. Never publish to the upstream author's repository.

## Before editing

1. Load `agentdock_context` for this working tree. Check Git status, the current
   task, and local rules. Read the complete files being changed.
2. Find the existing responsibility and tests. Make a bounded implementation plan
   before changing code. Do not create parallel runtimes, task systems or themes.
3. Preserve unrelated changes. Do not reset, stash or overwrite another agent's work.

## Implementation boundaries

- `Runtime.Call` / `callObserved` owns external tool admission and root call identity.
  Children inherit an immutable binding. Never infer a conversation from the latest
  task, selected window or account. Unattributed calls stay unattributed.
- Tool RPCs, internal spans and command processes have separate lifetimes. Count
  only roots in totals. Unknown timings and outcomes are nullable, never fake zero
  or success. Use monotonic durations inside a process.
- Persist redacted facts before presenting them. Keep parameters, diff previews,
  output, caches, queues and subscriptions bounded. Never recursively observe
  journal writes, UI queries, heartbeats or background probes as AI requests.
- Views render state; services own I/O and cancellation. Bind selected, checked and
  expanded states explicitly. Hover or keyboard focus is not business state.
- Theme colors, borders and sizing belong in shared resources. Do not set hardcoded
  brushes in event handlers. Keep default and focused control geometry identical.
- All I/O must have cancellation, errors and resource cleanup. Do not synchronously
  block the UI with `.Wait()` / `.Result`; use `async void` only for event handlers.
- Keep one canonical implementation. Name files and types by responsibility, not
  New, Final or V2. Put necessary old-data conversion at a documented boundary.
- Keep authentication, permission checks, installation rollback and configuration
  ownership intact. A display setting must never restart the running core.

## Verification and delivery

Review the changed code and the pull request once. For normal development work, do
not add or run broad regression suites, repeated stress loops, AI experiments,
emulator matrices, installation E2E or release probes. Keep focused existing tests
that directly exercise authentication, permissions, installer rollback, persistent
state and runtime contracts; avoid tests that mirror source strings or UI layout.

The default acceptance gate is `.github/workflows/ci.yml`: formatting plus production
compilation. Run additional tests only when the user explicitly requests them. Build
release packages only for an explicit release task, and report unexecuted checks
honestly.

## Release notes

Before creating or editing `docs/releases/v*.md` or a GitHub Release body, read
`docs/release-notes-policy.md`. Release notes are the complete user-facing delta
from the previous published version to the target version. A same-version rebuild
or asset replacement does not create a separate re-release section or date-based
changelog. Merge all user-visible fixes into their platform or feature sections,
give major new capabilities enough detail, keep the download chooser compact, and
keep CI/test results, run IDs and publication mechanics out of the Release body.

Implementation map: `docs/implementation-1.1.5.md`.
