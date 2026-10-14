# AgentDock Workbench for Android

This directory contains the native Kotlin + Jetpack Compose Android Workbench. It is a controller and productized bridge for an **external** Termux installation and an AgentDock Core running in a verified Debian PRoot. It does not embed Termux, a root filesystem, Chromium, the Go Core, ADB, root, Shizuku, accessibility automation, or vendor-specific keepalive hacks.

The Android UI consumes the same server-authoritative Core HTTP/SSE contracts as the desktop Workbench. Installation and lifecycle requests are sent through Termux `RUN_COMMAND` to a fixed, user-installed bridge script. Existing nodes are discovery-only until the user explicitly adopts or changes them.

## Storage layout

- Android settings, credentials, pending bridge operations and transfer caches remain in the APK's private Android data directory. Export files are written only to a URI selected through Android's document picker.
- Core binaries, conversations, tasks, execution records, identities, logs, transactions, rollback data and runtime `scratch`/`cache` remain under Termux-private `~/.agentdock-workbench/node`.
- User projects live under `~/storage/shared/Documents/AgentDock/Workspaces` by default. A configured workspace must resolve below Termux shared storage so Android file managers can access it.
- User-facing build artifacts live under `~/storage/shared/Documents/AgentDock/Artifacts`. They are separated from project sources and survive private-runtime cleanup or uninstall.
- Shizuku commands receive per-session temporary directories below `/data/local/tmp/agentdock-workbench`; terminal sessions remove their directory and stale abandoned directories are reclaimed after 24 hours.

The APK reclaims only its own interrupted export and project-transfer cache files after 24 hours. The bridge rotates the Core log at 8 MiB, bounds retained diagnostics, reclaims terminal transaction records and private temporary files, and removes inactive private `scratch`/`cache` entries after seven days. Failed installation transactions are eligible for deletion only when they are older than seven days, remain before the version-switch phase, and still match an explicit cleanup preview.

The deployment page provides a verified migration from the legacy private workspace to shared storage. It copies project files into a staging directory, migrates legacy artifacts into the public artifact root, omits registered `scratch`/`cache`, compares tree digests, publishes only into empty destinations, and deletes the private source after successful verification. The Core workspace registry is migrated in place so historical tasks retain the same workspace ID. Full uninstall removes AgentDock-owned private runtime data, bridge files and only a PRoot installation marked as created by AgentDock; public workspaces, public artifacts, existing Termux packages and externally managed PRoot installations remain intact.

Builds, dependency resolution, lint, unit tests, emulator tests, screenshots and APK packaging for this lane run only in `.github/workflows/parallel-android.yml`.
