using System.Diagnostics;
using System.Runtime.InteropServices;
using LanAi.Workspace.Injection.Cdp;

namespace LanAi.Workspace.Injection;

/// <summary>How <see cref="CodexAppLauncher"/> resolved the debug endpoint.</summary>
public enum CodexLaunchOutcome
{
    /// <summary>A debug port was already listening; the user's session was left alone.</summary>
    AttachedToExisting,

    /// <summary>The app was started with a debug port.</summary>
    Launched,

    /// <summary>
    /// The app is running without a debug port. Restarting it would interrupt any
    /// in-flight task, so the caller must confirm with the user first.
    /// </summary>
    BlockedByRunningInstance,

    /// <summary>The official desktop package is not installed for this user.</summary>
    NotInstalled,

    /// <summary>The app was started but never opened the debug port.</summary>
    DebugPortUnavailable,
}

public sealed record CodexLaunchResult(
    CodexLaunchOutcome Outcome,
    int Port,
    uint ProcessId,
    string Message)
{
    public bool CanAttach => Outcome is CodexLaunchOutcome.AttachedToExisting
        or CodexLaunchOutcome.Launched;
}

public sealed record CodexLaunchRequest
{
    /// <summary>Loopback port for the DevTools endpoint.</summary>
    public int Port { get; init; } = 9777;

    /// <summary>
    /// Permits terminating a running instance that has no debug port. Defaults to
    /// <c>false</c>: force-closing the official app discards the user's in-flight
    /// turn, so this must be an explicit, user-visible decision.
    /// </summary>
    public bool AllowTerminateExisting { get; init; }

    public TimeSpan DebugPortTimeout { get; init; } = TimeSpan.FromSeconds(20);
}

/// <summary>
/// Starts the official Codex desktop app with a loopback DevTools port, or attaches
/// to an instance that already exposes one.
/// </summary>
/// <remarks>
/// The package lives under a protected <c>WindowsApps</c> directory, so launching
/// its executable directly fails with access denied. Passing arguments to a packaged
/// app requires the shell activation contract
/// (<c>IApplicationActivationManager.ActivateApplication</c>); that is the only
/// method verified to work here.
/// </remarks>
public sealed class CodexAppLauncher
{
    private const string DefaultPackageFamilyName = "OpenAI.Codex_2p2nqsd0c76g0";
    private const string DefaultApplicationId = "App";
    private const string ProcessName = "ChatGPT";

    private readonly CdpTargetLocator _locator;
    private readonly string _packageFamilyName;
    private readonly string _applicationId;

    public CodexAppLauncher(
        CdpTargetLocator? locator = null,
        string packageFamilyName = DefaultPackageFamilyName,
        string applicationId = DefaultApplicationId)
    {
        _locator = locator ?? new CdpTargetLocator();
        _packageFamilyName = packageFamilyName;
        _applicationId = applicationId;
    }

    public string ApplicationUserModelId => $"{_packageFamilyName}!{_applicationId}";

    /// <summary>
    /// The per-user package data directory, which exists only while the package is
    /// installed. Keyed on the package family name so it survives version upgrades.
    /// </summary>
    public string PackageDataDirectory => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "Packages",
        _packageFamilyName);

    public bool IsInstalled => Directory.Exists(PackageDataDirectory);

    /// <summary>
    /// Ensures a DevTools endpoint is reachable, preferring attachment over restart.
    /// </summary>
    public async Task<CodexLaunchResult> EnsureDebugPortAsync(
        CodexLaunchRequest request,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(request);

        var existing = await _locator
            .TryGetBrowserAsync(request.Port, cancellationToken)
            .ConfigureAwait(false);
        if (existing is not null)
        {
            return new CodexLaunchResult(
                CodexLaunchOutcome.AttachedToExisting,
                request.Port,
                0,
                $"已连接到正在运行的实例（{existing.Browser}），未重启应用。");
        }

        if (!IsInstalled)
        {
            return new CodexLaunchResult(
                CodexLaunchOutcome.NotInstalled,
                request.Port,
                0,
                "未检测到官方 Codex 桌面应用安装。");
        }

        var running = GetRunningProcesses();
        if (running.Length > 0 && !request.AllowTerminateExisting)
        {
            return new CodexLaunchResult(
                CodexLaunchOutcome.BlockedByRunningInstance,
                request.Port,
                0,
                "官方应用正在运行但未开启调试端口。开启注入需要重启它，"
                    + "这会中断正在进行的任务，请先征得用户确认。");
        }

        // A packaged app enforces a single instance: a second activation is routed to
        // the existing process and the new arguments are dropped, so the running
        // instance has to go first.
        TerminateRunning(running);

        var processId = Activate(ApplicationUserModelId, $"--remote-debugging-port={request.Port}");

        var ready = await WaitForDebugPortAsync(
                request.Port,
                request.DebugPortTimeout,
                cancellationToken)
            .ConfigureAwait(false);

        return ready is null
            ? new CodexLaunchResult(
                CodexLaunchOutcome.DebugPortUnavailable,
                request.Port,
                processId,
                $"应用已启动（pid {processId}），但在超时前未开放调试端口 {request.Port}。")
            : new CodexLaunchResult(
                CodexLaunchOutcome.Launched,
                request.Port,
                processId,
                $"已带调试端口启动（{ready.Browser}）。");
    }

    private async Task<CdpBrowserInfo?> WaitForDebugPortAsync(
        int port,
        TimeSpan timeout,
        CancellationToken cancellationToken)
    {
        var deadline = DateTimeOffset.UtcNow + timeout;
        while (DateTimeOffset.UtcNow < deadline)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var info = await _locator.TryGetBrowserAsync(port, cancellationToken).ConfigureAwait(false);
            if (info is not null)
            {
                return info;
            }

            await Task.Delay(TimeSpan.FromMilliseconds(500), cancellationToken).ConfigureAwait(false);
        }

        return null;
    }

    private static Process[] GetRunningProcesses()
    {
        try
        {
            return Process.GetProcessesByName(ProcessName);
        }
        catch (InvalidOperationException)
        {
            return [];
        }
    }

    private static void TerminateRunning(Process[] processes)
    {
        foreach (var process in processes)
        {
            try
            {
                process.Kill(entireProcessTree: true);
                process.WaitForExit(5000);
            }
            catch (Exception exception) when (
                exception is InvalidOperationException
                    or System.ComponentModel.Win32Exception
                    or NotSupportedException)
            {
                // Already gone, or owned by another session; nothing to reclaim.
            }
            finally
            {
                process.Dispose();
            }
        }
    }

    private static uint Activate(string applicationUserModelId, string arguments)
    {
        var manager = (IApplicationActivationManager)new ApplicationActivationManager();
        var hr = manager.ActivateApplication(applicationUserModelId, arguments, 0, out var processId);
        if (hr != 0)
        {
            Marshal.ThrowExceptionForHR(hr);
        }

        return processId;
    }

    [ComImport]
    [Guid("2e941141-7f97-4756-ba1d-9decde894a3d")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IApplicationActivationManager
    {
        [PreserveSig]
        int ActivateApplication(
            [In, MarshalAs(UnmanagedType.LPWStr)] string applicationUserModelId,
            [In, MarshalAs(UnmanagedType.LPWStr)] string? arguments,
            [In] int options,
            [Out] out uint processId);
    }

    [ComImport]
    [Guid("45BA127D-10A8-46EA-8AB7-56EA9078943C")]
    private class ApplicationActivationManager
    {
    }
}
