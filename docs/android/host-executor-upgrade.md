# Phone host executor (PR-B)

Depends on PR-A `fix/android-termux-protocol`. This change does not install software on the user device.

## Enablement

Export the updated four-file Termux bridge bundle, install its modules through the explicit bootstrap, grant RUN_COMMAND, and run the end-to-end host probe. Pair the local Core through the existing Workbench flow. The connection page starts a visible executor only with notification permission. It never starts or repairs Core itself.

Default execution is unchanged. Select `backend=termux_host` explicitly with an absolute Termux working directory. Do not combine mobile backends with host runtime, Skill, target_kind or external_path selectors.

## Contracts

- Core owns all calls, tasks, permissions and sessions.
- The restricted lease only exchanges results and disconnects; it cannot submit tools or call management APIs.
- Registration requires authenticated direct loopback traffic. Forwarded and public-host requests are rejected.
- A start is offered once. Missing delivery is preserved as unknown, never replayed.
- Stream offsets, response revisions and acknowledged input sequences deduplicate retries.
- Private records bind original IDs and request fingerprints. Existing IDs never launch a second process.
- Owned supervisors control stream draining, PTY, cancellation and independent deadlines. Blocked stdin cannot disable deadline handling.
- Provider credentials are not passed through RUN_COMMAND or child environments.
- Missing providers never imply successful cancellation or exit zero.

## Validation and limits

Actions checks affected Go packages on Linux, Windows and macOS, Linux race tests, and isolated supervisor regressions. Android retains JVM, Lint, both APK shapes and API 26/33/34/35/37 emulator gates. These checks do not replace real Termux, Shizuku or OPPO background acceptance.

The exchange batch is four commands. Termux retains at most 4 MiB combined output; overflow terminates the owned process with explicit limited-output status. Unknown records are not removed to create capacity. Core restart requires explicit reconnect and never reissues old uncertain commands.
