using System.Text.Json;

namespace AgentDock.ControlPanel;

public static class ActivitySummaryFormatter
{
    public static string Format(JsonElement overview)
    {
        var summary = overview.Field("conversation_summary");
        var recent = summary.OptionalNumber("recently_active");
        var total = summary.OptionalNumber("total");
        if (recent is null or < 0 || total is null or < 0 || recent > total)
            throw new InvalidOperationException("Core 未提供有效的近期活动对话统计。");
        var pending = overview.Field("statistics").Number("pending");
        return $"{recent} 运行中 · {pending} 待审批 · {total} 总对话";
    }
}
