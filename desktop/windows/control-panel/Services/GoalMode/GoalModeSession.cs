using System.IO;

namespace AgentDock.ControlPanel;

// One explicitly bound goal. Polls are serialized by its owner; no UI selection is consulted.
internal sealed class GoalModeSession(GoalEntry entry, IGoalActivitySource activitySource,
    IGoalBrowser browser, TimeProvider clock, Func<CancellationToken, Task> persist) : IDisposable
{
    internal GoalEntry Entry { get; } = entry;
    private bool _disposed;

    internal async Task PollAsync(CancellationToken cancellationToken)
    {
        if (!Entry.Enabled || _disposed) return;
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, Entry.Cancellation.Token);
        linked.CancelAfter(TimeSpan.FromSeconds(25));
        var ct = linked.Token;
        try
        {
            var activity = await activitySource.ReadAsync(Entry.Binding.ConversationId, ct);
            var stop = GoalModePolicy.StopReason(Entry.Binding, activity);
            if (stop is not null) { await PauseAsync(stop, cancellationToken); return; }
            var observation = await browser.ObserveAsync(Entry.Binding.Tab, Entry.Binding.RecoverClosedTab, ct);
            if (!Entry.Enabled) return;
            if (observation.Tab != Entry.Binding.Tab)
            {
                Entry.Binding = Entry.Binding with { Tab = observation.Tab };
                Entry.HasObservation = false;
                await persist(ct);
            }
            var page = observation.Page;
            if (page.State is "wrong_page" or "unsupported" or "error")
            {
                await PauseAsync("页面不可安全续执行：" + page.State + " " + page.Detail, cancellationToken);
                return;
            }
            var now = clock.GetTimestamp();
            var changed = activity.ActivityKey != Entry.LastActivityKey;
            if (Entry.HasObservation && changed) Entry.AwaitingProgress = false;
            var busy = activity.Busy || page.State != "ready";
            if (!Entry.HasObservation || clock.GetElapsedTime(Entry.LastObserved, now) > GoalModePolicy.MaximumObservationGap ||
                changed || page.Key != Entry.PageKey || busy)
                Entry.QuietSince = now;
            Entry.HasObservation = true;
            Entry.LastObserved = now;
            Entry.LastActivityKey = activity.ActivityKey;
            Entry.PageKey = page.Key;
            var quiet = clock.GetElapsedTime(Entry.QuietSince, now);
            Entry.Status = Entry.AwaitingProgress ? "已发送，等待真实任务进展" : busy ? "等待执行或用户输入结束" :
                $"监测中，连续静默 {Math.Floor(quiet.TotalSeconds)} / 300 秒";
            if (!GoalModePolicy.IsDue(quiet, busy, Entry.AwaitingProgress)) return;

            // Re-read the authoritative task just before dispatch, independently of visible UI state.
            var latest = await activitySource.ReadAsync(Entry.Binding.ConversationId, ct);
            stop = GoalModePolicy.StopReason(Entry.Binding, latest);
            if (stop is not null) { await PauseAsync(stop, cancellationToken); return; }
            if (latest.Busy || latest.ActivityKey != activity.ActivityKey)
            { Entry.QuietSince = clock.GetTimestamp(); return; }
            ct.ThrowIfCancellationRequested();
            if (!Entry.Enabled) return;
            Entry.AwaitingProgress = true;
            Entry.DispatchUncertain = true;
            Entry.Status = "正在发送；未确认时不会自动重试";
            await persist(ct); // A rejected write prevents the browser mutation.
            ct.ThrowIfCancellationRequested();
            if (!Entry.Enabled) return;
            var receipt = await browser.SendAsync(Entry.Binding.Tab, page.Key, ct);
            if (receipt.State == "sent" && receipt.Receipt.Length > 0)
            {
                Entry.DispatchUncertain = false;
                Entry.LastSentAt = clock.GetUtcNow();
                Entry.SendCount++;
                Entry.Status = "已确认发送，等待真实任务进展";
                await persist(ct);
            }
            else if (receipt.State == "not_sent")
            {
                Entry.DispatchUncertain = false;
                Entry.AwaitingProgress = false;
                Entry.QuietSince = clock.GetTimestamp();
                Entry.Status = "发送前页面已变化，本轮未发送";
                await persist(ct);
            }
            else await PauseAsync("发送结果未确认，请检查绑定对话后重新开启。", cancellationToken);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ObjectDisposedException or HttpRequestException or System.Text.Json.JsonException or
            InvalidOperationException or ArgumentException or OperationCanceledException or System.Net.WebSockets.WebSocketException)
        {
            Entry.HasObservation = false;
            if (Entry.DispatchUncertain)
                await PauseAsync("发送结果未确认，已停止自动重试。", CancellationToken.None);
            else if (ex is OperationCanceledException && !Entry.Enabled) { }
            else await PauseAsync("目标模式已暂停：" + ex.Message, CancellationToken.None);
        }
    }

    private async Task PauseAsync(string reason, CancellationToken cancellationToken)
    {
        Entry.Enabled = false;
        Entry.Status = reason;
        browser.Dispose();
        try { await persist(cancellationToken); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or OperationCanceledException)
        { Entry.Status += " 状态保存失败：" + ex.Message; }
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        Entry.Enabled = false;
        Entry.Cancellation.Cancel();
        browser.Dispose();
    }
}
