using System.IO;
using System.Net.Http;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace AgentDock.ControlPanel;

internal static class GoalJson
{
    internal static JsonElement Value(this JsonElement value, string name) =>
        value.ValueKind == JsonValueKind.Object && value.TryGetProperty(name, out var found) ? found : default;
    internal static string String(this JsonElement value, string name) => value.Value(name).ValueKind == JsonValueKind.String ? value.Value(name).GetString()! : "";
    internal static bool Boolean(this JsonElement value, string name) => value.Value(name).ValueKind == JsonValueKind.True;
    internal static JsonElement[] Items(this JsonElement value, string name) => value.Value(name).ValueKind == JsonValueKind.Array ? value.Value(name).EnumerateArray().ToArray() : [];
}

internal sealed class GoalCdp : IDisposable
{
    private readonly ClientWebSocket _socket = new();
    private int _sequence;
    internal string BrowserSession { get; private set; } = "";

    internal static Uri Endpoint(string endpoint)
    {
        if (!Uri.TryCreate(endpoint, UriKind.Absolute, out var uri) || uri.Scheme != "http" ||
            uri.Host != "127.0.0.1" || uri.Port <= 0 || uri.UserInfo.Length != 0 || uri.AbsolutePath != "/" ||
            uri.Query.Length != 0 || uri.Fragment.Length != 0)
            throw new ArgumentException("浏览器端点必须是本机 http://127.0.0.1:端口，不允许远程连接。");
        return uri;
    }

    internal async Task ConnectAsync(string endpoint, string expectedSession, CancellationToken ct)
    {
        var origin = Endpoint(endpoint);
        using var http = new HttpClient(new SocketsHttpHandler { UseProxy = false, AllowAutoRedirect = false, ConnectTimeout = TimeSpan.FromSeconds(3) });
        using var response = await http.GetAsync(new Uri(origin, "/json/version"), HttpCompletionOption.ResponseHeadersRead, ct);
        response.EnsureSuccessStatusCode();
        await using var body = await response.Content.ReadAsStreamAsync(ct);
        var bytes = new byte[65537]; var length = 0;
        while (length < bytes.Length)
        {
            var count = await body.ReadAsync(bytes.AsMemory(length), ct);
            if (count == 0) break;
            length += count;
        }
        if (length > 65536) throw new IOException("浏览器版本响应超过限制。");
        using var document = JsonDocument.Parse(bytes.AsMemory(0, length));
        BrowserSession = document.RootElement.String("webSocketDebuggerUrl");
        if (!Uri.TryCreate(BrowserSession, UriKind.Absolute, out var socketUri) || socketUri.Scheme != "ws" ||
            socketUri.Host != origin.Host || socketUri.Port != origin.Port || socketUri.UserInfo.Length != 0 ||
            !Regex.IsMatch(socketUri.AbsolutePath, @"^/devtools/browser/[A-Za-z0-9-]+$") || socketUri.Query.Length != 0)
            throw new IOException("浏览器未提供有效的本机调试会话。");
        if (expectedSession.Length > 0 && expectedSession != BrowserSession)
            throw new IOException("浏览器会话已重启，请重新手动绑定。");
        _socket.Options.Proxy = null;
        await _socket.ConnectAsync(socketUri, ct);
    }

    // The owner serializes requests. Events are ignored; only the exact request ID is accepted.
    internal async Task<JsonElement> CallAsync(string method, object parameters, CancellationToken ct, string sessionId = "")
    {
        var id = ++_sequence;
        var message = new Dictionary<string, object> { ["id"] = id, ["method"] = method, ["params"] = parameters };
        if (sessionId.Length > 0) message["sessionId"] = sessionId;
        await _socket.SendAsync(JsonSerializer.SerializeToUtf8Bytes(message).AsMemory(), WebSocketMessageType.Text, true, ct);
        var buffer = new byte[16384];
        for (var events = 0; events < 1000; events++)
        {
            using var output = new MemoryStream();
            ValueWebSocketReceiveResult received;
            do
            {
                received = await _socket.ReceiveAsync(buffer.AsMemory(), ct);
                if (received.MessageType == WebSocketMessageType.Close) throw new IOException("浏览器调试连接已关闭。");
                if (output.Length + received.Count > 512 * 1024) throw new IOException("浏览器响应超过限制。");
                output.Write(buffer, 0, received.Count);
            } while (!received.EndOfMessage);
            using var document = JsonDocument.Parse(output.ToArray());
            var root = document.RootElement;
            if (root.Value("id").ValueKind != JsonValueKind.Number || root.Value("id").GetInt32() != id) continue;
            if (root.Value("error").ValueKind == JsonValueKind.Object) throw new IOException("浏览器命令失败：" + root.Value("error").String("message"));
            return root.Value("result").Clone();
        }
        throw new IOException("浏览器事件超过单次处理限制。");
    }

    public void Dispose() => _socket.Dispose();
}

internal sealed class GoalBrowser : IGoalBrowser
{
    private GoalCdp? _cdp;
    private bool _recovered;
    private bool _disposed;

