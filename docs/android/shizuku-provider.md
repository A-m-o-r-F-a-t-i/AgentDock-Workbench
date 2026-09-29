# Shizuku provider (PR-C)

This optional Android execution backend depends on PR-B. It does not start ADB, pair wireless debugging, install Shizuku, or change production settings.

## Connection

Install/start Shizuku independently, grant its native permission from Workbench, and run the system-channel probe. Shizuku server API 13+ is required for the context constructor and native-library path. The backend reports the actual UID (2000 for the wireless-debugging shell, 0 for an already authorized root service). It is disabled by default and never used as an automatic fallback for a failed Termux command. Android 11+ wireless startup remains in the Shizuku application; reboot requires starting that service again.

The APK acquires a protected Provider Binder and binds a versioned UserService with a stable tag. Per-binding generations reject late callbacks. A bounded two-thread Binder executor limits uninterruptible calls; a call timeout does not kill unrelated UserService jobs. A daemon UserService lets its admitted command deadlines survive APK process recreation. Commands are still bounded and its destroy handler requests cancellation and cleanup.

## Execution and limits

The existing Core owns calls, tasks, permissions, sessions and output cursors. Short AIDL controls start/observe an original operation. An operation is reserved before process launch; lost start responses are followed only by observation. The process registry remembers IDs, including compact tombstones after old terminal outputs expire. Missing or uncertain state never launches a replacement process.

Each command runs in a new native session/process group. The native layer retains its waitable leader until group cleanup and reaping, so cancellation cannot signal a reused leader PID. Pipe I/O is non-blocking; PTY mode creates a controlling terminal. Output and input acknowledgement are distinct. Deadlines run inside the UserService supervisor, independent of APK/Core polling. Descendants that deliberately escape the owned group are outside this finite-session containment and must not be described as automatically stopped.

Limits: four active Shizuku jobs, 128 retained records, 8192 seen IDs per service instance, 4 MiB per-job output and 16 MiB combined retained output. Overflow terminates the owned group and reports output_limited. Old confirmed terminal output may be reclaimed after one hour; unknown records are preserved. Output is transferred in 16 KiB binary-safe chunks. A killed service/kernel process can still leave an unknown outcome; neither APK nor Core invents a cancellation receipt.

## Build and validation

SDK API/provider 13.1.5, AIDL, stable R8 keep rules and a small NDK process adapter are included. Native libraries use 16 KiB page alignment and are extracted for the shell process classloader. Debug and release-shaped candidates remain test-signed only.

JVM tests cover protocol/identity bounds and stale-generation rejection. Instrumentation executes actual native processes in the emulator application sandbox: exit status, stream capture, controlling PTY, input acknowledgement, blocked stdin, cancellation isolation, deadlines and overflow. These tests do not replace real Shizuku authorization, a minified APK UserService bind, OPPO ARM64 operation, reboot or background-policy acceptance. No candidate is installed on the user's phone by this PR.

Reference behavior was independently implemented from the public Shizuku API and the reviewed RikkaHub Agent connection approach; RikkaHub implementation files were not copied.
