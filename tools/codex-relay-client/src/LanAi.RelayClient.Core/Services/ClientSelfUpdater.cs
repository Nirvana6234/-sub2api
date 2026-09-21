using System.ComponentModel;
using System.Diagnostics;
using System.IO.Compression;
using System.Text;
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
    /// to have exited by the time it runs. The helper logs its own steps beside
    /// <paramref name="stagingDirectory"/>'s parent, so a relaunch that silently fails still
    /// leaves something to read afterward.
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
/// <para>
/// Downloading and unzipping are the parts worth testing directly and can run against a real
/// temporary directory and a stub server, the same way <c>ClaudeCodeSettingsWriterTests</c>
/// exercises real files rather than a filesystem abstraction. What happens after — killing this
/// process, running a shell script — cannot be observed from inside the process doing it, which
/// is exactly what <see cref="IClientRelaunchHost"/> is for.
/// </para>
/// <para>
/// Windows downloads into <c>&lt;install directory&gt;\update\</c>, not a temp directory. Two
/// things depend on that: a size-matching zip already there is reused rather than fetched again
/// (a cheap stand-in for a real range-resume, cheap enough that one was not worth building), and
/// an already-extracted, never-applied package from an earlier attempt — the helper started but
/// the relaunch step failed, say — is reused on the next click rather than downloaded and
/// unzipped from nothing. <see cref="ApplyWindowsAsync"/> only ever hands the helper a directory
/// that finished extracting cleanly: it extracts into a sibling <c>.tmp</c> directory first and
/// renames it into place, so a crash mid-extraction never leaves a half-written directory that
/// a later click would trust.
/// </para>
/// </remarks>
internal sealed class ClientSelfUpdater
{
    private readonly HttpClient _http;
    private readonly IClientRelaunchHost _host;
    private readonly Func<string?> _currentProcessPath;

    public ClientSelfUpdater(HttpClient http, IClientRelaunchHost host)
        : this(http, host, () => Environment.ProcessPath)
    {
    }

