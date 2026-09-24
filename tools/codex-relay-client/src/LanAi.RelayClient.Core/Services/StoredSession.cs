using System.Text.Json.Serialization;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>
/// The part of a signed-in session that survives a restart.
/// </summary>
/// <remarks>
/// Persisted encrypted (see <see cref="DpapiSessionStore"/>). Holds tokens, so it
/// must never be logged, shown in a window title, or written anywhere in plaintext.
/// </remarks>
internal sealed record StoredSession
{
    [JsonPropertyName("server")]
    public string ServerAddress { get; init; } = string.Empty;

    [JsonPropertyName("access_token")]
    public string AccessToken { get; init; } = string.Empty;

    [JsonPropertyName("refresh_token")]
    public string RefreshToken { get; init; } = string.Empty;

    /// <summary>When the access token stops being accepted.</summary>
    [JsonPropertyName("access_expires_at")]
    public DateTimeOffset AccessExpiresAt { get; init; }

    [JsonPropertyName("user_email")]
    public string UserEmail { get; init; } = string.Empty;

    [JsonPropertyName("user_name")]
    public string UserName { get; init; } = string.Empty;

    /// <summary>
    /// Whether this session can be renewed without asking for the password again.
    /// </summary>
    /// <remarks>
    /// False when the server issued an access token alone — a documented fallback
    /// when token-pair generation fails. Such a session works until the access
    /// token expires and then requires a fresh sign-in; the client must not
    /// present that as an error.
    /// </remarks>
    public bool CanRenew => !string.IsNullOrWhiteSpace(RefreshToken);

    public static StoredSession FromTokens(string serverAddress, AuthTokens tokens, DateTimeOffset now)
    {
        ArgumentNullException.ThrowIfNull(tokens);

        return new StoredSession
        {
            ServerAddress = serverAddress,
            AccessToken = tokens.AccessToken,
            RefreshToken = tokens.RefreshToken,

            // A server that omits expires_in leaves us with no way to schedule a
            // renewal. Treating that as "already expired" would sign the user out
            // immediately; assuming a long life would let calls fail unexplained.
            // A short assumed life makes the client verify early instead.
            AccessExpiresAt = now.AddSeconds(tokens.ExpiresInSeconds > 0 ? tokens.ExpiresInSeconds : 300),
            UserEmail = tokens.User?.Email ?? string.Empty,
            UserName = tokens.User?.DisplayName ?? string.Empty,
        };
    }

    /// <summary>Whether the access token needs renewing before the next call.</summary>
    /// <param name="margin">
    /// How far ahead of real expiry to renew, so a call cannot be issued with a
    /// token that expires while it is in flight.
    /// </param>
    public bool NeedsRenewal(DateTimeOffset now, TimeSpan margin) => now + margin >= AccessExpiresAt;
}
