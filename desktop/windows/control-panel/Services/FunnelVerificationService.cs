using System.Diagnostics;

namespace AgentDock.ControlPanel;

internal sealed class FunnelVerificationService : IDisposable
{
    internal const int RepairFailureThreshold = 4;
    internal static readonly TimeSpan RepairCooldown = TimeSpan.FromMinutes(15);

    private readonly object _gate = new();
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;
    private readonly Func<DateTimeOffset> _now;
    private CancellationTokenSource? _current;
    private Task _work = Task.CompletedTask;
    private string _identity = "";
    private DateTimeOffset _completedAt = DateTimeOffset.MinValue;
    private string _lastRepairIdentity = "";
    private DateTimeOffset _lastRepairAt = DateTimeOffset.MinValue;
    private long _generation;
    private bool _disposed;

    internal FunnelVerificationService(
        Func<TimeSpan, CancellationToken, Task>? delay = null,
        Func<DateTimeOffset>? now = null)
    {
        _delay = delay ?? Task.Delay;
        _now = now ?? (() => DateTimeOffset.UtcNow);
    }

    internal Task Ensure(
        string identity,
        Func<CancellationToken, Task<NativeTunnelStatus>> probe,
        Func<CancellationToken, Task> repair,
        Action<NativeTunnelStatus> report)
    {
        lock (_gate)
        {
            if (_disposed) return Task.CompletedTask;
            if (_identity == identity && (!_work.IsCompleted || _now() - _completedAt < TimeSpan.FromMinutes(5))) return _work;
            _current?.Cancel();
            var generation = ++_generation;
            _identity = identity;
            var cancellation = new CancellationTokenSource(TimeSpan.FromMinutes(10));
            _current = cancellation;
            _work = Task.Run(() => RunAsync(identity, generation, cancellation, probe, repair, report));
            return _work;
        }
    }

    private async Task RunAsync(
        string identity,
        long generation,
        CancellationTokenSource cancellation,
        Func<CancellationToken, Task<NativeTunnelStatus>> probe,
        Func<CancellationToken, Task> repair,
        Action<NativeTunnelStatus> report)
    {
        using (cancellation)
        {
            try
            {
                var consecutivePublicFailures = 0;
                for (var attempt = 0; attempt < 12; attempt++)
                {
                    cancellation.Token.ThrowIfCancellationRequested();
                    var started = Stopwatch.GetTimestamp();
                    var result = await probe(cancellation.Token).ConfigureAwait(false);
                    result.PublicProbeMs ??= (long)Stopwatch.GetElapsedTime(started).TotalMilliseconds;
                    if (!Report(generation, cancellation, result, report)) return;
                    if (result.Ready) return;
                    if (result.DiagnosticCode is not ("public_unreachable" or "verification_pending" or "probe_failed")) return;

                    consecutivePublicFailures = IsRepairCandidate(result) ? consecutivePublicFailures + 1 : 0;
                    if (consecutivePublicFailures >= RepairFailureThreshold && TryReserveRepair(identity, generation, cancellation))
                    {
                        try
                        {
                            await repair(cancellation.Token).ConfigureAwait(false);
                            consecutivePublicFailures = 0;
                            if (attempt < 11) await _delay(TimeSpan.FromSeconds(1), cancellation.Token).ConfigureAwait(false);
                            continue;
                        }
                        catch (OperationCanceledException) when (cancellation.IsCancellationRequested) { throw; }
                        catch (Exception error)
                        {
                            consecutivePublicFailures = 0;
                            if (!Report(generation, cancellation, RecoveryFailed(result, error), report)) return;
                        }
                    }
                    if (attempt < 11) await _delay(Backoff(attempt), cancellation.Token).ConfigureAwait(false);
                }
            }
            catch (OperationCanceledException) when (cancellation.IsCancellationRequested) { }
            catch (Exception error)
            {
                lock (_gate)
                {
                    if (!_disposed && generation == _generation)
                        report(new NativeTunnelStatus { Provider = "tailscale", Mode = "funnel", Phase = "Degraded", DiagnosticCode = "probe_failed", Diagnostic = "公网验证未完成：" + error.Message });
                }
            }
            finally
            {
                lock (_gate)
                {
                    if (generation == _generation) { _completedAt = _now(); _current = null; }
                }
            }
        }
    }

    private bool Report(long generation, CancellationTokenSource cancellation, NativeTunnelStatus result, Action<NativeTunnelStatus> report)
    {
        lock (_gate)
        {
            if (_disposed || generation != _generation || cancellation.IsCancellationRequested) return false;
            report(result);
            return true;
        }
    }

    private bool TryReserveRepair(string identity, long generation, CancellationTokenSource cancellation)
    {
        lock (_gate)
        {
            if (_disposed || generation != _generation || cancellation.IsCancellationRequested) return false;
            var now = _now();
            if (_lastRepairIdentity == identity && now - _lastRepairAt < RepairCooldown) return false;
            _lastRepairIdentity = identity;
            _lastRepairAt = now;
            return true;
        }
    }

    private static bool IsRepairCandidate(NativeTunnelStatus status) =>
        status.DiagnosticCode == "public_unreachable" && status.LocalReady && status.Running && status.FunnelEnabled;

    private static NativeTunnelStatus RecoveryFailed(NativeTunnelStatus status, Exception error) => new()
    {
        Provider = status.Provider,
        Mode = status.Mode,
        Phase = "Degraded",
        LocalReady = status.LocalReady,
        VerifiedAt = status.VerifiedAt,
        PublicProbeMs = status.PublicProbeMs,
        Installed = status.Installed,
        Configured = status.Configured,
        Running = status.Running,
        Ready = false,
        FunnelEnabled = status.FunnelEnabled,
        BackendState = status.BackendState,
        DeviceName = status.DeviceName,
        DnsName = status.DnsName,
        PublicUrl = status.PublicUrl,
        LocalOrigin = status.LocalOrigin,
        BinaryPath = status.BinaryPath,
        KeyExpiry = status.KeyExpiry,
        DiagnosticCode = "recovery_failed",
        Diagnostic = "公网持续失联，已尝试刷新 AgentDock Funnel 映射但未成功：" + error.Message,
        AuthorizationUrl = status.AuthorizationUrl
    };

    internal static TimeSpan Backoff(int attempt) => TimeSpan.FromSeconds(Math.Min(60, Math.Pow(2, Math.Clamp(attempt, 0, 10) + 1)));

    internal void Cancel()
    {
        lock (_gate)
        {
            ++_generation;
            _identity = "";
            _completedAt = DateTimeOffset.MinValue;
            _current?.Cancel();
            _current = null;
        }
    }

    public void Dispose()
    {
        lock (_gate) { _disposed = true; Cancel(); }
    }
}
