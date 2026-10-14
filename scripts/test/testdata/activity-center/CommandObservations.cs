using System.IO;
using System.Reflection;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Markup;
using System.Windows.Threading;
using System.Xml.Linq;
using AgentDock.ControlPanel;

internal static partial class Program
{
    private static void TestCommandObservationRendering(string root)
    {
        Directory.CreateDirectory(root);
        SynchronizationContext.SetSynchronizationContext(new DispatcherSynchronizationContext(Dispatcher.CurrentDispatcher));
        if (Application.Current is null)
        {
            var application = new Application { ShutdownMode = ShutdownMode.OnExplicitShutdown };
            using var styles = typeof(Program).Assembly.GetManifestResourceStream("ActualAppStyles.xaml")!;
            var source = XDocument.Load(styles).Root!;
            XNamespace presentation = "http://schemas.microsoft.com/winfx/2006/xaml/presentation";
            var resources = source.Element(presentation + "Application.Resources")!;
            var dictionary = resources.Element(presentation + "ResourceDictionary") ?? new XElement(presentation + "ResourceDictionary", source.Attributes().Where(attribute => attribute.IsNamespaceDeclaration), resources.Elements());
            application.Resources = (ResourceDictionary)XamlReader.Parse(dictionary.ToString(), new ParserContext { BaseUri = new Uri("pack://application:,,,/agentdock-tray;component/") });
        }
        using var runtime = new RuntimeService(root);
        var window = new ExecutionWindow(runtime);
        try
        {
            var field = typeof(ExecutionWindow).GetField("_callsById", BindingFlags.Instance | BindingFlags.NonPublic)!;
            var canonical = (Dictionary<string, ExecutionCallRow>)field.GetValue(window)!;
            ExecutionCallRow Row(int index) => new(JsonSerializer.SerializeToElement(new {
                call_id = "call_" + index.ToString("x32"), conversation_id = LocalFixture.ConversationA,
                tool_name = index == 1 ? "exec_command" : "session_observe", status = "succeeded",
                origin_call_id = index == 1 ? "" : "call_" + 1.ToString("x32"), routine_observation = index != 1,
                created_seq = index, updated_seq = index, rpc_elapsed_ms = 5000
            }));
            for (var index = 1; index <= 21; index++) { var row = Row(index); canonical.Add(row.Id, row); }
            InvokeExecution(window, "SyncInsertionTimeline");
            Require(window.Calls.Count == 1 && canonical.Count == 21, "Window projection lost audit identities or failed to fold observations.");
            var origin = window.Calls.Single();
            foreach (var key in new[] { "CallRowTemplate", "DetailedCallRowTemplate" })
            {
                var template = (DataTemplate)((FrameworkElement)window.FindName("CallsList")).FindResource(key);
                var visual = (FrameworkElement)template.LoadContent();
                visual.DataContext = origin;
                visual.Measure(new Size(1200, 42)); visual.Arrange(new Rect(0, 0, 1200, 42)); visual.UpdateLayout();
                var badge = Descendants(visual).OfType<Button>().Single(button => button.Content?.ToString()?.Contains("观察 20") == true);
                Require(badge.Visibility == Visibility.Visible && badge.ToolTip?.ToString()?.Contains("21") != true, "Observation badge binding is invalid.");
                badge.RaiseEvent(new RoutedEventArgs(Button.ClickEvent));
                PumpUntil(() => window.Calls.Count == 21, TimeSpan.FromSeconds(3));
                Require(origin.ObservationsExpanded && canonical.Count == 21, "Template action did not restore independent observation rows.");
                badge.RaiseEvent(new RoutedEventArgs(Button.ClickEvent));
                PumpUntil(() => window.Calls.Count == 1, TimeSpan.FromSeconds(3));
                Require(!origin.ObservationsExpanded && canonical.Count == 21, "Template collapse deleted canonical rows.");
            }
            canonical.Remove(origin.Id);
            InvokeExecution(window, "SyncInsertionTimeline");
            Require(window.Calls.Count == 20, "Pruning the original command hid orphan observations.");
        }
        finally { window.Close(); }
    }
}
