using AgentDock.ControlPanel;

internal static class FunnelVerificationServiceTests
{
    internal static async Task Run(Action<bool, string> check)
    {
        await RepairsOnlyAfterSustainedEligibleFailures(check);
        await IgnoresIneligibleFailures(check);
        await ReportsRepairFailureAndKeepsVerifying(check);
        await EnforcesRepairCooldown(check);
    }

    private static NativeTunnelStatus PublicFailure(bool localReady = true) => new()
    {
        Provider = "tailscale", Mode = "funnel", Phase = "Degraded", DiagnosticCode = "public_unreachable",
        LocalReady = localReady, Running = true, FunnelEnabled = true,
        PublicUrl = "https://device.example.ts.net", LocalOrigin = "http://127.0.0.1:8765"
    };

    private static NativeTunnelStatus Ready() => new()
    {
        Provider = "tailscale", Mode = "funnel", Phase = "Ready", Ready = true,
        LocalReady = true, Running = true, FunnelEnabled = true,
        PublicUrl = "https://device.example.ts.net", LocalOrigin = "http://127.0.0.1:8765"
    };

    private static FunnelVerificationService Create(Func<DateTimeOffset>? now = null) =>
        new((_, token) => token.IsCancellationRequested ? Task.FromCanceled(token) : Task.CompletedTask, now);

    private static async Task RepairsOnlyAfterSustainedEligibleFailures(Action<bool, string> check)
    {
        using var service = Create();
        var probes = 0; var repairs = 0; var reports = new List<NativeTunnelStatus>();
        NativeTunnelStatus Probe() => ++probes <= FunnelVerificationService.RepairFailureThreshold ? PublicFailure() : Ready();
        await service.Ensure("device|origin", _ => Task.FromResult(Probe()), _ => { repairs++; return Task.CompletedTask; }, reports.Add);
        check(repairs == 1 && probes == FunnelVerificationService.RepairFailureThreshold + 1,
            "sustained public failure triggers exactly one repair before re-verification");
        check(reports.Last().Ready && reports.Count(status => status.DiagnosticCode == "public_unreachable") == FunnelVerificationService.RepairFailureThreshold,
            "repair does not suppress failure evidence and readiness requires a new successful probe");

        using var shortService = Create();
        probes = 0; repairs = 0;
        await shortService.Ensure("short", _ => Task.FromResult(++probes <= 3
            ? PublicFailure()
            : new NativeTunnelStatus { Provider = "tailscale", Mode = "funnel", DiagnosticCode = "oauth_origin_mismatch" }),
            _ => { repairs++; return Task.CompletedTask; }, _ => { });
        check(repairs == 0, "three transient public failures never mutate Funnel configuration");
    }

    private static async Task IgnoresIneligibleFailures(Action<bool, string> check)
    {
        foreach (var scenario in new[] { "probe_failed", "local_unhealthy" })
        {
            using var service = Create();
            var repairs = 0;
            await service.Ensure(scenario, _ => Task.FromResult(scenario == "probe_failed"
                    ? new NativeTunnelStatus { Provider = "tailscale", Mode = "funnel", DiagnosticCode = "probe_failed" }
                    : PublicFailure(localReady: false)),
                _ => { repairs++; return Task.CompletedTask; }, _ => { });
            check(repairs == 0, "ineligible verification failures do not write network state: " + scenario);
        }
    }

    private static async Task ReportsRepairFailureAndKeepsVerifying(Action<bool, string> check)
    {
        using var service = Create();
        var probes = 0; var repairs = 0; var reports = new List<NativeTunnelStatus>();
        await service.Ensure("repair-failure", _ => Task.FromResult(++probes <= 4 ? PublicFailure() : Ready()), _ =>
        {
            repairs++;
            throw new IOException("synthetic repair failure");
        }, reports.Add);
        check(repairs == 1 && probes == 5 && reports.Last().Ready,
            "a failed repair is bounded and later verification can still recover");
        check(reports.Any(status => status.DiagnosticCode == "recovery_failed" && status.Diagnostic.Contains("synthetic repair failure")),
            "repair failure remains visible as a distinct diagnostic");
    }

    private static async Task EnforcesRepairCooldown(Action<bool, string> check)
    {
        var now = DateTimeOffset.Parse("2026-10-03T00:00:00Z");
        using var service = Create(() => now);
        var repairs = 0;
        async Task RunCycle(string identity)
        {
            var probes = 0;
            await service.Ensure(identity, _ => Task.FromResult(++probes <= 4 ? PublicFailure() : Ready()),
                _ => { repairs++; return Task.CompletedTask; }, _ => { });
        }
        await RunCycle("same");
        now += TimeSpan.FromMinutes(6);
        await RunCycle("same");
        check(repairs == 1, "same Funnel identity cannot be repeatedly rewritten inside the cooldown");
        now += TimeSpan.FromMinutes(10);
        await RunCycle("same");
        check(repairs == 2, "repair becomes eligible again after the bounded cooldown");
    }
}
