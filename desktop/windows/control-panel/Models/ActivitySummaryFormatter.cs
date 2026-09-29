using System.Text.Json;

namespace AgentDock.ControlPanel;

public static class ActivitySummaryFormatter
{
    public static string Format(JsonElement overview, JsonElement conversations)
    {
        var stats = overview.Field("statistics");
        var running = stats.Number("running");
        var pending = stats.Number("pending");
        var selected = conversations.Field("selected_ids");
        var total = selected.ValueKind == JsonValueKind.Array ? selected.GetArrayLength() : conversations.Number("total");
        return $"{running} 运行中 · {pending} 待审批 · {total} 总对话";
    }
}
