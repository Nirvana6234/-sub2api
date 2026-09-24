using LanAi.RelayClient.Services;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>The few things the macOS launcher needs the operating system to do.</summary>
/// <remarks>
/// Split out so the decision table below — which is the part that can be wrong in a
/// way the user pays for — is exercised on Windows. Only the implementation of these
/// four members is Mac-only.
/// </remarks>
internal interface IMacCodexProcess
{
    bool IsInstalled();

    bool IsRunning();

    /// <summary>Asks the app to quit, returning whether it is gone.</summary>
    bool Quit();

    /// <summary>Starts the app, returning whether the launch was accepted.</summary>
    bool Launch();
}

/// <summary>
/// Starts the official ChatGPT desktop app on macOS.
/// </summary>
/// <remarks>
/// <para>
/// The Windows launcher exists to obtain a DevTools port, because that is how the CDP
/// overlay attaches. <b>macOS v1 has no overlay</b>, so there is no port to negotiate
/// and this only has to start the app — which is why it shares the interface but not
/// a line of the implementation.
/// </para>
/// <para>
/// <b>It still refuses to attach to a running instance, and that is deliberate.</b>
/// The reason differs from Windows': there, a running app lacks the debug port. Here,
/// the client has just rewritten <c>~/.codex</c>, and <b>whether an already-running
/// ChatGPT re-reads that file is exactly the thing nobody has verified on real
/// hardware yet</b> (the open question recorded as G-1). Assuming it does would let
/// the client report 就绪 while ChatGPT is still talking to the user's own account —
/// the failure would be invisible, and the user would find out from their OpenAI bill.
/// Asking to restart is the answer that is correct under both possibilities.
/// </para>
/// <para>
/// If G-1 comes back saying a running app does pick up the config, this class can drop
/// straight to <see cref="CodexLaunchOutcome.AttachedToExisting"/> and the restart
/// prompt disappears. That is a two-line change, and it is the right direction to be
/// wrong in.
/// </para>
/// </remarks>
internal sealed class MacCodexAppLauncher : ICodexAppLauncher
{
    private readonly IMacCodexProcess _process;

    public MacCodexAppLauncher(IMacCodexProcess process) =>
        _process = process ?? throw new ArgumentNullException(nameof(process));

    public bool IsInstalled => _process.IsInstalled();

    public Task<CodexLaunchResult> EnsureDebugPortAsync(
        CodexLaunchRequest request,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(request);
        cancellationToken.ThrowIfCancellationRequested();

        return Task.FromResult(Decide(request));
    }

    private CodexLaunchResult Decide(CodexLaunchRequest request)
    {
        if (!_process.IsInstalled())
        {
            return new CodexLaunchResult(
                CodexLaunchOutcome.NotInstalled, 0, 0, "还没有检测到 ChatGPT 桌面版。");
        }

        if (_process.IsRunning())
        {
            if (!request.AllowTerminateExisting)
            {
                return new CodexLaunchResult(
                    CodexLaunchOutcome.BlockedByRunningInstance,
                    0,
                    0,
                    "ChatGPT 正在运行，需要重启后才能接入。");
            }

            if (!_process.Quit())
            {
                // Reported as still blocked rather than as a failure to launch. The
                // user's next step is the same — close ChatGPT — and saying "启动失败"
                // would point them at the wrong thing.
                ClientLog.Warning("ChatGPT 未能退出，无法重启接入");
                return new CodexLaunchResult(
                    CodexLaunchOutcome.BlockedByRunningInstance,
                    0,
                    0,
                    "ChatGPT 没有退出，请手动关闭后重试。");
            }
        }

        if (!_process.Launch())
        {
            return new CodexLaunchResult(
                CodexLaunchOutcome.DebugPortUnavailable, 0, 0, "无法启动 ChatGPT。");
        }

        // Port and process id stay zero: there is no debug endpoint on this platform,
        // and inventing a number would make the value look meaningful to a reader.
        return new CodexLaunchResult(CodexLaunchOutcome.Launched, 0, 0, "已启动 ChatGPT。");
    }
}
