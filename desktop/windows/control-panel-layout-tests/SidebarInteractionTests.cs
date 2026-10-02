using System.IO;
using System.Net;
using System.Net.Http;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Windows;
using System.Windows.Automation.Peers;
using System.Windows.Automation.Provider;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Input;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Threading;
using AgentDock.ControlPanel;

internal static class SidebarInteractionTests
{
    private const BindingFlags Private = BindingFlags.Instance | BindingFlags.NonPublic;
    private static T Field<T>(object owner, string name) => (T)owner.GetType().GetField(name, Private)!.GetValue(owner)!;
    private static void Set(object owner, string name, object? value) => owner.GetType().GetField(name, Private)!.SetValue(owner, value);
    private static void PumpUntil(Func<bool> finished)
    {
        if (finished()) return;
        var deadline = DateTime.UtcNow.AddSeconds(10);
        var frame = new DispatcherFrame();
        var timer = new DispatcherTimer(DispatcherPriority.Background) { Interval = TimeSpan.FromMilliseconds(1) };
        timer.Tick += (_, _) => { if (finished() || DateTime.UtcNow >= deadline) frame.Continue = false; };
        timer.Start();
        try { Dispatcher.PushFrame(frame); } finally { timer.Stop(); }
        if (!finished()) throw new TimeoutException("Sidebar fixture did not settle.");
    }
    private static void Await(Task task) { PumpUntil(() => task.IsCompleted); task.GetAwaiter().GetResult(); }
    private static void Reload(ExecutionWindow window) => Await((Task)typeof(ExecutionWindow).GetMethod("LoadSidebarAsync", Private, null, [typeof(SidebarNavigationState)], null)!.Invoke(window, [null])!);
    private static IEnumerable<DependencyObject> Children(DependencyObject root)
    {
        yield return root;
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(root); index++)
            foreach (var child in Children(VisualTreeHelper.GetChild(root, index))) yield return child;
    }
    private static Button More(ExecutionWindow window, string project)
    {
        var footer = window.Objects.Single(row => row.IsGroupFooter && row.WorkspaceKey.Id == project);
        var list = (ListBox)window.FindName("ObjectsList");
        var root = (FrameworkElement)window.Content;
        root.Measure(new Size(1280, 900)); root.Arrange(new Rect(0, 0, 1280, 900)); root.UpdateLayout();
        list.ScrollIntoView(footer); root.UpdateLayout();
        return Children(root).OfType<Button>().Single(button => button.Name == "ProjectMore" && ReferenceEquals(button.DataContext, footer));
    }
    private static Expander Project(ExecutionWindow window, string project)
    {
        var root = (FrameworkElement)window.Content;
        root.Measure(new Size(1280, 900)); root.Arrange(new Rect(0, 0, 1280, 900)); root.UpdateLayout();
        return Children(root).OfType<Expander>().Single(expander =>
            expander.DataContext is CollectionViewGroup { Name: WorkspaceGroupKey key } && key.Id == project);
    }
    private static void Click(ExecutionWindow window, string project, bool preview = false)
    {
        var button = More(window, project);
        if (preview)
        {
            // The button-specific preview is Direct, not Tunnel. Raising it
            // on the child bypasses the real ListBoxItem event setter. Start
            // with Mouse.PreviewMouseDown so WPF reraises the left-button
            // event at each ancestor, matching the actual input route.
            var input = new MouseButtonEventArgs(Mouse.PrimaryDevice, Environment.TickCount, MouseButton.Left)
                { RoutedEvent = Mouse.PreviewMouseDownEvent, Source = button };
            button.RaiseEvent(input);
            if (!input.Handled) throw new InvalidOperationException("Footer input was not consumed by the production preview route.");
        }
        else button.RaiseEvent(new RoutedEventArgs(Button.ClickEvent, button));
    }
    private static void Settled(ExecutionWindow window) => PumpUntil(() => !Field<bool>(window, "_sidebarLoading") && Field<HashSet<string>>(window, "_sidebarPaging").Count == 0);

    private static void KeyboardPage(ExecutionWindow window,string project,Key key)
    {
        var button=More(window,project);
        var source=PresentationSource.FromVisual(button) ?? throw new InvalidOperationException("Keyboard test needs an actual presentation source");
        button.Focus();Keyboard.Focus(button);
        button.RaiseEvent(new KeyEventArgs(Keyboard.PrimaryDevice,source,Environment.TickCount,key){RoutedEvent=Keyboard.KeyDownEvent});
        button.RaiseEvent(new KeyEventArgs(Keyboard.PrimaryDevice,source,Environment.TickCount,key){RoutedEvent=Keyboard.KeyUpEvent});
    }

    internal static void Run(Action<bool, string> check)
    {
        var previousContext = SynchronizationContext.Current;
        SynchronizationContext.SetSynchronizationContext(new DispatcherSynchronizationContext(Dispatcher.CurrentDispatcher));
        var root = Path.Combine(Path.GetTempPath(), "agentdock-sidebar-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        using var runtime = new RuntimeService(root);
        using var handler = new FixtureTransport();
        using var client = new ActivityClient(_ => Task.FromResult(new ActivityConnection(new Uri("http://127.0.0.1:1"), "fixture-only")), handler);
        var window = new ExecutionWindow(runtime, client);
        var loaded = typeof(ExecutionWindow).GetMethod("Window_Loaded", Private)!;
        window.RemoveHandler(FrameworkElement.LoadedEvent, Delegate.CreateDelegate(typeof(RoutedEventHandler), window, loaded));
        Set(window, "_initialized", true);
        Set(window, "_sidebarStreamTask", Task.CompletedTask);
        var navigation = (SidebarNavigationState)typeof(ExecutionWindow).GetMethod("CurrentNavigation", Private)!.Invoke(window, null)!;
        navigation.For("A").Expand(); navigation.For("B").Expand();
        Set(window, "_selected", new ExecutionObject { Id = "A-0", Kind = "conversation" });
        var closed = false;
        try
        {
            window.ShowInTaskbar = false; window.ShowActivated = false; window.Left = -10000; window.Top = -10000; window.Show();
            var handle = new WindowInteropHelper(window).Handle;
            check(handle != IntPtr.Zero, "Sidebar fixture has no native WPF window.");
            Reload(window);
            check(window.Objects.Count(row => !row.IsGroupFooter) == 10, "Initial project pages were not retained.");
            var projectA = Project(window, "A");
            var projectB = Project(window, "B");
            check(projectA.IsExpanded && projectB.IsExpanded, "Initial expanded project state was not rendered.");
            handler.PromoteB = true;
            Reload(window); Settled(window);
            check(window.Objects.First(row => !row.IsGroupFooter).WorkspaceKey.Id == "A",
                "Passive activity refresh reordered an existing project.");
            var renderedOrder = CollectionViewSource.GetDefaultView(window.Objects).Groups!
                .Cast<CollectionViewGroup>().Select(group => ((WorkspaceGroupKey)group.Name).Id).ToArray();
            check(renderedOrder.SequenceEqual(["A", "B"]),
                "Rendered project order changed during a passive activity refresh.");
            check(ReferenceEquals(projectA, Project(window, "A")) && ReferenceEquals(projectB, Project(window, "B")),
                "A project activity update replaced group containers instead of moving them incrementally.");
            check(projectA.IsExpanded && projectB.IsExpanded,
                "A project activity update changed the visible expansion state.");
            navigation.For("A").Collapse(); Reload(window);
            var collapsedA = Project(window, "A");
            check(!collapsedA.IsExpanded, "A deliberate project collapse was not rendered.");
            handler.PromoteB = false;
            Reload(window); Settled(window);
            check(ReferenceEquals(collapsedA, Project(window, "A")) && !collapsedA.IsExpanded,
                "A collapsed project was recreated or expanded during an activity reorder.");
            navigation.For("A").Expand(); Reload(window);
            handler.IncludeC = true;
            Reload(window); Settled(window);
            var addedOrder = CollectionViewSource.GetDefaultView(window.Objects).Groups!
                .Cast<CollectionViewGroup>().Select(group => ((WorkspaceGroupKey)group.Name).Id).ToArray();
            check(addedOrder.SequenceEqual(["A", "B", "C"]),
                "A newly discovered project was not appended without disturbing existing projects.");
            check(ReferenceEquals(projectA, Project(window, "A")) && ReferenceEquals(projectB, Project(window, "B")),
                "Adding a project replaced existing group containers.");
            _ = Project(window, "C");
            handler.IncludeC = false;
            Reload(window); Settled(window);
            check(!CollectionViewSource.GetDefaultView(window.Objects).Groups!.Cast<CollectionViewGroup>()
                    .Any(group => ((WorkspaceGroupKey)group.Name).Id == "C") &&
                ReferenceEquals(projectA, Project(window, "A")) && ReferenceEquals(projectB, Project(window, "B")),
                "Removing a project disturbed retained group containers.");
            var before = handler.Requests;
            Click(window, "A", preview: true); Settled(window);
            check(handler.Requests == before + 1 && navigation.For("A").HistoryLimit == 20,
                $"Preview event did not perform exactly one page advance: requests {before}->{handler.Requests}, limit={navigation.For("A").HistoryLimit}, unexpected={handler.UnexpectedRequests}.");
            var completion = handler.DelayNext();
            before = handler.Requests;
            Click(window, "A");
            Click(window, "A");
            check(handler.Requests == before + 1, "Repeated click entered an in-progress project twice.");
            Click(window, "B");
            completion.TrySetResult(); Settled(window);
            check(navigation.For("A").HistoryLimit == 40 && navigation.For("B").HistoryLimit == 20, "Independent queued projects lost their page state.");
            check(handler.MaximumInFlight == 1, "Window sidebar requests raced rather than applying their navigation snapshot.");
            foreach (var failure in new[] { "http", "json" })
            {
                var old = window.Objects.Select(row => row.Id).ToArray();
                var limit = navigation.For("A").HistoryLimit;
                handler.Failure = failure;
                Click(window, "A"); Settled(window);
                check(navigation.For("A").HistoryLimit == limit && window.Objects.Select(row => row.Id).SequenceEqual(old), "Failed pagination changed the committed cursor or displayed rows.");
                check(((FrameworkElement)window.FindName("WarningPanel")).Visibility == Visibility.Visible, "Pagination failure did not expose a retryable warning.");
            }
            var retainedA = window.Objects.Where(row => !row.IsGroupFooter && row.WorkspaceKey.Id == "A").Select(row => row.Id).ToArray();
            handler.Failure = "group";
            Reload(window); Settled(window);
            check(window.Objects.Where(row => !row.IsGroupFooter && row.WorkspaceKey.Id == "A").Select(row => row.Id).SequenceEqual(retainedA),
                "A malformed project replaced its last trusted project snapshot.");
            check(window.Objects.First(row => row.WorkspaceKey.Id == "B").WorkspaceKey.Title == "Project B refreshed",
                "A malformed project prevented an independent healthy project from updating.");
            check(((TextBlock)window.FindName("WarningText")).Text.Contains("SIDEBAR_CONVERSATION_ID_MISSING"),
                "Group-local recovery did not expose its stable error code.");
            Reload(window); Settled(window);
            var peer = new ButtonAutomationPeer(More(window, "B"));
            ((IInvokeProvider)peer.GetPattern(PatternInterface.Invoke)).Invoke();
            PumpUntil(() => navigation.For("B").HistoryLimit == 40); Settled(window);
            check(navigation.For("B").HistoryLimit == 40, "Automation invocation did not share the paging operation.");
            // Search invalidates an old request and restores the independent
            // original navigation state when it is cleared.
            completion = handler.DelayNext(); Click(window, "A");
            var search = (TextBox)window.FindName("SearchBox");
            search.Text = "needle"; Reload(window);
            Field<DispatcherTimer>(window, "_filterTimer").Stop();
            completion.TrySetResult(); Settled(window);
            check(handler.Cancelled > 0 && navigation.For("A").HistoryLimit == 40, "Stale search response committed old navigation.");
            search.Text = ""; Reload(window); Field<DispatcherTimer>(window, "_filterTimer").Stop();
            for (var cycle = 0; cycle < 100; cycle++)
            {
                navigation.For("A").Collapse(); Reload(window);
                navigation.For("A").Expand(); Reload(window);
                Click(window, "A"); Settled(window);
                Click(window, "A"); Settled(window);
                check(navigation.For("A").HistoryLimit == 40 && window.Objects.Select(row => row.Id).Distinct().Count() == window.Objects.Count, "Expand/collapse cycle produced duplicate rows or incorrect pagination.");
            }
            handler.Failure="empty";Click(window,"A");Settled(window);
            var emptyFooter=window.Objects.Single(row=>row.IsGroupFooter&&row.WorkspaceKey.Id=="A");
            check(!emptyFooter.HasMore&&!emptyFooter.CanLoadMore,"Empty final page remained pageable");
            var objectList=(ListBox)window.FindName("ObjectsList");objectList.UpdateLayout();
            if(objectList.ItemContainerGenerator.ContainerFromItem(emptyFooter) is ListBoxItem emptyContainer)
                check(emptyContainer.Visibility==Visibility.Collapsed&&emptyContainer.Height==0,"Empty footer anchor is visible instead of collapsed");
            Reload(window);
            handler.DeletedId="A-1";Reload(window);
            check(!window.Objects.Any(row=>row.Id=="A-1")&&window.Objects.Select(row=>row.Id).Distinct().Count()==window.Objects.Count,"Deleted history row survived a replacement page");
            handler.DeletedId="";Reload(window);
            // A real HWND is created only in the isolated runner, offscreen and
            // without application startup. Use the actual Button class handlers;
            // never call its click handler directly to claim keyboard coverage.
            foreach(var key in new[]{Key.Return,Key.Space})
            {
                before=handler.Requests;var limit=navigation.For("B").HistoryLimit;
                KeyboardPage(window,"B",key);Settled(window);
                check(handler.Requests==before+1&&navigation.For("B").HistoryLimit==limit+20,"Keyboard input did not advance exactly once: "+key);
            }
            var scope = (string)typeof(ExecutionWindow).GetMethod("SidebarScope", Private)!.Invoke(window, null)!;
            var recordFailure = typeof(ExecutionWindow).GetMethod("RecordSidebarFailures", Private)!;
            var retryAllowed = typeof(ExecutionWindow).GetMethod("SidebarAutomaticRefreshAllowed", Private)!;
            var resetBudget = typeof(ExecutionWindow).GetMethod("ResetSidebarRecoveryBudget", Private)!;
            var clearFailure = typeof(ExecutionWindow).GetMethod("ClearSidebarFailure", Private)!;
            var repeatedFailure = new SidebarProtocolException("SIDEBAR_TRANSPORT_UNAVAILABLE", "page", "transport", handler.Requests);
            for (var attempt = 0; attempt < 6; attempt++)
                recordFailure.Invoke(window, [scope, new[] { repeatedFailure }, true]);
            check(!(bool)retryAllowed.Invoke(window, null)! && !Field<bool>(window, "_sidebarDirty"),
                "An unchanged sidebar failure retained an unbounded automatic retry path.");
            var warning = (FrameworkElement)window.FindName("WarningPanel");
            var closeWarning = Children(warning).OfType<Button>().Single(button => Equals(button.Content, "关闭"));
            closeWarning.RaiseEvent(new RoutedEventArgs(Button.ClickEvent, closeWarning));
            check(warning.Visibility == Visibility.Collapsed && !(bool)retryAllowed.Invoke(window, null)!,
                "Closing the warning incorrectly marked the underlying sidebar failure as repaired.");
            resetBudget.Invoke(window, null);
            check((bool)retryAllowed.Invoke(window, null)!, "An explicit recovery trigger did not reset the bounded retry budget.");
            clearFailure.Invoke(window, [scope]);
            completion = handler.DelayNext(); Click(window, "A");
            window.Close(); closed = true;
            completion.TrySetResult(); Settled(window);
            check(handler.InFlight == 0 && window.Objects.All(row => !row.IsPaging), "Closing a pending page left transport or paging state behind.");
            check(handler.UnexpectedRequests == 0, "Sidebar fixture attempted an undeclared backend action.");
        }
        finally
        {
            if (!closed) window.Close();
            SynchronizationContext.SetSynchronizationContext(previousContext);
            if (Directory.Exists(root)) Directory.Delete(root, true);
        }
    }

    private sealed class FixtureTransport : HttpMessageHandler
    {
        internal int Requests, InFlight, MaximumInFlight, Cancelled, UnexpectedRequests;
        internal string Failure = "";
        internal string DeletedId = "";
        internal bool PromoteB;
        internal bool IncludeC;
        private TaskCompletionSource? _next;
        internal TaskCompletionSource DelayNext() => _next = new(TaskCreationOptions.RunContinuationsAsynchronously);
        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token)
        {
            if (request.RequestUri!.AbsolutePath != "/internal/runtime/execution/sidebar")
            {
                UnexpectedRequests++;
                throw new HttpRequestException("Fixture forbids all non-sidebar backend actions: " + request.RequestUri.AbsolutePath);
            }
            Requests++; InFlight++; MaximumInFlight = Math.Max(MaximumInFlight, InFlight);
            var delay = _next; _next = null;
            try
            {
                if (delay is not null) await delay.Task.WaitAsync(token);
                using var parsed = JsonDocument.Parse(await request.Content!.ReadAsStringAsync(token));
                var body = parsed.RootElement;
                var failure = Failure; Failure = "";
                if (failure == "http") return Reply("{\"error\":{\"message\":\"isolated transient error\"}}", HttpStatusCode.ServiceUnavailable);
                if (failure == "json") return Reply("{invalid-json");
                var modes = body.GetProperty("modes"); var limits = body.GetProperty("limits");
                var groupIds = IncludeC ? new[] { "A", "B", "C" } : new[] { "A", "B" };
                var groups = groupIds.Select(id =>
                {
                    var mode = modes.TryGetProperty(id, out var value) ? value.GetString() : "auto";
                    var limit = limits.TryGetProperty(id, out value) ? value.GetInt32() : 5;
                    var empty = id=="A"&&failure=="empty";
                    var count = mode == "collapsed" || empty ? 0 : limit;
                    var malformed = id == "A" && failure == "group";
                    var title = id == "B" && failure == "group" ? "Project B refreshed" : "Project " + id;
                    var activity = DateTimeOffset.Parse(id == "C" ? "2026-10-02T14:20:00Z" : id == "B" && PromoteB ? "2026-10-02T14:10:00Z" : id == "A" ? "2026-10-02T14:00:00Z" : "2026-10-02T13:50:00Z");
                    return new { workspace_id = id, title, total = 500, recent_count = 5, execution_count = 0, mode, history_limit = limit, history_cursor = "cursor_" + id, has_more = mode != "collapsed"&&!empty, last_activity_at = activity, conversations = Enumerable.Range(0, count).Where(index=>id+"-"+index!=DeletedId).Select(index => new { conversation_id = malformed && index == 0 ? "" : id + "-" + index, title = "Conversation " + index, task_ids = Array.Empty<string>(), state = new { workspace_id = id }, statistics = new { } }).ToArray() };
                }).ToArray();
                // The service preserves the requested selected conversation even
                // when its group is collapsed or a search does not show its row.
                var selectedId = body.GetProperty("selected_id").GetString() ?? "A-0";
                var selectedProject = selectedId.Split('-')[0];
                var selected = new { conversation_id = selectedId, title = "Selected conversation", task_ids = Array.Empty<string>(), state = new { workspace_id = selectedProject }, statistics = new { } };
                return Reply(JsonSerializer.Serialize(new { latest_seq = Requests, groups, selected, server_now = DateTimeOffset.UtcNow }));
            }
            catch (OperationCanceledException) { Cancelled++; throw; }
            finally { InFlight--; }
        }
        private static HttpResponseMessage Reply(string text, HttpStatusCode code = HttpStatusCode.OK) => new(code) { Content = new StringContent(text, Encoding.UTF8, "application/json") };
    }
}
