using System.Net.Http;

namespace AgentDock.ControlPanel;

// Local bearer-authenticated control traffic and anonymous public discovery have
// different routing requirements. Do not change the process-wide DefaultProxy.
internal static class RuntimeHttpClients
{
    private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(6);
    private static readonly TimeSpan LocalTimeout = TimeSpan.FromSeconds(10);

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

        // Explicit environment proxy configuration keeps the .NET behavior,
        // including NO_PROXY and scheme-specific proxy selection. Environment
        // variables are the process's launch configuration, not Windows settings.
        if (HasEnvironmentProxy())
            return new SocketsHttpHandler
            {
                AllowAutoRedirect = false,
                UseCookies = false,
                ConnectTimeout = ConnectTimeout
            };

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

    private static bool HasEnvironmentProxy() =>
        new[] { "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY" }.Any(name =>
            !string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable(name)));
}
