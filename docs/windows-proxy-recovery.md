# Windows proxy changes and local control traffic

The Windows control panel targets .NET 8. Public discovery previously created a
new `SocketsHttpHandler` but still inherited the process-wide `HttpClient.DefaultProxy`.
A cached local proxy address can outlive its listener or a port change. Restarting
Tray rebuilds that state; it does not establish that Core was restarted.

## Routing

- Local health and runtime API traffic is explicitly direct, with redirects and
  cookies disabled. An unavailable desktop proxy cannot intercept local bearer
  credentials or disable local management requests.
- Public discovery uses a fresh WinHTTP user-proxy session for each check when
  no explicit proxy environment variables exist. Windows manual proxy, bypass,
  PAC and auto-detect settings remain owned by Windows.
- Explicit HTTP_PROXY, HTTPS_PROXY, ALL_PROXY and NO_PROXY launch configuration
  retains .NET routing. No process-wide default proxy or system settings are
  modified by the application. There is no silent direct-network fallback.
- Public requests stay anonymous, do not follow redirects, and retain the
  existing 24-second cancellation deadline and 256 KiB metadata limit. Failed
  writes or tool calls are never replayed by this change.

## Diagnostic scope

An inbound MCP connection, `/healthz`, anonymous OAuth discovery and an actual
`tools/call` test different paths. The screenshot's refused loopback proxy is a
local outbound-probe failure. It alone does not establish that all inbound tool
calls failed. Correlate request IDs and received/finished/response-write events
before attributing a missing tool result to Core or restarting it.

The incident inspection found the old proxy port closed, a different system
proxy port listening, and a Tray start immediately after the screenshot while
Core's process had continued running for two days. The inspected time window
also contained successful MCP tool completions and normal anonymous challenges;
no tool cancellation, response-write error or handler panic was shown in that
window. Do not relabel expected anonymous 401/405 checks as tool failures.

## Verification

The network regression executable runs only with GITHUB_ACTIONS=true and
AGENTDOCK_PROXY_ACCEPTANCE=1. It uses loopback HTTP fixtures, temporary runtime
files and the disposable runner's proxy settings, restoring proxy settings and
process environment in finally/disposal. Never run it against a user's desktop.

The tests cover a cached dead proxy, switching live proxy ports without process
restart, disabling the proxy, local API isolation, redirects, authentication
metadata validation, cancellation and explicit environment routing. Fixture HTTP
is used to exercise proxy selection without installing test trust roots; the
production public-address validator continues requiring HTTPS for remote hosts.
