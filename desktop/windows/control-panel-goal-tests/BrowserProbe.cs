using System.Text.Json;
using AgentDock.ControlPanel;

internal static class BrowserProbe
{
    internal static async Task RunAsync(string endpoint)
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(15));
        var ct = timeout.Token;
        using var cdp = new GoalCdp();
        await cdp.ConnectAsync(endpoint, "", ct);
        var before = OperatingSystem.IsWindows() ? Native.GetForegroundWindow() : IntPtr.Zero;
        var created = await cdp.CallAsync("Target.createTarget", new { url = "about:blank", hidden = true, background = true }, ct);
        var id = created.String("targetId");
        try
        {
            var after = OperatingSystem.IsWindows() ? Native.GetForegroundWindow() : IntPtr.Zero;
            var noWindow = false;
            try { await cdp.CallAsync("Browser.getWindowForTarget", new { targetId = id }, ct); }
            catch (IOException ex) when (ex.Message.Contains("window not found", StringComparison.OrdinalIgnoreCase)) { noWindow = true; }
            Console.WriteLine(JsonSerializer.Serialize(new { hiddenTargetCreated = id.Length > 0, noBrowserWindow = noWindow, foregroundUnchangedDuringCreation = before == after }));
            if (!noWindow) throw new InvalidOperationException("The hidden target unexpectedly has a browser window.");
        }
        finally { await cdp.CallAsync("Target.closeTarget", new { targetId = id }, ct); }
    }
}
