using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Recognises the API key this client manages on the user's behalf (F3.2.1).
/// </summary>
/// <remarks>
/// <para>
/// Identification is by <em>name</em>, never by value: the key's secret can be
/// re-read from the server at any time (the list endpoint returns it in full), so
/// nothing here depends on having cached it.
/// </para>
/// <para>
/// The name is <c>共飞直连客户端-&lt;机器名&gt;-&lt;安装ID&gt;</c>. Both parts
/// matter. The machine name keeps one account's several machines on separate
/// leases, so signing out on one does not revoke the other's authorization. The
/// install id separates successive installations on the same machine, so a
/// reinstall does not adopt — and keep renewing — a lease it cannot account for.
/// </para>
/// <para>
/// M2 only reads. Issuing a key, cleaning up orphans and renewing the lease are
/// F3.2's job and land with M3 — issuing in particular is gated on V-3, which is
/// still unverified, so nothing before then may depend on being able to create one.
/// </para>
/// </remarks>
internal sealed class ManagedKeyNaming
{
    private const string Product = "共飞直连客户端";

    private readonly IInstallIdProvider _installId;

    public ManagedKeyNaming(IInstallIdProvider installId) =>
        _installId = installId ?? throw new ArgumentNullException(nameof(installId));

    /// <summary>The full name this installation gives its managed key.</summary>
    public string KeyName() => $"{Product}-{MachineName()}-{_installId.Get()}";

    /// <summary>
    /// The prefix shared by every key this product created on this machine.
    /// </summary>
    /// <remarks>
    /// Broader than <see cref="KeyName"/> on purpose: it also matches leases left
    /// by earlier installations, which F3.2.1 wants found so they can be cleaned
    /// up rather than accumulating in the user's key list.
    /// </remarks>
    public static string MachinePrefix() => $"{Product}-{MachineName()}-";

    /// <summary>Whether <paramref name="key"/> belongs to this exact installation.</summary>
    public bool IsMine(RelayApiKey key)
    {
        ArgumentNullException.ThrowIfNull(key);
        return string.Equals(key.Name, KeyName(), StringComparison.Ordinal);
    }

    /// <summary>
    /// Whether <paramref name="key"/> was created by this product on this machine,
    /// by this installation or an earlier one.
    /// </summary>
    public static bool IsFromThisMachine(RelayApiKey key)
    {
        ArgumentNullException.ThrowIfNull(key);
        return key.Name.StartsWith(MachinePrefix(), StringComparison.Ordinal);
    }

    /// <summary>
    /// Picks the key to treat as current.
    /// </summary>
    /// <remarks>
    /// <para>
    /// This installation's own key wins outright. Only when it has none does the
    /// search widen to leases from earlier installations on this machine — those
    /// are adoptable (they authorise the same account against the same relay) but
    /// must never take precedence over the one whose name this client will use
    /// when it issues or renews.
    /// </para>
    /// <para>
    /// A key with <em>no</em> expiry sorts last, not first. Under the F3.2 lease
    /// model such a key is a defect — an authorization that outlives the client —
    /// most likely produced by an update that cleared <c>expires_at</c>. Treating
    /// "never expires" as the best candidate would make the client adopt exactly
    /// the key the lease model exists to prevent, and keep renewing it forever.
    /// </para>
    /// </remarks>
    public RelayApiKey? FindCurrent(IEnumerable<RelayApiKey> keys)
    {
        ArgumentNullException.ThrowIfNull(keys);

        RelayApiKey[] fromThisMachine = keys.Where(IsFromThisMachine).ToArray();

        return Best(fromThisMachine.Where(IsMine)) ?? Best(fromThisMachine);
    }

    /// <summary>
    /// Leases from earlier installations on this machine, which F3.2.1 removes.
    /// </summary>
    /// <remarks>
    /// Returned rather than deleted here: deletion is destructive and belongs with
    /// the lease machinery, where it can be done once the replacement is in hand.
    /// </remarks>
    public IEnumerable<RelayApiKey> FindOrphans(IEnumerable<RelayApiKey> keys)
    {
        ArgumentNullException.ThrowIfNull(keys);
        return keys.Where(k => IsFromThisMachine(k) && !IsMine(k));
    }

    private static RelayApiKey? Best(IEnumerable<RelayApiKey> candidates) =>
        candidates
            .OrderByDescending(k => k.ExpiresAt.HasValue)
            .ThenByDescending(k => k.ExpiresAt ?? DateTimeOffset.MinValue)
            .FirstOrDefault();

    private static string MachineName()
    {
        try
        {
            return Environment.MachineName;
        }
        catch (InvalidOperationException)
        {
            // Environment.MachineName can throw when the computer name is not
            // available. A stable fallback keeps the naming rule total rather than
            // letting key identification take the whole panel down.
            return "unknown";
        }
    }
}
