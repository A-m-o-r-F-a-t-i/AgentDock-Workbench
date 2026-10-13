using System.IO;
using System.Text.Json;
using System.Windows.Threading;

namespace AgentDock.ControlPanel;

// App/tray-owned: closing the execution-center window never stops an enabled goal.
internal sealed class GoalModeService : IDisposable
{
    private readonly string _root;
    private readonly ActivityClient _client;
    private readonly GoalActivitySource _source;
    private readonly Dictionary<string, GoalEntry> _entries = [];
    private readonly Dictionary<string, GoalModeSession> _sessions = [];
    private readonly SemaphoreSlim _gate = new(1, 1), _storeGate = new(1, 1);
    private readonly CancellationTokenSource _lifetime = new();
    private readonly DispatcherTimer _timer = new(DispatcherPriority.Background) { Interval = GoalModePolicy.PollInterval };
    private FileStream? _lease;
    private bool _loaded, _polling, _disposed;
    private string StorePath => Path.Combine(_root, "goal-mode-bindings.json");
    internal GoalEntry? Find(string conversationId) => _entries.GetValueOrDefault(conversationId);

    internal GoalModeService(RuntimeService runtime)
    {
        _root = runtime.RuntimeRoot;
        _client = new ActivityClient(runtime);
        _source = new GoalActivitySource(_client.ExecutionGetAsync);
        _timer.Tick += Timer_Tick;
    }

    internal async Task LoadAsync(CancellationToken ct)
    {
        await _gate.WaitAsync(ct);
        try
        {
            if (_loaded) return;
            Directory.CreateDirectory(_root);
            // Prevent two independently launched observer/tray processes from sending the same goal.
            _lease ??= new FileStream(Path.Combine(_root, "goal-mode.lock"), FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
            if (File.Exists(StorePath))
            {
                if (new FileInfo(StorePath).Length > 131072) throw new IOException("目标模式设置超过大小限制，原文件已保留。");
                var saved = JsonSerializer.Deserialize<GoalEntry[]>(await File.ReadAllTextAsync(StorePath, ct), ActivityClient.JsonOptions) ?? [];
                if (saved.Length > GoalModePolicy.MaximumBindings) throw new IOException("目标模式绑定数量超过限制。");
                foreach (var entry in saved)
                {
                    if (entry is null || entry.Binding is null || entry.Binding.Tab is null || string.IsNullOrWhiteSpace(entry.Binding.ConversationId))
                        throw new IOException("目标模式绑定损坏，原文件已保留。");
                    GoalCdp.Endpoint(entry.Binding.Tab.Endpoint);
                    GoalBrowser.ConversationUrl(entry.Binding.Tab.Url);
                    entry.Enabled = false;
                    entry.Status = entry.DispatchUncertain ? "上次发送结果未确认，请先检查原对话。" : "应用重启后已暂停，请重新绑定开启。";
                    _entries.Add(entry.Binding.ConversationId, entry);
                }
            }
            _loaded = true;
        }
        catch { _entries.Clear(); _lease?.Dispose(); _lease = null; throw; }
        finally { _gate.Release(); }
    }

    internal async Task<GoalActivity> ReadActivityAsync(string conversationId, CancellationToken ct) => await _source.ReadAsync(conversationId, ct);

    internal async Task EnableAsync(string conversationId, string title, GoalBrowserTab tab, bool recover, CancellationToken ct)
    {
        await LoadAsync(ct);
        await _gate.WaitAsync(ct);
        GoalBrowser? browser = null;
        try
        {
            if (_disposed) throw new IOException("目标模式服务已退出。");
            if (_entries.Count >= GoalModePolicy.MaximumBindings && !_entries.ContainsKey(conversationId))
                throw new InvalidOperationException("最多同时保留 8 个目标绑定，请移除不用的绑定。");
            if (_entries.Values.Any(e => e.Enabled && e.Binding.ConversationId != conversationId &&
                e.Binding.Tab.BrowserSession == tab.BrowserSession && e.Binding.Tab.Url == tab.Url))
                throw new InvalidOperationException("这个浏览器对话已绑定其他目标，请先解除旧绑定。");
            var activity = await _source.ReadAsync(conversationId, ct);
            var binding = new GoalBinding(conversationId, activity.TaskId, activity.ThreadId, title, tab, recover);
            var reason = GoalModePolicy.StopReason(binding, activity);
            if (reason is not null) throw new InvalidOperationException(reason);
            browser = new GoalBrowser();
            var observed = await browser.ObserveAsync(tab, false, ct);
            if (observed.Page.State is not ("ready" or "busy" or "draft")) throw new InvalidOperationException("绑定页面暂不可识别，请在浏览器确认对话已加载。");
            if (_sessions.Remove(conversationId, out var prior)) prior.Dispose();
            var entry = new GoalEntry { Binding = binding, Enabled = true, Status = "已绑定，开始新的五分钟观察" };
            _entries[conversationId] = entry;
            try { await SaveAsync(ct); }
            catch { entry.Enabled = false; entry.Status = "绑定未保存，未开启"; throw; }
            _sessions[conversationId] = new(entry, _source, browser, TimeProvider.System, SaveAsync);
            browser = null;
            _timer.Start();
        }
        finally { browser?.Dispose(); _gate.Release(); }
    }

    internal async Task DisableAsync(string conversationId, bool remove, CancellationToken ct)
    {
        // Cancel immediately, even when a poll currently owns the async gate.
        if (_entries.TryGetValue(conversationId, out var entry))
        { entry.Enabled = false; entry.Status = "已手动关闭"; entry.Cancellation.Cancel(); }
        await _gate.WaitAsync(ct);
        try
        {
            if (!_loaded) return;
            if (_sessions.Remove(conversationId, out var session)) session.Dispose();
            if (remove) _entries.Remove(conversationId);
            await SaveAsync(ct);
        }
        finally { _gate.Release(); }
    }

    private async void Timer_Tick(object? sender, EventArgs e)
    {
        if (_disposed || _polling || !_gate.Wait(0)) return;
        _polling = true;
        try
        {
            await Task.WhenAll(_sessions.Values.Where(s => s.Entry.Enabled).Select(s => s.PollAsync(_lifetime.Token)));
            if (!_entries.Values.Any(entry => entry.Enabled)) _timer.Stop();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ObjectDisposedException or OperationCanceledException)
        {
            foreach (var entry in _entries.Values.Where(entry => entry.Enabled))
            { entry.Enabled = false; entry.Status = "后台检测已停止：" + ex.Message; }
            foreach (var session in _sessions.Values) session.Dispose();
            _timer.Stop();
        }
        finally { _polling = false; _gate.Release(); }
    }

    private async Task SaveAsync(CancellationToken ct)
    {
        await _storeGate.WaitAsync(ct);
        var temporary = StorePath + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try
        {
            var json = JsonSerializer.Serialize(_entries.Values.ToArray(), ActivityClient.JsonOptions);
            if (System.Text.Encoding.UTF8.GetByteCount(json) > 131072) throw new IOException("目标模式设置超过大小限制。");
            await File.WriteAllTextAsync(temporary, json, ct);
            File.Move(temporary, StorePath, true);
        }
        finally
        {
            try { if (File.Exists(temporary)) File.Delete(temporary); } catch (IOException) { }
            _storeGate.Release();
        }
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _timer.Stop(); _timer.Tick -= Timer_Tick;
        _lifetime.Cancel();
        foreach (var session in _sessions.Values) session.Dispose();
        _client.Dispose(); _lease?.Dispose();
        // Gates and cancellation remain alive until any in-flight async continuation unwinds.
    }
}
