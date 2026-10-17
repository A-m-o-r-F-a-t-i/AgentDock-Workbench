using System.Net.Http;
using System.Runtime.InteropServices;

namespace AgentDock.ControlPanel;

// Local bearer-authenticated control traffic and anonymous public discovery have
// different routing requirements. Do not change the process-wide DefaultProxy.
internal static class RuntimeHttpClients
{
    private const uint WinHttpAccessTypeAutomaticProxy = 4;
    private const uint WinHttpFlagAsync = 0x10000000;
    private const int ErrorInvalidParameter = 87;
    private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(6);
    private static readonly TimeSpan LocalTimeout = TimeSpan.FromSeconds(10);
    private static readonly Lazy<bool> WinInetProxySessionCompatible = new(ProbeWinInetProxySessionCompatibility);

    internal static HttpClient CreateLocal() => new(CreateDirectHandler()) { Timeout = LocalTimeout };

    private static SocketsHttpHandler CreateDirectHandler() => new()
    {
        UseProxy = false,
        AllowAutoRedirect = false,
        UseCookies = false,
        ConnectTimeout = ConnectTimeout
    };

    internal static HttpMessageHandler CreatePublicHandler(Uri endpoint)
    {
        if (endpoint.IsLoopback) return CreateDirectHandler();
        if (HasEnvironmentProxy()) return CreateSystemProxyHandler();
        return CreateRemotePublicHandler(WinInetProxySessionCompatible.Value);
    }

    internal static HttpMessageHandler CreatePublicHandler(Uri endpoint, bool winInetProxySessionCompatible)
    {
        if (endpoint.IsLoopback) return CreateDirectHandler();

        // Explicit environment proxy configuration keeps the .NET behavior,
        // including NO_PROXY and scheme-specific proxy selection. Environment
        // variables are the process's launch configuration, not Windows settings.
        if (HasEnvironmentProxy()) return CreateSystemProxyHandler();

        return CreateRemotePublicHandler(winInetProxySessionCompatible);
    }

    private static HttpMessageHandler CreateRemotePublicHandler(bool winInetProxySessionCompatible)
    {
        // Some Windows builds reject WINHTTP_ACCESS_TYPE_AUTOMATIC_PROXY in
        // WinHttpOpen with ERROR_INVALID_PARAMETER before any request is sent.
        // Preserve the previous system-proxy transport as a compatibility path.
        if (!winInetProxySessionCompatible) return CreateSystemProxyHandler();

        // .NET 8 caches the Windows proxy in HttpClient.DefaultProxy, including
        // across new SocketsHttpHandler instances. A fresh native session reads
        // current user settings (manual, PAC and auto-detect) for each probe.
        // The discovery operation retains its existing 24-second overall deadline.
        return new WinHttpHandler
        {
            WindowsProxyUsePolicy = WindowsProxyUsePolicy.UseWinInetProxy,
            AutomaticRedirection = false,
            CookieUsePolicy = CookieUsePolicy.IgnoreCookies,
            SendTimeout = ConnectTimeout,
            ReceiveHeadersTimeout = ConnectTimeout,
            ReceiveDataTimeout = ConnectTimeout
        };
    }

    private static SocketsHttpHandler CreateSystemProxyHandler() => new()
    {
        AllowAutoRedirect = false,
        UseCookies = false,
        ConnectTimeout = ConnectTimeout
    };

    private static bool ProbeWinInetProxySessionCompatibility()
    {
        var session = WinHttpOpen("AgentDock Control Panel", WinHttpAccessTypeAutomaticProxy,
            IntPtr.Zero, IntPtr.Zero, WinHttpFlagAsync);
        if (session != IntPtr.Zero)
        {
            WinHttpCloseHandle(session);
            return true;
        }

        // Only the stable platform incompatibility has a fallback. Other native
        // failures remain on the WinHTTP path so their diagnostics are preserved.
        return Marshal.GetLastWin32Error() != ErrorInvalidParameter;
    }

    private static bool HasEnvironmentProxy() =>
        new[] { "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY" }.Any(name =>
            !string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable(name)));

    [DllImport("winhttp.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr WinHttpOpen(string userAgent, uint accessType, IntPtr proxyName,
        IntPtr proxyBypass, uint flags);

    [DllImport("winhttp.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool WinHttpCloseHandle(IntPtr handle);
}
