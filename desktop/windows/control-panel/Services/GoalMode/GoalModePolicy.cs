using System.Text.Json.Serialization;

namespace AgentDock.ControlPanel;

internal static class GoalModePolicy
{
    internal static readonly TimeSpan IdleWindow = TimeSpan.FromMinutes(5);
    internal static readonly TimeSpan PollInterval = TimeSpan.FromSeconds(10);
    internal static readonly TimeSpan MaximumObservationGap = TimeSpan.FromSeconds(45);
    internal const string ContinueMessage = "当前任务尚未完成，请继续任务";
    internal const int MaximumBindings = 8;

    internal static string? StopReason(GoalBinding binding, GoalActivity activity)
    {
        if (activity.ConversationId != binding.ConversationId || activity.TaskId != binding.TaskId || activity.ThreadId != binding.ThreadId)
            return "对话、任务或分支绑定已改变，请重新绑定。";
        if (activity.Terminated) return "对话已终止、归档或删除，目标模式已停止。";
        if (activity.TaskStatus == "completed") return "任务已结束，目标模式已停止。";
        if (activity.TaskStatus != "active" || activity.ThreadStatus != "open")
            return "任务或分支未处于可执行状态，请处理阻塞后重新开启。";
        return null;
    }

    internal static bool IsDue(TimeSpan quiet, bool busy, bool awaitingProgress) =>
        !busy && !awaitingProgress && quiet >= IdleWindow;
}

internal sealed record GoalActivity(string ConversationId, string TaskId, string ThreadId,
    string TaskStatus, string ThreadStatus, bool Terminated, bool Busy, string ActivityKey);

internal sealed record GoalBrowserTab(string Endpoint, string BrowserSession, string TargetId,
    string BrowserContextId, string Url, string Title)
{
    public string Display => $"{Title}\n{Url}";
}

internal sealed record GoalBinding(string ConversationId, string TaskId, string ThreadId,
    string Title, GoalBrowserTab Tab, bool RecoverClosedTab);

internal sealed class GoalEntry
{
    public GoalBinding Binding { get; set; } = new("", "", "", "", new("", "", "", "", "", ""), false);
    public bool Enabled { get; set; }
    public string Status { get; set; } = "未开启";
    public int SendCount { get; set; }
    public bool AwaitingProgress { get; set; }
    public bool DispatchUncertain { get; set; }
    public DateTimeOffset? LastSentAt { get; set; }
    public string LastActivityKey { get; set; } = "";
    [JsonIgnore] public string PageKey { get; set; } = "";
    [JsonIgnore] public long QuietSince { get; set; }
    [JsonIgnore] public long LastObserved { get; set; }
    [JsonIgnore] public bool HasObservation { get; set; }
    [JsonIgnore] public CancellationTokenSource Cancellation { get; } = new();
}

internal sealed record GoalPage(string State, string Key = "", string Receipt = "", string Detail = "");

internal interface IGoalActivitySource
{
    Task<GoalActivity> ReadAsync(string conversationId, CancellationToken cancellationToken);
}

internal interface IGoalBrowser : IDisposable
{
    Task<(GoalBrowserTab Tab, GoalPage Page)> ObserveAsync(GoalBrowserTab tab, bool recover, CancellationToken cancellationToken);
    Task<GoalPage> SendAsync(GoalBrowserTab tab, string expectedKey, CancellationToken cancellationToken);
}
