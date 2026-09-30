using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using AgentDock.ControlPanel;

// Exercises production WPF controls and async methods against an in-memory
// transport. No App startup, real bearer token, installed Core or installer.
internal static class FrontendSafetyTests
{
    private const BindingFlags Private = BindingFlags.Instance | BindingFlags.NonPublic;
    private static object? Invoke(object owner, string name, params object?[] args) =>
        owner.GetType().GetMethod(name, Private)!.Invoke(owner, args);
    private static T Field<T>(object owner, string name) => (T)owner.GetType().GetField(name, Private)!.GetValue(owner)!;
    private static void Set(object owner, string name, object? value) => owner.GetType().GetField(name, Private)!.SetValue(owner, value);
    private static T Named<T>(Window owner, string name) where T : class => (T)owner.FindName(name);
    private static Task Run(object owner, string name, params object?[] args) => (Task)Invoke(owner, name, args)!;
    private static void PumpUntil(Func<bool> finished)
    {
        if (finished()) return;
        var deadline = Stopwatch.StartNew();
        var frame = new DispatcherFrame();
        var timer = new DispatcherTimer(DispatcherPriority.Background) { Interval = TimeSpan.FromMilliseconds(1) };
        timer.Tick += (_, _) => { if (finished() || deadline.Elapsed > TimeSpan.FromSeconds(15)) frame.Continue = false; };
        timer.Start();
        try { Dispatcher.PushFrame(frame); }
        finally { timer.Stop(); }
        if (!finished()) throw new TimeoutException("Frontend safety fixture did not settle.");
    }
    private static void Await(Task task) { PumpUntil(() => task.IsCompleted); task.GetAwaiter().GetResult(); }
    private static void AwaitCancelled(Task task)
    {
        try { Await(task); } catch (OperationCanceledException) { }
    }
    private static bool Failed<T>(Task task) where T : Exception
    {
        try { Await(task); return false; } catch (T) { return true; }
    }
    private static IEnumerable<DependencyObject> LogicalChildren(DependencyObject parent)
    {
        yield return parent;
        foreach (var child in LogicalTreeHelper.GetChildren(parent).OfType<DependencyObject>())
            foreach (var item in LogicalChildren(child)) yield return item;
    }
    private static void Layout(Window window)
    {
        var root = (FrameworkElement)window.Content;
        root.Measure(new Size(1280, 800)); root.Arrange(new Rect(0, 0, 1280, 800)); root.UpdateLayout();
    }
    private static void StopSearchTimer(ExecutionWindow window) => Field<DispatcherTimer>(window, "_filterTimer").Stop();
    private static void ReloadSidebar(ExecutionWindow window) => Await((Task)typeof(ExecutionWindow)
        .GetMethod("LoadSidebarAsync", Private, null, [typeof(SidebarNavigationState)], null)!.Invoke(window, [null])!);
    private static void SelectTask(ExecutionWindow window, string task, string branch) => Invoke(window, "BeginTaskSelection", task, branch);

    internal static void Run(Action<bool, string> check)
    {
        var previous = SynchronizationContext.Current;
        SynchronizationContext.SetSynchronizationContext(new DispatcherSynchronizationContext(Dispatcher.CurrentDispatcher));
        try
        {
            var failures = new List<Exception>();
            var scenarios = new (string Name, Action<Action<bool, string>> Run)[]
            {
                ("selection", SelectionSnapshots), ("tasks", TaskTargetsAndRefresh),
                ("stop", StopAvailability), ("close", InitializationClose),
                ("export", FixedExports), ("panel", PanelMutationGuards)
            };
            for (var iteration = 0; iteration < 3; iteration++)
            {
                foreach (var scenario in scenarios)
                {
                    try { scenario.Run(check); Console.WriteLine($"Safety {scenario.Name} iteration {iteration + 1}: passed"); }
                    catch (Exception error)
                    {
                        failures.Add(error);
                        Console.Error.WriteLine($"Safety {scenario.Name} iteration {iteration + 1}: {error}");
                    }
                }
            }
            if (failures.Count > 0) throw new AggregateException("Native frontend safety regressions failed.", failures);
            Console.WriteLine("WIN-01..WIN-08 native WPF safety regressions passed (3 iterations, fixture transport only).");
        }
        finally { SynchronizationContext.SetSynchronizationContext(previous); }
    }

