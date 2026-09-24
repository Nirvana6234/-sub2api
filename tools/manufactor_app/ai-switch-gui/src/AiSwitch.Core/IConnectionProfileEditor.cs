namespace LanAi.Workspace.Core;

public static class ConnectionProfileIds
{
    public const string LocalMachine = "local-machine";

    public const string LanDefault = "lan-default";

    public static bool IsFixed(string? id) =>
        string.Equals(id, LocalMachine, StringComparison.OrdinalIgnoreCase) ||
        string.Equals(id, LanDefault, StringComparison.OrdinalIgnoreCase);
}

public enum ConnectionProfileSelectionGroup
{
    Cloud,
    Local,
}

public enum ConnectionSecretChangeKind
{
    Keep,
    Replace,
    Clear,
}

/// <summary>
/// Describes an explicit secret mutation. Existing secrets are never included
/// in editor read models; an empty password field should use <see cref="Keep"/>.
/// </summary>
public sealed class ConnectionSecretChange
{
    private ConnectionSecretChange(ConnectionSecretChangeKind kind, string? replacement)
    {
        Kind = kind;
        Replacement = replacement;
    }

    public ConnectionSecretChangeKind Kind { get; }

    public string? Replacement { get; }

    public static ConnectionSecretChange Keep { get; } =
        new(ConnectionSecretChangeKind.Keep, replacement: null);

    public static ConnectionSecretChange Clear { get; } =
        new(ConnectionSecretChangeKind.Clear, replacement: null);

    public static ConnectionSecretChange Replace(string secret)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(secret);
        return new ConnectionSecretChange(ConnectionSecretChangeKind.Replace, secret);
    }

    public override string ToString() => Kind.ToString();
}

public sealed record ConnectionClientDraft(
    string BaseUrl,
    ConnectionSecretChange SecretChange)
{
    public static ConnectionClientDraft Empty { get; } =
        new(string.Empty, ConnectionSecretChange.Keep);
}

public sealed record ConnectionProfileDraft(
    string Name,
    ConnectionProfileKind Kind,
    string? Notes,
    ConnectionClientDraft Codex,
    ConnectionClientDraft ClaudeCode,
    ConnectionClientDraft GeminiCli,
    string? DashboardUrl = null,
    ConnectionClientDraft? GrokCli = null);

public sealed record ConnectionProfileSelection(
    string? CloudProfileId,
    string? LocalProfileId,
    string? ActiveProfileId = null);

/// <summary>
/// Selects an independent source for each official client when applying the
/// legacy "mixed mode".  It contains only profile identifiers and never
/// carries URLs or secrets.
/// </summary>
public sealed record ConnectionProfileRouting(
    string CodexProfileId,
    string ClaudeCodeProfileId,
    string GeminiCliProfileId,
    string GrokCliProfileId = "",
    IReadOnlyList<string>? BackupProfileIds = null,
    bool BackupUpstreamEnabled = false);

/// <summary>
/// Controlled mutation API for connection metadata stored in the legacy
/// profiles.json document. Returned profiles contain credential references,
/// never the underlying secret values.
/// </summary>
public interface IConnectionProfileEditor
{
    Task<ConnectionProfile> AddAsync(
        ConnectionProfileDraft draft,
        CancellationToken cancellationToken = default);

    Task<ConnectionProfile> UpdateAsync(
        string id,
        ConnectionProfileDraft draft,
        CancellationToken cancellationToken = default);

    Task DeleteAsync(
        string id,
        CancellationToken cancellationToken = default);

    Task<ConnectionProfileSelection> GetSelectionAsync(
        CancellationToken cancellationToken = default);

    Task SetSelectedAsync(
        ConnectionProfileSelectionGroup group,
        string id,
        CancellationToken cancellationToken = default);

    Task<ConnectionProfileRouting> GetRoutingAsync(
        CancellationToken cancellationToken = default);

    Task SetRoutingAsync(
        ConnectionProfileRouting routing,
        CancellationToken cancellationToken = default);
}




