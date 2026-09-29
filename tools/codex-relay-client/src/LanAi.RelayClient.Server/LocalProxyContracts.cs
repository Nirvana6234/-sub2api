using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>Which tool a local-proxy account serves.</summary>
public enum LocalProxyKind
{
    /// <summary>Not usable by the local proxy.</summary>
    Unsupported,

    /// <summary>A ChatGPT sign-in — serves Codex.</summary>
    Codex,

    /// <summary>A Claude sign-in — serves Claude Code.</summary>
    ClaudeCode,
}

/// <summary>
/// The access token the local proxy sends one request to the official API with — from this
/// machine's own sign-in, or from an official account signed in within the client.
/// </summary>
/// <remarks>
/// Never carries a refresh token: that stays with whichever source refreshes the sign-in.
/// Held in memory only, and never written to a log. (Until 2026-09 it also came from the relay
/// server, for the user's accounts there; that route is gone from the client.)
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