    private static void SelectionSnapshots(Action<bool, string> check)
    {
        using var f = new Fixture(); var w = f.Window;
        ReloadSidebar(w);
        Layout(w);
        Await(Run(w, "SelectAllObjectsAsync"));
        check(Field<string[]>(w, "_frozenSelection").SequenceEqual(new[] { "A1", "A2" }), "WIN-01: initial select-all failed.");
        check(Named<ListBox>(w, "ObjectsList").SelectedItems.Cast<ExecutionObject>().Count(row => !row.IsGroupFooter) == 2,
            "WIN-01: fixture never materialized the two selected WPF rows.");
        Named<TextBox>(w, "SearchBox").Text = "A2"; StopSearchTimer(w);
        check(Field<string[]?>(w, "_frozenSelection") is null, "WIN-01: search kept a frozen selection.");
        ReloadSidebar(w);
        var selected = (string[])Invoke(w, "SelectedObjectIds")!;
        check(selected.SequenceEqual(new[] { "A2" }), $"WIN-01: narrowed selection=[{string.Join(",", selected)}], cachedScope={Field<string>(w, "_sidebarScope")}, requestedScope={Invoke(w, "SidebarScope")}, rows=[{string.Join(",", w.Objects.Select(row => row.Id))}], selectedRows=[{string.Join(",", Named<ListBox>(w, "ObjectsList").SelectedItems.Cast<ExecutionObject>().Select(row => row.Id))}], warning={Named<TextBlock>(w, "WarningText").Text}");
        Await(Run(w, "BatchAsync", "conversation", selected, "archive", "", null));
        check(f.Transport.Batches.Single().SequenceEqual(new[] { "A2" }), "WIN-01: actual batch request had wrong IDs.");

        var gate = f.Transport.DelayNext("/internal/runtime/conversations", selection: true);
        var pending = Run(w, "SelectAllObjectsAsync"); PumpUntil(() => gate.Entered);
        Named<TextBox>(w, "SearchBox").Text = "other";
        Named<TextBox>(w, "SearchBox").Text = "A2"; StopSearchTimer(w);
        gate.Release.TrySetResult(); AwaitCancelled(pending);
        check(Field<string[]?>(w, "_frozenSelection") is null, "WIN-01: A-B-A accepted an old select-all response.");

        gate = f.Transport.DelayNext("/internal/runtime/conversations", selection: true);
        pending = Run(w, "SelectAllObjectsAsync"); PumpUntil(() => gate.Entered);
        Named<ListBox>(w, "ObjectsList").UnselectAll();
        gate.Release.TrySetResult(); AwaitCancelled(pending);
        check(Field<string[]?>(w, "_frozenSelection") is null, "WIN-01: pending select-all overrode manual selection.");

        gate = f.Transport.DelayNext("/internal/runtime/conversations", selection: true);
        pending = Run(w, "SelectAllObjectsAsync"); PumpUntil(() => gate.Entered);
        Await(Run(w, "SetConversationViewAsync", "trash"));
        gate.Release.TrySetResult(); AwaitCancelled(pending);
        check(Field<string[]?>(w, "_frozenSelection") is null, "WIN-01: old select-all crossed view boundaries.");
    }

