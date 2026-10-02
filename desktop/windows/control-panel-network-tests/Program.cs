using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using AgentDock.ControlPanel;

internal static class Program
{
    private static int _assertions;
    private static readonly List<string> Completed = [];
    private const string ProxyOrigin = "http://agentdock-proxy-fixture.invalid";
    private static void Check(bool condition, string message)
    {
        Interlocked.Increment(ref _assertions);
        if (!condition) throw new InvalidOperationException(message);
    }

    private static async Task<int> Main(string[] args)
    {
        if (Environment.GetEnvironmentVariable("GITHUB_ACTIONS") != "true" ||
            Environment.GetEnvironmentVariable("AGENTDOCK_PROXY_ACCEPTANCE") != "1")
        {
            Console.Error.WriteLine("Network acceptance requires an explicitly enabled disposable GitHub runner.");
            return 2;
        }
        if (args.Length == 2 && args[0] == "--environment-child")
        {
            using var client = new HttpClient(RuntimeHttpClients.CreatePublicHandler(new Uri(args[1])));
            var result = await RuntimeService.CheckPublicDiscoveryAsync(client, args[1], CancellationToken.None);
            Check(result.Success, "Fresh process environment routing: " + result.Message);
            return 0;
        }

        var root = Path.Combine(Environment.GetEnvironmentVariable("RUNNER_TEMP") ?? throw new IOException("Missing CI temp"),
            "agentdock-network-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var variables = new[] { "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY" };
        var savedEnvironment = variables.ToDictionary(name => name, Environment.GetEnvironmentVariable);
        var savedProxy = HttpClient.DefaultProxy;
        using var settings = new NativeProxySettings();
        try
        {
            foreach (var name in variables) Environment.SetEnvironmentVariable(name, null);
            await using var proxyA = new HttpFixture(request => Discovery(request, ProxyOrigin));
            await using var proxyB = new HttpFixture(request => Discovery(request, ProxyOrigin));
            var reservation = new TcpListener(IPAddress.Loopback, 0);
            reservation.Start();
            var dead = "127.0.0.1:" + ((IPEndPoint)reservation.LocalEndpoint).Port;
            reservation.Stop();
            settings.SetManual(dead);
            var staleProxy = new WebProxy("http://" + dead, false);
            HttpClient.DefaultProxy = staleProxy;

            // This is the exact pre-fix public handler configuration, with a stale
            // process proxy. A new HttpClient/handler alone cannot recover it.
            using (var legacy = new HttpClient(new SocketsHttpHandler
                { AllowAutoRedirect = false, ConnectTimeout = TimeSpan.FromSeconds(6) }))
            {
                var failed = await RuntimeService.CheckPublicDiscoveryAsync(legacy, ProxyOrigin, CancellationToken.None);
                Check(!failed.Success && failed.StatusCode is null, "Legacy stale proxy must fail before reaching HTTP.");
                settings.SetManual(proxyA.Address);
                var stillFailed = await RuntimeService.CheckPublicDiscoveryAsync(legacy, ProxyOrigin, CancellationToken.None);
                Check(!stillFailed.Success && proxyA.Count == 0, "Legacy proxy remains stale after Windows setting changes.");
            }
            Completed.Add("legacy-stale-proxy-reproduced");

            await Probe(ProxyOrigin);
            Check(proxyA.Count == 4 && proxyB.Count == 0, "Current Windows proxy A receives full anonymous discovery.");
            settings.SetManual(proxyB.Address);
            await Probe(ProxyOrigin);
            Check(proxyA.Count == 4 && proxyB.Count == 4, "Next probe switches to proxy B without process restart.");
            await Task.WhenAll(Probe(ProxyOrigin), Probe(ProxyOrigin));
            Check(proxyB.Count == 12, "Concurrent probes own independent native sessions.");
            Check(ReferenceEquals(HttpClient.DefaultProxy, staleProxy), "Application must not mutate global DefaultProxy.");
            Check(proxyA.Requests.Concat(proxyB.Requests).All(request => request.Method == "GET" &&
                !request.Headers.ContainsKey("Authorization") && !request.Headers.ContainsKey("Cookie")),
                "Public discovery neither sends local credentials nor carries response cookies.");
            Completed.Add("native-proxy-switch-and-concurrent-discovery");

            string directOrigin = "";
            await using var direct = new HttpFixture(request => Discovery(request, directOrigin), anyAddress: true);
            var ipv4 = Dns.GetHostAddresses(Dns.GetHostName()).First(address =>
                address.AddressFamily == AddressFamily.InterNetwork && !IPAddress.IsLoopback(address));
            directOrigin = $"http://{ipv4}:{direct.Port}";
            settings.SetManual(null);
            await Probe(directOrigin);
            Check(direct.Count == 4 && proxyB.Count == 12, "Disabling Windows proxy restores direct transport without restart.");
            Completed.Add("native-proxy-disable-recovery");

            await RunEnvironmentChild(ProxyOrigin, proxyA.Address, "");
            Check(proxyA.Count == 8, "Explicit launch environment proxy is honored.");
            await RunEnvironmentChild(directOrigin, proxyA.Address, ipv4.ToString());
            Check(direct.Count == 8 && proxyA.Count == 8, "NO_PROXY is honored by environment routing.");
            Completed.Add("environment-proxy-and-no-proxy");

            settings.SetManual(dead);
            await LocalApiTests(root);
            await PublicValidationTests(root);
            settings.Dispose(); // Verify restoration before reporting successful acceptance.
            Console.WriteLine(JsonSerializer.Serialize(new { assertions = _assertions, completed = Completed,
                production_runtime_started = false, system_proxy_restored_on_exit = true }));
            return 0;
        }
        catch (Exception error)
        {
            Console.Error.WriteLine(error);
            return 1;
        }
        finally
        {
            HttpClient.DefaultProxy = savedProxy;
            foreach (var (name, value) in savedEnvironment) Environment.SetEnvironmentVariable(name, value);
            if (Directory.Exists(root)) Directory.Delete(root, true);
        }
    }

    private static async Task Probe(string origin)
    {
        using var handler = RuntimeHttpClients.CreatePublicHandler(new Uri(origin));
        Check(handler is WinHttpHandler { AutomaticRedirection: false, WindowsProxyUsePolicy: WindowsProxyUsePolicy.UseWinInetProxy },
            "Remote discovery reads current Windows user proxy through a fresh native handler.");
        using var client = new HttpClient(handler);
        var result = await RuntimeService.CheckPublicDiscoveryAsync(client, origin, CancellationToken.None);
        Check(result.Success, "Public discovery succeeds: " + result.Message);
    }

    private static async Task LocalApiTests(string root)
    {
        await using var recipient = new HttpFixture(_ => new(200));
        await using var local = new HttpFixture(_ => new(200));
        File.WriteAllText(Path.Combine(root, "control-panel-settings.json"), JsonSerializer.Serialize(new { port = local.Port }));
        const string bearer = "network-fixture-only";
        File.WriteAllText(Path.Combine(root, "auth-token.dpapi"), Convert.ToBase64String(ProtectedData.Protect(
            Encoding.UTF8.GetBytes(bearer), Encoding.UTF8.GetBytes("agentdock.startup.v1"), DataProtectionScope.CurrentUser)));
        using var runtime = new RuntimeService(root);
        await runtime.SetMcpEnabledAsync("fixture", true);
        await runtime.SetMcpEnabledAsync("fixture", false);
        Check(local.Count == 2 && local.Requests.All(request => request.Method == "POST" &&
            request.Headers.GetValueOrDefault("Authorization") == "Bearer " + bearer), "Local API bypasses dead proxy and retains local authentication.");
        local.Respond = _ => new(302, "{}", $"Location: {recipient.Origin}/must-not-receive\r\n");
        await ExpectApiFailure(runtime);
        Check(local.Count == 3 && recipient.Count == 0, "Local mutation is not redirected or replayed.");
        local.Respond = _ => new(503);
        await ExpectApiFailure(runtime);
        Check(local.Count == 4, "Failed local mutation is sent exactly once.");
        local.Respond = _ => new(200);
        await runtime.SetMcpEnabledAsync("fixture", true);
        Check(local.Count == 5, "Same local client recovers on the next explicit request.");
        Completed.Add("local-api-isolation-redirect-and-no-write-replay");
    }

    private static async Task ExpectApiFailure(RuntimeService runtime)
    {
        try { await runtime.SetMcpEnabledAsync("fixture", true); }
        catch (InvalidOperationException) { return; }
        throw new InvalidOperationException("Expected local API failure.");
    }

    private static async Task PublicValidationTests(string root)
    {
        string origin = "";
        await using var local = new HttpFixture(request => Discovery(request, origin));
        origin = local.Origin;
        using var runtime = new RuntimeService(root);
        var success = await runtime.TestUrlAsync(origin);
        Check(success.Success && local.Count == 4, "Actual TestUrlAsync wiring bypasses proxy for loopback.");
        Check(local.Requests.All(request => !request.Headers.ContainsKey("Authorization")), "Public check does not borrow runtime bearer.");
        var invalid = await runtime.TestUrlAsync(ProxyOrigin);
        Check(!invalid.Success, "Public entrypoint still rejects remote plaintext HTTP.");
        await using var recipient = new HttpFixture(_ => new(200));
        local.Respond = _ => new(302, "{}", $"Location: {recipient.Origin}/must-not-receive\r\n");
        var redirected = await runtime.TestUrlAsync(origin);
        Check(!redirected.Success && redirected.StatusCode == 302 && recipient.Count == 0, "Public redirect is rejected, not followed.");
        var before = local.Count;
        local.Respond = request => PathOf(request) == "/mcp"
            ? new(401, "{}", "WWW-Authenticate: Bearer resource_metadata=\"https://wrong.invalid/metadata\"\r\n")
            : Discovery(request, origin);
        var mismatch = await runtime.TestUrlAsync(origin);
        Check(!mismatch.Success && local.Count == before + 2, "Origin mismatch stops discovery without fallback/retry.");
        before = local.Count;
        var started = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        local.Respond = _ => { started.TrySetResult(); return new(200, "{}", DelayMilliseconds: 2000); };
        using var cancel = new CancellationTokenSource();
        var pending = runtime.TestUrlAsync(origin, cancel.Token);
        await started.Task.WaitAsync(TimeSpan.FromSeconds(5));
        cancel.Cancel();
        var cancelled = await pending.WaitAsync(TimeSpan.FromSeconds(2));
        Check(!cancelled.Success && local.Count == before + 1, "Cancellation terminates in-flight discovery without replay.");
        local.Respond = request => Discovery(request, origin);
        Check((await runtime.TestUrlAsync(origin)).Success, "Discovery recovers after cancellation without recreating RuntimeService.");
        Completed.Add("public-address-auth-redirect-cancellation-contract");
    }

    private static string PathOf(HttpFixture.Request request) =>
        Uri.TryCreate(request.Target, UriKind.Absolute, out var uri) ? uri.AbsolutePath : request.Target;

    private static HttpFixture.Response Discovery(HttpFixture.Request request, string origin) => PathOf(request) switch
    {
        "/healthz" => new(200, "{\"ok\":true,\"version\":\"fixture\"}", "Set-Cookie: fixture=not-forwarded\r\n"),
        "/mcp" => new(401, "{}", $"WWW-Authenticate: Bearer resource_metadata=\"{origin}/.well-known/oauth-protected-resource/mcp\"\r\n"),
        "/.well-known/oauth-protected-resource/mcp" => new(200, JsonSerializer.Serialize(new
            { resource = origin + "/mcp", authorization_servers = new[] { origin } })),
        "/.well-known/oauth-authorization-server" => new(200, JsonSerializer.Serialize(new
        {
            issuer = origin, authorization_endpoint = origin + "/oauth/authorize", token_endpoint = origin + "/oauth/token",
            registration_endpoint = origin + "/register", code_challenge_methods_supported = new[] { "S256" },
            grant_types_supported = new[] { "authorization_code", "refresh_token" }
        })),
        _ => new(404)
    };

    private static async Task RunEnvironmentChild(string origin, string proxy, string bypass)
    {
        var executable = Environment.ProcessPath ?? throw new IOException("Missing test executable.");
        var info = new ProcessStartInfo(executable) { UseShellExecute = false, RedirectStandardOutput = true, RedirectStandardError = true };
        if (Path.GetFileNameWithoutExtension(executable).Equals("dotnet", StringComparison.OrdinalIgnoreCase))
            info.ArgumentList.Add(Assembly.GetExecutingAssembly().Location);
        info.ArgumentList.Add("--environment-child");
        info.ArgumentList.Add(origin);
        foreach (var key in new[] { "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY" }) info.Environment[key] = "http://" + proxy;
        info.Environment["NO_PROXY"] = bypass;
        using var process = Process.Start(info) ?? throw new IOException("Could not start isolated environment test.");
        var output = process.StandardOutput.ReadToEndAsync();
        var error = process.StandardError.ReadToEndAsync();
        try
        {
            await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(30));
            Check(process.ExitCode == 0, "Environment child failed: " + await error + await output);
        }
        finally
        {
            if (!process.HasExited) { process.Kill(entireProcessTree: true); await process.WaitForExitAsync(); }
        }
    }
}
