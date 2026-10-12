using System.IO;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using AgentDock.ControlPanel;

internal static class Program
{
    [STAThread]
    private static int Main(string[] args)
    {
        var app = new Application { ShutdownMode = ShutdownMode.OnExplicitShutdown };
        app.Resources.MergedDictionaries.Add(new ResourceDictionary { Source = new Uri("pack://application:,,,/agentdock-tray;component/Themes/Light.xaml") });
        app.Startup += async (_, _) =>
        {
            try
            {
                if (args.Length == 3 && args[0] == "--runtime-snapshot")
                {
                    using var client = new ActivityClient(new RuntimeService(args[1]));
                    using var deadline = new CancellationTokenSource(TimeSpan.FromSeconds(20));
                    var activity = await new GoalActivitySource(client.ExecutionGetAsync).ReadAsync(args[2], deadline.Token);
                    Console.WriteLine(JsonSerializer.Serialize(activity));
                }
                else await TestsAsync(args.Length > 0 ? args[0] : null);
                app.Shutdown(0);
            }
            catch (Exception ex) { Console.Error.WriteLine(ex); app.Shutdown(1); }
        };
        return app.Run();
    }

    private static async Task TestsAsync(string? artifactDirectory)
    {
        var count = 0;
        void Check(bool ok, string name) { if (!ok) throw new Exception(name); count++; }
        var root = Path.Combine(Path.GetTempPath(), "agentdock-goal-tests-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        try
        {
            var runtime = new RuntimeService(root);
            var tab = new GoalBrowserTab("http://127.0.0.1:9222", "test-session", "test-tab", "", "https://chatgpt.com/c/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "隔离测试对话");
            var seed = new GoalEntry { Enabled = true, DispatchUncertain = true, Binding = new("conversation", "task", "main", "后台自动续执行", tab, true) };
            var store = Path.Combine(root, "goal-mode-bindings.json");
            await File.WriteAllTextAsync(store, JsonSerializer.Serialize(new[] { seed }, ActivityClient.JsonOptions));
            using (var service = new GoalModeService(runtime))
            {
                await service.LoadAsync(default);
                Check(service.Find("conversation") is { Enabled: false, DispatchUncertain: true }, "restart must pause, retaining uncertain intent");
                using var second = new GoalModeService(runtime);
                try { await second.LoadAsync(default); throw new Exception("duplicate observer acquired lease"); }
                catch (IOException) { count++; }
                var dialog = new GoalModeWindow(service, "conversation", "后台自动续执行 · 隔离测试");
                Check(((CheckBox)dialog.FindName("ConfirmBinding")).IsChecked != true, "binding requires explicit confirmation");
                Check(((CheckBox)dialog.FindName("RecoverClosed")).IsChecked == true, "hidden recovery is disclosed in UI");
                ((ListBox)dialog.FindName("TabsList")).ItemsSource = new[] { tab };
                Check(((ListBox)dialog.FindName("TabsList")).SelectedIndex == -1, "never auto-select a conversation");
                var content = (FrameworkElement)dialog.Content;
                // Detach from the never-shown Window so its hidden visibility does not blank the render.
                dialog.Content = null;
                content.Resources = dialog.Resources;
                System.Windows.Documents.TextElement.SetFontFamily(content, dialog.FontFamily);
                System.Windows.Documents.TextElement.SetFontSize(content, dialog.FontSize);
                System.Windows.Documents.TextElement.SetForeground(content, dialog.Foreground);
                ((Grid)content).Background = (Brush)Application.Current.Resources["AppBackground"];
                var renderHost = new Window { Content = content, Width = 740, Height = 610,
                    Left = -20000, Top = -20000, ShowActivated = false, ShowInTaskbar = false,
                    WindowStartupLocation = WindowStartupLocation.Manual, Title = "AgentDock goal-mode regression" };
                renderHost.Show();
                await System.Windows.Threading.Dispatcher.Yield(System.Windows.Threading.DispatcherPriority.ApplicationIdle);
                foreach (var size in new[] { new Size(740, 610), new Size(640, 560) })
                {
                    renderHost.Width = size.Width; renderHost.Height = size.Height;
                    await System.Windows.Threading.Dispatcher.Yield(System.Windows.Threading.DispatcherPriority.ApplicationIdle);
                    content.UpdateLayout();
                    var confirm = (FrameworkElement)dialog.FindName("ConfirmBinding");
                    var enable = (FrameworkElement)dialog.FindName("EnableButton");
                    var point = enable.TransformToAncestor(content).Transform(new Point());
                    Check(confirm.ActualHeight > 0 && enable.ActualWidth > 0 && point.Y + enable.ActualHeight <= content.ActualHeight + 1 &&
                        point.X + enable.ActualWidth <= content.ActualWidth + 1,
                        $"controls fit {size}: button={point}, size={enable.ActualWidth}x{enable.ActualHeight}, content={content.ActualWidth}x{content.ActualHeight}");
                }
                if (artifactDirectory is not null)
                {
                    Directory.CreateDirectory(artifactDirectory);
                    // Export measured geometry, not a bitmap: local offscreen bitmap rendering was blank.
                    var geometry = new { testedWindowSizes = new[] { "740x610", "640x560" },
                        content.ActualWidth, content.ActualHeight, visible = content.IsVisible,
                        bindingRequiresConfirmation = true, selectedTabIndex = ((ListBox)dialog.FindName("TabsList")).SelectedIndex };
                    await File.WriteAllTextAsync(Path.Combine(artifactDirectory, "goal-mode-layout.json"), JsonSerializer.Serialize(geometry));
                }
                renderHost.Close();
                dialog.Close();
                await service.DisableAsync("conversation", false, default);
                Check(service.Find("conversation")?.Enabled == false, "explicit stop persists disabled");
                await service.DisableAsync("conversation", true, default);
                Check(service.Find("conversation") is null && JsonDocument.Parse(await File.ReadAllTextAsync(store)).RootElement.GetArrayLength() == 0, "remove clears persisted binding");
            }
            await File.WriteAllTextAsync(store, "[broken");
            using (var invalid = new GoalModeService(runtime))
            {
                try { await invalid.LoadAsync(default); throw new Exception("corrupt state accepted"); }
                catch (JsonException) { count++; }
                await invalid.DisableAsync("conversation", true, default);
                Check(await File.ReadAllTextAsync(store) == "[broken", "failed load does not overwrite the original store");
            }
            Console.WriteLine($"PASS {count} native goal-mode assertions; isolated offscreen UI host, no Core or production tray was started");
        }
        finally { Directory.Delete(root, true); }
    }
}
