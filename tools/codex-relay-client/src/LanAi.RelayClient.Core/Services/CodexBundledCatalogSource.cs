using System.Diagnostics;
using System.Text;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Where the model list Codex ships with can be read from.</summary>
internal interface ICodexCatalogSource
{
    /// <summary>
    /// The catalog JSON the installed Codex bundles, or null when it cannot be had — Codex
    /// is not installed, the command failed, or it answered with something that is not JSON.
    /// </summary>
    Task<string?> GetBundledCatalogAsync(CancellationToken cancellationToken = default);
}

/// <summary>
/// Reads the catalog by asking the installed Codex for it: <c>codex debug models --bundled</c>.
/// </summary>
/// <remarks>
/// <para>
/// <b>Asking the binary rather than shipping a copy</b> is the point: the answer always
/// matches the Codex on this machine, whatever version the Store has updated it to.
/// </para>
/// <para>
/// <b>Run against a private <c>CODEX_HOME</c>.</b> The command otherwise loads the user's
/// <c>config.toml</c>, which by then points at this client's own relay — a process that asks
/// the relay for a catalog while the relay waits on it is a deadlock, not a lookup.
/// <c>--bundled</c> skips the refresh, and the empty home makes sure nothing else can
/// reach out either. It also means the user's own <c>~/.codex</c> is never read.
/// </para>
/// <para>
/// The answer is kept per executable and modification time, so an updated Codex is picked
/// up without a restart and an unchanged one costs one process start per run of the client.
/// </para>
/// </remarks>
internal sealed class CodexBundledCatalogSource : ICodexCatalogSource
{
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(20);

    private readonly Func<string?> _locateExecutable;
    private readonly Func<string, CancellationToken, Task<string?>> _run;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private (string Key, string Json)? _cached;

    public CodexBundledCatalogSource(
        Func<string?>? locateExecutable = null,
        Func<string, CancellationToken, Task<string?>>? run = null)
    {
        _locateExecutable = locateExecutable ?? LocateInstalledCodex;
        _run = run ?? RunAsync;
    }

    public async Task<string?> GetBundledCatalogAsync(CancellationToken cancellationToken = default)
    {
        string? executable;
        try
        {
            executable = _locateExecutable();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("查找 Codex 可执行文件失败", ex);
            return null;
        }

        if (executable is null)
        {
            return null;
        }

        string key = executable + "|" + SafeStamp(executable);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_cached is { } cached && cached.Key == key)
            {
                return cached.Json;
            }

            string? json = await _run(executable, cancellationToken).ConfigureAwait(false);
            if (json is not null)
            {
                _cached = (key, json);
            }

