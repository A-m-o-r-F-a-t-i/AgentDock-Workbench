using System.IO;
using System.Text.Json;

namespace AgentDock.ControlPanel;

internal sealed class GoalActivitySource(Func<string, CancellationToken, Task<JsonElement>> read) : IGoalActivitySource
{
    public async Task<GoalActivity> ReadAsync(string conversationId, CancellationToken ct)
    {
        var conversation = (await read("/internal/runtime/conversations/" + Uri.EscapeDataString(conversationId), ct)).Value("conversation");
        if (conversation.String("conversation_id") != conversationId) throw new IOException("无法确认目标对话身份。");
        var state = conversation.Value("state");
        var taskId = state.String("active_task_id");
        var threadId = state.String("active_task_thread_id");
        if (threadId.Length == 0) threadId = "main";
        var terminated = Present(conversation, "terminated_at") || Present(conversation, "archived_at") || Present(conversation, "trashed_at") || Present(conversation, "deleted_at");
        if (taskId.Length == 0) return new(conversationId, "", threadId, "unbound", "", terminated, false, "");
        var taskPath = "/internal/runtime/tasks/" + Uri.EscapeDataString(taskId);
        var taskResult = await read(taskPath, ct);
        var task = taskResult.Value("task");
        if (task.ValueKind == JsonValueKind.Undefined) task = taskResult;
        var thread = (await read(taskPath + "/threads/" + Uri.EscapeDataString(threadId), ct)).Value("thread");
        if (task.String("id") != taskId || thread.String("id") != threadId)
            throw new IOException("无法确认目标任务或分支身份。");
        terminated |= Present(task, "archived_at") || Present(task, "trashed_at") || Present(task, "cancelled_at");
        var overview = await read("/internal/runtime/execution", ct);
        var facts = overview.Value("conversation_activity").Value(conversationId);
        if (facts.ValueKind != JsonValueKind.Object || overview.Value("in_flight").ValueKind != JsonValueKind.Object)
            throw new IOException("目标对话缺少可验证的执行状态。");
        if (facts.Value("pending").ValueKind != JsonValueKind.Number || facts.Value("running").ValueKind != JsonValueKind.Number)
            throw new IOException("目标对话的执行计数不可用。");
        var live = await read("/internal/runtime/activity/live?task_id=" + Uri.EscapeDataString(taskId) + "&thread_id=" + Uri.EscapeDataString(threadId), ct);
        if (live.Value("sessions").ValueKind != JsonValueKind.Array)
            throw new IOException("无法读取后台命令状态。");
        var busy = Positive(facts, "pending") || Positive(facts, "running") || overview.Value("in_flight").Boolean(conversationId) ||
            live.Items("sessions").Any(s => s.String("status") is "running" or "starting" or "unknown");
        var activityKey = string.Join("|", facts.String("last_activity_at"), facts.String("last_tool_call_at"),
            facts.String("last_interaction_at"), task.String("updated_at"), thread.String("updated_at"));
        return new(conversationId, taskId, threadId, task.String("status"), thread.String("status"), terminated, busy, activityKey);
    }

    private static bool Present(JsonElement value, string name) => value.Value(name).ValueKind is not (JsonValueKind.Undefined or JsonValueKind.Null) && value.String(name).Length > 0;
    private static bool Positive(JsonElement value, string name) => value.Value(name).ValueKind == JsonValueKind.Number && value.Value(name).GetInt64() > 0;
}
