using System.ComponentModel;
using System.Diagnostics;
using System.IO.Compression;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>What came of trying to apply an update the user asked for.</summary>
public enum ClientSelfUpdateOutcome
{
    /// <summary>
    /// Windows: the package is staged and a helper is waiting for this process to exit. The
    /// caller must now tear this process down — see <c>ClientUpdateViewModel</c>.
    /// </summary>
    Restarting,

    /// <summary>macOS: Terminal is open with the install command, waiting on the user.</summary>
    OpenedTerminal,

    /// <summary>Neither could be attempted automatically; the browser went to the download page.</summary>
    OpenedDownloadPage,

    /// <summary>Nothing irreversible happened. <see cref="ClientSelfUpdateResult.Note"/> says what to tell the user.</summary>
    Problem,
}

/// <param name="Note">Wording for the user, in Chinese, when there is something worth saying.</param>
public sealed record ClientSelfUpdateResult(ClientSelfUpdateOutcome Outcome, string? Note = null);

/// <summary>
/// The process-spawning half of applying an update — kept behind a seam for the same reason
/// <c>ICodexAppLauncher</c> is: nothing here can run inside a test without actually killing a
/// process or opening a terminal.
/// </summary>
internal interface IClientRelaunchHost
{
    /// <summary>
    /// Windows only. Starts a helper that waits for <paramref name="currentProcessId"/> to
    /// exit, copies <paramref name="stagingDirectory"/> over
    /// <paramref name="installDirectory"/>, and relaunches <paramref name="exePath"/>. Returns
    /// once the helper has been started, not once the swap is done — this process is expected
    /// to have exited by the time it runs.
    /// </summary>
    bool StartRelaunchHelper(string stagingDirectory, string installDirectory, string exePath, int currentProcessId);

    /// <summary>macOS only. Opens Terminal with <paramref name="command"/> typed in, not yet run.</summary>
    bool OpenTerminalWithCommand(string command);

    /// <summary>Opens <paramref name="url"/> in the default browser.</summary>
    bool OpenUrl(Uri url);
}

/// <summary>
/// Applies a <see cref="ClientUpdateInfo"/> the user has already agreed to.
/// </summary>
/// <remarks>
/// Downloading and unzipping are the parts worth testing directly and can run against a real
/// temporary directory and a stub server, the same way <c>ClaudeCodeSettingsWriterTests</c>
/// exercises real files rather than a filesystem abstraction. What happens after — killing this
/// process, running a shell script — cannot be observed from inside the process doing it, which
/// is exactly what <see cref="IClientRelaunchHost"/> is for.
/// </remarks>
internal sealed class ClientSelfUpdater(HttpClient http, IClientRelaunchHost host)
{
    private readonly HttpClient _http = http ?? throw new ArgumentNullException(nameof(http));
    private readonly IClientRelaunchHost _host = host ?? throw new ArgumentNullException(nameof(host));

    public async Task<ClientSelfUpdateResult> ApplyAsync(ClientUpdateInfo update, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(update);

        switch (update.Channel)
        {
            case ClientUpdateChannel.SelfReplace when update.PackageUrl is { } packageUrl:
                return await ApplyWindowsAsync(packageUrl, cancellationToken).ConfigureAwait(false);

            case ClientUpdateChannel.RunInTerminal when update.TerminalCommand is { } command:
                return _host.OpenTerminalWithCommand(command)
                    ? new ClientSelfUpdateResult(ClientSelfUpdateOutcome.OpenedTerminal)
                    : new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法打开终端，请手动执行安装命令。");

            default:
                // Reached for OpenDownloadPage, and defensively for a SelfReplace/RunInTerminal
                // channel whose URL/command field was not actually set — a checker bug should
                // still land somewhere useful rather than throw on a null the switch trusted.
                return _host.OpenUrl(update.DownloadPage)
                    ? new ClientSelfUpdateResult(ClientSelfUpdateOutcome.OpenedDownloadPage)
                    : new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法打开下载页面，请手动前往下载。");
        }
    }

    private async Task<ClientSelfUpdateResult> ApplyWindowsAsync(Uri packageUrl, CancellationToken cancellationToken)
    {
        string root = Path.Combine(Path.GetTempPath(), $"gongfei-client-update-{Guid.NewGuid():N}");
        string zipPath = root + ".zip";
        string extractDirectory = root;
        try
        {
            await using (FileStream file = File.Create(zipPath))
            using (HttpResponseMessage response = await _http
                       .GetAsync(packageUrl, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                       .ConfigureAwait(false))
            {
                response.EnsureSuccessStatusCode();
                await response.Content.CopyToAsync(file, cancellationToken).ConfigureAwait(false);
            }

            // The release zip has no top-level wrapper folder (see "打包 Windows zip" in
            // client-release.yml) — its contents sit directly where AppContext.BaseDirectory's
            // do, so extracting straight into a staging directory mirrors the install layout
            // with nothing to strip.
            ZipFile.ExtractToDirectory(zipPath, extractDirectory);

            // Not a fixed filename: the shipped exe is renamed at packaging time
            // (共飞-ChatGPT助手.exe), and a user is free to rename it again. Whatever this
            // process was actually launched as is what has to come back.
            string? exePath = Environment.ProcessPath;
            string? installDirectory = exePath is null ? null : Path.GetDirectoryName(exePath);
            if (exePath is null || installDirectory is null)
            {
                TryDeleteDirectory(extractDirectory);
                return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法确定当前程序的安装目录。");
            }

            if (!_host.StartRelaunchHelper(extractDirectory, installDirectory, exePath, Environment.ProcessId))
            {
                TryDeleteDirectory(extractDirectory);
                return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法启动更新助手，请前往下载页手动更新。");
            }

            // extractDirectory is deliberately left in place: the helper copies from it after
            // this process exits, and deletes it once done.
            return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Restarting);
        }
        catch (Exception ex) when (ex is IOException or HttpRequestException or InvalidDataException or UnauthorizedAccessException)
        {
            ClientLog.Warning("下载或解压更新包失败", ex);
            TryDeleteDirectory(extractDirectory);
            return new ClientSelfUpdateResult(
                ClientSelfUpdateOutcome.Problem,
                "下载或解压更新包失败，请稍后重试或前往下载页手动更新。");
        }
        finally
        {
            TryDeleteFile(zipPath);
        }
    }