    private static void TaskTargetsAndRefresh(Action<bool, string> check)
    {
        using var f = new Fixture(); var w = f.Window;
        f.Transport.TaskLinked = true;
        SelectTask(w, "task-A", "main");
        Invoke(w, "OpenDetails", "Task A", Named<FrameworkElement>(w, "TaskDetailsPanel"));
        Await(Run(w, "LoadTaskAsync", "task-A", "main", true));
        var gate = f.Transport.DelayNext("/internal/runtime/tasks/task-A");
        Named<ComboBox>(w, "BranchCombo").SelectedValue = "fork-B";
        PumpUntil(() => gate.Entered);
        check(!Named<Button>(w, "ContinueBranchButton").IsEnabled && !Named<Button>(w, "TaskMenuButton").IsEnabled,
            "WIN-02: task actions remained enabled while new branch was loading.");
        var confirmations = 0;
        Await(w.ContinueSelectedBranchAsync(_ => { confirmations++; return true; }));
        check(confirmations == 0 && f.Transport.Controls.Count == 0, "WIN-02: unloaded branch submitted the old target.");
        gate.Release.TrySetResult(); PumpUntil(() => Field<int>(w, "_taskReads") == 0);
        Await(w.ContinueSelectedBranchAsync(target => target.TaskId == "task-A" && target.ThreadId == "fork-B"));
        check(f.Transport.Controls.Last().Text("thread_id") == "fork-B", "WIN-02: actual continue request used the wrong branch.");
        var count = f.Transport.Controls.Count;
        Await(w.ContinueSelectedBranchAsync(_ => { SelectTask(w, "task-B", "main"); return true; }));
        check(f.Transport.Controls.Count == count, "WIN-02: selection changed during confirmation but request was sent.");

        // Inverted responses within one conversation must not replace the current target.
        gate = f.Transport.DelayNext("/internal/runtime/tasks/task-A");
        SelectTask(w, "task-A", "fork-B");
        var stale = Run(w, "LoadTaskAsync", "task-A", "fork-B", true); PumpUntil(() => gate.Entered);
        SelectTask(w, "task-B", "main"); Await(Run(w, "LoadTaskAsync", "task-B", "main", true));
        gate.Release.TrySetResult(); Await(stale);
        check(Field<TaskActionTarget>(w, "_taskActionTarget").TaskId == "task-B", "WIN-02: older task response replaced newer selection.");

        var target = Field<TaskActionTarget>(w, "_taskActionTarget");
        gate = f.Transport.DelayNext("/internal/runtime/activity/control");
        var cancel = w.CancelSelectedTaskAsync(target, "fixture cancellation"); PumpUntil(() => gate.Entered);
        SelectTask(w, "task-A", "fork-B"); Await(Run(w, "LoadTaskAsync", "task-A", "fork-B", true));
        var oldReads = f.Transport.Paths.Count(path => path == "/internal/runtime/tasks/task-B");
        gate.Release.TrySetResult(); Await(cancel);
        check(f.Transport.Paths.Count(path => path == "/internal/runtime/tasks/task-B") == oldReads,
            "WIN-02: cancellation completion reloaded a task after selection changed.");

        // Use the actual tick path with open details, unchanged binding, and updated data.
        f.Transport.StepVersion = 1;
        Set(w, "_ticks", 4); Set(w, "_lastSidebarRefresh", Stopwatch.GetTimestamp()); Set(w, "_streamConnected", true);
        Await(Run(w, "TickAsync"));
        check(Named<TextBlock>(w, "CurrentTaskStatus").Text == "1/1" && Named<TextBlock>(w, "TaskStepsText").Text.Contains("step-1"),
            "WIN-05: open details stopped summary or steps refresh.");
        check(Named<TextBlock>(w, "MilestonesText").Text == "milestone-1" && Field<string>(w, "_branch") == "fork-B",
            "WIN-05: refresh lost milestones or selected branch.");
        f.Transport.StepVersion = 2;
        Await(Run(w, "RefreshExecutionAsync"));
        check(Named<TextBlock>(w, "MilestonesText").Text == "milestone-2" && Field<string>(w, "_branch") == "fork-B",
            "WIN-05: manual refresh failed or reset branch selection.");
        f.Transport.LongDetails = true;
        Await(Run(w, "LoadTaskAsync", "task-A", "fork-B", true)); Layout(w);
        var scroll = Named<ScrollViewer>(w, "TaskDetailsScroll");
        scroll.ScrollToVerticalOffset(40); Layout(w); var offset = scroll.VerticalOffset;
        Await(Run(w, "LoadTaskAsync", "task-A", "fork-B", true)); Layout(w);
        check(Math.Abs(scroll.VerticalOffset - offset) < 1, "WIN-05: passive detail refresh moved the scroll position.");
    }