            return json;
        }
        finally
        {
            _gate.Release();
        }
    }

    private static string SafeStamp(string path)
    {
        try
        {
            return File.GetLastWriteTimeUtc(path).Ticks.ToString(System.Globalization.CultureInfo.InvariantCulture);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            return "?";
        }
    }

    private static async Task<string?> RunAsync(string executable, CancellationToken cancellationToken)
    {
        string home = AppPaths.InData("codex-catalog-probe");
        try
        {
            Directory.CreateDirectory(home);
            var info = new ProcessStartInfo(executable)
            {
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true,
                StandardOutputEncoding = Encoding.UTF8,
            };
            info.ArgumentList.Add("debug");
            info.ArgumentList.Add("models");
            info.ArgumentList.Add("--bundled");
            info.Environment["CODEX_HOME"] = home;

            using Process? process = Process.Start(info);
            if (process is null)
            {
                return null;
            }

            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(Timeout);
            Task<string> stdout = process.StandardOutput.ReadToEndAsync(timeout.Token);
            _ = process.StandardError.ReadToEndAsync(timeout.Token);
            try
            {
                await process.WaitForExitAsync(timeout.Token).ConfigureAwait(false);
                string output = await stdout.ConfigureAwait(false);
                if (process.ExitCode == 0 && output.TrimStart().StartsWith('{'))
                {
                    return output;
                }

                ClientLog.Warning($"读取 Codex 自带模型目录失败（退出码 {process.ExitCode}）");
                return null;
            }
            catch (OperationCanceledException)
            {
                TryKill(process);
                if (cancellationToken.IsCancellationRequested)
                {
                    throw;
                }

                ClientLog.Warning("读取 Codex 自带模型目录超时");
                return null;
            }
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            ClientLog.Warning("读取 Codex 自带模型目录失败", ex);
            return null;
        }
    }

    private static void TryKill(Process process)
    {
        try
        {
            process.Kill(entireProcessTree: true);
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
        }
    }

    /// <summary>
    /// The Codex command-line binary that ships inside the desktop app, or null.
    /// </summary>
    /// <remarks>
    /// Windows: the Store package's <c>app\resources\codex.exe</c> (highest version wins, since
    /// an update leaves the old folder behind until it is cleaned up), then the user-level
    /// install the <c>codex</c> shim uses. macOS: inside the app bundle. Where it cannot be
    /// found the answer is null and Codex keeps its own list — never an error.
    /// </remarks>
    internal static string? LocateInstalledCodex()
    {
        if (OperatingSystem.IsWindows())
        {
            string? packaged = InstalledPackageRoots()
                .Select(root => Path.Combine(root, "app", "resources", "codex.exe"))
                .FirstOrDefault(File.Exists);
            if (packaged is not null)
            {
                return packaged;
            }

            string local = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "OpenAI", "Codex", "bin");
            return SafeDirectories(local, "*")
                .OrderByDescending(d => Directory.GetLastWriteTimeUtc(d))
                .Select(d => Path.Combine(d, "codex.exe"))
                .FirstOrDefault(File.Exists);
        }

        if (OperatingSystem.IsMacOS())
        {
            string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            string[] bundles =
            [
                "/Applications/ChatGPT.app",
                Path.Combine(home, "Applications", "ChatGPT.app"),
                "/Applications/Codex.app",
                Path.Combine(home, "Applications", "Codex.app"),
            ];
            string[] inside = ["Contents/Resources/codex", "Contents/Resources/bin/codex", "Contents/MacOS/codex"];
            return bundles
                .SelectMany(b => inside.Select(i => Path.Combine(b, i)))
                .FirstOrDefault(File.Exists);
        }

        return null;
    }

    private static IEnumerable<string> SafeDirectories(string root, string pattern)
    {
        try
        {
            return Directory.Exists(root) ? Directory.GetDirectories(root, pattern) : [];
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            return [];
        }
    }

    /// <summary>
    /// The Store package folders of the desktop app, newest first.
    /// </summary>
    /// <remarks>
    /// From the per-user package repository in the registry, not by listing
    /// <c>C:\Program Files\WindowsApps</c>: that folder cannot be listed without elevation, though
    /// a file inside it can be run once its full path is known. An update leaves the old
    /// package registered until it is cleaned up, hence the ordering by version.
    /// </remarks>
    [System.Runtime.Versioning.SupportedOSPlatform("windows")]
    private static IEnumerable<string> InstalledPackageRoots()
    {
        const string repository =
            @"Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages";
        try
        {
            using Microsoft.Win32.RegistryKey? packages = Microsoft.Win32.Registry.CurrentUser.OpenSubKey(repository);
            if (packages is null)
            {
                return [];
            }

            return [.. packages.GetSubKeyNames()
                .Where(name => name.StartsWith("OpenAI.Codex_", StringComparison.OrdinalIgnoreCase) &&
                               name.Contains("_x64__", StringComparison.OrdinalIgnoreCase))
                .OrderByDescending(PackageVersion)
                .Select(name =>
                {
                    using Microsoft.Win32.RegistryKey? key = packages.OpenSubKey(name);
                    return key?.GetValue("PackageRootFolder") as string;
                })
                .Where(root => !string.IsNullOrWhiteSpace(root))
                .Select(root => root!)];
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return [];
        }
    }

    private static Version PackageVersion(string packageName)
    {
        // OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0
        string[] parts = packageName.Split('_');
        return parts.Length > 1 && Version.TryParse(parts[1], out Version? version) ? version : new Version(0, 0);
    }
}
