using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Text.Json;

namespace AgentDock.ControlPanel;

public static class FileExplorerReveal
{
    public const int MaximumLocations = 16;

    public static string[] Locations(JsonElement record)
    {
        if (record.Text("tool_name") is not ("apply_patch" or "file_edit") || record.Text("parent_call_id").Length > 0 ||
            record.Text("runtime") is not ("" or "windows")) return [];
        var edit = record.Field("file_edit");
        var paths = edit.Array("affected_files").Select(file => file.Text("move_to").Length > 0 ? file.Text("move_to") : file.Text("path")).ToList();
        var preferred = edit.Text("new_path").Length > 0 ? edit.Text("new_path") : edit.Text("path");
        if (paths.Count == 0 && preferred.Length > 0) paths.Add(preferred);
        var locations = paths.Select(path => Normalize(path, edit.Text("path_base"))).Where(path => path is not null).Cast<string>().ToList();
        // Older records have absolute file changes but no recorded base. Never guess
        // a relative path from today's workspace or from a nested command directory.
        if (locations.Count == 0)
            locations.AddRange(record.Array("file_changes").Select(file => Normalize(file.Text("path"), "")).Where(path => path is not null).Cast<string>());
        return locations.Distinct(StringComparer.OrdinalIgnoreCase).Take(MaximumLocations).ToArray();
    }

    public static string? Normalize(string path, string pathBase)
    {
        if (string.IsNullOrWhiteSpace(path) || path.Contains("[REDACTED]", StringComparison.OrdinalIgnoreCase) ||
            path.Any(char.IsControl) || path.IndexOfAny(['"', '<', '>', '|', '*', '?']) >= 0 ||
            path.Contains("://", StringComparison.Ordinal) || path.StartsWith(@"\\.\", StringComparison.Ordinal)) return null;
        try
        {
            if (Path.IsPathFullyQualified(path)) return Path.GetFullPath(path);
            if (Path.IsPathRooted(path) || path.Contains(':') || !Path.IsPathFullyQualified(pathBase) ||
                pathBase.Contains("[REDACTED]", StringComparison.OrdinalIgnoreCase)) return null;
            return Path.GetFullPath(path, pathBase);
        }
        catch (Exception error) when (error is ArgumentException or NotSupportedException or PathTooLongException) { return null; }
    }

    public static string ExistingTarget(string path, Func<string, bool> fileExists, Func<string, bool> directoryExists)
    {
        var target = Normalize(path, "") ?? throw new IOException("执行记录没有可定位的本机文件路径。");
        if (fileExists(target) || directoryExists(target)) return target;
        for (var parent = Path.GetDirectoryName(target); !string.IsNullOrEmpty(parent); parent = Path.GetDirectoryName(parent))
            if (directoryExists(parent)) return parent;
        throw new IOException("文件及其所在目录已不存在或无法访问：" + target);
    }

    public static ProcessStartInfo StartInfo(string path)
    {
        var target = Normalize(path, "") ?? throw new IOException("文件路径无效。");
        return new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "explorer.exe"))
        {
            UseShellExecute = false,
            Arguments = "/select,\"" + target + "\""
        };
    }

    public static Task<string> ShowAsync(string path, CancellationToken cancellationToken) => Task.Run(() =>
    {
        cancellationToken.ThrowIfCancellationRequested();
        var target = ExistingTarget(path, File.Exists, Directory.Exists);
        cancellationToken.ThrowIfCancellationRequested();
        try { using var process = Process.Start(StartInfo(target)); }
        catch (Win32Exception error) { throw new IOException("无法打开文件资源管理器：" + error.Message, error); }
        return target;
    }, cancellationToken);
}
