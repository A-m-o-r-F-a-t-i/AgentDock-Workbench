using System.Text.Json;
using AgentDock.ControlPanel;

internal static class ConversationStartTimeTests
{
    internal static void Run(Action<bool, string> check)
    {
        var utc = TimeZoneInfo.Utc;
        var offset = TimeZoneInfo.CreateCustomTimeZone("UTC+8", TimeSpan.FromHours(8), "UTC+8", "UTC+8");
        var sample = JsonSerializer.SerializeToElement(new { created_at = "2026-10-12T15:06:59Z", last_activity_at = "2026-10-13T02:00:00Z" });
        check(ConversationStartTime.Format(sample, utc) == "2026-10-12 15:06", "start includes year month day hour minute without activity substitution");
        check(ConversationStartTime.Format(sample, offset) == "2026-10-12 23:06", "start uses display timezone");
        foreach (var value in new object[] { new { created_at = "invalid" }, new { created_at = "0001-01-01T00:00:00Z" }, new { last_activity_at = "2026-10-12T15:06:00Z" }, new { } })
            check(ConversationStartTime.Format(JsonSerializer.SerializeToElement(value), utc) == "", "missing start is hidden, not fabricated");
        check(ConversationStartTime.Format(default, utc) == "", "empty selection clears start");
    }
}
