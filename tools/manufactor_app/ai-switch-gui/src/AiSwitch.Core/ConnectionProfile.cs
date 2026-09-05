namespace LanAi.Workspace.Core;

/// <summary>
/// Describes request routing. API keys are represented only by a secure credential reference.
/// </summary>
public sealed record ConnectionProfile
{
    public required string Id { get; init; }

    public required string Name { get; init; }

    public ConnectionProfileKind Kind { get; init; }

    public required string BaseUrl { get; init; }

    /// <summary>
    /// Optional per-CLI endpoints. Legacy Sub2API profiles commonly use /v1 for Codex
    /// while Claude and Gemini use the server root.
    /// </summary>
    public IReadOnlyDictionary<CliKind, string> ClientBaseUrls { get; init; }
        = new Dictionary<CliKind, string>();

    public string? ApiKeyCredentialId { get; init; }

    /// <summary>
    /// Optional browser address for the administration UI. Production Sub2API
    /// serves the API and embedded browser UI from the same endpoint.
    /// </summary>
    public string? DashboardUrl { get; init; }

    /// <summary>
    /// Non-reversible, display-safe metadata for existing client credentials.
    /// It lets a user distinguish stored keys without serializing or exposing
    /// the complete secret in the UI, logs, or workspace database.
    /// </summary>
    public IReadOnlyDictionary<CliKind, ConnectionCredentialHint> ClientCredentialHints { get; init; }
        = new Dictionary<CliKind, ConnectionCredentialHint>();

    public IReadOnlyList<CliKind> EnabledClients { get; init; } = Array.Empty<CliKind>();

    public IReadOnlyDictionary<CliKind, string> DefaultModels { get; init; }
        = new Dictionary<CliKind, string>();

    public string? Notes { get; init; }

    public DateTimeOffset? LastHealthCheckAt { get; init; }

    public int? LastLatencyMilliseconds { get; init; }
}

public sealed record ConnectionCredentialHint(
    string MaskedPreview,
    string Fingerprint);