    private static void StopAvailability(Action<bool, string> check)
    {
        using var f = new Fixture(); var w = f.Window;
        var row = Field<ExecutionObject>(w, "_selected");
        var clock = Field<ConversationActivityClock>(w, "_activityClock");
        row.InFlight = true;
        foreach (var seconds in new[] { 179, 180, 181 })
        {
            var now = DateTimeOffset.UtcNow;
            row.LastToolCallAt = now.AddSeconds(-seconds); clock.Synchronize(now);
            Invoke(w, "UpdateComposerAvailability");
            check(Named<Button>(w, "InsertButton").IsEnabled == (seconds < 180), "WIN-03: insertion time boundary changed.");
            check(Named<Button>(w, "StopConversationButton").IsEnabled && Named<Button>(w, "StopConversationButton").Visibility == Visibility.Visible,
                "WIN-03: long-running conversation lost its stop button.");
        }
        check(!LogicalChildren(Named<FrameworkElement>(w, "InsertionPanel")).Contains(Named<Button>(w, "StopConversationButton")),
            "WIN-03: stop still belongs to the expiring insertion pane.");
        row.Terminated = true; Invoke(w, "UpdateComposerAvailability");
        check(!Named<Button>(w, "StopConversationButton").IsEnabled, "WIN-03: terminated conversation can be stopped again.");
        row.Terminated = false; row.Trashed = true; Invoke(w, "UpdateComposerAvailability");
        check(!Named<Button>(w, "StopConversationButton").IsEnabled, "WIN-03: trash row retained a live stop action.");
    }

    private static void InitializationClose(Action<bool, string> check)
    {
        foreach (var path in new[] { "/internal/runtime/permissions/effective", "/internal/runtime/execution", "/internal/runtime/execution/sidebar" })
        {
            using var f = new Fixture(initialized: false); var w = f.Window;
            var gate = f.Transport.DelayNext(path);
            var initialize = Run(w, "InitializeWindowAsync"); PumpUntil(() => gate.Entered);
            var requests = f.Transport.Paths.Count;
            w.Close(); gate.Release.TrySetResult(); AwaitCancelled(initialize);
            check(!Field<DispatcherTimer>(w, "_pulse").IsEnabled, "WIN-06: closed initialization restarted the timer at " + path);
            check(Field<TaskCompletionSource>(w, "_ready").Task.IsCanceled, "WIN-06: closed initialization announced ready.");
            check(f.Transport.Paths.Count == requests, "WIN-06: initialization issued more requests after close.");
        }
    }

    private static void FixedExports(Action<bool, string> check)
    {
        using var f = new Fixture(); var w = f.Window;
        var ids = new[] { "B" };
        var normal = ConversationExportScope.Capture(ids, new ExecutionObject { Id = "B" }); ids[0] = "C";
        var unknown = ConversationExportScope.Capture([], new ExecutionObject { IsUnknown = true });
        var orphanRow = new ExecutionObject { Id = "orphan-B", IsOrphan = true };
        var orphan = ConversationExportScope.Capture([], orphanRow); orphanRow.Id = "changed";
        foreach (var pair in new[] { (normal, "call-B"), (unknown, "call-unattributed"), (orphan, "call-orphan-B") })
        {
            // Current selection deliberately never equals the menu target.
            var read = w.ReadConversationExportAsync(pair.Item1); Await(read);
            check(read.Result.Single().Text("call_id") == pair.Item2, "WIN-08: actual export read followed current selection or mutable menu data.");
        }
        check(Failed<InvalidOperationException>(w.ReadConversationExportAsync(ConversationExportScope.Capture([], null))),
            "WIN-08: invalid target silently produced an empty successful export.");
    }

