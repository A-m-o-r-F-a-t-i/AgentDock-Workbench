using System.Net.Http;
using System.Runtime.InteropServices;
using System.Text.Json;
using AgentDock.ControlPanel;

if (args.Length > 0)
{
    if (args[0] == "--discover")
    { using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(15)); Console.WriteLine(JsonSerializer.Serialize(await RoxyDiscovery.EndpointsAsync(timeout.Token))); return; }
    if (args.Length == 2 && args[0] == "--hidden-probe")
    { await BrowserProbe.RunAsync(args[1]); return; }
    await LiveAsync(args);
    return;
}
await PolicyTests.RunAsync();
await SourceTests.RunAsync();

static async Task LiveAsync(string[] args)
{
    if (args.Length < 3 || args[0] is not ("--observe" or "--continue" or "--recover"))
        throw new ArgumentException("Usage: --observe|--continue|--recover endpoint targetId [--confirm-send]");
    using var deadline = new CancellationTokenSource(TimeSpan.FromMinutes(8));
    var ct = deadline.Token;
    var tabs = await GoalBrowser.ListAsync(args[1], ct);
    var tab = tabs.Single(t => t.TargetId == args[2]);
    using var browser = new GoalBrowser();
    var initial = await browser.ObserveAsync(tab, false, ct);
    Console.WriteLine(JsonSerializer.Serialize(new { phase = "observe", state = initial.Page.State, visibility = initial.Page.Detail, target = tab.TargetId }));
    if (args[0] == "--observe") return;
    if (!args.Contains("--confirm-send") || !tab.Title.Contains("续执行测试", StringComparison.Ordinal) || !initial.Page.Key.Contains("测试已就绪", StringComparison.Ordinal))
        throw new InvalidOperationException("Live sends require the explicitly prepared isolation test conversation and --confirm-send.");
    var foreground = OperatingSystem.IsWindows() ? Native.GetForegroundWindow() : IntPtr.Zero;
    if (args[0] == "--recover")
    {
        using var cdp = new GoalCdp();
        await cdp.ConnectAsync(tab.Endpoint, tab.BrowserSession, ct);
        await cdp.CallAsync("Target.closeTarget", new { targetId = tab.TargetId }, ct);
        Console.WriteLine("Original isolation-test tab closed; hidden-page recovery is now under test.");
    }
    var source = new FakeSource(); // Explicit unfinished-state fixture; the browser and 300-second clock are real.
    var entry = new GoalEntry { Binding = new("conversation", "task", "main", "isolation-test", tab, args[0] == "--recover"), Enabled = true };
    var started = System.Diagnostics.Stopwatch.StartNew();
    using var session = new GoalModeSession(entry, source, browser, TimeProvider.System, _ => Task.CompletedTask);
    var sentAt = double.PositiveInfinity;
    for (var iteration = 0; iteration < 44; iteration++)
    {
        await session.PollAsync(ct);
        Console.WriteLine(JsonSerializer.Serialize(new { elapsedSeconds = Math.Round(started.Elapsed.TotalSeconds, 3), entry.Status,
            entry.Enabled, entry.SendCount, entry.DispatchUncertain, target = entry.Binding.Tab.TargetId,
            foregroundUnchanged = !OperatingSystem.IsWindows() || Native.GetForegroundWindow() == foreground }));
        if (!entry.Enabled) throw new InvalidOperationException(entry.Status);
        if (entry.SendCount > 0 && double.IsPositiveInfinity(sentAt)) sentAt = started.Elapsed.TotalSeconds;
        if (started.Elapsed.TotalSeconds - sentAt >= 30) break;
        await Task.Delay(TimeSpan.FromSeconds(10), ct);
    }
    var final = await browser.ObserveAsync(entry.Binding.Tab, false, ct);
    var reply = final.Page.Key.Contains("续执行测试已收到", StringComparison.Ordinal);
    Console.WriteLine(JsonSerializer.Serialize(new { phase = "result", sentAtSeconds = sentAt, entry.SendCount,
        assistantReceipt = reply, visibility = final.Page.Detail, hiddenRecovery = entry.Binding.Tab.TargetId != tab.TargetId }));
    if (sentAt < 300 || entry.SendCount != 1 || !reply) throw new InvalidOperationException("Live test did not satisfy all assertions.");
}

internal static class Native
{
    [DllImport("user32.dll")] internal static extern IntPtr GetForegroundWindow();
}

