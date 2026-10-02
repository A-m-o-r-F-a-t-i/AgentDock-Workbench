# Android structured device tools (PR-D)

This stage adds `android_device_read` and `android_device_act` to the existing Core tool registry. Both use the verified `android_shizuku` backend and the original Core Call/Session lifecycle. The RPC returning a session does not complete the call; process receipts do. Default PRoot and explicit `termux_host` commands retain their prior behavior.

## Available operations

| Tool | Actions | Output / effect |
| --- | --- | --- |
| android_device_read | status | Local broker metadata only; no device process or reauthorization. |
| android_device_read | packages, package_info, resolve_activity | Installed packages, package detail and a resolvable launcher component. |
| android_device_read | foreground, display, properties | Window/display/property state; acquire current primary-display geometry before coordinate input. |
| android_device_read | settings_get, appops_get, logcat | Specific settings/appops and bounded recent logs. |
| android_device_read | screenshot, ui_dump | A unique capture in an explicitly selected Android shared directory. |
| android_device_act | app_start, app_stop | Start the exact component or force-stop the named package. |
| android_device_act | package_install, component_enable, component_disable | Install an existing APK through sized stdin, or change the exact component. |
| android_device_act | permission_grant, permission_revoke | Change the explicit runtime permission where Android permits it. |
| android_device_act | settings_put, appops_set | Record before/after values; settings_put verifies exact readback. |
| android_device_act | tap, swipe, keyevent, text | Primary-display input. text supports printable ASCII only and rejects unsupported text instead of silently losing characters. |

Each action has its own closed parameter schema; irrelevant fields, backend overrides, shell text, invalid packages/components, control characters and traversal paths are rejected. User strings remain literal shell arguments. These generated commands are admitted once by Core, not recursively dispatched as a second tool invocation. Their activity records retain the original structured tool name. Settings values and text are redacted from command audit previews.

## Captures and verification

`capture_dir` is mandatory for captures and must be an explicit absolute shared-storage directory such as `/storage/emulated/0/Termux/AgentDock/Captures`. Output names are derived from the original Call ID, existing targets are refused, and screenshot completion checks the PNG signature. No screenshot binary is placed in an oversized Binder JSON message. When Core can access that same shared directory, use the existing image/file tools after the original command has completed. Different PRoot mount mappings require an explicit accessible path; the APK does not infer host paths.

A running command reports artifact_state=pending, never ready. A failed or uncertain capture reports unconfirmed, even when a partial file may exist. Permission profiles evaluate capture as a filesystem write outside the workspace. Non-capture system reads remain reads outside the workspace; act operations are not treated as confined arbitrary shell.

`verification` describes confirmed checks only after command_ok=true. Settings are compared exactly after writing. appops before/after output is preserved for inspection. Other Android utilities expose their exit status and output; command_result_only is not an independent observation that every requested application effect occurred. For app lifecycle, UI input and package changes, follow with the corresponding read operation. Protected screens and OEM restrictions surface as actual errors or unconfirmed results; they are not bypassed.

## Diagnostics and recovery

The connection page can copy an allowlisted report containing build identity, enabled/connected state and the two backend states/UIDs. It does not export credentials, raw commands, environment, arbitrary error text or the executor lease. The report does not run a device probe or modify any connection.

Uncertain commands are observed through their original session ID. Do not repeat package installation, input or settings changes just because an HTTP/Binder callback timed out. To roll back the feature, stop the executor, confirm outstanding cancellations where possible and disable the Shizuku backend. Existing Core state, credentials and projects remain intact.

## Verification evidence

CI includes closed-schema coverage for every action, literal argument handling, read/write separation, capture path checks, original root/ToolName retention, cross-conversation session denial, read-only profile capture denial and cancellation receipt integration. Native process and Binder state tests belong to PR-C; Termux bridge tests belong to PR-A/B. Final exact-head results live in the stacked PR checks.

Real Shizuku authorization, a minified APK service bind, OPPO ARM64 system effects, OEM background policy, reboot recovery and production installation remain separate acceptance tasks. This PR changes no installed phone runtime and performs no device actions.
