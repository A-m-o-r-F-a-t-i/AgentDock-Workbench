using System.Windows;

namespace AgentDock.ControlPanel;

public partial class App
{
    internal GoalModeService? GoalModes { get; private set; }
    internal GoalModeService GetGoalModes()
    {
        if (GoalModes is null)
        {
            GoalModes = new GoalModeService(Runtime);
            Exit += (_, _) => GoalModes.Dispose();
        }
        return GoalModes;
    }
}

public partial class ExecutionWindow
{
    private void UpdateGoalModeButton()
    {
        if (GoalModeButton is null) return;
        var entry = _selected is null ? null : (System.Windows.Application.Current as App)?.GoalModes?.Find(_selected.Id);
        GoalModeButton.IsEnabled = _selected is { IsUnknown: false, IsOrphan: false, IsGroupFooter: false };
        GoalModeButton.Content = entry?.Enabled == true ? "目标模式 · 开" : entry is not null ? "目标模式 · 停" : "目标模式";
        GoalModeButton.ToolTip = entry is null ? "绑定浏览器对话，五分钟停更后自动续执行" : entry.Status + "\n" + entry.Binding.Tab.Url;
    }

    private void GoalMode_Click(object sender, RoutedEventArgs e)
    {
        if (_selected is not { IsUnknown: false, IsOrphan: false, IsGroupFooter: false } selected ||
            System.Windows.Application.Current is not App app) return;
        var dialog = new GoalModeWindow(app.GetGoalModes(), selected.Id, selected.Title) { Owner = this };
        dialog.ShowDialog();
        UpdateGoalModeButton();
    }
}
