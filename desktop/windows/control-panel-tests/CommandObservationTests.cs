using System.Text.Json;
using AgentDock.ControlPanel;

internal static class CommandObservationTests
{
    private static ExecutionCallRow Row(string id, string tool, string origin = "", string conversation = "conv_a", string status = "succeeded", bool routine = true, bool gap = false, long? elapsed = 5000) => new(JsonSerializer.SerializeToElement(new
    {
        call_id = id, tool_name = tool, origin_call_id = origin, conversation_id = conversation,
        status, routine_observation = routine, history_incomplete = gap, rpc_elapsed_ms = elapsed
    }));

    public static void Run(Action<bool, string> check)
    {
        var original = Row("command", "exec_command");
        var observations = Enumerable.Range(1, 20).Select(i => Row("observe" + i, "session_observe", "command")).ToArray();
        var canonical = new[] { original }.Concat(observations).ToArray();
        var compact = CommandObservationPresentation.Project(canonical, true);
        check(compact.Count == 1 && ReferenceEquals(compact[0], original), "20 routine observations fold under one original command");
        check(original.HasCommandObservations && original.ObservationCaption.Contains("20") && original.ObservationHint.Contains("100.000"), "observation count and known RPC duration remain available");
        check(canonical.Length == 21 && canonical[20].Id == "observe20", "presentation preserves canonical audit records");
        original.ObservationsExpanded = true;
        check(CommandObservationPresentation.Project(canonical, true).SequenceEqual(canonical), "explicit expansion restores original observations and order");
        original.ObservationsExpanded = false;
        check(CommandObservationPresentation.Project(canonical, false).Count == 21, "search and non-active views retain all matching observations");
        check(CommandObservationPresentation.Project(observations, true).Count == 20, "orphan observations stay visible when origin is outside retained page");
        foreach (var row in new[] {
            Row("failed", "session_observe", "command", status: "failed"),
            Row("unknown", "session_observe", "command", status: "unknown"),
            Row("running", "session_observe", "command", status: "running"),
            Row("gap", "session_observe", "command", gap: true),
            Row("old", "session_observe", "command", routine: false),
            Row("other", "session_observe", "command", conversation: "conv_b"),
            Row("input", "session_act", "command"),
            Row("list", "session_observe") })
            check(CommandObservationPresentation.Project(new[] { original, row }, true).Count == 2, "important observation remains visible: " + row.Id);
        var unknownDuration = Row("no-time", "session_observe", "command", elapsed: null);
        CommandObservationPresentation.Project(new[] { original, unknownDuration }, true);
        check(original.ObservationHint.Contains("1 次耗时未记录"), "unknown duration is not fabricated as a measured zero");
        var changed = Row("bad-history", "exec_command", gap: true);
        check(CommandObservationPresentation.Project(new[] { changed, Row("wait", "session_observe", "bad-history") }, true).Count == 2, "incomplete original history is not a safe folding target");
        CommandObservationPresentation.Project(new[] { original }, true);
        check(!original.HasCommandObservations, "removed observations clear badge without retaining hidden references");
    }
}
