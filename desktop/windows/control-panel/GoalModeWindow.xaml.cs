using System.IO;
using System.Net.WebSockets;
using System.Windows;
using System.Windows.Controls;

namespace AgentDock.ControlPanel;

public partial class GoalModeWindow : Window
{
    private readonly GoalModeService _service;
    private readonly string _conversationId, _title;
    private readonly CancellationTokenSource _lifetime = new();
    private CancellationTokenSource? _enabling;
    private bool _busy, _closed;

    internal GoalModeWindow(GoalModeService service, string conversationId, string title)
    {
        InitializeComponent();
        _service = service; _conversationId = conversationId; _title = title;
        ConversationText.Text = title;
        TaskText.Text = conversationId;
    }

    private async Task GuardAsync(Func<Task> action)
    {
        if (_busy || _closed) return;
        _busy = true;
        EnableButton.IsEnabled = DiscoverButton.IsEnabled = ReadTabsButton.IsEnabled = false;
        try { await action(); }
        catch (OperationCanceledException) { if (!_closed) StatusText.Text = "操作已取消或超时。"; }
        catch (Exception ex) when (ex is IOException or HttpRequestException or WebSocketException or
            System.Text.Json.JsonException or InvalidOperationException or ArgumentException or UnauthorizedAccessException)
        { if (!_closed) StatusText.Text = ex.Message; }
        finally
        {
            _busy = false;
            if (!_closed) EnableButton.IsEnabled = DiscoverButton.IsEnabled = ReadTabsButton.IsEnabled = true;
        }
    }

    private async void Window_Loaded(object sender, RoutedEventArgs e) => await GuardAsync(async () =>
    {
        await _service.LoadAsync(_lifetime.Token);
        var current = _service.Find(_conversationId);
        if (current is not null)
        {
            EndpointBox.Text = current.Binding.Tab.Endpoint;
            StatusText.Text = current.Status + "\n已绑定：" + current.Binding.Tab.Url;
            TaskText.Text = $"{_conversationId} · {current.Binding.TaskId} · {current.Binding.ThreadId}";
        }
        await DiscoverAsync();
    });

    private async Task DiscoverAsync()
    {
        var endpoints = await RoxyDiscovery.EndpointsAsync(_lifetime.Token);
        if (_closed) return;
        EndpointBox.ItemsSource = endpoints;
        if (endpoints.Length == 1)
        { EndpointBox.Text = endpoints[0]; await ReadTabsAsync(); }
        else StatusText.Text = endpoints.Length == 0 ? "未发现运行中 Roxy 的调试端点，可手动填写已有本机端点。" : "发现多个浏览器端点，请选择后读取标签页。";
    }

    private async Task ReadTabsAsync()
    {
        ConfirmBinding.IsChecked = false; TabsList.ItemsSource = null;
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
        deadline.CancelAfter(TimeSpan.FromSeconds(12));
        var tabs = await GoalBrowser.ListAsync(EndpointBox.Text.Trim(), deadline.Token);
        if (_closed) return;
        TabsList.ItemsSource = tabs;
        TabsList.SelectedIndex = -1;
        StatusText.Text = tabs.Length == 0 ? "没有可绑定的已有 ChatGPT 对话。请先打开对应对话。" :
            _service.Find(_conversationId)?.Status ?? "请选择标签页并核对，尚未开启自动发送。";
    }

    private async void Discover_Click(object sender, RoutedEventArgs e) => await GuardAsync(DiscoverAsync);
    private async void ReadTabs_Click(object sender, RoutedEventArgs e) => await GuardAsync(ReadTabsAsync);
    private void Tab_Changed(object sender, SelectionChangedEventArgs e) { if (ConfirmBinding is not null) ConfirmBinding.IsChecked = false; }
    private async void Enable_Click(object sender, RoutedEventArgs e) => await GuardAsync(async () =>
    {
        if (TabsList.SelectedItem is not GoalBrowserTab tab || ConfirmBinding.IsChecked != true)
        { StatusText.Text = "请手动选择标签页，并确认它对应当前 AgentDock 对话。"; return; }
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(_lifetime.Token);
        deadline.CancelAfter(TimeSpan.FromSeconds(25));
        _enabling = deadline;
        try
        {
            await _service.EnableAsync(_conversationId, _title, tab, RecoverClosed.IsChecked == true, deadline.Token);
            if (!_closed) { DialogResult = true; Close(); }
        }
        finally { _enabling = null; }
    });

    // Stop remains available while discovery or another async action is in progress.
    private async void Stop_Click(object sender, RoutedEventArgs e)
    {
        _enabling?.Cancel();
        try { await _service.DisableAsync(_conversationId, false, _lifetime.Token); if (!_closed) StatusText.Text = "目标模式已停止。"; }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or OperationCanceledException) { if (!_closed) StatusText.Text = ex.Message; }
    }
    private async void Remove_Click(object sender, RoutedEventArgs e)
    {
        _enabling?.Cancel();
        try { await _service.DisableAsync(_conversationId, true, _lifetime.Token); if (!_closed) StatusText.Text = "绑定已解除。"; }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or OperationCanceledException) { if (!_closed) StatusText.Text = ex.Message; }
    }
    private void Window_Closed(object? sender, EventArgs e) { _closed = true; _lifetime.Cancel(); }
}
