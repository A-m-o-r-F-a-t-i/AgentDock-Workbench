using System.Diagnostics;
using System.IO;
using AgentDock.ControlPanel;

internal static class TaskRuntimeDrainTests
{
    private const int ProcessExitTimeoutSeconds = 10;

    internal static void Run(Action<bool, string> check)
    {
        var root = Path.Combine(Path.GetTempPath(), "agentdock-runtime-drain-" + Guid.NewGuid().ToString("N"));
        Process? runtime = null;
        try
        {
            Directory.CreateDirectory(Path.Combine(root, "bin"));
            File.WriteAllText(
                Path.Combine(root, "active-version.json"),
                "{\"active_version\":\"1.1.9\",\"fallback_version\":\"1.1.8\"}");

            var expectedPaths = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
            {
                Path.GetFullPath(Path.Combine(root, "bin", "agentdock.exe")),
                Path.GetFullPath(Path.Combine(root, "bin", "agentdock-tray.exe")),
                Path.GetFullPath(Path.Combine(root, "versions", "v1.1.9", "agentdock-core.exe")),
                Path.GetFullPath(Path.Combine(root, "versions", "v1.1.9", "agentdock-tray.exe")),
                Path.GetFullPath(Path.Combine(root, "versions", "v1.1.8", "agentdock-core.exe")),
                Path.GetFullPath(Path.Combine(root, "versions", "v1.1.8", "agentdock-tray.exe"))
            };
            var actualPaths = TaskAdminService.InstalledRuntimePaths(root);
            check(actualPaths.SetEquals(expectedPaths), "Runtime drain covers stable and active/fallback tray/Core images");

            var stableCorePath = Path.Combine(root, "bin", "agentdock.exe");
            File.WriteAllText(stableCorePath, "fixture");
            using (File.Open(stableCorePath, FileMode.Open, FileAccess.Read, FileShare.Read))
            {
                check(!TaskAdminService.RuntimeFilesReadyForReplacement(actualPaths), "Runtime drain detects a locked replacement target");
            }
            check(TaskAdminService.RuntimeFilesReadyForReplacement(actualPaths), "Runtime drain accepts released replacement targets");

            File.Copy(Path.Combine(Environment.SystemDirectory, "cmd.exe"), stableCorePath, overwrite: true);
            var startInfo = new ProcessStartInfo(stableCorePath)
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                WorkingDirectory = root
            };
            startInfo.ArgumentList.Add("/d");
            startInfo.ArgumentList.Add("/c");
            startInfo.ArgumentList.Add("ping.exe -n 30 127.0.0.1 >NUL");
            runtime = Process.Start(startInfo) ?? throw new IOException("Could not start the installed runtime fixture");
            Thread.Sleep(250);
            var running = !runtime.HasExited;
            check(running, "Installed runtime fixture remains active before runtime drain");
            if (!running) return;

            var loadedPath = runtime.MainModule?.FileName ?? string.Empty;
            check(
                string.Equals(Path.GetFullPath(loadedPath), Path.GetFullPath(stableCorePath), StringComparison.OrdinalIgnoreCase),
                "Installed runtime fixture exposes the stable Core path");
            check(!TaskAdminService.RuntimeFilesReadyForReplacement(actualPaths), "Runtime drain detects a live stable Core image lock");

            TaskAdminService.StopInstalledRuntime(root);
            check(runtime.WaitForExit(ProcessExitTimeoutSeconds * 1000), "Runtime drain terminates the installed runtime process tree");
            check(runtime.HasExited, "Installed runtime fixture exited after runtime drain");
            check(TaskAdminService.RuntimeFilesReadyForReplacement(actualPaths), "Runtime drain releases stable and generation replacement targets");
        }
        finally
        {
            if (runtime is not null)
            {
                try
                {
                    if (!runtime.HasExited)
                    {
                        runtime.Kill(entireProcessTree: true);
                        runtime.WaitForExit(ProcessExitTimeoutSeconds * 1000);
                    }
                }
                catch (InvalidOperationException)
                {
                }
                finally
                {
                    runtime.Dispose();
                }
            }
            if (Directory.Exists(root))
            {
                Directory.Delete(root, recursive: true);
            }
        }
    }
}