    private static void TryDeleteFile(string path)
    {
        try
        {
            if (File.Exists(path)) File.Delete(path);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Best-effort cleanup; a leftover temp file is not worth failing the update over.
        }
    }

    private static void TryDeleteDirectory(string path)
    {
        try
        {
            if (Directory.Exists(path)) Directory.Delete(path, recursive: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Same trade-off as TryDeleteFile.
        }
    }
}

/// <summary>The real <see cref="IClientRelaunchHost"/>: actual processes, actually spawned.</summary>
internal sealed class ClientRelaunchHost : IClientRelaunchHost
{
    public bool StartRelaunchHelper(string stagingDirectory, string installDirectory, string exePath, int currentProcessId)
    {
        try
        {
            string scriptPath = Path.Combine(Path.GetTempPath(), $"gongfei-client-update-{Guid.NewGuid():N}.ps1");
            File.WriteAllText(scriptPath, BuildRelaunchScript(stagingDirectory, installDirectory, exePath, currentProcessId));

            var startInfo = new ProcessStartInfo("powershell.exe")
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                WindowStyle = ProcessWindowStyle.Hidden,
            };
            startInfo.ArgumentList.Add("-NoProfile");
            startInfo.ArgumentList.Add("-ExecutionPolicy");
            startInfo.ArgumentList.Add("Bypass");
            startInfo.ArgumentList.Add("-WindowStyle");
            startInfo.ArgumentList.Add("Hidden");
            startInfo.ArgumentList.Add("-File");
            startInfo.ArgumentList.Add(scriptPath);
            Process.Start(startInfo);
            return true;
        }
        catch (Exception ex) when (ex is Win32Exception or InvalidOperationException or PlatformNotSupportedException or IOException)
        {
            ClientLog.Warning("无法启动更新助手", ex);
            return false;
        }
    }

    public bool OpenTerminalWithCommand(string command)
    {
        try
        {
            // AppleScript, not `Process.Start(command)` directly: the point is for the user to
            // see the command and press Return themselves, not for this process to run it.
            string script = "tell application \"Terminal\"\n" +
                             "  activate\n" +
                             $"  do script \"{EscapeAppleScriptString(command)}\"\n" +
                             "end tell";
            var startInfo = new ProcessStartInfo("osascript") { UseShellExecute = false };
            startInfo.ArgumentList.Add("-e");
            startInfo.ArgumentList.Add(script);
            Process.Start(startInfo);
            return true;
        }
        catch (Exception ex) when (ex is Win32Exception or InvalidOperationException or PlatformNotSupportedException or IOException)
        {
            ClientLog.Warning("无法打开终端执行安装命令", ex);
            return false;
        }
    }

    public bool OpenUrl(Uri url) => BrowserLauncher.TryOpen(url);

    private static string EscapeAppleScriptString(string value) =>
        value.Replace("\\", "\\\\").Replace("\"", "\\\"");

    /// <summary>
    /// Waits for this process to actually exit, then mirrors the staged files over the install
    /// directory and relaunches. Written to <c>%TEMP%</c> rather than into
    /// <paramref name="installDirectory"/> — that is the directory about to be overwritten.
    /// </summary>
    /// <remarks>
    /// <para>
    /// <c>robocopy</c> rather than <c>Copy-Item</c>: a self-update is exactly the case its
    /// retry flags exist for — a file that is still momentarily locked (by an AV scan, an
    /// Explorer thumbnail, this very process taking a moment to release its own exe) gets a
    /// handful of retries instead of failing the whole update. <c>/IS /IT</c> forces the copy
    /// even when robocopy's own same-size/same-timestamp heuristic would otherwise call an old
    /// and a new file "the same" and skip it.
    /// </para>
    /// <para>
    /// <c>Wait-Process -ErrorAction SilentlyContinue</c> also covers the process already being
    /// gone by the time this runs — <c>Wait-Process</c> otherwise errors immediately on an
    /// unknown id rather than treating "already exited" as done.
    /// </para>
    /// </remarks>
    private static string BuildRelaunchScript(string staging, string install, string exePath, int processId) => $$"""
        $ErrorActionPreference = 'Continue'
        try { Wait-Process -Id {{processId}} -Timeout 60 -ErrorAction SilentlyContinue } catch {}
        Start-Sleep -Milliseconds 500
        robocopy '{{Escape(staging)}}' '{{Escape(install)}}' /E /IS /IT /R:5 /W:1 /NFL /NDL /NJH /NJS | Out-Null
        Start-Sleep -Milliseconds 200
        Remove-Item -LiteralPath '{{Escape(staging)}}' -Recurse -Force -ErrorAction SilentlyContinue
        Start-Process -FilePath '{{Escape(exePath)}}'
        Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue
        """;

    private static string Escape(string value) => value.Replace("'", "''");
}
