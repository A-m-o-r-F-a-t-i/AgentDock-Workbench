using System.Text.Json;

namespace AgentDock.ControlPanel;

public partial class ExecutionWindow
{
    private long _batchSelectionVersion;
    private string _frozenSelectionScope = "";
    private CancellationTokenSource? _batchSelectionRequest;

    private void InvalidateBatchSelection()
    {
        ++_batchSelectionVersion;
        _batchSelectionRequest?.Cancel();
        _frozenSelection = null;
        _frozenSelectionScope = "";
    }

    private string[] SelectedObjectIds()
    {
        if (_closed || _sidebarScope != SidebarScope()) return [];
        if (_frozenSelection is not null && _frozenSelectionScope == SidebarScope())
            return _frozenSelection.ToArray();
        return ObjectsList.SelectedItems.Cast<ExecutionObject>()
            .Where(item => !item.IsUnknown && !item.IsOrphan && !item.IsGroupFooter)
            .Select(item => item.Id).Distinct(StringComparer.Ordinal).ToArray();
    }

    private string SelectionDescription() =>
        $"对话范围：{(_conversationView == "trash" ? "回收站" : _conversationView == "archived" ? "已归档" : "当前对话")}；搜索：{(SearchBox.Text.Trim().Length == 0 ? "全部" : SearchBox.Text.Trim())}";

    private async Task SelectAllObjectsAsync()
    {
        if (_closed) return;
        InvalidateBatchSelection();
        var version = _batchSelectionVersion;
        var scope = SidebarScope();
        var query = ListQuery(true);
        using var request = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
        _batchSelectionRequest = request;
        try
        {
            var page = await _client.ExecutionGetAsync("/internal/runtime/conversations?" + query, request.Token);
            if (_closed || request.IsCancellationRequested || version != _batchSelectionVersion || scope != SidebarScope()) return;
            var ids = page.Array("selected_ids");
            if (ids.Any(value => value.ValueKind != JsonValueKind.String || string.IsNullOrWhiteSpace(value.GetString())))
                throw new JsonException("全选响应包含无效对话标识，未改变选择。");
            _frozenSelection = ids.Select(value => value.GetString()!).Distinct(StringComparer.Ordinal).ToArray();
            _frozenSelectionScope = scope;
            var updating = _updating;
            _updating = true;
            try { ObjectsList.SelectAll(); }
            finally { _updating = updating; }
            Warn($"已选择 {_frozenSelection.Length} 个对话。{SelectionDescription()}");
        }
        finally
        {
            if (ReferenceEquals(_batchSelectionRequest, request)) _batchSelectionRequest = null;
        }
    }
}
