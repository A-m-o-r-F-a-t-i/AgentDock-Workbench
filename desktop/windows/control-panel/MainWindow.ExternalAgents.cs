using System.Windows;
using System.Windows.Controls;

namespace AgentDock.ControlPanel;

public partial class MainWindow
{
    private bool _externalAgentsUiReady;
    private bool _externalAgentRefreshInProgress;

    private void ExternalAgentsPanel_Loaded(object sender, RoutedEventArgs e)
    {
        _externalAgentsUiReady = true;
        if (ExternalAgentEndpointComboBox.SelectedIndex < 0)
        {
            ExternalAgentEndpointComboBox.SelectedIndex = 0;
        }
        if (ExternalAgentClientComboBox.SelectedIndex < 0)
        {
            ExternalAgentClientComboBox.SelectedIndex = 0;
        }
        RefreshExternalAgentAccess();
    }

    private void ExternalAgentsPanel_IsVisibleChanged(object sender, DependencyPropertyChangedEventArgs e)
    {
        if (_externalAgentsUiReady && e.NewValue is true)
        {
            RefreshExternalAgentAccess();
        }
    }

    private void ExternalAgentSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_externalAgentsUiReady && !_externalAgentRefreshInProgress)
        {
            RefreshExternalAgentAccess();
        }
    }

    private void ExternalAgentSourceTextChanged(object sender, TextChangedEventArgs e)
    {
        if (_externalAgentsUiReady && !_externalAgentRefreshInProgress)
        {
            RefreshExternalAgentAccess();
        }
    }

    private void RefreshExternalAgentButton_Click(object sender, RoutedEventArgs e)
    {
        RefreshExternalAgentAccess();
    }

    private void CopyExternalAgentEndpoint_Click(object sender, RoutedEventArgs e)
    {
        CopyExternalAgentText(ExternalAgentEndpointTextBox.Text, "ExternalAgentEndpointCopied");
    }

    private void CopyExternalAgentConfiguration_Click(object sender, RoutedEventArgs e)
    {
        CopyExternalAgentText(ExternalAgentConfigurationTextBox.Text, "ExternalAgentConfigurationCopied");
    }

    private void CopyExternalAgentText(string value, string successTextKey)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return;
        }

        try
        {
            System.Windows.Clipboard.SetText(value);
            SetExternalAgentStatus(UiText.Get(successTextKey), isError: false);
        }
        catch (Exception error)
        {
            SetExternalAgentStatus(UiText.Format("ExternalAgentCopyFailed", error.Message), isError: true);
        }
    }

    private void RefreshExternalAgentAccess()
    {
        if (!_externalAgentsUiReady || _externalAgentRefreshInProgress)
        {
            return;
        }

        _externalAgentRefreshInProgress = true;
        try
        {
            var localEndpoint = _snapshot?.LocalMcpUrl ?? LocalMcpTextBox.Text;
            var publicEndpoint = _snapshot?.PublicMcpUrl ?? PublicMcpTextBox.Text;
            var hasPublicEndpoint = ExternalAgentAccess.TryNormalizeEndpoint(publicEndpoint, out _);
            ExternalAgentPublicEndpointItem.IsEnabled = hasPublicEndpoint;

            if (!hasPublicEndpoint && SelectedEndpointSource() == "public")
            {
                ExternalAgentEndpointComboBox.SelectedIndex = 0;
            }

            var candidate = SelectedEndpointSource() == "public" ? publicEndpoint : localEndpoint;
            if (!ExternalAgentAccess.TryNormalizeEndpoint(candidate, out var endpoint))
            {
                ExternalAgentEndpointTextBox.Text = string.IsNullOrWhiteSpace(candidate)
                    ? UiText.Get("NotConfigured")
                    : candidate.Trim();
                ExternalAgentConfigurationTextBox.Text = UiText.Get("ExternalAgentInvalidEndpoint");
                CopyExternalAgentEndpointButton.IsEnabled = false;
                CopyExternalAgentConfigurationButton.IsEnabled = false;
                SetExternalAgentStatus(UiText.Get("ExternalAgentEndpointUnavailable"), isError: true);
                return;
            }

            ExternalAgentEndpointTextBox.Text = endpoint;
            ExternalAgentConfigurationTextBox.Text = ExternalAgentAccess.BuildConfiguration(
                SelectedExternalAgentClient(),
                endpoint,
                UiText.Get("ExternalAgentSetupPromptTemplate"));
            CopyExternalAgentEndpointButton.IsEnabled = true;
            CopyExternalAgentConfigurationButton.IsEnabled = true;
            SetExternalAgentStatus(UiText.Get("ExternalAgentEndpointReady"), isError: false);
        }
        finally
        {
            _externalAgentRefreshInProgress = false;
        }
    }

    private string SelectedEndpointSource()
    {
        return (ExternalAgentEndpointComboBox.SelectedItem as ComboBoxItem)?.Tag?.ToString() ?? "local";
    }

    private ExternalAgentClientKind SelectedExternalAgentClient()
    {
        return (ExternalAgentClientComboBox.SelectedItem as ComboBoxItem)?.Tag?.ToString() switch
        {
            "claude-code" => ExternalAgentClientKind.ClaudeCode,
            "generic-mcp" => ExternalAgentClientKind.GenericMcp,
            "agent-prompt" => ExternalAgentClientKind.AgentSetupPrompt,
            _ => ExternalAgentClientKind.Codex,
        };
    }

    private void SetExternalAgentStatus(string message, bool isError)
    {
        ExternalAgentStatusText.Text = message;
        ExternalAgentStatusText.SetResourceReference(
            TextBlock.ForegroundProperty,
            isError ? "DangerBrush" : "SecondaryText");
    }
}
