namespace LanAi.RelayClient.Services;

/// <summary>Decides whether this process is the one client instance on the machine.</summary>
/// <remarks>
/// <para>
/// Split into two responsibilities that fail independently, because on macOS they are
/// implemented by two unrelated mechanisms:
/// </para>
/// <list type="bullet">
/// <item><description>
/// <see cref="IsPrimary"/> — the exclusion itself. This one must work, or a second
/// launch starts a second client that fights the first over <c>~/.codex</c>.
/// </description></item>
/// <item><description>
/// <see cref="TryActivateExistingInstance"/> — telling the running copy to show
/// itself. A convenience: when it fails the user sees nothing happen and clicks
/// again, which is poor, but nothing is corrupted.
/// </description></item>
/// </list>
/// <para>
/// Windows gets both from kernel objects (a named mutex and a named event). macOS has
/// no equivalent of the second, so an implementation is allowed to enforce exclusion
/// and decline activation — hence the two are separate members rather than one
/// "single instance" call that either works or does not.
/// </para>
/// </remarks>
internal interface ISingleInstanceCoordinator : IDisposable
{
    /// <summary>Whether this process owns the instance slot.</summary>
    bool IsPrimary { get; }

    /// <summary>Begins listening for activation requests. No-op unless primary.</summary>
    void StartListening();

    /// <summary>
    /// Asks the running instance to surface. Returns false when there is nothing to
    /// signal, when this process is itself the primary, or when the platform cannot
    /// carry the signal.
    /// </summary>
    bool TryActivateExistingInstance();

    Task StopListeningAsync();
}
