using LanAi.Workspace.Injection;
using LanAi.Workspace.Injection.Sentinel;
using Xunit;

namespace AiSwitch.Injection.Tests;

public sealed class CodexInjectionSessionTests
{
    private static CodexLimitSnapshot Snapshot(CodexLimitLevel level, double? percent = null, string? reset = null)
        => new(level, new CodexLimitFacts { UsedPercent = percent, ResetText = reset }, DateTimeOffset.UtcNow);

    [Fact]
    public void OverlayShowsReachedState()
    {
        var (tone, label) = CodexInjectionSession.Describe(Snapshot(CodexLimitLevel.Reached));

        Assert.Equal("reached", tone);
        Assert.Contains("已用尽", label);
    }

    [Fact]
    public void OverlayShowsPercentageWhenKnown()
    {
        var (tone, label) = CodexInjectionSession.Describe(Snapshot(CodexLimitLevel.Approaching, 91.4));

        Assert.Equal("approaching", tone);
        Assert.Contains("91%", label);
    }

    /// <summary>
    /// The percentage is a guess that may never be readable, so the approaching state
    /// must still render without one.
    /// </summary>
    [Fact]
    public void OverlayFallsBackWhenPercentageIsUnavailable()
    {
        var (tone, label) = CodexInjectionSession.Describe(Snapshot(CodexLimitLevel.Approaching));

        Assert.Equal("approaching", tone);
        Assert.Contains("接近上限", label);
    }

    [Fact]
    public void OverlayShowsNormalAndUnknown()
    {
        Assert.Equal("normal", CodexInjectionSession.Describe(Snapshot(CodexLimitLevel.Normal)).Tone);
        Assert.Equal("unknown", CodexInjectionSession.Describe(Snapshot(CodexLimitLevel.Unknown)).Tone);
        Assert.Equal("unknown", CodexInjectionSession.Describe(null).Tone);
    }

    [Fact]
    public async Task AcceptBeforeStartIsReportedNotThrown()
    {
        using var session = new CodexInjectionSession(new FakeRelaySwitchGateway());

        var outcome = await session.AcceptAsync(CancellationToken.None);

        Assert.False(outcome.Success);
        Assert.Contains("未启动", outcome.Summary);
    }

    [Fact]
    public void DeclineBeforeStartIsHarmless()
    {
        using var session = new CodexInjectionSession(new FakeRelaySwitchGateway());

        session.Decline();

        Assert.False(session.IsRunning);
    }

    [Fact]
    public void NeedsRestartConsentIsSurfacedForARunningInstanceWithoutADebugPort()
    {
        var blocked = new CodexInjectionStartResult(
            false,
            CodexLaunchOutcome.BlockedByRunningInstance,
            "官方应用正在运行但未开启调试端口。");

        Assert.True(blocked.NeedsRestartConsent);

        var attached = new CodexInjectionStartResult(true, CodexLaunchOutcome.AttachedToExisting, "ok");
        Assert.False(attached.NeedsRestartConsent);
    }

    [Fact]
    public void DefaultsRefuseToRestartTheOfficialApp()
    {
        var options = new CodexInjectionSessionOptions();

        Assert.False(options.AllowTerminateExisting);
        Assert.Equal(9777, options.Port);
    }
}
