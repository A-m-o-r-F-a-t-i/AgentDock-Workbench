using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;
using Application = System.Windows.Application;
using Color = System.Windows.Media.Color;

namespace AgentDock.ControlPanel;

// Keep the OS caption, hit testing, system menu, resize borders and DPI behavior.
// A WPF palette change alone does not theme the HWND's non-client area.
internal static class NativeWindowTheme
{
    internal const int ImmersiveDarkMode = 20;
    internal const int CaptionColor = 35;
    internal const int TextColor = 36;
    internal const int DefaultColor = unchecked((int)0xffffffff);
    private static readonly HashSet<Window> Windows = [];
    private static bool _registered;
    private static bool _enabled;
    internal static int TrackedCount => Windows.Count;

    internal static void Initialize()
    {
        _enabled = true;
        if (_registered) return;
        // A class handler covers auxiliary dialogs without per-window styles.
        // It lives for the application lifetime and holds no window references.
        EventManager.RegisterClassHandler(typeof(Window), FrameworkElement.LoadedEvent, new RoutedEventHandler(WindowLoaded));
        _registered = true;
    }

    internal static void RefreshWindows()
    {
        if (!_enabled || Application.Current is not { } app) return;
        foreach (Window window in app.Windows) Attach(window);
    }

    private static void Attach(Window window)
    {
        if (Windows.Add(window))
        {
            // Main and execution windows initialize DesktopTheme in their
            // constructors, before their native handle is created.
            window.SourceInitialized += SourceInitialized;
            window.Closed += WindowClosed;
        }
        Apply(window);
    }

    private static void WindowLoaded(object sender, RoutedEventArgs args)
    {
        if (_enabled && sender is Window window) Attach(window);
    }

    private static void SourceInitialized(object? sender, EventArgs args)
    {
        if (_enabled && sender is Window window) Apply(window);
    }

    private static void WindowClosed(object? sender, EventArgs args)
    {
        if (sender is Window window) Detach(window);
    }

    private static void Detach(Window window)
    {
        window.SourceInitialized -= SourceInitialized;
        window.Closed -= WindowClosed;
        Windows.Remove(window);
    }

    internal static (int Mode, int Caption, int Text) Attributes(bool dark, bool highContrast, Color background, Color foreground) =>
        highContrast ? (0, DefaultColor, DefaultColor) : (dark ? 1 : 0, ColorRef(background), ColorRef(foreground));

    private static int ColorRef(Color value) => value.R | value.G << 8 | value.B << 16;

    private static void Apply(Window window)
    {
        // Reading Handle never creates a window or launches a service.
        var handle = new WindowInteropHelper(window).Handle;
        if (handle == IntPtr.Zero || Application.Current is not { } app) return;
        if (app.TryFindResource("AppBackground") is not SolidColorBrush background ||
            app.TryFindResource("PrimaryText") is not SolidColorBrush foreground) return;
        var attributes = Attributes(DesktopTheme.EffectiveDark, SystemParameters.HighContrast, background.Color, foreground.Color);
        TrySet(handle, ImmersiveDarkMode, attributes.Mode);
        if (OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000))
        {
            // Explicit palette colors also honor an app override opposite to
            // the OS theme. High contrast restores DWM's own default colors.
            TrySet(handle, CaptionColor, attributes.Caption);
            TrySet(handle, TextColor, attributes.Text);
        }
    }

    private static void TrySet(IntPtr handle, int attribute, int value)
    {
        try { _ = DwmSetWindowAttribute(handle, attribute, ref value, sizeof(int)); }
        catch (DllNotFoundException) { /* Older systems retain their native chrome. */ }
        catch (EntryPointNotFoundException) { /* Unsupported API: no content-theme failure. */ }
        // Unsupported attributes return an HRESULT; they do not prevent startup.
    }

    internal static void Dispose()
    {
        _enabled = false;
        foreach (var window in Windows.ToArray()) Detach(window);
    }

    [DllImport("dwmapi.dll", ExactSpelling = true)]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attribute, ref int value, int size);
}
