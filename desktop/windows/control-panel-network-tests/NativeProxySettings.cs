using System.ComponentModel;
using System.Runtime.InteropServices;

// Only the explicitly enabled disposable CI runner may construct this fixture.
internal sealed class NativeProxySettings : IDisposable
{
    private readonly Snapshot _original;
    private bool _disposed;

    internal NativeProxySettings()
    {
        if (Environment.GetEnvironmentVariable("GITHUB_ACTIONS") != "true" ||
            Environment.GetEnvironmentVariable("AGENTDOCK_PROXY_ACCEPTANCE") != "1")
            throw new InvalidOperationException("System proxy fixture requires isolated GitHub Actions opt-in.");
        _original = Read();
    }

    internal void SetManual(string? address) => Apply(new(false, null, address, ""));

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        Apply(_original);
    }

    private sealed record Snapshot(bool AutoDetect, string? Pac, string? Proxy, string? Bypass);

    private static Snapshot Read()
    {
        if (!WinHttpGetIEProxyConfigForCurrentUser(out var config))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        try
        {
            return new(config.AutoDetect, Marshal.PtrToStringUni(config.Pac),
                Marshal.PtrToStringUni(config.Proxy), Marshal.PtrToStringUni(config.Bypass));
        }
        finally
        {
            if (config.Pac != IntPtr.Zero) GlobalFree(config.Pac);
            if (config.Proxy != IntPtr.Zero) GlobalFree(config.Proxy);
            if (config.Bypass != IntPtr.Zero) GlobalFree(config.Bypass);
        }
    }

    private static void Apply(Snapshot value)
    {
        var proxy = Marshal.StringToHGlobalUni(value.Proxy ?? "");
        var bypass = Marshal.StringToHGlobalUni(value.Bypass ?? "");
        var pac = Marshal.StringToHGlobalUni(value.Pac ?? "");
        var size = Marshal.SizeOf<Option>();
        var options = Marshal.AllocHGlobal(size * 4);
        try
        {
            uint flags = 1; // PROXY_TYPE_DIRECT
            if (!string.IsNullOrEmpty(value.Proxy)) flags |= 2;
            if (!string.IsNullOrEmpty(value.Pac)) flags |= 4;
            if (value.AutoDetect) flags |= 8;
            Option[] values = [
                new() { Name = 1, Value = new() { Flags = flags } },
                new() { Name = 2, Value = new() { Text = proxy } },
                new() { Name = 3, Value = new() { Text = bypass } },
                new() { Name = 4, Value = new() { Text = pac } }
            ];
            for (var i = 0; i < values.Length; i++) Marshal.StructureToPtr(values[i], options + i * size, false);
            var list = new OptionList { Size = Marshal.SizeOf<OptionList>(), Count = values.Length, Options = options };
            if (!InternetSetOption(IntPtr.Zero, 75, ref list, list.Size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            if (!RefreshInternetOption(IntPtr.Zero, 39, IntPtr.Zero, 0) ||
                !RefreshInternetOption(IntPtr.Zero, 37, IntPtr.Zero, 0))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            var actual = Read();
            if (actual.AutoDetect != value.AutoDetect || (actual.Proxy ?? "") != (value.Proxy ?? "") ||
                (actual.Pac ?? "") != (value.Pac ?? "") || (actual.Bypass ?? "") != (value.Bypass ?? ""))
                throw new InvalidOperationException("Native proxy configuration readback mismatch.");
        }
        finally
        {
            Marshal.FreeHGlobal(options);
            Marshal.FreeHGlobal(proxy);
            Marshal.FreeHGlobal(bypass);
            Marshal.FreeHGlobal(pac);
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IeConfig
    {
        [MarshalAs(UnmanagedType.Bool)] public bool AutoDetect;
        public IntPtr Pac, Proxy, Bypass;
    }
    [StructLayout(LayoutKind.Explicit)]
    private struct OptionValue
    {
        [FieldOffset(0)] public uint Flags;
        [FieldOffset(0)] public IntPtr Text;
    }
    [StructLayout(LayoutKind.Sequential)]
    private struct Option { public int Name; public OptionValue Value; }
    [StructLayout(LayoutKind.Sequential)]
    private struct OptionList
    {
        public int Size;
        public IntPtr Connection;
        public int Count, Error;
        public IntPtr Options;
    }
    [DllImport("winhttp.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool WinHttpGetIEProxyConfigForCurrentUser(out IeConfig config);
    [DllImport("kernel32.dll")]
    private static extern IntPtr GlobalFree(IntPtr handle);
    [DllImport("wininet.dll", EntryPoint = "InternetSetOptionW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool InternetSetOption(IntPtr internet, int option, ref OptionList value, int size);
    [DllImport("wininet.dll", EntryPoint = "InternetSetOptionW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool RefreshInternetOption(IntPtr internet, int option, IntPtr value, int size);
}
