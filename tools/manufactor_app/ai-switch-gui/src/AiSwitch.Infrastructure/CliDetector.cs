using System.Diagnostics;
using System.Text;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Detects official command-line clients without invoking a shell search or
/// changing global configuration. Version probes are bounded and non-interactive.
/// </summary>
public sealed class CliDetector : ICliDetector
{
    private static readonly string[] PreferredWindowsExtensions = [".exe", ".cmd", ".bat"];

    private readonly TimeSpan _versionTimeout;
    private readonly Func<string?> _pathProvider;

    public CliDetector(TimeSpan? versionTimeout = null, Func<string?>? pathProvider = null)
    {
        _versionTimeout = versionTimeout ?? TimeSpan.FromSeconds(4);
        if (_versionTimeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(versionTimeout), "The timeout must be positive.");
        }

        _pathProvider = pathProvider ?? (() => Environment.GetEnvironmentVariable("PATH"));
    }

    public async Task<IReadOnlyList<CliInstallation>> DetectAsync(
        CliKind? cli = null,
        CancellationToken cancellationToken = default)
    {
        CliKind[] kinds = cli is { } selected
            ? [selected]
            : Enum.GetValues<CliKind>();

        string[] pathEntries = GetPathEntries(_pathProvider());
        CliInstallation[] results = await Task.WhenAll(
                kinds.Select(kind => DetectKindAsync(kind, pathEntries, cancellationToken)))
            .ConfigureAwait(false);
        return results;
    }

    private async Task<CliInstallation> DetectKindAsync(
        CliKind kind,
        IReadOnlyList<string> pathEntries,
        CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        string[] candidates = EnumerateExecutableCandidates(kind, pathEntries).ToArray();
        CandidateProbe[] probes = await Task.WhenAll(candidates.Select(async candidate =>
            new CandidateProbe(
                candidate,
                await ProbeVersionAsync(candidate, cancellationToken).ConfigureAwait(false))))
            .ConfigureAwait(false);
        CandidateProbe[] launchable = probes.Where(probe => probe.Result.CanLaunch).ToArray();
        CandidateProbe? preferred = launchable.FirstOrDefault();
        return new CliInstallation
        {
            Kind = kind,
            IsInstalled = preferred is not null,
            ExecutablePath = preferred?.Path,
            Version = preferred?.Result.Version,
            Capabilities = preferred is null ? CliCapability.None : GetCapabilities(kind),
            DetectedAt = DateTimeOffset.UtcNow,
            AlternativeExecutablePaths = launchable.Skip(1).Select(probe => probe.Path).ToArray(),
        };
    }

    private static IEnumerable<string> EnumerateExecutableCandidates(
        CliKind kind,
        IReadOnlyList<string> pathEntries)
    {
        string[] names = GetCommandNames(kind);
        var seen = new HashSet<string>(
            OperatingSystem.IsWindows() ? StringComparer.OrdinalIgnoreCase : StringComparer.Ordinal);

        if (OperatingSystem.IsWindows())
        {
            // Prefer a native executable, then the two script types CreateProcess
            // can safely run through cmd.exe. PowerShell shims are intentionally
            // excluded because they are affected by execution policy.
            foreach (string extension in PreferredWindowsExtensions)
            {
                foreach (string name in names)
                {
                    foreach (string directory in pathEntries)
                    {
                        string candidate = Path.Combine(directory, name + extension);
                        if (File.Exists(candidate) && seen.Add(candidate))
                        {
                            yield return Path.GetFullPath(candidate);
                        }
                    }
                }
            }

            yield break;
        }

        foreach (string name in names)
        {
            foreach (string directory in pathEntries)
            {
                string candidate = Path.Combine(directory, name);
                if (File.Exists(candidate) && seen.Add(candidate))
                {
                    yield return Path.GetFullPath(candidate);
                }
            }
        }

    }

    private async Task<VersionProbeResult> ProbeVersionAsync(
        string executablePath,
        CancellationToken cancellationToken)
    {
        using var process = new Process
        {
            StartInfo = CreateVersionStartInfo(executablePath),
            EnableRaisingEvents = true,
        };

        try
        {
            if (!process.Start())
            {
                return default;
            }

            Task<string> standardOutput = process.StandardOutput.ReadToEndAsync(cancellationToken);
            Task<string> standardError = process.StandardError.ReadToEndAsync(cancellationToken);

            using var timeoutSource = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeoutSource.CancelAfter(_versionTimeout);

            try
            {
                await process.WaitForExitAsync(timeoutSource.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                TryKill(process);
                return default;
            }

            string output = await standardOutput.ConfigureAwait(false);
            string error = await standardError.ConfigureAwait(false);
            string? version = process.ExitCode == 0
                ? FirstSafeLine(output) ?? FirstSafeLine(error)
                : null;
            return new VersionProbeResult(true, version);
        }
        catch (OperationCanceledException)
        {
            TryKill(process);
            throw;
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or System.ComponentModel.Win32Exception or IOException)
        {
            TryKill(process);
            return default;
        }
    }

    private static ProcessStartInfo CreateVersionStartInfo(string executablePath)
    {
        bool isCommandScript = OperatingSystem.IsWindows() &&
            (executablePath.EndsWith(".cmd", StringComparison.OrdinalIgnoreCase) ||
             executablePath.EndsWith(".bat", StringComparison.OrdinalIgnoreCase));

        var startInfo = new ProcessStartInfo
        {
            FileName = isCommandScript
                ? Environment.GetEnvironmentVariable("ComSpec") ?? "cmd.exe"
                : executablePath,
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            RedirectStandardInput = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8,
        };

        if (isCommandScript)
        {
            string escapedPath = executablePath.Replace("\"", "\"\"");
            startInfo.Arguments = $"/d /s /c \"\"{escapedPath}\" --version\"";
        }
        else
        {
            startInfo.ArgumentList.Add("--version");
        }

        return startInfo;
    }

    private static string[] GetPathEntries(string? path)
    {
        if (string.IsNullOrWhiteSpace(path))
        {
            return Array.Empty<string>();
        }

        var comparer = OperatingSystem.IsWindows()
            ? StringComparer.OrdinalIgnoreCase
            : StringComparer.Ordinal;

        return path
            .Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .Select(entry => entry.Trim().Trim('"'))
            .Where(entry => !string.IsNullOrWhiteSpace(entry))
            .Select(Environment.ExpandEnvironmentVariables)
            .Select(entry =>
            {
                try
                {
                    return Path.GetFullPath(entry);
                }
                catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
                {
                    return string.Empty;
                }
            })
            .Where(entry => entry.Length > 0)
            .Distinct(comparer)
            .ToArray();
    }

    private static string[] GetCommandNames(CliKind kind)
        => kind switch
        {
            CliKind.Codex => ["codex"],
            CliKind.ClaudeCode => ["claude", "claude-code"],
            CliKind.GeminiCli => ["gemini"],
            CliKind.GrokCli => ["grok"],
            _ => throw new ArgumentOutOfRangeException(nameof(kind), kind, "Unsupported CLI kind."),
        };

    private static CliCapability GetCapabilities(CliKind kind)
        => kind switch
        {
            CliKind.Codex =>
                CliCapability.NewSession |
                CliCapability.ResumeSession |
                CliCapability.ForkSession |
                CliCapability.ListSessions |
                CliCapability.ConfigurationOverride,
            CliKind.ClaudeCode =>
                CliCapability.NewSession |
                CliCapability.ResumeSession |
                CliCapability.StructuredOutput |
                CliCapability.ConfigurationOverride,
            CliKind.GeminiCli =>
                CliCapability.NewSession |
                CliCapability.ResumeSession |
                CliCapability.ListSessions |
                CliCapability.StructuredOutput |
                CliCapability.ConfigurationOverride,
            CliKind.GrokCli =>
                CliCapability.NewSession |
                CliCapability.ConfigurationOverride,
            _ => CliCapability.None,
        };

    private static string? FirstSafeLine(string value)
    {
        string? line = value
            .Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .FirstOrDefault();

        if (string.IsNullOrWhiteSpace(line))
        {
            return null;
        }

        const int maxLength = 256;
        return line.Length <= maxLength ? line : line[..maxLength];
    }

    private static void TryKill(Process process)
    {
        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or System.ComponentModel.Win32Exception or NotSupportedException)
        {
            // Best effort only. The probe has no input and a strict timeout.
        }
    }

    private readonly record struct VersionProbeResult(bool CanLaunch, string? Version);

    private sealed record CandidateProbe(string Path, VersionProbeResult Result);
}


