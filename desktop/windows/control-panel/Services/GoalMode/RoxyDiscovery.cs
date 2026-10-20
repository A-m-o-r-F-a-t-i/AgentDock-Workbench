using System.Diagnostics;
using System.IO;

namespace AgentDock.ControlPanel;

internal static class RoxyDiscovery
{
    // Read only the DevToolsActivePort of already-running Roxy browser processes.
    // Never scan ports, start a browser, change debugging flags, or return profile paths.
    internal static async Task<string[]> EndpointsAsync(CancellationToken cancellationToken)
    {
        const string script = """
        $ErrorActionPreference='Stop'
        Get-CimInstance Win32_Process -Filter "Name='RoxyChrome.exe'" | Where-Object { $_.CommandLine -notmatch '--type=' } | ForEach-Object {
          $m=[regex]::Match($_.CommandLine,'--user-data-dir(?:=|\s+)("[^"]+"|\S+)')
          if($m.Success) {
            $p=Join-Path $m.Groups[1].Value.Trim('"') 'DevToolsActivePort'
            if(Test-Path -LiteralPath $p) {
              $n=0; $v=Get-Content -LiteralPath $p -TotalCount 1
              if([int]::TryParse($v,[ref]$n) -and $n -gt 0 -and $n -le 65535) { 'http://127.0.0.1:'+$n }
            }
          }
        }
        """;
        var start = new ProcessStartInfo(Path.Combine(Environment.SystemDirectory, @"WindowsPowerShell\v1.0\powershell.exe"))
        { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true };
        foreach (var arg in new[] { "-NoProfile", "-NonInteractive", "-Command", script }) start.ArgumentList.Add(arg);
        using var process = new Process { StartInfo = start };
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        deadline.CancelAfter(TimeSpan.FromSeconds(10));
        if (!process.Start()) throw new IOException("Roxy 端点读取未启动。");
        try
        {
            var output = ReadBoundedAsync(process.StandardOutput, deadline.Token);
            var error = ReadBoundedAsync(process.StandardError, deadline.Token);
            await process.WaitForExitAsync(deadline.Token);
            var text = await output;
            await error;
            if (process.ExitCode != 0) throw new IOException("无法从运行中的 Roxy 读取调试端点，可手动填写本机端点。");
            return text.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries).Select(s => s.Trim())
                .Where(s => s.StartsWith("http://127.0.0.1:", StringComparison.Ordinal)).Distinct().Take(8).ToArray();
        }
        finally { if (!process.HasExited) process.Kill(true); }
    }

    private static async Task<string> ReadBoundedAsync(StreamReader reader, CancellationToken ct)
    {
        var buffer = new char[8193]; var length = 0;
        while (length < buffer.Length)
        {
            var read = await reader.ReadAsync(buffer.AsMemory(length), ct);
            if (read == 0) break;
            length += read;
        }
        if (length > 8192) throw new IOException("Roxy 端点读取超过大小限制。");
        return new string(buffer, 0, length);
    }
}
