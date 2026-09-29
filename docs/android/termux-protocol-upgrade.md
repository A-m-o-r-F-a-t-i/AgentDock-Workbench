# Termux callback protocol upgrade (PR-A)

Base: PR #28, `e89b95afdbf2f739d64bd4e84885bebc193d2716`.

## Fixed contract

- Official RUN_COMMAND `err=-1` means no internal error. Missing/zero codes do not imply success.
- Official stdout/stderr original lengths are decimal strings containing Java UTF-16 lengths. Integer values remain accepted for earlier fixtures/variants; malformed counts are rejected.
- The callback may receive an empty or populated start ACK before the final result. The fixed deployment bridge always returns request-bound JSON, so an empty ACK cannot settle an operation.
- PendingIntent uses an explicit non-exported result Service and a reconstructible request-specific identity. It remains usable until a real terminal receipt or timeout and is cancelled when the operation is settled or expired.
- Callback validation failures and truncation keep the business outcome `unknown`; the original operation must be queried. A valid bound receipt with a nonzero command exit remains a known failure.
- Both output streams share the UTF-8 callback budget. Raw error/output text is not copied into diagnostic messages.

## Evidence

Official contract: https://github.com/termux/termux-app/wiki/RUN_COMMAND-Intent
Official sender: `termux-shared/src/main/java/com/termux/shared/shell/command/result/ResultSender.java`.
Reference behavior reviewed in ExTV/rikkahub-agent `a88f5854b4d2e2706ea2ae8304a7d66a3f9f2bbc`; no reference source files were copied.

Regression tests cover return codes, missing/wrong field types, original length strings, UTF-8 bounds, ACK sequences and real Android PendingIntent delivery. Device installation and real Termux/Shizuku integration are separate acceptance steps, not claimed by JVM/emulator tests.

PR-B (host execution), PR-C (native Shizuku), and PR-D (device tools) remain separate dependent changes. This PR does not expose an arbitrary shell through the deployment interface.
