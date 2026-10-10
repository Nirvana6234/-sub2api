using System.Diagnostics;
using System.Text;
using System.Text.Json;
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
    private readonly Func<string> _describeLookup;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private (string Key, string Json)? _cached;
    private string? _lastMissing;

    public CodexBundledCatalogSource(
        Func<string?>? locateExecutable = null,
        Func<string, CancellationToken, Task<string?>>? run = null,
        Func<string>? describeLookup = null)
    {
        _locateExecutable = locateExecutable ?? LocateInstalledCodex;
        _run = run ?? RunAsync;
        _describeLookup = describeLookup ?? DescribeLookup;
    }

    /// <summary>
    /// Looks for Codex and reads its catalog once, and writes down what happened. Meant to be
    /// started when the client starts, not awaited.
    /// </summary>
    /// <remarks>
    /// <para>
    /// The same lookup runs later, on every model-list request, and fails the same way there —
    /// but then all the log says is that the catalog could not be read. Whether Codex is not
    /// installed, is installed somewhere this code does not look, or is found and will not run
    /// are three different problems with three different fixes, and only the machine that has
    /// it can tell them apart. So the answer is recorded up front, with every place that was
    /// looked at.
    /// </para>
    /// <para>
    /// A successful read is kept (see <see cref="GetBundledCatalogAsync"/>), so the first
    /// request from Codex does not pay for starting it.
    /// </para>
    /// </remarks>
    public async Task ProbeAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            // Every outcome is logged where it happens: not found (with the places looked at) and
            // read (with the count) by GetBundledCatalogAsync, a failed run by RunAsync.
            await GetBundledCatalogAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
        }
        catch (Exception ex)
        {
            // A diagnostic that takes the client down is worse than none.
            ClientLog.Warning("启动时检测 Codex 自带模型目录失败", ex);
        }
    }

    private static string CountModels(string json)
    {
        try
        {
            using JsonDocument document = JsonDocument.Parse(json);
            return document.RootElement.TryGetProperty("models", out JsonElement models) && models.ValueKind == JsonValueKind.Array
                ? models.GetArrayLength().ToString(System.Globalization.CultureInfo.InvariantCulture)
                : "?";
        }
        catch (JsonException)
        {
            return "?";
        }
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
            // Asked on every model-list request, so say it when the answer changes, not every time:
            // the next line in the log is "读不到 Codex 自带的模型目录", and without this nothing
            // says why. It does change — at start-up no Codex is running, and by the first request
            // one is, which is exactly the fact that explains a layout this code does not know.
            string description = SafeDescribe();
            if (!string.Equals(Interlocked.Exchange(ref _lastMissing, description), description, StringComparison.Ordinal))
            {
                ClientLog.Info($"没有找到 Codex 的命令行程序，无法读取它自带的模型目录。找过：{description}");
            }

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
                ClientLog.Info($"已读到 Codex 自带的模型目录（{CountModels(json)} 个模型），位置：{executable}");
            }

            return json;
        }
        finally
        {
            _gate.Release();
        }
    }

    private string SafeDescribe()
    {
        try
        {
            return _describeLookup();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or System.Security.SecurityException)
        {
            return "（列出查找位置时出错：" + ex.GetType().Name + "）";
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
            Task<string> stderr = process.StandardError.ReadToEndAsync(timeout.Token);
            try
            {
                await process.WaitForExitAsync(timeout.Token).ConfigureAwait(false);
                string output = await stdout.ConfigureAwait(false);
                if (process.ExitCode == 0 && output.TrimStart().StartsWith('{'))
                {
                    return output;
                }

                ClientLog.Warning($"读取 Codex 自带模型目录失败（退出码 {process.ExitCode}），程序：{executable}，它说：{await FirstLineAsync(stderr).ConfigureAwait(false)}");
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

    /// <summary>The first line of what the process printed on stderr, trimmed for a log line.</summary>
    private static async Task<string> FirstLineAsync(Task<string> stderr)
    {
        try
        {
            string text = (await stderr.ConfigureAwait(false)).Trim();
            if (text.Length == 0)
            {
                return "（没有输出）";
            }

            string line = text.Split('\n')[0].Trim();
            return line.Length > 200 ? line[..200] + "…" : line;
        }
        catch (Exception ex) when (ex is OperationCanceledException or IOException or InvalidOperationException)
        {
            return "（读不到输出）";
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
    internal static string? LocateInstalledCodex() => CandidateExecutables().FirstOrDefault(File.Exists);

    /// <summary>
    /// Every place the lookup tries, best first. One list for both finding Codex and saying
    /// where it looked, so the log cannot describe a different search from the one that ran.
    /// </summary>
    internal static IEnumerable<string> CandidateExecutables()
    {
        if (OperatingSystem.IsWindows())
        {
            foreach (string root in InstalledPackageRoots())
            {
                yield return Path.Combine(root, "app", "resources", "codex.exe");
            }

            string local = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "OpenAI", "Codex", "bin");
            foreach (string directory in SafeDirectories(local, "*").OrderByDescending(d => Directory.GetLastWriteTimeUtc(d)))
            {
                yield return Path.Combine(directory, "codex.exe");
            }

            // Last, for an install that is neither the Store package nor the user-level one (a
            // different installer, another drive): wherever the Codex that is running right now
            // lives. Empty until Codex is up — which it always is by the time it asks for the model
            // list, and the lookup is repeated on every ask.
            foreach (string directory in RunningCodexDirectories())
            {
                yield return Path.Combine(directory, "resources", "codex.exe");
                yield return Path.Combine(directory, "codex.exe");
                yield return Path.Combine(directory, "app", "resources", "codex.exe");
            }

            yield break;
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
            // Contents/Resources/codex-cli/bin/codex is where the shipped ChatGPT.app keeps it
            // (seen on a real Mac, Intel and Apple silicon alike); the rest are older guesses.
            string[] inside =
            [
                "Contents/Resources/codex-cli/bin/codex",
                "Contents/Resources/codex",
                "Contents/Resources/bin/codex",
                "Contents/MacOS/codex",
            ];
            foreach (string path in bundles.SelectMany(b => inside.Select(i => Path.Combine(b, i))))
            {
                yield return path;
            }
        }
    }

    /// <summary>What the lookup tried and what each place held, as one line for the log.</summary>
    /// <remarks>
    /// On Windows the Store packages the registry lists are counted separately: "none
    /// registered" and "registered, but no codex.exe inside" send the next person to different
    /// places, and a list of paths alone would not tell them apart.
    /// </remarks>
    internal static string DescribeLookup()
    {
        var text = new StringBuilder();
        if (OperatingSystem.IsWindows())
        {
            text.Append("注册表里登记的 Codex 商店包 ").Append(InstalledPackageRoots().Count()).Append(" 个；");
            string[] running = [.. RunningCodexDirectories()];
            text.Append(running.Length == 0
                ? "没有正在运行的 Codex 进程；"
                : "正在运行的 Codex 在 " + string.Join("、", running) + "；");
        }

        string[] candidates = [.. CandidateExecutables()];
        if (candidates.Length == 0)
        {
            text.Append("没有可检查的位置");
        }

        foreach (string path in candidates)
        {
            text.Append(path).Append(File.Exists(path) ? "（有）" : "（无）").Append('；');
        }

        return text.ToString().TrimEnd('；');
    }

    /// <summary>The folders of the Codex desktop app processes that are running, if any.</summary>
    /// <remarks>
    /// Reading a process's path can be refused (another user's process, a protected one), and a
    /// process can exit between listing it and asking; each is "not this one", not a failure.
    /// </remarks>
    private static IEnumerable<string> RunningCodexDirectories()
    {
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (string name in new[] { "Codex", "ChatGPT" })
        {
            Process[] processes;
            try
            {
                processes = Process.GetProcessesByName(name);
            }
            catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception)
            {
                continue;
            }

            foreach (Process process in processes)
            {
                string? directory = null;
                try
                {
                    directory = Path.GetDirectoryName(process.MainModule?.FileName);
                }
                catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception or NotSupportedException)
                {
                }
                finally
                {
                    process.Dispose();
                }

                if (!string.IsNullOrEmpty(directory) && seen.Add(directory))
                {
                    yield return directory;
                }
            }
        }
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
