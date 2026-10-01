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
            CheckNative(first, check);
            foreach (var preference in new[] { "light", "dark", "system", "dark" })
            {
                DesktopTheme.Save(preference);
                CheckNative(first, check);
                check(first.WindowStyle == WindowStyle.SingleBorderWindow && first.ResizeMode == ResizeMode.CanResize,
                    "Native theme replaced system window chrome or resize behavior");
            }
            // A dialog created after theme initialization must also be covered.
            var later = new Window { Title = "Unshown auxiliary caption test", ShowInTaskbar = false };
            windows.Add(later);
            var laterHandle = new WindowInteropHelper(later).EnsureHandle();
            later.RaiseEvent(new RoutedEventArgs(FrameworkElement.LoadedEvent, later));
            CheckNative(later, check);
            DesktopTheme.Save("light");
            CheckNative(first, check);
            CheckNative(later, check);
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

    private static void CheckNative(Window window, Action<bool, string> check)
    {
        var handle = new WindowInteropHelper(window).Handle;
        check(handle != IntPtr.Zero, "Native caption test requires a real HWND");
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000)) return;
        var resources = System.Windows.Application.Current.Resources;
        var expected = NativeWindowTheme.Attributes(DesktopTheme.EffectiveDark, SystemParameters.HighContrast,
            ((SolidColorBrush)resources["AppBackground"]).Color, ((SolidColorBrush)resources["PrimaryText"]).Color);
        var code = DwmGetWindowAttribute(handle, NativeWindowTheme.ImmersiveDarkMode, out var actual, sizeof(int));
        check(code == 0 && actual == expected.Mode,
            $"Native dark-mode lifecycle: HRESULT={code}, actual={actual}, expected={expected.Mode}");
        // Caption/text colors are documented for DwmSetWindowAttribute only.
        // Reading them with DwmGetWindowAttribute returns E_INVALIDARG even
        // when setting them succeeded. Exercise the production setter against
        // the real HWND and require successful native acknowledgements.
        var applied = NativeWindowTheme.Apply(window);
        check(applied is { Mode: 0, Caption: 0, Text: 0 },
            $"DWM did not accept the native theme attributes: {applied}");
    }

    [DllImport("dwmapi.dll", ExactSpelling = true)]
    private static extern int DwmGetWindowAttribute(IntPtr handle, int attribute, out int value, int size);
}
