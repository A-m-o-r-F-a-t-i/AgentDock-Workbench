using System.Windows;

namespace AgentDock.ControlPanel;

public partial class ExecutionWindow
{
    private async void CommandObservations_Click(object sender, RoutedEventArgs e)
    {
        e.Handled = true;
        if (sender is not FrameworkElement { DataContext: ExecutionCallRow row } || !row.HasCommandObservations) return;
        var anchor = CaptureCallAnchor();
        row.ObservationsExpanded = !row.ObservationsExpanded;
        SyncInsertionTimeline();
        await RestoreCallAnchorAsync(anchor);
    }
}
