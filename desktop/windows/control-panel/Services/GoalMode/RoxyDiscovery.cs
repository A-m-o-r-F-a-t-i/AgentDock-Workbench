using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Text.RegularExpressions;

namespace AgentDock.ControlPanel;

internal static class RoxyDiscovery
{
    private const int WbemFlagReturnImmediately = 0x10;
    private static readonly Regex UserDataDirectoryPattern = new(
        @"--user-data-dir(?:=|\s+)(""[^""]+""|\S+)",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);

    // Read only the DevToolsActivePort of already-running Roxy browser processes.
    // Never scan ports, start a browser, change debugging flags, or return profile paths.
    internal static async Task<string[]> EndpointsAsync(CancellationToken cancellationToken)
    {
        try
        {
            return await Task.Run(() => DiscoverEndpoints(cancellationToken), CancellationToken.None)
                .WaitAsync(TimeSpan.FromSeconds(10), cancellationToken);
        }
        catch (TimeoutException ex)
        {
            throw new IOException("读取运行中的 Roxy 调试端点超时，可手动填写本机端点。", ex);
        }
    }

    internal static string? UserDataDirectoryFromCommandLine(string commandLine)
    {
        var match = UserDataDirectoryPattern.Match(commandLine ?? "");
        return match.Success ? match.Groups[1].Value.Trim('"') : null;
    }

    private static string[] DiscoverEndpoints(CancellationToken cancellationToken)
    {
        object? locator = null;
        object? services = null;
        object? results = null;
        var endpoints = new HashSet<string>(StringComparer.Ordinal);
        try
        {
            var locatorType = Type.GetTypeFromProgID("WbemScripting.SWbemLocator")
                ?? throw new IOException("Windows 进程查询服务不可用。");
            locator = Activator.CreateInstance(locatorType)
                ?? throw new IOException("Windows 进程查询服务无法启动。");
            dynamic dynamicLocator = locator;
            services = dynamicLocator.ConnectServer(".", @"root\cimv2");
            dynamic dynamicServices = services;
            results = dynamicServices.ExecQuery(
                "SELECT CommandLine FROM Win32_Process WHERE Name='RoxyChrome.exe'",
                "WQL",
                WbemFlagReturnImmediately);
            foreach (var process in (dynamic)results)
            {
                object? properties = null;
                object? commandLineProperty = null;
                try
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    properties = process.Properties_;
                    dynamic dynamicProperties = properties;
                    commandLineProperty = dynamicProperties.Item("CommandLine", 0);
                    dynamic dynamicCommandLineProperty = commandLineProperty;
                    object? rawCommandLine = dynamicCommandLineProperty.Value;
                    var commandLine = Convert.ToString(rawCommandLine, CultureInfo.InvariantCulture) ?? "";
                    if (commandLine.Contains("--type=", StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    var userDataDirectory = UserDataDirectoryFromCommandLine(commandLine);
                    if (string.IsNullOrWhiteSpace(userDataDirectory))
                    {
                        continue;
                    }
                    var portPath = Path.Combine(userDataDirectory, "DevToolsActivePort");
                    string? firstLine;
                    try
                    {
                        firstLine = File.ReadLines(portPath).FirstOrDefault();
                    }
                    catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
                    {
                        continue;
                    }
                    if (int.TryParse(firstLine, NumberStyles.None, CultureInfo.InvariantCulture, out var port) && port is > 0 and <= 65535)
                    {
                        endpoints.Add($"http://127.0.0.1:{port}");
                        if (endpoints.Count == 8)
                        {
                            break;
                        }
                    }
                }
                finally
                {
                    ReleaseComObject(commandLineProperty);
                    ReleaseComObject(properties);
                    ReleaseComObject(process);
                }
            }
            return [.. endpoints];
        }
        catch (COMException ex)
        {
            throw new IOException("无法从运行中的 Roxy 读取调试端点，可手动填写本机端点。", ex);
        }
        finally
        {
            ReleaseComObject(results);
            ReleaseComObject(services);
            ReleaseComObject(locator);
        }
    }

    private static void ReleaseComObject(object? value)
    {
        if (value is not null && Marshal.IsComObject(value))
        {
            _ = Marshal.FinalReleaseComObject(value);
        }
    }
}
