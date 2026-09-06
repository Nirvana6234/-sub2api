namespace LanAi.Paseo.Adapter.Host;

/// <summary>Everything the host needs to bring a private Paseo runtime up.</summary>
/// <remarks>
/// All paths are ours: a private Node, a private <c>PASEO_HOME</c>, a private
/// bridge build. Nothing here resolves through <c>PATH</c> or the user's own
/// <c>~/.paseo</c> — a client that borrowed either would inherit whatever the
/// user's own Paseo install happens to be configured to do.
/// </remarks>
public sealed record PaseoRuntimeOptions
{
    /// <summary>Private <c>node.exe</c> shipped with the client. Never the system Node.</summary>
    public required string NodeExecutablePath { get; init; }

    /// <summary>Entry script of <c>@getpaseo/cli</c> inside our prebuilt node_modules.</summary>
    public required string DaemonEntryPath { get; init; }

    /// <summary>Entry script of the compiled bridge (<c>bridge/dist/index.js</c>).</summary>
    public required string BridgeEntryPath { get; init; }

    /// <summary>
    /// Private Paseo home. Must not be the user's <c>~/.paseo</c>.
    /// </summary>
    /// <remarks>
    /// Also the revocation lever: replacing this directory changes the daemon's
    /// <c>serverId</c>, which is the only way to invalidate a pairing offer that
    /// has already been handed out.
    /// </remarks>
    public required string PaseoHomePath { get; init; }

    /// <summary>Directories agents may run in, as key → absolute path.</summary>
    /// <remarks>
    /// This map is the consent record. It is handed to the bridge at spawn time
    /// and never crosses the contract, so no consumer can name a directory the
    /// user did not choose.
    /// </remarks>
    public required IReadOnlyList<WorkdirRegistration> Workdirs { get; init; }

    /// <summary>Fixed loopback port, or <c>null</c> to pick a free one.</summary>
    public int? Port { get; init; }

    /// <summary>
    /// Self-hosted relay to dial out to, as <c>host:port</c>. <c>null</c> keeps
    /// Paseo's default public relay.
    /// </summary>
    /// <remarks>
    /// The 共飞 deployment runs its own relay beside the server, so this is
    /// normally set. It only takes effect when someone enables the relay; the
    /// generated config always starts with it off.
    /// </remarks>
    public string? RelayEndpoint { get; init; }

    /// <summary>Whether the relay endpoint speaks TLS.</summary>
    public bool RelayUseTls { get; init; } = true;

    /// <summary>
    /// Whether this host permits the relay operations at all.
    /// </summary>
    /// <remarks>
    /// Off by default. Turning remote access on changes who can reach the machine,
    /// so it is granted by the process that can ask the user, not assumed by the
    /// library. Without the grant the bridge refuses <c>relay.*</c> outright.
    /// </remarks>
    public bool AllowRelayOperations { get; init; }

    /// <summary>How long a start may take before it counts as a crash.</summary>
    public TimeSpan StartTimeout { get; init; } = TimeSpan.FromSeconds(30);

    /// <summary>How long an ordered stop waits for the daemon to exit before the cage takes over.</summary>
    public TimeSpan StopTimeout { get; init; } = TimeSpan.FromSeconds(10);

    /// <summary>Consecutive failed starts before the supervisor stops retrying.</summary>
    public int MaxRestartAttempts { get; init; } = 5;
}

/// <summary>One consented work directory.</summary>
public sealed record WorkdirRegistration(string Key, string Path, string Label);
