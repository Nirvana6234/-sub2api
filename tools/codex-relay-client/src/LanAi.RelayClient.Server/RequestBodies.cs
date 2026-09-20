using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

// Request payloads that used to be anonymous types.
//
// Anonymous types cannot be registered with a JsonSerializerContext, so they are
// the one shape that blocks source-generated serialization outright — and under
// PublishTrimmed their properties are exactly the kind of thing the trimmer
// removes, which fails silently as an empty request body rather than loudly.
//
// Every property carries an explicit [JsonPropertyName]. This is not decoration:
// the shared options use JsonSerializerDefaults.Web, whose camelCase policy only
// lowercases the first character — it does NOT convert TempToken to temp_token.
// Relying on the policy would rename temp_token to tempToken and break the
// endpoint, so the wire name is pinned here instead.
//
// All four bodies send every field unconditionally, so a record is safe. The
// conditional bodies (registration, key creation, group switch) stay dictionaries
// on purpose — see the note in RelayJsonContext.

/// <summary>
/// What this build calls itself when it signs in.
/// </summary>
/// <remarks>
/// The relay decides session policy from this, and it is fixed at the moment the
/// password is presented — it travels with the session from then on and cannot be
/// restated on a later call. Sent on every route that exchanges a credential for
/// tokens: sign-in, the 2FA step that completes it, and registration.
/// </remarks>
internal static class ClientSource
{
    public const string Desktop = "desktop";
}

internal sealed record LoginBody(
    [property: JsonPropertyName("email")] string Email,
    [property: JsonPropertyName("password")] string Password)
{
    [JsonPropertyName("source")]
    public string Source { get; init; } = ClientSource.Desktop;
}

internal sealed record TwoFactorBody(
    [property: JsonPropertyName("temp_token")] string TempToken,
    [property: JsonPropertyName("totp_code")] string TotpCode)
{
    [JsonPropertyName("source")]
    public string Source { get; init; } = ClientSource.Desktop;
}

internal sealed record RefreshTokenBody(
    [property: JsonPropertyName("refresh_token")] string RefreshToken);

internal sealed record VerifyOrderBody(
    [property: JsonPropertyName("out_trade_no")] string OutTradeNo);

internal sealed record BalanceOrderBody(
    [property: JsonPropertyName("amount")] decimal Amount,
    [property: JsonPropertyName("payment_type")] string PaymentType,
    [property: JsonPropertyName("order_type")] string OrderType,
    [property: JsonPropertyName("is_mobile")] bool IsMobile);

internal sealed record PawAutoGroupBody(
    [property: JsonPropertyName("auto_group")] bool AutoGroup,
    [property: JsonPropertyName("auto_group_ids")] IReadOnlyList<long> AutoGroupIds,
    [property: JsonPropertyName("auto_group_strategy")] string AutoGroupStrategy);
