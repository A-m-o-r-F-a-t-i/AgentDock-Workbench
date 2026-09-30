using System.Net;
using System.Text.Json;
using System.Windows;
using System.Windows.Threading;

namespace AgentDock.ControlPanel;

internal sealed record TaskActionTarget(string TaskId, string ThreadId, int Generation, long SelectionVersion);

public partial class ExecutionWindow
{
    private long _taskSelectionVersion;
    private int _taskReads;
    private TaskActionTarget? _taskActionTarget;

    private void BeginTaskSelection(string taskId, string branch)
    {
        ++_taskSelectionVersion;
        ++_taskEpoch;
        _selectedTaskId = taskId;
        _branch = branch;
        _taskActionTarget = null;
        UpdateTaskActionAvailability();
    }

    private bool IsCurrentTaskTarget(TaskActionTarget target) => !_closed &&
        target.Generation == _generation && target.SelectionVersion == _taskSelectionVersion &&
        target.TaskId == _selectedTaskId && target.ThreadId == _branch && _taskActionTarget == target;

    private void UpdateTaskActionAvailability()
    {
        var enabled = _taskActionTarget is { } target && IsCurrentTaskTarget(target);
        ContinueBranchButton.IsEnabled = enabled;
        ContinueTaskButton.IsEnabled = enabled;
        TaskMenuButton.IsEnabled = enabled;
    }

    internal async Task ContinueSelectedBranchAsync(Func<TaskActionTarget, bool> confirm)
    {
        if (_taskActionTarget is not { } target || !IsCurrentTaskTarget(target)) return;
        if (!confirm(target)) return;
        if (!IsCurrentTaskTarget(target)) { Warn("任务或分支选择已变化，请重新确认。"); return; }
        await _client.ControlAsync(new { action = "thread_switch", task_id = target.TaskId, thread_id = target.ThreadId }, _lifetime.Token);
        if (IsCurrentTaskTarget(target)) Warn("继续分支已更新。");
    }

    internal async Task CancelSelectedTaskAsync(TaskActionTarget target, string reason)
    {
        if (!IsCurrentTaskTarget(target)) { Warn("任务选择已变化，取消操作未提交。"); return; }
        if (string.IsNullOrWhiteSpace(reason)) return;
        await _client.ControlAsync(new { action = "cancel", task_id = target.TaskId, summary = reason }, _lifetime.Token);
        if (IsCurrentTaskTarget(target)) await LoadTaskAsync(target.TaskId, target.ThreadId, true);
    }

    private async Task RefreshSelectedTaskAsync()
    {
        if (_closed || _selectedTaskId.Length == 0 || _taskReads > 0) return;
        await LoadTaskAsync(_selectedTaskId, _branch, TaskDetailsPanel.Visibility == Visibility.Visible);
    }

    private async Task RefreshExecutionAsync()
    {
        ResetSidebarRecoveryBudget();
        await LoadWorkspacesAsync();
        await LoadObjectsAsync();
        if (_closed) return;
        await LoadCallsAsync(false);
        await RefreshSelectedTaskAsync();
        await RefreshOverviewAsync();
    }

