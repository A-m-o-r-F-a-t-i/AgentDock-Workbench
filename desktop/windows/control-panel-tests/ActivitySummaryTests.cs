using System.Text.Json;
using AgentDock.ControlPanel;

internal static class ActivitySummaryTests
{
    public static void Run(Action<bool, string> check)
    {
        var overview = JsonSerializer.SerializeToElement(new
        {
            statistics = new { running = 19, pending = 3, unknown = 5 },
            conversation_summary = new { recently_active = 2, total = 7 }
        });
        check(ActivitySummaryFormatter.Format(overview) == "2 运行中 · 3 待审批 · 7 总对话", "overview counts recently active conversations, not calls");
        var empty = JsonSerializer.SerializeToElement(new
        {
            statistics = new { running = 9, pending = 0 },
            conversation_summary = new { recently_active = 0, total = 0 }
        });
        check(ActivitySummaryFormatter.Format(empty) == "0 运行中 · 0 待审批 · 0 总对话", "unattributed or old live calls cannot invent recent conversations");
        foreach (var invalid in new[] { "{}", "{\"statistics\":{\"running\":5}}", "{\"conversation_summary\":{\"recently_active\":2,\"total\":1}}", "{\"conversation_summary\":{\"recently_active\":-1,\"total\":4}}" })
        {
            using var json = JsonDocument.Parse(invalid);
            var rejected = false;
            try { ActivitySummaryFormatter.Format(json.RootElement); }
            catch (InvalidOperationException) { rejected = true; }
            check(rejected, "missing or invalid summary stays unavailable");
        }
        var row = new ExecutionObject { RecentlyActive = true };
        for (var turn = 0; turn < 10; turn++)
        {
            row.InFlight = turn % 2 == 0;
            check(row.ExecutionStateText == "" && row.RecentlyActive, "RPC starts and completions do not flash executing text or clear the dot");
        }
        row.InFlight = true;
        row.RecentlyActive = false;
        check(row.VisibleInAuto && row.ExecutionStateText == "", "expired background calls remain accessible without recent label");
        row.PendingCount = 1;
        check(row.ExecutionStateText == "待审批 1", "actionable approval state remains visible");
    }
}
