using System.Windows;
using System.Windows.Threading;

namespace AgentDock.ControlPanel;

public partial class MainWindow
{
    private long _accessRevision;
    private bool _namedDraftDirty;
    private bool _accessApplyFailed;
    private DateTimeOffset? _activitySummaryAt;
    private readonly DispatcherTimer _activitySummaryTimer = new(DispatcherPriority.Background)
    {
        Interval = TimeSpan.FromSeconds(3)
    };
    private readonly SemaphoreSlim _activitySummaryGate = new(1, 1);

    protected override void OnInitialized(EventArgs e)
    {
        base.OnInitialized(e);
        _activitySummaryTimer.Tick += ActivitySummaryTimer_Tick;
        _activitySummaryTimer.Start();
        Closed += (_, _) => _activitySummaryTimer.Stop();
    }

    private async void ActivitySummaryTimer_Tick(object? sender, EventArgs e)
    {
        if (IsVisible) await RefreshActivitySummaryAsync();
    }

    private void InvalidateAccessChecks()
    {
        _accessRevision++;
        _lastAutoTestOrigin = "";
        if (PublicTestStatusText is not null) PublicTestStatusText.Text = UiText.Get("NotChecked");
        if (TailscaleDiagnosticText is not null) TailscaleDiagnosticText.Text = UiText.Get("NotChecked");
    }

    private void NamedDraft_Changed(object sender, RoutedEventArgs e)
    {
        if (_updatingUi) return;
        _namedDraftDirty = true;
        _accessApplyFailed = false;
        InvalidateAccessChecks();
        UpdateTunnelModeUi();
    }

    // Only success commits the selected draft. Applying another provider preserves
    // the hidden named-domain draft, including an unsubmitted replacement credential.
    internal void FinishAccessApply(bool success, string mode)
    {
        _accessApplyFailed = !success;
        if (!success) return;
        _tunnelSelectionDirty = false;
        if (mode == "named")
        {
            var updating = _updatingUi;
            _updatingUi = true;
            try { TunnelTokenPasswordBox.Clear(); _passwordHistory.Remove(TunnelTokenPasswordBox); }
            finally { _updatingUi = updating; }
            _namedDraftDirty = false;
        }
    }

    private void OpenActivityCenter_Click(object sender, RoutedEventArgs e)
    {
        if (System.Windows.Application.Current is App app) app.ShowActivityCenter();
    }

    private async Task RefreshActivitySummaryAsync()
    {
        if (!await _activitySummaryGate.WaitAsync(0)) return;
        try
        {
            using var client = new ActivityClient(_runtime);
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(4));
            var overview = await client.ExecutionGetAsync("/internal/runtime/execution", timeout.Token);
            ActivitySummaryText.Text = ActivitySummaryFormatter.Format(overview);
            _activitySummaryAt = DateTimeOffset.Now;
        }
        catch (Exception ex) when (ex is System.Net.Http.HttpRequestException or System.IO.IOException or System.Text.Json.JsonException or OperationCanceledException or InvalidOperationException)
        {
            ActivitySummaryText.Text = UiText.Get("StatusUnavailable") + (_activitySummaryAt is { } at ? " · " + UiText.Format("LastRefresh", at) : "");
        }
        finally
        {
            _activitySummaryGate.Release();
        }
    }
}
