using System.Text.Json;
using AgentDock.ControlPanel;

internal static class FileExplorerRevealTests
{
    internal static void Run(Action<bool, string> check)
    {
        // Windows path semantics are tested on Windows; other policy runners
        // do not reinterpret drive-qualified paths as Unix relative names.
        if (!OperatingSystem.IsWindows()) return;
        JsonElement Record(object edit, object[]? changes = null, string runtime = "windows", string parent = "") =>
            JsonSerializer.SerializeToElement(new { tool_name = "file_edit", runtime, parent_call_id = parent, file_edit = edit, file_changes = changes ?? [] });
        check(FileExplorerReveal.Locations(Record(new { path = @"C:\project\a.txt", new_path = "" })).SequenceEqual([@"C:\project\a.txt"]), "empty optional destination falls back to original path");
        var paths = FileExplorerReveal.Locations(Record(new { path_base = @"C:\project", path = "src/a.txt" }));
        check(paths.SequenceEqual([@"C:\project\src\a.txt"]), "recorded base resolves nested edit paths");
        paths = FileExplorerReveal.Locations(Record(new { path_base = @"C:\project", path = "old.txt", new_path = "new.txt" }));
        check(paths.SequenceEqual([@"C:\project\new.txt"]), "move reveals destination rather than deleted source");
        paths = FileExplorerReveal.Locations(Record(new { path_base = @"C:\project", affected_files = new[] { new { path = "a.txt", move_to = "b.txt" }, new { path = "B.txt", move_to = "b.txt" } } }));
        check(paths.SequenceEqual([@"C:\project\b.txt"]), "affected move destinations are deduplicated");
        paths = FileExplorerReveal.Locations(Record(new { path = "relative.txt" }, [new { path = @"D:\历史 文件\file.txt" }]));
        check(paths.SequenceEqual([@"D:\历史 文件\file.txt"]), "legacy absolute changes remain revealable");
        check(FileExplorerReveal.Locations(Record(new { path = "relative.txt" })).Length == 0, "unknown legacy root is not guessed");
        check(FileExplorerReveal.Locations(Record(new { path = @"C:\file.txt" }, runtime: "wsl")).Length == 0, "WSL records are not retargeted to Windows");
        check(FileExplorerReveal.Locations(Record(new { path = @"C:\file.txt" }, parent: "root")).Length == 0, "internal child record has no root menu");
        foreach (var invalid in new[] { "https://example.test/a", "file://C:/a", "C:relative.txt", @"\root-relative.txt", "a\n.txt", "C:\\bad\".txt", "[REDACTED]/file", @"\\.\pipe\test" })
            check(FileExplorerReveal.Normalize(invalid, "") is null, "unsafe or ambiguous path rejected: " + invalid);
        var deleted = FileExplorerReveal.ExistingTarget(@"C:\project\deleted\file.txt", _ => false, path => path == @"C:\project");
        check(deleted == @"C:\project", "deleted file uses nearest existing ancestor without creating a directory");
        var info = FileExplorerReveal.StartInfo(@"C:\项目 文件\a,b.txt");
        check(!info.UseShellExecute && info.FileName.EndsWith("explorer.exe", StringComparison.OrdinalIgnoreCase) && info.Arguments == "/select,\"C:\\项目 文件\\a,b.txt\"", "Explorer selects the quoted path without executing the file");
        try { FileExplorerReveal.ExistingTarget(@"C:\missing\file.txt", _ => false, _ => false); check(false, "missing ancestors must fail"); }
        catch (System.IO.IOException) { check(true, "missing ancestors are actionable"); }
        var many = Enumerable.Range(0, 40).Select(index => (object)new { path = $@"C:\project\{index}.txt" }).ToArray();
        check(FileExplorerReveal.Locations(Record(new { }, many)).Length == FileExplorerReveal.MaximumLocations, "file menu remains bounded");
    }
}