internal sealed class FakeClock : TimeProvider
{
    private long _ticks;
    public override long TimestampFrequency => 1000;
    public override long GetTimestamp() => _ticks;
    public override DateTimeOffset GetUtcNow() => DateTimeOffset.UnixEpoch.AddMilliseconds(_ticks);
    internal void Advance(int milliseconds) => _ticks += milliseconds;
}
internal sealed class FakeSource : IGoalActivitySource
{
    internal GoalActivity Activity = new("conversation", "task", "main", "active", "open", false, false, "progress-1");
    public Task<GoalActivity> ReadAsync(string conversationId, CancellationToken ct) => Task.FromResult(Activity);
}
internal sealed class FakeBrowser : IGoalBrowser
{
    internal GoalPage Page = new("ready", "reply-1");
    internal GoalPage Receipt = new("sent", "reply-2", "user-message-2");
    internal int Sent;
    public Task<(GoalBrowserTab, GoalPage)> ObserveAsync(GoalBrowserTab tab, bool recover, CancellationToken ct) => Task.FromResult((tab, Page));
    public Task<GoalPage> SendAsync(GoalBrowserTab tab, string key, CancellationToken ct) { Sent++; return Task.FromResult(Receipt); }
    public void Dispose() { }
}
internal static class PolicyTests
{
    internal static async Task RunAsync()
    {
        var assertions = 0;
        void Check(bool ok, string name) { if (!ok) throw new Exception(name); assertions++; }
        var tab = new GoalBrowserTab("http://127.0.0.1:9222", "session", "tab", "", "https://chatgpt.com/c/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "test");
        (GoalModeSession Session, GoalEntry Entry, FakeClock Clock, FakeSource Source, FakeBrowser Browser) Fixture(bool rejectWrite = false)
        {
            var entry = new GoalEntry { Enabled = true, Binding = new("conversation", "task", "main", "test", tab, false) };
            var clock = new FakeClock(); var source = new FakeSource(); var browser = new FakeBrowser();
            return (new(entry, source, browser, clock, _ => rejectWrite ? Task.FromException(new IOException("read-only")) : Task.CompletedTask), entry, clock, source, browser);
        }
        async Task Poll((GoalModeSession Session, GoalEntry Entry, FakeClock Clock, FakeSource Source, FakeBrowser Browser) f, int count = 1)
        { for (var i = 0; i < count; i++) { f.Clock.Advance(10000); await f.Session.PollAsync(default); } }
        var f = Fixture();
        await f.Session.PollAsync(default);
        await Poll(f, 29); f.Clock.Advance(9999); await f.Session.PollAsync(default);
        Check(f.Browser.Sent == 0, "299999 ms must not send");
        f.Clock.Advance(1); await f.Session.PollAsync(default);
        Check(f.Browser.Sent == 1 && f.Entry.SendCount == 1, "300000 ms sends exactly once with a receipt");
        await Poll(f, 80); Check(f.Browser.Sent == 1, "no repeated sends without real progress");
        f.Source.Activity = f.Source.Activity with { ActivityKey = "progress-2" };
        await Poll(f, 31); Check(f.Browser.Sent == 2, "real progress rearms after another five quiet minutes");
        foreach (var state in new[] { "busy", "draft", "unsupported", "wrong_page", "error" })
        {
            f = Fixture(); f.Browser.Page = new(state, "reply-1");
            await f.Session.PollAsync(default); await Poll(f, 40);
            Check(f.Browser.Sent == 0, "page guard " + state);
        }
        foreach (var activity in new[] {
            new GoalActivity("other", "task", "main", "active", "open", false, false, "1"),
            new GoalActivity("conversation", "other", "main", "active", "open", false, false, "1"),
            new GoalActivity("conversation", "task", "branch", "active", "open", false, false, "1"),
            new GoalActivity("conversation", "task", "main", "completed", "open", false, false, "1"),
            new GoalActivity("conversation", "task", "main", "blocked", "open", false, false, "1"),
            new GoalActivity("conversation", "task", "main", "active", "blocked", false, false, "1"),
            new GoalActivity("conversation", "task", "main", "active", "open", true, false, "1"),
            new GoalActivity("conversation", "task", "main", "active", "open", false, true, "1") })
        {
            f = Fixture(); f.Source.Activity = activity; await f.Session.PollAsync(default); await Poll(f, 40);
            Check(f.Browser.Sent == 0, "authoritative guard " + activity);
        }
        f = Fixture(); await f.Session.PollAsync(default); f.Clock.Advance(3600000); await f.Session.PollAsync(default);
        Check(f.Browser.Sent == 0, "resume after observation gap does not send");
        f = Fixture(true); await f.Session.PollAsync(default); await Poll(f, 31);
        Check(f.Browser.Sent == 0 && !f.Entry.Enabled, "rejected intent write prevents sending");
        f = Fixture(); f.Browser.Receipt = new("uncertain"); await f.Session.PollAsync(default); await Poll(f, 70);
        Check(f.Browser.Sent == 1 && !f.Entry.Enabled && f.Entry.DispatchUncertain, "uncertain dispatch pauses without retry");
        f = Fixture(); await f.Session.PollAsync(default); f.Session.Dispose(); await Poll(f, 40);
        Check(f.Browser.Sent == 0, "manual cancellation cannot rearm");
        foreach (var endpoint in new[] { "http://example.com:9222", "http://127.0.0.1:9222/redirect", "http://user@127.0.0.1:9222", "https://127.0.0.1:9222", "http://127.0.0.1:9222?x" })
        { try { GoalCdp.Endpoint(endpoint); Check(false, "unsafe endpoint accepted"); } catch (ArgumentException) { assertions++; } }
        foreach (var url in new[] { "https://chatgpt.com/", "https://chatgpt.com.evil.test/c/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "http://chatgpt.com/c/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" })
        { try { GoalBrowser.ConversationUrl(url); Check(false, "unsafe page accepted"); } catch (ArgumentException) { assertions++; } }
        Console.WriteLine($"PASS {assertions} goal-mode assertions");
    }
}
