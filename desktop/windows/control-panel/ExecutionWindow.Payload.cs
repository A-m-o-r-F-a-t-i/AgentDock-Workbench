using System.IO;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;

namespace AgentDock.ControlPanel;

public partial class ExecutionWindow
{
    private McpUiPreference? _outputPreference;
    private async Task OpenDisplayPreferencesAsync()
    {
        var service = new DisplayPreferenceService(_runtime);
        var display = await service.ReadAsync(_lifetime.Token);
        _outputPreference = display;
        if (!ExecutionDialogs.Preferences(this, _preferences, display, async output =>
        {
            using var save = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
            save.CancelAfter(TimeSpan.FromSeconds(12));
            _outputPreference = await service.SaveOutputAsync(output, display.Revision, save.Token);
        })) return;
        FontSize = _preferences.FontSize; SavePreferences();
        if (_detailCall is { } row)
        {
            await LoadPayloadPageAsync(row, "request", false, automatic: true);
            await LoadPayloadPageAsync(row, "response", false, automatic: true);
        }
    }

    private async Task LoadPayloadPageAsync(ExecutionCallRow row, string kind, bool previous, bool automatic = false)
    {
        var payload = kind == "request" ? row.RequestPayload : row.ResponsePayload;
        if (payload.Reference.Length == 0 || payload.Busy) return;
        if (automatic && kind == "request")
        {
            if (!payload.HasReadError) await LoadEntireRequestAsync(row);
            return;
        }
        var needsPolicyReload = _outputPreference is not null && payload.NeedsBudgetReload(_outputPreference.ToolOutput.Budget);
        if (automatic && !needsPolicyReload && (!payload.NeedsLoad || payload.HasReadError)) return;
        var reference = payload.Reference;
        var box = kind == "request" ? RequestPayloadText : ResponsePayloadText;
        var scroll = box.VerticalOffset;
        var start = box.SelectionStart; var length = box.SelectionLength;
        var generation = _generation;
        payload.SetBusy(true);
        long policyRevision = 0;
        try
        {
            var display = await new DisplayPreferenceService(_runtime).ReadAsync(SelectionToken);
            if (_outputPreference is null || display.Revision >= _outputPreference.Revision) _outputPreference = display;
            var budget = _outputPreference.ToolOutput.Budget; policyRevision = _outputPreference.Revision;
            if (_closed || generation != _generation || !ReferenceEquals(_detailCall, row)) return;
            var reset = payload.NeedsBudgetReload(budget);
            var offset = automatic || reset ? 0 : payload.RequestedOffset(previous);
            var storageKind = kind == "request" ? "request" : row.ResponsePayloadKind;
            var page = await _client.ExecutionGetAsync("/internal/runtime/calls/" + Escape(row.Id) + "/payload/" + storageKind + "?offset=" + offset + "&" + budget.Query, SelectionToken);
            if (_closed || generation != _generation || !ReferenceEquals(_detailCall, row) || policyRevision != _outputPreference.Revision) return;
            if (!payload.ApplyPage(page, reference, reset ? false : previous, budget)) return;
            await Dispatcher.InvokeAsync(() =>
            {
                if (_closed || generation != _generation || !ReferenceEquals(_detailCall, row)) return;
                if (automatic)
                {
                    box.ScrollToVerticalOffset(scroll);
                    if (start <= box.Text.Length) box.Select(start, Math.Min(length, box.Text.Length - start));
                }
                else box.ScrollToHome();
            }, DispatcherPriority.Loaded);
        }
        catch (OperationCanceledException) { }
        catch (Exception ex) when (ex is HttpRequestException or IOException or JsonException)
        {
            if (reference == payload.Reference) payload.ReadFailed(ex.Message);
        }
        finally
        {
            payload.SetBusy(false);
            if (!_closed && generation == _generation && ReferenceEquals(_detailCall, row) && policyRevision > 0 && _outputPreference is not null && policyRevision != _outputPreference.Revision)
                await LoadPayloadPageAsync(row, kind, false, automatic: true);
        }
    }
    private async Task<bool> LoadEntireRequestAsync(ExecutionCallRow row)
    {
        var payload = row.RequestPayload;
        if (payload.Reference.Length == 0 || payload.IsFullyLoaded) return true;
        if (payload.Busy) return false;
        var reference = payload.Reference;
        var generation = _generation;
        var content = new StringBuilder(payload.TotalBytes is > 0 and <= int.MaxValue ? (int)payload.TotalBytes : 0);
        payload.SetBusy(true);
        try
        {
            var offset = 0L;
            while (true)
            {
                var page = await _client.ExecutionGetAsync("/internal/runtime/calls/" + Escape(row.Id) + "/payload/request?offset=" + offset + "&" + PayloadPageBudget.FullRead.Query, SelectionToken);
                if (_closed || generation != _generation || !ReferenceEquals(_detailCall, row) || reference != payload.Reference) return false;
                if (!payload.TryReadPage(page, reference, PayloadPageBudget.FullRead, out var slice)) return false;
                if (slice.Offset != offset) throw new InvalidDataException("完整调用参数分页起点不连续。");
                content.Append(slice.Text);
                if (!slice.HasMore) break;
                if (slice.NextOffset <= offset) throw new InvalidDataException("完整调用参数分页没有推进。");
                offset = slice.NextOffset;
            }
            if (!payload.ApplyComplete(content.ToString(), reference)) return false;
            await Dispatcher.InvokeAsync(() => RequestPayloadText.ScrollToHome(), DispatcherPriority.Loaded);
            return true;
        }
        catch (OperationCanceledException) { return false; }
        catch (Exception ex) when (ex is HttpRequestException or IOException or JsonException)
        {
            if (reference == payload.Reference) payload.ReadFailed(ex.Message);
            return false;
        }
        finally { payload.SetBusy(false); }
    }
    private async void LoadAllRequest_Click(object sender, RoutedEventArgs e)
    {
        if (_detailCall is { } row) await LoadEntireRequestAsync(row);
    }
    private async void CopyFullRequest_Click(object sender, RoutedEventArgs e)
    {
        if (_detailCall is not { } row) return;
        if (await LoadEntireRequestAsync(row)) CopyText(row.RequestText);
    }
    private async void PayloadPage_Click(object sender, RoutedEventArgs e)
    {
        if (_detailCall is not { } row || sender is not FrameworkElement { Tag: string tag }) return;
        var split = tag.Split(':', 2);
        await LoadPayloadPageAsync(row, split[0], split.Length > 1 && split[1] == "previous");
    }
    private void ControlPanel_Click(object sender, RoutedEventArgs e) => (System.Windows.Application.Current as App)?.ShowControlPanel();
}