    private static void PanelMutationGuards(Action<bool, string> check)
    {
        var root = Path.Combine(Path.GetTempPath(), "agentdock-panel-safety-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        using var runtime = new RuntimeService(root);
        var panel = new MainWindow(runtime);
        try
        {
            var inventory = new CapabilityInventory
            {
                Plugins = [new PluginCapabilityInfo { Name = "A", Enabled = true }, new PluginCapabilityInfo { Name = "B", Enabled = false }]
            };
            Set(panel, "_capabilityInventory", inventory);
            Invoke(panel, "RenderCapabilityInventory");
            var pages = Named<TabControl>(panel, "MainPages");
            pages.SelectedItem = pages.Items.OfType<TabItem>().Single(tab => Equals(tab.Tag, "capabilities"));
            Layout(panel);
            var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            var writes = 0; var reads = 0;
            var mutation = panel.RunCapabilityMutationAsync(async () => { writes++; await release.Task; }, () => { reads++; return Task.CompletedTask; });
            var controls = LogicalChildren(Named<StackPanel>(panel, "PluginListPanel")).OfType<Control>().Where(c => c is Button or CheckBox).ToArray();
            check(controls.Length >= 6 && controls.All(c => !c.IsEnabled), "WIN-04: plugin toggles, Heavy or delete remained enabled while writing.");
            check(Failed<InvalidOperationException>(panel.RunCapabilityMutationAsync(() => { writes++; return Task.CompletedTask; }, () => Task.CompletedTask)),
                "WIN-04: reentrant mutation was silently accepted.");
            check(writes == 1, "WIN-04: duplicate mutation reached backend.");
            release.TrySetResult(); Await(mutation);
            check(reads == 1 && Field<bool>(panel, "_capabilityControlsEnabled"), "WIN-04: completed mutation failed to read back and enable controls.");
            check(Failed<IOException>(panel.RunCapabilityMutationAsync(() => Task.FromException(new IOException("write failed")), () => { reads++; return Task.CompletedTask; })),
                "WIN-04: mutation failure was hidden.");
            check(reads == 2, "WIN-04: failed write skipped readback.");
            Await(panel.RunCapabilityMutationAsync(() => Task.CompletedTask, () => { inventory.Errors["plugins"] = "fixture unavailable"; return Task.CompletedTask; }));
            check(!Field<bool>(panel, "_capabilityControlsEnabled"), "WIN-04: unavailable inventory was made editable.");

            pages.SelectedItem = pages.Items.OfType<TabItem>().Single(tab => Equals(tab.Tag, "advanced"));
            Layout(panel);
            Set(panel, "_settingsLoaded", true);
            check(Named<TextBox>(panel, "PortTextBox").IsEnabled, "WIN-07: editor was not enabled before save.");
            Named<TextBox>(panel, "BrowserCdpUrlTextBox").Text = "http://127.0.0.1:9222";
            var settings = new ControlPanelSettings { BrowserCdpUrl = "http://127.0.0.1:9222", AcpProfiles = [new AcpProfileSettings { Id = "old", Kind = "custom" }] };
            release = new(TaskCreationOptions.RunContinuationsAsynchronously);
            var saves = 0;
            var save = panel.SaveSettingsSnapshotAsync(settings, async () => { saves++; await release.Task; });
            foreach (var name in new[] { "PortTextBox", "BrowserCdpUrlTextBox", "BrowserConnectionModeComboBox", "LanguageComboBox", "AcpAddCustomProfileButton", "SaveSettingsButton" })
                check(!Named<Control>(panel, name).IsEnabled, "WIN-07: editable during save: " + name);
            check(Failed<InvalidOperationException>(panel.SaveSettingsSnapshotAsync(settings, () => { saves++; return Task.CompletedTask; })) && saves == 1,
                "WIN-07: repeated save reached persistence.");
            release.TrySetResult(); Await(save);
            check(Named<Button>(panel, "SaveSettingsButton").IsEnabled, "WIN-07: successful save did not release busy state.");
            Named<TextBox>(panel, "BrowserCdpUrlTextBox").Text = "new-unsaved-draft";
            var profiles = Field<List<AcpProfileSettings>>(panel, "_acpProfiles"); profiles.Add(new AcpProfileSettings { Id = "new", Kind = "custom" });
            check(Failed<IOException>(panel.SaveSettingsSnapshotAsync(settings, () => Task.FromException(new IOException("save failed")))),
                "WIN-07: failed save was hidden.");
            check(Named<TextBox>(panel, "BrowserCdpUrlTextBox").Text == "new-unsaved-draft" && profiles.Any(p => p.Id == "new") && Named<StackPanel>(panel, "SettingsEditorPanel").IsEnabled,
                "WIN-07: failed save overwrote draft or retained disabled controls.");
        }
        finally
        {
            panel.CloseForReplacement();
            // Render telemetry drains on a worker. Wait for its bounded private
            // log handle before deleting the fixture; never hide a test failure.
            PumpUntil(() =>
            {
                try { if (Directory.Exists(root)) Directory.Delete(root, true); return true; }
                catch (IOException) { return false; }
            });
        }
    }

    private sealed class Fixture : IDisposable
    {
        private readonly string _root = Path.Combine(Path.GetTempPath(), "agentdock-safety-" + Guid.NewGuid().ToString("N"));
        private readonly RuntimeService _runtime;
        internal FixtureTransport Transport { get; } = new();
        internal ExecutionWindow Window { get; }
        internal Fixture(bool initialized = true)
        {
            Directory.CreateDirectory(_root); _runtime = new RuntimeService(_root);
            var client = new ActivityClient(_ => Task.FromResult(new ActivityConnection(new Uri("http://127.0.0.1:1"), "fixture-only")), Transport);
            Window = new ExecutionWindow(_runtime, client);
            var loaded = typeof(ExecutionWindow).GetMethod("Window_Loaded", Private)!;
            Window.RemoveHandler(FrameworkElement.LoadedEvent, Delegate.CreateDelegate(typeof(RoutedEventHandler), Window, loaded));
            Set(Window, "_updating", true);
            var row = ExecutionObject.From(JsonSerializer.SerializeToElement(new { conversation_id = "A2", title = "A2", task_ids = Array.Empty<string>(), state = new { workspace_id = "fixture" }, statistics = new { } }), "conversation");
            row.WorkspaceKey = new WorkspaceGroupKey("fixture", "Fixture");
            Window.Objects.Add(row);
            // Materialize bindings/templates before exercising native selection.
            Layout(Window);
            Await(Dispatcher.CurrentDispatcher.InvokeAsync(() => { }, DispatcherPriority.ContextIdle).Task);
            Named<ListBox>(Window, "ObjectsList").SelectedItem = row;
            Set(Window, "_selected", row); Set(Window, "_sidebarStreamTask", Task.CompletedTask);
            Set(Window, "_sidebarScope", "active\n");
            Set(Window, "_updating", false); Set(Window, "_initialized", initialized);
        }
        public void Dispose()
        {
            if (!Field<bool>(Window, "_closed")) Window.Close();
            _runtime.Dispose();
            if (Directory.Exists(_root)) Directory.Delete(_root, true);
        }
    }

    private sealed class DelayGate(string path, bool selection)
    {
        internal string Path { get; } = path;
        internal bool Selection { get; } = selection;
        internal bool Entered;
        internal TaskCompletionSource Release { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
    }
    private sealed class FixtureTransport : HttpMessageHandler
    {
        internal List<string> Paths { get; } = [];
        internal List<string[]> Batches { get; } = [];
        internal List<JsonElement> Controls { get; } = [];
        internal int StepVersion;
        internal bool TaskLinked, LongDetails;
        private DelayGate? _next;
        internal DelayGate DelayNext(string path, bool selection = false) => _next = new(path, selection);
        private static string Query(Uri uri, string name) => uri.Query.TrimStart('?').Split('&')
            .Select(part => part.Split('=', 2)).Where(parts => parts.Length == 2 && parts[0] == name)
            .Select(parts => Uri.UnescapeDataString(parts[1])).FirstOrDefault() ?? "";
        private static HttpResponseMessage Reply(object body) => new(HttpStatusCode.OK)
            { Content = new StringContent(JsonSerializer.Serialize(body), Encoding.UTF8, "application/json") };
        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken token)
        {
            var uri = request.RequestUri!; var path = uri.AbsolutePath;
            Paths.Add(path);
            JsonElement body = default;
            if (request.Content is not null)
            {
                using var document = JsonDocument.Parse(await request.Content.ReadAsStringAsync(token));
                body = document.RootElement.Clone();
            }
            var delay = _next;
            if (delay is not null && delay.Path == path && (!delay.Selection || Query(uri, "selection") == "true"))
            {
                _next = null; delay.Entered = true;
                // Deliberately ignore cancellation until released: the production
                // transport and selection guards must still reject a late reply.
                await delay.Release.Task;
            }
            if (path.EndsWith("/stream", StringComparison.Ordinal))
            {
                await Task.Delay(Timeout.Infinite, token);
                throw new InvalidOperationException("Unreachable stream fixture.");
            }
            if (path == "/internal/runtime/permissions/effective") return Reply(new { workspaces = Array.Empty<object>() });
            if (path == "/internal/runtime/execution") return Reply(new { server_now = DateTimeOffset.UtcNow, conversation_activity = new { }, statistics = new { }, in_flight = new { } });
            if (path == "/internal/runtime/execution/sidebar")
            {
                var ids = body.Text("search") == "A2" ? new[] { "A2" } : new[] { "A1", "A2" };
                var chosen = body.Text("selected_id", "A2");
                return Reply(new { latest_seq = 0, server_now = DateTimeOffset.UtcNow,
                    groups = new[] { new { workspace_id = "fixture", title = "Fixture", root = "C:/fixture", total = ids.Length, mode = "history", history_limit = 5, history_cursor = "fixture-cursor", recent_count = 0, execution_count = 0, has_more = false, conversations = ids.Select(Row).ToArray() } }, selected = Row(chosen) });
            }
            if (path == "/internal/runtime/conversations")
                return Reply(new { selected_ids = Query(uri, "search") == "A2" ? new[] { "A2" } : new[] { "A1", "A2" } });
            if (path == "/internal/runtime/conversations/batch")
            {
                Batches.Add(body.Array("ids").Select(id => id.GetString()!).ToArray());
                return Reply(new { succeeded = Batches[^1].Length, failed = 0, skipped = 0, items = Array.Empty<object>() });
            }
            if (path == "/internal/runtime/calls")
            {
                if (Query(uri, "include_output") == "true")
                {
                    var id = Query(uri, "unattributed") == "true" ? "unattributed" : Query(uri, "conversation_id");
                    return Reply(new { calls = new[] { new { call_id = "call-" + id } }, has_more = false });
                }
                return Reply(new { calls = Array.Empty<object>(), latest_seq = 0, has_more = false, next_before = 0 });
            }
            if (path.EndsWith("/insertions", StringComparison.Ordinal)) return Reply(new { insertions = Array.Empty<object>(), server_now = DateTimeOffset.UtcNow });
            if (path.StartsWith("/internal/runtime/conversations/", StringComparison.Ordinal)) return Reply(new { conversation = Row(path.Split('/')[4]) });
            if (path == "/internal/runtime/activity/control") { Controls.Add(body); return Reply(new { ok = true }); }
            if (path.StartsWith("/internal/runtime/tasks/", StringComparison.Ordinal))
            {
                var parts = path.Split('/'); var task = parts[4];
                if (path.EndsWith("/activity", StringComparison.Ordinal)) return Reply(new { events = new[] { new { summary = "milestone-" + StepVersion } } });
                if (path.EndsWith("/threads", StringComparison.Ordinal)) return Reply(new { threads = new[] { new { id = "main", title = "Main" }, new { id = "fork-B", title = "Fork B" } } });
                if (parts.Length == 7) return Reply(new { thread = new { id = parts[6], next_action = "next-" + StepVersion, current_step_id = "S1", steps = new[] { new { id = "S1", title = "step-" + StepVersion, status = StepVersion > 0 ? "completed" : "pending" } } } });
                return Reply(new { task = new { id = task, title = task, goal = LongDetails ? string.Join("\n", Enumerable.Repeat("Long fixture goal", 80)) : "Fixture goal", active_thread_id = "main", completion_conditions = new[] { "fixture" } } });
            }
            throw new InvalidOperationException("Undeclared fixture request: " + request.Method + " " + uri);
        }
        private object Row(string id) => new { conversation_id = id, title = id, task_ids = TaskLinked ? new[] { "task-A" } : Array.Empty<string>(),
            state = new { workspace_id = "fixture", active_task_id = TaskLinked ? "task-A" : "", active_task_thread_id = TaskLinked ? "main" : "" }, statistics = new { } };
    }
}
