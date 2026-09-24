using System.Runtime.InteropServices;

namespace LanAi.RelayClient.Services;

/// <summary>Which host the ChatGPT desktop package is being fetched for.</summary>
/// <remarks>
/// An enum rather than reading <see cref="OperatingSystem"/> inside the resolver, so
/// every combination can be tested from one machine. The mapping is the part that can
/// be wrong, and it is wrong in a way nothing catches: a Windows package downloads
/// perfectly on a Mac and simply will not open.
/// </remarks>
internal enum CodexHostPlatform
{
    Other,
    Windows,
    MacOS,
}

/// <summary>
/// The ChatGPT desktop package this machine needs, and where the mirror keeps it.
/// </summary>
/// <remarks>
/// <para>
/// The mirror publishes one build per platform and architecture. Before this existed
/// the client asked for <c>win-x64</c> unconditionally, which is a wrong-file bug
/// rather than a missing feature: a Mac would download a Windows <c>.msix</c>, report
/// the download succeeded, and then fail to open it.
/// </para>
/// <para>
/// <b>Architecture is read from the OS, not the process.</b> A client running under
/// Rosetta reports a process architecture of x64 on hardware that wants the arm64
/// build, and would send an Apple Silicon user to the Intel download.
/// </para>
/// </remarks>
internal sealed record CodexPackageProfile(
    string MirrorPath,
    string DownloadFileName,
    IReadOnlySet<string> SupportedExtensions)
{
    /// <remarks>
    /// A <c>latest/</c> path rather than a pinned version: the mirror redirects to the
    /// current build, so the client does not carry a version number that goes stale
    /// between releases.
    /// </remarks>
    public const string MirrorBase = "https://codexapp.agentsmirror.com/latest/";

    private static readonly IReadOnlySet<string> WindowsExtensions =
        new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            ".exe",
            ".msi",
            ".msix",
            ".appx",
            ".msixbundle",
            ".appxbundle",
        };

    /// <remarks>
    /// No <c>.app</c>: it is a directory, so it would never appear in a file
    /// enumeration, and a downloaded one arrives inside a <c>.dmg</c> anyway.
    /// </remarks>
    private static readonly IReadOnlySet<string> MacExtensions =
        new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            ".dmg",
            ".pkg",
        };

    public Uri DownloadUri => new(new Uri(MirrorBase), MirrorPath);

    /// <summary>Whether an already-downloaded package can be opened on this host.</summary>
    public bool IsMac => SupportedExtensions == MacExtensions;

    /// <summary>The profile for the machine this client is running on.</summary>
    public static CodexPackageProfile? ForCurrentPlatform() =>
        Resolve(CurrentPlatform(), RuntimeInformation.OSArchitecture);

    private static CodexHostPlatform CurrentPlatform()
    {
        if (OperatingSystem.IsWindows())
        {
            return CodexHostPlatform.Windows;
        }

        return OperatingSystem.IsMacOS() ? CodexHostPlatform.MacOS : CodexHostPlatform.Other;
    }

    /// <summary>Maps a host to its package, or null when the mirror publishes none.</summary>
    /// <remarks>
    /// Null rather than a guess. The alternative — falling back to the x64 Windows
    /// build — is what this type was written to remove, and a plausible-looking wrong
    /// download is harder to diagnose than no download at all.
    /// </remarks>
    internal static CodexPackageProfile? Resolve(CodexHostPlatform platform, Architecture architecture) =>
        (platform, architecture) switch
        {
            (CodexHostPlatform.Windows, Architecture.X64) =>
                new("win-x64", "ChatGPT-Windows-x64.msix", WindowsExtensions),

            (CodexHostPlatform.Windows, Architecture.Arm64) =>
                new("win-arm64", "ChatGPT-Windows-arm64.msix", WindowsExtensions),

            (CodexHostPlatform.MacOS, Architecture.Arm64) =>
                new("mac-arm64", "ChatGPT-macOS-arm64.dmg", MacExtensions),

            // Intel Macs. The client itself ships arm64-only for v1, so this is
            // reachable only from an x64 build of the client — but the mirror
            // publishes the download, and sending an Intel Mac an Apple Silicon
            // package would be the same wrong-file bug in the other direction.
            (CodexHostPlatform.MacOS, Architecture.X64) =>
                new("mac-intel", "ChatGPT-macOS-intel.dmg", MacExtensions),

            _ => null,
        };
}
