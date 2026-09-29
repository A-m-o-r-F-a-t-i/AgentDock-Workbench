namespace AgentDock.ControlPanel;

internal sealed class ConversationExportScope
{
    internal IReadOnlyList<string> ConversationIds { get; }
    internal bool Unattributed { get; }
    internal string Description { get; }
    internal bool HasTarget => Unattributed || ConversationIds.Count > 0;

    private ConversationExportScope(IEnumerable<string> ids, bool unattributed, string description)
    {
        ConversationIds = Array.AsReadOnly(ids.Where(id => !string.IsNullOrWhiteSpace(id)).Distinct(StringComparer.Ordinal).ToArray());
        Unattributed = unattributed;
        Description = description;
    }

    internal static ConversationExportScope Capture(IEnumerable<string> ids, ExecutionObject? target)
    {
        if (target?.IsUnknown == true) return new([], true, "未归属调用");
        // Orphans still have a verifiable original conversation ID in the journal.
        if (target?.IsOrphan == true) return new([target.Id], false, "孤立对话：" + target.Id);
        var selectedIds = ids.ToArray();
        return new(selectedIds, false, "对话：" + string.Join("、", selectedIds));
    }
}