    internal static string ConversationUrl(string url)
    {
        if (!Uri.TryCreate(url, UriKind.Absolute, out var uri) || uri.Scheme != "https" || uri.Host != "chatgpt.com" ||
            !uri.IsDefaultPort || uri.UserInfo.Length != 0 ||
            !Regex.IsMatch(uri.AbsolutePath, @"^/(?:g/[A-Za-z0-9_-]+/)?c/[0-9a-fA-F-]{36}$"))
            throw new ArgumentException("请选择已有 ChatGPT 对话，首页或其他站点不能绑定。");
        return uri.GetLeftPart(UriPartial.Path);
    }

    internal static async Task<GoalBrowserTab[]> ListAsync(string endpoint, CancellationToken ct)
    {
        using var cdp = new GoalCdp();
        await cdp.ConnectAsync(endpoint, "", ct);
        var result = await cdp.CallAsync("Target.getTargets", new { }, ct);
        var tabs = new List<GoalBrowserTab>();
        foreach (var target in result.Items("targetInfos").Where(t => t.String("type") == "page").Take(200))
        {
            try { tabs.Add(Tab(endpoint, cdp.BrowserSession, target)); }
            catch (ArgumentException) { }
        }
        return tabs.ToArray();
    }

    private static GoalBrowserTab Tab(string endpoint, string browserSession, JsonElement target) => new(
        endpoint, browserSession, target.String("targetId"), target.String("browserContextId"),
        ConversationUrl(target.String("url")), target.String("title"));

    private async Task<GoalCdp> ConnectionAsync(GoalBrowserTab tab, CancellationToken ct)
    {
        if (_disposed) throw new IOException("目标模式浏览器连接已关闭。");
        if (_cdp is null)
        {
            _cdp = new GoalCdp();
            await _cdp.ConnectAsync(tab.Endpoint, tab.BrowserSession, ct);
        }
        return _cdp;
    }

    public async Task<(GoalBrowserTab Tab, GoalPage Page)> ObserveAsync(GoalBrowserTab tab, bool recover, CancellationToken ct)
    {
        var cdp = await ConnectionAsync(tab, ct);
        var targets = (await cdp.CallAsync("Target.getTargets", new { }, ct)).Items("targetInfos");
        var found = targets.FirstOrDefault(t => t.String("targetId") == tab.TargetId);
        if (found.ValueKind == JsonValueKind.Undefined)
        {
            if (!recover || _recovered) throw new IOException("绑定页面已关闭，请重新绑定或允许隐藏页面接续。");
            var parameters = new Dictionary<string, object> { ["url"] = tab.Url, ["background"] = true, ["hidden"] = true };
            if (tab.BrowserContextId.Length > 0) parameters["browserContextId"] = tab.BrowserContextId;
            var created = await cdp.CallAsync("Target.createTarget", parameters, ct);
            tab = tab with { TargetId = created.String("targetId") };
            _recovered = true;
            // Keep this CDP connection alive: hidden target lifetime belongs to this session.
            for (var attempt = 0; attempt < 30; attempt++)
            {
                await Task.Delay(300, ct);
                var page = await EvaluateAsync(tab, "observe", "", "", ct);
                if (page.State is "ready" or "draft" or "busy") return (tab, page);
            }
        }
        else if (found.String("browserContextId") != tab.BrowserContextId ||
            !string.Equals(found.String("url").Split('?', '#')[0], tab.Url, StringComparison.Ordinal))
            return (tab, new("wrong_page"));
        return (tab, await EvaluateAsync(tab, "observe", "", "", ct));
    }

    private async Task<GoalPage> EvaluateAsync(GoalBrowserTab tab, string action, string key, string nonce, CancellationToken ct)
    {
        var cdp = await ConnectionAsync(tab, ct);
        var attached = await cdp.CallAsync("Target.attachToTarget", new { targetId = tab.TargetId, flatten = true }, ct);
        var session = attached.String("sessionId");
        try
        {
            var result = await cdp.CallAsync("Runtime.evaluate", new {
                expression = GoalPageScript.Build(action, tab.Url, key, nonce), returnByValue = true,
                awaitPromise = false, timeout = 3000, allowUnsafeEvalBlockedByCSP = false
            }, ct, session);
            if (result.Value("exceptionDetails").ValueKind == JsonValueKind.Object) throw new IOException("页面脚本执行失败，未自动重试。");
            var value = result.Value("result").Value("value");
            return new(value.String("state"), value.String("key"), value.String("receipt"), value.String("detail"));
        }
        finally
        {
            if (!ct.IsCancellationRequested && !_disposed)
                await cdp.CallAsync("Target.detachFromTarget", new { sessionId = session }, ct);
        }
    }

    public async Task<GoalPage> SendAsync(GoalBrowserTab tab, string expectedKey, CancellationToken ct)
    {
        var nonce = Guid.NewGuid().ToString("N");
        var prepared = await EvaluateAsync(tab, "prepare", expectedKey, nonce, ct);
        if (prepared.State != "prepared") return prepared;
        await Task.Delay(350, ct); // React must observe the editor input before its Send button is used.
        var submitted = await EvaluateAsync(tab, "submit", expectedKey, nonce, ct);
        if (submitted.State != "dispatched") return submitted;
        for (var attempt = 0; attempt < 20; attempt++)
        {
            await Task.Delay(350, ct);
            var receipt = await EvaluateAsync(tab, "receipt", expectedKey, nonce, ct);
            if (receipt.State == "sent") return receipt;
        }
        return new("uncertain");
    }

    public void Dispose()
    {
        _disposed = true;
        _cdp?.Dispose();
        _cdp = null;
    }
}
