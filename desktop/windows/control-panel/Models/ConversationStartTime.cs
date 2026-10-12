using System.Globalization;
using System.Text.Json;

namespace AgentDock.ControlPanel;

public static class ConversationStartTime
{
    public static string Format(JsonElement snapshot, TimeZoneInfo? zone = null) =>
        snapshot.Date("created_at") is { } started
            ? TimeZoneInfo.ConvertTime(started, zone ?? TimeZoneInfo.Local).ToString("yyyy-MM-dd HH:mm", CultureInfo.InvariantCulture)
            : "";
}