    private async Task LoadTaskAsync(string id, string branch, bool details)
    {
        if (_closed) return;
        var token = SelectionToken;
        var generation = _generation;
        var version = _taskSelectionVersion;
        var epoch = ++_taskEpoch;
        bool Current() => !_closed && !token.IsCancellationRequested && generation == _generation &&
            epoch == _taskEpoch && version == _taskSelectionVersion && id == _selectedTaskId;
        _taskActionTarget = null;
        UpdateTaskActionAvailability();
        ++_taskReads;
        try
        {
            var raw = await _client.ExecutionGetAsync("/internal/runtime/tasks/" + Escape(id), token);
            if (!Current()) return;
            var task = raw.Field("task"); if (task.ValueKind == JsonValueKind.Undefined) task = raw;
            JsonElement threadList;
            try { threadList = await _client.ExecutionGetAsync("/internal/runtime/tasks/" + Escape(id) + "/threads", token); }
            catch (HttpRequestException ex) when (ex.StatusCode == HttpStatusCode.NotFound) { threadList = default; }
            if (!Current()) return;
            var threads = threadList.Array("threads");
            var active = branch.Length > 0 ? branch : id == _currentConversationTaskId ? _conversationSnapshot.Field("state").Text("active_task_thread_id", "main") : task.Text("active_thread_id", "main");
            if (active.Length == 0) active = "main";
            JsonElement threadRaw;
            try { threadRaw = await _client.ExecutionGetAsync("/internal/runtime/tasks/" + Escape(id) + "/threads/" + Escape(active), token); }
            catch (HttpRequestException ex) when (ex.StatusCode == HttpStatusCode.NotFound) { threadRaw = default; }
            if (!Current()) return;
            var thread = threadRaw.Field("thread");
            var showDetails = details || TaskDetailsPanel.Visibility == Visibility.Visible;
            JsonElement milestones = default;
            if (showDetails)
            {
                milestones = await _client.ExecutionGetAsync("/internal/runtime/tasks/" + Escape(id) + "/activity?milestones=true&limit=100&after=0", token);
                if (!Current()) return;
            }
            _taskSnapshot = task;
            _branch = active;
            var steps = thread.Field("steps").ValueKind == JsonValueKind.Array ? thread.Array("steps") : task.Array("steps");
            var done = steps.Count(step => step.Text("status") == "completed");
            var currentId = thread.Text("current_step_id", task.Text("current_step_id"));
            var current = steps.FirstOrDefault(step => step.Text("id") == currentId).Text("title");
            CurrentTaskStatus.Text = steps.Length == 0 ? "进度未记录" : $"{done}/{steps.Length}";
            CurrentTaskProgress.Visibility = steps.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
            CurrentTaskProgress.Value = steps.Length == 0 ? 0 : done * 100.0 / steps.Length;
            CurrentTaskNext.Text = current; CurrentTaskNext.ToolTip = current;
            if (showDetails)
            {
                var offset = TaskDetailsScroll.VerticalOffset;
                var choices = threads.Select(value => new ExecutionChoice(value.Text("id", "main"), value.Text("title", value.Text("id", "main")))).ToArray();
                var updating = _updating;
                _updating = true;
                try
                {
                    if (BranchCombo.ItemsSource is not IEnumerable<ExecutionChoice> existing || !existing.SequenceEqual(choices)) BranchCombo.ItemsSource = choices;
                    BranchCombo.SelectedValue = active;
                }
                finally { _updating = updating; }
                var next = thread.Text("next_action", task.Text("next_action"));
                TaskGoalText.Text = task.Text("goal") + (next.Length > 0 ? "\n下一动作：" + next : "");
                TaskStepsText.Text = steps.Length == 0 ? "进度未记录" : string.Join("\n", steps.Select(step => ExecutionJson.State(step.Text("status")) + "  " + step.Text("title")));
                var conditions = task.Array("conditions"); if (conditions.Length == 0) conditions = task.Array("completion_conditions");
                TaskAcceptanceText.Text = "验收条件\n" + (conditions.Length == 0 ? "未记录" : string.Join("\n", conditions.Select(condition => condition.ValueKind == JsonValueKind.String ? condition.GetString() : condition.Text("text", condition.Pretty()))));
                MilestonesText.Text = string.Join("\n", milestones.Array("events").Select(value => value.Text("summary")));
                await Dispatcher.InvokeAsync(() => { if (Current() && TaskDetailsPanel.Visibility == Visibility.Visible) TaskDetailsScroll.ScrollToVerticalOffset(offset); }, DispatcherPriority.Loaded);
                if (!Current()) return;
            }
            // Old records may be displayed, but an unverified thread never becomes an action target.
            if (thread.Text("id") == active) _taskActionTarget = new(id, active, generation, version);
            UpdateTaskActionAvailability();
        }
        finally { --_taskReads; }
    }
}
