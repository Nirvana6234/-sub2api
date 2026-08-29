using LanAi.RelayClient.Platform.MacOS;
using LanAi.Workspace.Injection;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>A ChatGPT app whose state the test dictates.</summary>
internal sealed class FakeMacCodexProcess : IMacCodexProcess
{
    public bool Installed { get; set; } = true;

    public bool Running { get; set; }

    /// <summary>Set to simulate an app that ignores the quit request.</summary>
    public bool RefusesToQuit { get; set; }

    public bool LaunchFails { get; set; }

    public int QuitCalls { get; private set; }

    public int LaunchCalls { get; private set; }

    public bool IsInstalled() => Installed;

    public bool IsRunning() => Running;

    public bool Quit()
    {
        QuitCalls++;
        if (RefusesToQuit)
        {
            return false;
        }

        Running = false;
        return true;
    }

    public bool Launch()
    {
        LaunchCalls++;
        if (LaunchFails)
        {
            return false;
        }

        Running = true;
        return true;
    }
}

/// <remarks>
/// The decision table, tested on Windows. What stays unverified is
/// <c>MacCodexProcess</c> — the bundle identifier, <c>open -b</c>, and whether an
/// Apple Events quit is granted — which is a much smaller surface than the branching
/// here.
/// </remarks>
public sealed class MacCodexAppLauncherTests
{
    private static Task<CodexLaunchResult> LaunchAsync(
        FakeMacCodexProcess process, bool allowRestart = false) =>
        new MacCodexAppLauncher(process)
            .EnsureDebugPortAsync(new CodexLaunchRequest { AllowTerminateExisting = allowRestart });

    [Fact]
    public async Task AnAppThatIsNotRunningIsSimplyLaunched()
    {
        var process = new FakeMacCodexProcess();

        CodexLaunchResult result = await LaunchAsync(process);

        Assert.Equal(CodexLaunchOutcome.Launched, result.Outcome);
        Assert.True(result.CanAttach);
        Assert.Equal(1, process.LaunchCalls);
        Assert.Equal(0, process.QuitCalls);
    }

    [Fact]
    public async Task AMissingAppIsReportedRatherThanLaunched()
    {
        var process = new FakeMacCodexProcess { Installed = false };

        CodexLaunchResult result = await LaunchAsync(process);

        Assert.Equal(CodexLaunchOutcome.NotInstalled, result.Outcome);
        Assert.False(result.CanAttach);
        Assert.Equal(0, process.LaunchCalls);
    }

    /// <remarks>
    /// <b>The load-bearing case.</b> The client has just rewritten <c>~/.codex</c>, and
    /// whether a running ChatGPT re-reads it is unverified on real hardware. Attaching
    /// to it anyway would have the client report 就绪 while ChatGPT is still on the
    /// user's own account — a failure with no symptom until the OpenAI bill arrives.
    /// </remarks>
    [Fact]
    public async Task ARunningAppIsNotSilentlyAttachedTo()
    {
        var process = new FakeMacCodexProcess { Running = true };

        CodexLaunchResult result = await LaunchAsync(process);

        Assert.Equal(CodexLaunchOutcome.BlockedByRunningInstance, result.Outcome);
        Assert.False(result.CanAttach);
        Assert.Equal(0, process.QuitCalls);
        Assert.Equal(0, process.LaunchCalls);
    }

    [Fact]
    public async Task WithConsentARunningAppIsQuitAndRelaunched()
    {
        var process = new FakeMacCodexProcess { Running = true };

        CodexLaunchResult result = await LaunchAsync(process, allowRestart: true);

        Assert.Equal(CodexLaunchOutcome.Launched, result.Outcome);
        Assert.Equal(1, process.QuitCalls);
        Assert.Equal(1, process.LaunchCalls);
    }

    /// <remarks>
    /// Reported as still blocked, not as a launch failure: the user's next step is the
    /// same either way — close ChatGPT — and "启动失败" would point them elsewhere.
    /// </remarks>
    [Fact]
    public async Task AnAppThatWillNotQuitIsNotRelaunchedOnTopOfItself()
    {
        var process = new FakeMacCodexProcess { Running = true, RefusesToQuit = true };

        CodexLaunchResult result = await LaunchAsync(process, allowRestart: true);

        Assert.Equal(CodexLaunchOutcome.BlockedByRunningInstance, result.Outcome);
        Assert.Equal(0, process.LaunchCalls);
        Assert.Contains("手动关闭", result.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AFailedLaunchIsReportedAsUnavailableRatherThanReady()
    {
        var process = new FakeMacCodexProcess { LaunchFails = true };

        CodexLaunchResult result = await LaunchAsync(process);

        Assert.Equal(CodexLaunchOutcome.DebugPortUnavailable, result.Outcome);
        Assert.False(result.CanAttach);
    }

    /// <remarks>
    /// There is no DevTools endpoint on this platform. Zero rather than a plausible
    /// port number, so nothing downstream reads meaning into it.
    /// </remarks>
    [Fact]
    public async Task NoDebugPortIsClaimed()
    {
        CodexLaunchResult result = await LaunchAsync(new FakeMacCodexProcess());

        Assert.Equal(0, result.Port);
        Assert.Equal(0u, result.ProcessId);
    }

    [Fact]
    public void InstallationIsReadFromTheAppRatherThanCached()
    {
        var process = new FakeMacCodexProcess { Installed = false };
        var launcher = new MacCodexAppLauncher(process);

        Assert.False(launcher.IsInstalled);

        // Installed while the client was running — a perfectly ordinary sequence,
        // since the client is what tells the user to go and install it.
        process.Installed = true;
        Assert.True(launcher.IsInstalled);
    }
}

/// <remarks>
/// The word this covers appears in the one message whose whole job is to tell the
/// user where the window went. Sending a Mac user to look in the 托盘 points them at
/// something their system does not have.
/// </remarks>
public sealed class PlatformWordsTests
{
    [Fact]
    public void WindowsCallsItTheTray() =>
        Assert.Equal("托盘", LanAi.RelayClient.Platform.PlatformWords.Resolve(isMacOS: false));

    [Fact]
    public void MacOsCallsItTheMenuBar() =>
        Assert.Equal("菜单栏", LanAi.RelayClient.Platform.PlatformWords.Resolve(isMacOS: true));

    [Fact]
    public void ThisMachineSaysTray() =>
        Assert.Equal("托盘", LanAi.RelayClient.Platform.PlatformWords.NotificationArea);
}
