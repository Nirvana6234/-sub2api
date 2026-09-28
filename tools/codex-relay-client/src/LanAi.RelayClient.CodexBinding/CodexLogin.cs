namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// The ChatGPT sign-in Codex keeps in <c>auth.json</c> (its <c>tokens</c> object), wherever it
/// currently lives: the file itself, or the client's snapshot while Codex points at the relay.
/// </summary>
/// <remarks>Holds secrets: never logged, never shown. <see cref="ToString"/> leaves them out.</remarks>
public sealed record CodexLogin(string AccessToken, string RefreshToken, string IdToken, string AccountId)
{
    public override string ToString() => $"CodexLogin {{ AccountId = {AccountId} }}";
}

/// <summary>What a token refresh returned. A field the server did not send stays as it was.</summary>
public sealed record CodexRefreshedTokens(string AccessToken, string? RefreshToken, string? IdToken)
{
    public override string ToString() => "CodexRefreshedTokens { … }";
}

/// <summary>Reads the user's own ChatGPT sign-in and writes refreshed tokens back where it lives.</summary>
public interface ICodexLoginStore
{
    /// <summary>The newest sign-in on this machine, or null when there is none.</summary>
    CodexLogin? ReadLogin();

    /// <summary>
    /// Writes <paramref name="refreshed"/> into every copy of the sign-in whose refresh token
    /// is <paramref name="previousRefreshToken"/>. Returns false when no copy has it any more —
    /// the user signed in again, or signed out, meanwhile.
    /// </summary>
    bool UpdateLoginTokens(string previousRefreshToken, CodexRefreshedTokens refreshed, DateTimeOffset refreshedAt);
}
