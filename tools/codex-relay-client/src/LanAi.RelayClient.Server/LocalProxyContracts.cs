using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>Whether an account can serve the local proxy.</summary>
/// <remarks>Codex only: Claude Code keeps going through the relay server.</remarks>
public enum LocalProxyKind
{
    /// <summary>Not usable by the local proxy (API key, setup token, another platform…).</summary>
    Unsupported,

    /// <summary>An OpenAI (ChatGPT) OAuth account — serves Codex.</summary>
    Codex,
}

/// <summary>
/// One of the user's own accounts on the relay (a "contribution"), as listed by
/// <c>GET /api/v1/account-contributions</c>.
/// </summary>
/// <remarks>
/// Only the non-sensitive fields. The server redacts credentials in this listing; the
/// one credential sub-key read here, <c>plan_type</c>, is a label, not a secret.
/// </remarks>
public sealed record ContributionAccount
{
    [JsonConstructor]
    public ContributionAccount(
        long id = default,
        string? name = null,
        string? platform = null,
        string? type = null,
        string? status = null,
        string? errorMessage = null,
        ContributionAccountCredentials? credentials = null,
        long? parentAccountId = null)
    {
        Id = id;
        Name = name ?? string.Empty;
        Platform = platform ?? string.Empty;
        Type = type ?? string.Empty;
        Status = status ?? string.Empty;
        ErrorMessage = errorMessage ?? string.Empty;
        Credentials = credentials;
        ParentAccountId = parentAccountId;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;

    [JsonPropertyName("platform")]
    public string Platform { get; init; } = string.Empty;

    [JsonPropertyName("type")]
    public string Type { get; init; } = string.Empty;

    [JsonPropertyName("status")]
    public string Status { get; init; } = string.Empty;

    [JsonPropertyName("error_message")]
    public string ErrorMessage { get; init; } = string.Empty;

    [JsonPropertyName("credentials")]
    public ContributionAccountCredentials? Credentials { get; init; }

    /// <summary>Set on a spark "shadow" account, which the local proxy does not use.</summary>
    [JsonPropertyName("parent_account_id")]
    public long? ParentAccountId { get; init; }

    public bool IsActive => string.Equals(Status, "active", StringComparison.OrdinalIgnoreCase);

    /// <summary>
    /// Mirrors the server's rule (<c>supportsLocalProxy</c>): a ChatGPT OAuth account, never a
    /// shadow. Setup tokens are long-lived secrets the server will not hand out.
    /// </summary>
    public LocalProxyKind LocalProxyKind =>
        string.Equals(Type, "oauth", StringComparison.OrdinalIgnoreCase) &&
        ParentAccountId is null &&
        string.Equals(Platform, "openai", StringComparison.OrdinalIgnoreCase)
            ? LocalProxyKind.Codex
            : LocalProxyKind.Unsupported;
}

/// <summary>The non-secret credential sub-keys the listing keeps after redaction.</summary>
public sealed record ContributionAccountCredentials
{
    [JsonConstructor]
    public ContributionAccountCredentials(string? planType = null) => PlanType = planType;

    [JsonPropertyName("plan_type")]
    public string? PlanType { get; init; }
}

/// <summary>The contribution listing envelope; only the items are read.</summary>
public sealed record ContributionAccountList
{
    [JsonConstructor]
    public ContributionAccountList(IReadOnlyList<ContributionAccount>? items = null, int total = default)
    {
        Items = items ?? Array.Empty<ContributionAccount>();
        Total = total;
    }

    [JsonPropertyName("items")]
    public IReadOnlyList<ContributionAccount> Items { get; init; } = Array.Empty<ContributionAccount>();

    [JsonPropertyName("total")]
    public int Total { get; init; }
}

/// <summary>
/// A short-lived access token for one of the user's own ChatGPT accounts, from
/// <c>POST /api/v1/account-contributions/:id/local-proxy-token</c>.
/// </summary>
/// <remarks>
/// Never carries a refresh token: the server stays the account's only refresher.
/// Held in memory only, and never written to a log.
/// </remarks>
public sealed record LocalProxyCredential
{
    [JsonConstructor]
    public LocalProxyCredential(
        long accountId = default,
        string? name = null,
        string? platform = null,
        string? accessToken = null,
        DateTimeOffset? expiresAt = null,
        string? chatgptAccountId = null,
        bool fedramp = default)
    {
        AccountId = accountId;
        Name = name ?? string.Empty;
        Platform = platform ?? string.Empty;
        AccessToken = accessToken ?? string.Empty;
        ExpiresAt = expiresAt;
        ChatGptAccountId = chatgptAccountId ?? string.Empty;
        FedRamp = fedramp;
    }

    [JsonPropertyName("account_id")]
    public long AccountId { get; init; }

    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;

    [JsonPropertyName("platform")]
    public string Platform { get; init; } = string.Empty;

    [JsonPropertyName("access_token")]
    public string AccessToken { get; init; } = string.Empty;

    [JsonPropertyName("expires_at")]
    public DateTimeOffset? ExpiresAt { get; init; }

    [JsonPropertyName("chatgpt_account_id")]
    public string ChatGptAccountId { get; init; } = string.Empty;

    [JsonPropertyName("fedramp")]
    public bool FedRamp { get; init; }

    /// <summary>Keeps the token out of any accidental <c>ToString</c> in a log line.</summary>
    public override string ToString() => $"LocalProxyCredential {{ AccountId = {AccountId}, Platform = {Platform} }}";
}
