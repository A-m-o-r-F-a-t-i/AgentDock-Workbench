using System.Windows;

namespace AgentDock.ControlPanel;

public partial class ExecutionWindow
{
    private void UpdateConversationStartTime()
    {
        var text = "";
        if (_selected is { IsUnknown: false, IsOrphan: false, IsGroupFooter: false } selected)
        {
            var snapshot = _conversationSnapshot.Text("conversation_id") == selected.Id ? _conversationSnapshot : selected.Snapshot;
            text = ConversationStartTime.Format(snapshot);
        }
        ConversationStartedAt.Text = text;
        ConversationStartedAt.ToolTip = "对话开始时间（本机时区）：" + TimeZoneInfo.Local.DisplayName;
        ConversationStartedAt.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }
}
