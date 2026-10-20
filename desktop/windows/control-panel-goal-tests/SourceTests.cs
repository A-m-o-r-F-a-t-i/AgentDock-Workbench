using System.Text.Json;
using System.Text.Json.Nodes;
using AgentDock.ControlPanel;

internal static class SourceTests
{
    internal static async Task RunAsync()
    {
        var count = 0;
        void Check(bool ok, string name) { if (!ok) throw new Exception(name); count++; }
        var conversation = JsonNode.Parse("""{"conversation":{"conversation_id":"conversation","state":{"active_task_id":"task","active_task_thread_id":"main"}}}""")!;
        var task = JsonNode.Parse("""{"task":{"id":"task","status":"active","updated_at":"2026-10-12T00:00:00Z"}}""")!;
        var thread = JsonNode.Parse("""{"thread":{"id":"main","status":"open","updated_at":"2026-10-12T00:00:00Z"}}""")!;
        var overview = JsonNode.Parse("""{"conversation_activity":{"conversation":{"pending":0,"running":0,"last_activity_at":"2026-10-12T00:00:00Z"}},"in_flight":{}}""")!;
        var sessions = JsonNode.Parse("""{"sessions":[]}""")!;
        var requested = new List<string>();
        var source = new GoalActivitySource((path, ct) =>
        {
            requested.Add(path);
            var node = path switch {
                "/internal/runtime/conversations/conversation" => conversation,
                "/internal/runtime/tasks/task" => task,
                "/internal/runtime/tasks/task/threads/main" => thread,
                "/internal/runtime/execution" => overview,
                "/internal/runtime/activity/live?task_id=task&thread_id=main" => sessions,
                _ => throw new Exception("unexpected route " + path)
            };
            return Task.FromResult(JsonSerializer.SerializeToElement(node));
        });
        var activity = await source.ReadAsync("conversation", default);
        Check(activity.TaskStatus == "active" && !activity.Busy && activity.ConversationId == "conversation", "real conversation_id contract");
        Check(requested.Count == 5, "bounded authoritative endpoint set");
        overview["conversation_activity"]!["conversation"]!["pending"] = 1;
        Check((await source.ReadAsync("conversation", default)).Busy, "pending approval blocks");
        overview["conversation_activity"]!["conversation"]!["pending"] = 0;
        overview["conversation_activity"]!["conversation"]!["running"] = 1;
        Check((await source.ReadAsync("conversation", default)).Busy, "running RPC blocks");
        overview["conversation_activity"]!["conversation"]!["running"] = 0;
        overview["in_flight"]!["conversation"] = true;
        Check((await source.ReadAsync("conversation", default)).Busy, "confirmed in-flight blocks");
        overview["in_flight"]!["conversation"] = false;
        sessions["sessions"]!.AsArray().Add(JsonNode.Parse("""{"status":"running"}"""));
        Check((await source.ReadAsync("conversation", default)).Busy, "asynchronous command outlives RPC");
        sessions["sessions"]![0]!["status"] = "exited";
        Check(!(await source.ReadAsync("conversation", default)).Busy, "exited command does not block");
        task["task"]!["archived_at"] = "2026-10-12T01:00:00Z";
        Check((await source.ReadAsync("conversation", default)).Terminated, "archived task stops");
        task["task"]!.AsObject().Remove("archived_at");
        conversation["conversation"]!["deleted_at"] = "2026-10-12T01:00:00Z";
        Check((await source.ReadAsync("conversation", default)).Terminated, "deleted conversation stops");
        conversation["conversation"]!.AsObject().Remove("deleted_at");
        var before = (await source.ReadAsync("conversation", default)).ActivityKey;
        overview["server_now"] = "2026-10-12T02:00:00Z";
        Check((await source.ReadAsync("conversation", default)).ActivityKey == before, "poll heartbeat is not progress");
        task["task"]!["updated_at"] = "2026-10-12T03:00:00Z";
        Check((await source.ReadAsync("conversation", default)).ActivityKey != before, "real task progress changes key");
        conversation["conversation"]!["state"]!["active_task_id"] = "";
        requested.Clear(); activity = await source.ReadAsync("conversation", default);
        Check(activity.TaskStatus == "unbound" && requested.Count == 1, "no task means no guessed unfinished state");
        conversation["conversation"]!["state"]!["active_task_id"] = "task";
        overview["conversation_activity"] = new JsonObject();
        try { await source.ReadAsync("conversation", default); throw new Exception("missing facts accepted"); }
        catch (IOException) { count++; }
        Console.WriteLine($"PASS {count} activity-source contract assertions");
    }
}
