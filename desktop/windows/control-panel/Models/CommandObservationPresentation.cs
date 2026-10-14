namespace AgentDock.ControlPanel;

public sealed partial class ExecutionCallRow
{
    private bool _observationsExpanded;
    private int _observationCount;
    private string _observationHint = "";
    public string OriginCallId => _value.Text("origin_call_id");
    public bool IsRoutineObservation => !IsInsertion && Tool == "session_observe" && Status == "succeeded" && _value.Flag("routine_observation") && HistoryWarning.Length == 0;
    public bool HasCommandObservations => _observationCount > 0;
    public bool ObservationsExpanded { get => _observationsExpanded; set { if (_observationsExpanded == value) return; _observationsExpanded = value; Notify(); } }
    public string ObservationCaption => (_observationsExpanded ? "▾" : "▸") + " 观察 " + _observationCount;
    public string ObservationHint => _observationHint;
    public void DescribeObservations(IReadOnlyList<ExecutionCallRow> rows)
    {
        var known = rows.Where(row => row.RpcElapsedMs is >= 0).ToArray();
        var duration = known.Sum(row => (double)row.RpcElapsedMs!.Value) / 1000;
        var hint = $"当前已加载的 {rows.Count} 次常规观察，已知 RPC 累计耗时 {duration:0.000} s" + (known.Length == rows.Count ? "" : $"，另有 {rows.Count - known.Length} 次耗时未记录") + "。展开后可逐项查看原始调用与输出，审计和根调用统计保持不变。";
        if (_observationCount == rows.Count && _observationHint == hint) return;
        _observationCount = rows.Count; _observationHint = hint; Notify();
    }
}

// This projection never owns or prunes the canonical call cache. Orphans,
// errors, missing source histories and cross-conversation relations stay visible.
public static class CommandObservationPresentation
{
    public static List<ExecutionCallRow> Project(IEnumerable<ExecutionCallRow> source, bool fold)
    {
        var rows = source.ToArray();
        var origins = rows.Where(row => !row.IsInsertion && row.ConversationId.Length > 0 && row.HistoryWarning.Length == 0 && row.Tool is "exec_command" or "android_device_read" or "android_device_act")
            .ToDictionary(row => row.Id, StringComparer.Ordinal);
        var related = rows.Where(row => row.IsRoutineObservation && row.OriginCallId != row.Id && origins.TryGetValue(row.OriginCallId, out var origin) && origin.ConversationId == row.ConversationId)
            .GroupBy(row => row.OriginCallId).ToDictionary(group => group.Key, group => group.ToArray(), StringComparer.Ordinal);
        foreach (var origin in origins.Values) origin.DescribeObservations(related.GetValueOrDefault(origin.Id, []));
        if (!fold) return rows.ToList();
        var hidden = related.Where(pair => !origins[pair.Key].ObservationsExpanded).SelectMany(pair => pair.Value).ToHashSet();
        return rows.Where(row => !hidden.Contains(row)).ToList();
    }
}
