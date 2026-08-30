using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

public sealed record ClientUpdateInfo(Version Version, Uri DownloadPage)
{
    public string VersionLabel => $"Ver{Version.Major}.{Version.Minor}";
}

/// <summary>Asks the relay whether a newer client has been published for this platform.</summary>
/// <remarks>
/// <para>
/// Reads <c>/settings/public</c> rather than a static <c>client-version.json</c>. The
/// static file was embedded into the backend binary, so publishing a version needed a
/// frontend build, a backend build and a redeploy, while the package it pointed at was
/// a settings row an operator could change in a form. The two drifted, and the drift
/// was invisible: every failure below returns "no update", so a manifest that had never
/// been deployed looked exactly like being up to date. It was, in fact, never deployed —
/// the production path returned the SPA's index.html.
/// </para>
/// <para>
/// Silence on failure is still the deliberate behaviour. This feeds a non-blocking
/// banner, and a client that cannot reach the relay has worse things to report than a
/// missed version check.
/// </para>
/// </remarks>
internal sealed class ClientVersionChecker
{
    /// <summary>Path of the site's own download page, relative to the relay root.</summary>
    /// <remarks>
    /// A constant, not a server field. It is this site's own route; carrying it through
    /// the settings payload would be one more thing to keep in step for no gain.
    /// </remarks>
    private const string DownloadPagePath = "download";

    private readonly Func<CancellationToken, Task<PublicSettings>> _fetchSettings;
    private readonly Version _currentVersion;
    private readonly bool _isMacOS;

    public ClientVersionChecker(
        Func<CancellationToken, Task<PublicSettings>> fetchSettings,
        Version currentVersion)
        : this(fetchSettings, currentVersion, OperatingSystem.IsMacOS())
    {
    }

    /// <param name="isMacOS">
    /// Injected so both branches are testable from one machine. Which field is read is
    /// the part that can be wrong, and it is wrong in a way nothing catches: a Mac user
    /// told to install a Windows release gets a download page with nothing on it.
    /// </param>
    internal ClientVersionChecker(
        Func<CancellationToken, Task<PublicSettings>> fetchSettings,
        Version currentVersion,
        bool isMacOS)
    {
        _fetchSettings = fetchSettings ?? throw new ArgumentNullException(nameof(fetchSettings));
        _currentVersion = currentVersion ?? throw new ArgumentNullException(nameof(currentVersion));
        _isMacOS = isMacOS;
    }

    public async Task<ClientUpdateInfo?> CheckAsync(CancellationToken cancellationToken = default)
    {
        PublicSettings settings;
        try
        {
            settings = await _fetchSettings(cancellationToken).ConfigureAwait(false);
        }
        catch (RelayApiException)
        {
            // RelayServerClient funnels transport, timeout and deserialisation faults
            // into this one type, so there is nothing else left to catch here.
            return null;
        }

        // Offering an update that leads to a disabled route is a dead end the user
        // cannot get out of, so treat the switch as part of the answer.
        if (!settings.ClientDownloadEnabled)
        {
            return null;
        }

        string? advertised = _isMacOS ? settings.ClientLatestVersionMac : settings.ClientLatestVersion;
        if (Displayed(advertised) is not { } latest || latest <= Displayed(_currentVersion))
        {
            return null;
        }

        return new ClientUpdateInfo(
            latest,
            new Uri(new Uri(ClientOptions.ServerAddress), DownloadPagePath));
    }

    /// <summary>Reduces a version to the two components the user is actually shown.</summary>
    /// <remarks>
    /// <para>
    /// Both sides of the comparison go through this, and that is the whole point.
    /// <see cref="ClientUpdateInfo.VersionLabel"/> and
    /// <see cref="ViewModels.ClientUpdateViewModel.CurrentVersionText"/> both render
    /// <c>Major.Minor</c>, so comparing anything finer lets the client offer an
    /// "update" whose number is identical to the one already on screen.
    /// </para>
    /// <para>
    /// That is not hypothetical. .NET orders <c>0.2.0</c> above <c>0.2</c> — an absent
    /// build component is -1, not 0 — so a setting typed as "0.2.0" would nag every
    /// user of 0.2 forever, to install 0.2. Normalising here rather than validating on
    /// write is deliberate: the value also arrives via direct database edits and
    /// settings restores, neither of which passes through the admin form.
    /// </para>
    /// </remarks>
    private static Version? Displayed(string? value) =>
        Version.TryParse(value?.Trim(), out Version? parsed) ? Displayed(parsed) : null;

    private static Version Displayed(Version value) => new(value.Major, value.Minor);
}
