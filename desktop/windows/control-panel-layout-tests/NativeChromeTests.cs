using System.IO;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;
using AgentDock.ControlPanel;

internal static class NativeChromeTests
{
    public static void Run(Action<bool, string> check)
    {
        var root = Path.Combine(Path.GetTempPath(), "agentdock-native-theme-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        File.WriteAllText(Path.Combine(root, "execution-center-settings.json"), "{\"theme\":\"dark\"}");
        var windows = new List<Window>();
        try
        {
            var first = new Window { Title = "Unshown native caption test", ShowInTaskbar = false };
            windows.Add(first);
            DesktopTheme.Initialize(root);
            var firstHandle = new WindowInteropHelper(first).EnsureHandle();
            CheckNative(firstHandle, check);
            foreach (var preference in new[] { "light", "dark", "system", "dark" })
            {
                DesktopTheme.Save(preference);
                CheckNative(firstHandle, check);
                check(first.WindowStyle == WindowStyle.SingleBorderWindow && first.ResizeMode == ResizeMode.CanResize,
                    "Native theme replaced system window chrome or resize behavior");
            }
            // A dialog created after theme initialization must also be covered.
            var later = new Window { Title = "Unshown auxiliary caption test", ShowInTaskbar = false };
            windows.Add(later);
            var laterHandle = new WindowInteropHelper(later).EnsureHandle();
            later.RaiseEvent(new RoutedEventArgs(FrameworkElement.LoadedEvent, later));
            CheckNative(laterHandle, check);
            DesktopTheme.Save("light");
            CheckNative(firstHandle, check);
            CheckNative(laterHandle, check);
            var beforeClose = NativeWindowTheme.TrackedCount;
            later.Close(); windows.Remove(later);
            check(NativeWindowTheme.TrackedCount == beforeClose - 1, "Closed native window retained theme handlers");
            var hc = NativeWindowTheme.Attributes(true, true, Colors.Black, Colors.White);
            check(hc.Mode == 0 && hc.Caption == NativeWindowTheme.DefaultColor && hc.Text == NativeWindowTheme.DefaultColor,
                "High contrast must restore system caption colors");
            var rgb = NativeWindowTheme.Attributes(true, false, Color.FromRgb(1, 2, 3), Color.FromRgb(4, 5, 6));
            check(rgb.Caption == 0x030201 && rgb.Text == 0x060504, "DWM COLORREF channel order changed");
        }
        finally
        {
            foreach (var window in windows) window.Close();
            NativeWindowTheme.Dispose();
            Directory.Delete(root, true);
        }
    }

    private static void CheckNative(IntPtr handle, Action<bool, string> check)
    {
        check(handle != IntPtr.Zero, "Native caption test requires a real HWND");
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000)) return;
        var resources = System.Windows.Application.Current.Resources;
        var expected = NativeWindowTheme.Attributes(DesktopTheme.EffectiveDark, SystemParameters.HighContrast,
            ((SolidColorBrush)resources["AppBackground"]).Color, ((SolidColorBrush)resources["PrimaryText"]).Color);
        foreach (var item in new[] { (NativeWindowTheme.ImmersiveDarkMode, expected.Mode),
            (NativeWindowTheme.CaptionColor, expected.Caption), (NativeWindowTheme.TextColor, expected.Text) })
        {
            var code = DwmGetWindowAttribute(handle, item.Item1, out var actual, sizeof(int));
            check(code == 0 && actual == item.Item2, $"Native caption attribute {item.Item1}: HRESULT={code}, actual={actual}, expected={item.Item2}");
        }
    }

    [DllImport("dwmapi.dll", ExactSpelling = true)]
    private static extern int DwmGetWindowAttribute(IntPtr handle, int attribute, out int value, int size);
}