    /// <param name="currentProcessPath">
    /// Stands in for <see cref="Environment.ProcessPath"/>, so a test can point the whole
    /// download/extract/apply sequence at a throwaway directory instead of wherever the test
    /// runner's own executable happens to live — writing an "update" folder there would be
    /// touching a path the test does not own and may not even have permission to.
    /// </param>
    internal ClientSelfUpdater(HttpClient http, IClientRelaunchHost host, Func<string?> currentProcessPath)
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
        _host = host ?? throw new ArgumentNullException(nameof(host));
        _currentProcessPath = currentProcessPath ?? throw new ArgumentNullException(nameof(currentProcessPath));
    }

    public async Task<ClientSelfUpdateResult> ApplyAsync(
        ClientUpdateInfo update,
        IProgress<double>? progress = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(update);

        switch (update.Channel)
        {
            case ClientUpdateChannel.SelfReplace when update.PackageUrl is { } packageUrl:
                return await ApplyWindowsAsync(packageUrl, progress, cancellationToken).ConfigureAwait(false);

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

    private async Task<ClientSelfUpdateResult> ApplyWindowsAsync(
        Uri packageUrl,
        IProgress<double>? progress,
        CancellationToken cancellationToken)
    {
        // Not a fixed filename: the shipped exe is renamed at packaging time
        // (共飞-ChatGPT助手.exe), and a user is free to rename it again. Whatever this
        // process was actually launched as is what has to come back.
        string? exePath = _currentProcessPath();
        string? installDirectory = exePath is null ? null : Path.GetDirectoryName(exePath);
        if (exePath is null || installDirectory is null)
        {
            return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法确定当前程序的安装目录。");
        }

        string updateRoot = Path.Combine(installDirectory, "update");
        string zipPath = Path.Combine(updateRoot, "package.zip");
        string extractDirectory = Path.Combine(updateRoot, "extracted");

        try
        {
            Directory.CreateDirectory(updateRoot);

            if (!HasUsableExtraction(extractDirectory))
            {
                await DownloadIfNeededAsync(packageUrl, zipPath, progress, cancellationToken).ConfigureAwait(false);
                ExtractAtomically(zipPath, extractDirectory);
            }

            if (!_host.StartRelaunchHelper(extractDirectory, installDirectory, exePath, Environment.ProcessId))
            {
                // Left in place on purpose: the extraction is still good, so the next click
                // can retry the helper without downloading or unzipping anything again.
                return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "无法启动更新助手，请稍后重试或前往下载页手动更新。");
            }

            return new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Restarting);
        }
        catch (Exception ex) when (ex is IOException or HttpRequestException or InvalidDataException or UnauthorizedAccessException)
        {
            ClientLog.Warning("下载或解压更新包失败", ex);
            return new ClientSelfUpdateResult(
                ClientSelfUpdateOutcome.Problem,
                "下载或解压更新包失败，请稍后重试或前往下载页手动更新。");
        }
    }

    private static bool HasUsableExtraction(string extractDirectory) =>
        Directory.Exists(extractDirectory) && Directory.EnumerateFileSystemEntries(extractDirectory).Any();

    /// <summary>
    /// Fetches <paramref name="packageUrl"/> into <paramref name="zipPath"/>, unless a file of
    /// exactly the advertised size is already there — an earlier attempt's download that never
    /// got applied. Reusing it turns a retry into "unzip and hand off" instead of "download 40MB
    /// again", at the cost of a false negative if a same-sized-but-different package is ever
    /// published, which republishing a real update (a different size almost always) corrects on
    /// its own.
    /// </summary>
    private async Task DownloadIfNeededAsync(
        Uri packageUrl,
        string zipPath,
        IProgress<double>? progress,
        CancellationToken cancellationToken)
    {
        using HttpResponseMessage response = await _http
            .GetAsync(packageUrl, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
            .ConfigureAwait(false);
        response.EnsureSuccessStatusCode();

        long? expectedLength = response.Content.Headers.ContentLength;
        if (expectedLength is > 0 && File.Exists(zipPath) && new FileInfo(zipPath).Length == expectedLength)
        {
            return;
        }

        await using (FileStream file = File.Create(zipPath))
        await using (Stream responseStream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false))
        {
            byte[] buffer = new byte[81920];
            long readTotal = 0;
            int read;
            while ((read = await responseStream.ReadAsync(buffer, cancellationToken).ConfigureAwait(false)) > 0)
            {
                await file.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
                readTotal += read;
                if (expectedLength is > 0)
                {
                    progress?.Report((double)readTotal / expectedLength.Value);
                }
            }
        }
    }

    /// <summary>
    /// Extracts into a sibling <c>.tmp</c> directory and renames it into place only once
    /// <see cref="ZipFile.ExtractToDirectory(string, string)"/> has returned without throwing —
    /// so <paramref name="extractDirectory"/> existing is always proof it is complete, never a
    /// half-written extraction a retry would otherwise trust.
    /// </summary>
    private static void ExtractAtomically(string zipPath, string extractDirectory)
    {
        string extractingTo = extractDirectory + ".tmp";
        if (Directory.Exists(extractingTo))
        {
            Directory.Delete(extractingTo, recursive: true);
        }

        try
        {
            ZipFile.ExtractToDirectory(zipPath, extractingTo);
        }
        catch
        {
            // A corrupt zip will never succeed by retrying with the same bytes — forcing a
            // fresh download is the only way a later click can recover on its own.
            TryDeleteFile(zipPath);
            TryDeleteDirectory(extractingTo);
            throw;
        }

        if (Directory.Exists(extractDirectory))
        {
            Directory.Delete(extractDirectory, recursive: true);
        }

        Directory.Move(extractingTo, extractDirectory);
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
            // Beside stagingDirectory's parent (…\update\), not %TEMP%: it has to survive
            // this process exiting to be worth reading afterward, and it sits next to the
            // package it describes rather than scattered into a temp folder nobody would
            // think to look in.
            string logPath = Path.Combine(Path.GetDirectoryName(stagingDirectory) ?? Path.GetTempPath(), "helper.log");
            string scriptPath = Path.Combine(Path.GetTempPath(), $"gongfei-client-update-{Guid.NewGuid():N}.ps1");

            // Encoding.UTF8, not File.WriteAllText's own no-BOM default: every path here can
            // carry Chinese characters — the install directory alone routinely does, on top of
            // whatever the release names itself — and Windows PowerShell 5.1 has no way to know
            // a BOM-less file is UTF-8. Without one it falls back to the system codepage and
            // silently mangles every non-ASCII character it reads, including inside this very
            // script's own $log path — which is exactly why a broken run leaves no log to read.
            File.WriteAllText(
                scriptPath,
                BuildRelaunchScript(stagingDirectory, installDirectory, exePath, currentProcessId, logPath),
                Encoding.UTF8);

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
    /// <para>
    /// The relaunch is wrapped in its own <c>try/catch</c>, logged either way, and never allowed
    /// to skip the final cleanup line. The first cut of this script let a relaunch failure (an
    /// AV product or SmartScreen holding a freshly written, unsigned exe, for one) propagate as
    /// an uncaught terminating error, which stopped the script cold right there — the files were
    /// already correctly swapped by that point, but with no relaunch and no log, that looked
    /// from the user's side exactly like the whole update silently doing nothing.
    /// </para>
    /// </remarks>
    private static string BuildRelaunchScript(string staging, string install, string exePath, int processId, string logPath) => $$"""
        $ErrorActionPreference = 'Continue'
        $log = '{{Escape(logPath)}}'
        function Log($msg) {
            Add-Content -LiteralPath $log -Value ("[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $msg) -ErrorAction SilentlyContinue
        }
        Log 'helper started'
        try {
            Wait-Process -Id {{processId}} -Timeout 60 -ErrorAction SilentlyContinue
            Log 'previous process no longer running'
        } catch {
            Log "wait-process reported: $_"
        }
        Start-Sleep -Milliseconds 500
        Log 'copying staged files over the install directory'
        robocopy '{{Escape(staging)}}' '{{Escape(install)}}' /E /IS /IT /R:5 /W:1 /NFL /NDL /NJH /NJS | Out-Null
        Log "robocopy exit code: $LASTEXITCODE"
        Start-Sleep -Milliseconds 200
        Remove-Item -LiteralPath '{{Escape(staging)}}' -Recurse -Force -ErrorAction SilentlyContinue
        try {
            Start-Process -FilePath '{{Escape(exePath)}}'
            Log 'relaunched'
        } catch {
            Log "relaunch failed: $_"
        }
        Log 'helper finished'
        Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue
        """;

    /// <summary>
    /// Single-quote escaping, and a trailing backslash stripped: one right before the closing
    /// quote would otherwise escape that quote instead of ending the string — robocopy in
    /// particular is notorious for exactly this with a path that happens to end in one.
    /// </summary>
    private static string Escape(string value) => value.TrimEnd('\\').Replace("'", "''");
}
