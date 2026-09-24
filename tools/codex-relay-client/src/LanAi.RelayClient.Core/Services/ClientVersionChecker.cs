using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>How this platform can be moved onto a newer build, once one is known to exist.</summary>
public enum ClientUpdateChannel
{
    /// <summary>Windows: this process can download the package and swap itself for it.</summary>
    SelfReplace,

    /// <summary>macOS: a shell command the user runs themselves in Terminal.</summary>
    RunInTerminal,

    /// <summary>Neither concrete action could be worked out; send the user to the download page.</summary>
    OpenDownloadPage,
}

/// <param name="PackageUrl">Set only for <see cref="ClientUpdateChannel.SelfReplace"/>.</param>
/// <param name="TerminalCommand">Set only for <see cref="ClientUpdateChannel.RunInTerminal"/>.</param>
public sealed record ClientUpdateInfo(
    Version Version,
    Uri DownloadPage,
    ClientUpdateChannel Channel = ClientUpdateChannel.OpenDownloadPage,
    Uri? PackageUrl = null,
    string? TerminalCommand = null)
{
    public string VersionLabel => $"Ver{Version.Major}.{Version.Minor}";
}

/// <summary>What asking the relay "is there something newer" came back with.</summary>
public enum ClientCheckStatus
{
    /// <summary>The relay could not be reached, or answered something this client cannot read.</summary>
    CheckFailed,

    /// <summary>The download page itself is switched off server-side; nothing can be offered.</summary>
    ChannelDisabled,

    /// <summary>This build is already on, or ahead of, what the relay advertises.</summary>
    UpToDate,

    /// <summary><see cref="Update"/> is set.</summary>
    Available,
}

/// <param name="Update">Non-null exactly when <paramref name="Status"/> is <see cref="ClientCheckStatus.Available"/>.</param>
public sealed record ClientCheckResult(ClientCheckStatus Status, ClientUpdateInfo? Update = null);

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
/// <see cref="ClientCheckStatus"/> exists because that silence, right for a passive
/// banner, is wrong for a button the user just pressed: a network failure and "you are
/// current" must not read the same on screen. <see cref="ClientUpdateViewModel"/> keeps
/// the original silent behaviour for the banner and adds an interactive path that tells
/// every outcome apart.
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

    /// <summary>
    /// The relay's own redirect to the admin-set Windows package link (or a locally hosted
    /// file, server-side — see <c>clientDownloadHandler</c>). Used instead of reading
    /// <c>client_download_direct_url</c> directly: the indirection is exactly what makes an
    /// admin swapping the URL take effect without this client knowing anything changed.
    /// </summary>
    private const string WindowsPackagePath = "api/v1/download/client";

    /// <summary>
    /// Name the install script is always published under, beside the macOS package. See
    /// <see cref="MacInstallScriptUrl"/>.
    /// </summary>
    private const string MacInstallScriptName = "install-mac.sh";

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

    public async Task<ClientCheckResult> CheckAsync(CancellationToken cancellationToken = default)
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
            return new ClientCheckResult(ClientCheckStatus.CheckFailed);
        }

        // Offering an update that leads to a disabled route is a dead end the user
        // cannot get out of, so treat the switch as part of the answer.
        if (!settings.ClientDownloadEnabled)
        {
            return new ClientCheckResult(ClientCheckStatus.ChannelDisabled);
        }

        string? advertised = _isMacOS ? settings.ClientLatestVersionMac : settings.ClientLatestVersion;
        if (Displayed(advertised) is not { } latest || latest <= Displayed(_currentVersion))
        {
            return new ClientCheckResult(ClientCheckStatus.UpToDate);
        }

        var downloadPage = new Uri(new Uri(ClientOptions.ServerAddress), DownloadPagePath);
        ClientUpdateInfo update = _isMacOS
            ? BuildMacUpdate(latest, downloadPage, settings.ClientDownloadDirectUrlMac)
            : BuildWindowsUpdate(latest, downloadPage);
        return new ClientCheckResult(ClientCheckStatus.Available, update);
    }

    private static ClientUpdateInfo BuildWindowsUpdate(Version latest, Uri downloadPage) =>
        new(
            latest,
            downloadPage,
            ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(new Uri(ClientOptions.ServerAddress), WindowsPackagePath));

    private static ClientUpdateInfo BuildMacUpdate(Version latest, Uri downloadPage, string? packageUrlMac)
    {
        Uri? script = MacInstallScriptUrl(packageUrlMac);
        return script is null
            ? new ClientUpdateInfo(latest, downloadPage, ClientUpdateChannel.OpenDownloadPage)
            : new ClientUpdateInfo(
                latest,
                downloadPage,
                ClientUpdateChannel.RunInTerminal,
                TerminalCommand: $"curl -fsSL {script} | bash");
    }

    /// <summary>
    /// The install script's URL, derived from the package's — same directory, fixed name.
    /// </summary>
    /// <remarks>
    /// Matches <c>ClientDownloadView.vue</c>'s <c>macInstallScriptUrl</c> exactly, on purpose:
    /// the release pipeline publishes the tar.gz and <c>install-mac.sh</c> into one directory
    /// as a pair, and this is the only other place that convention is relied on. A URL with no
    /// path segment to strip, or the wrong scheme, means the setting is not usable — same as
    /// leaving it empty — rather than being guessed at.
    /// </remarks>
    private static Uri? MacInstallScriptUrl(string? packageUrl)
    {
        if (!Uri.TryCreate(packageUrl?.Trim(), UriKind.Absolute, out Uri? package) ||
            package.Scheme is not ("http" or "https"))
        {
            return null;
        }

        string path = package.GetLeftPart(UriPartial.Path);
        int lastSlash = path.LastIndexOf('/');
        if (lastSlash < 0)
        {
            return null;
        }

        return new Uri(path[..(lastSlash + 1)] + MacInstallScriptName);
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
