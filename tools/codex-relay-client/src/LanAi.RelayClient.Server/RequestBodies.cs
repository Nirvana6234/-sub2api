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

internal sealed record LoginBody(
    [property: JsonPropertyName("email")] string Email,
    [property: JsonPropertyName("password")] string Password);

internal sealed record TwoFactorBody(
    [property: JsonPropertyName("temp_token")] string TempToken,
    [property: JsonPropertyName("totp_code")] string TotpCode);

internal sealed record RefreshTokenBody(
    [property: JsonPropertyName("refresh_token")] string RefreshToken);

internal sealed record VerifyOrderBody(
    [property: JsonPropertyName("out_trade_no")] string OutTradeNo);

internal sealed record BalanceOrderBody(
    [property: JsonPropertyName("amount")] decimal Amount,
    [property: JsonPropertyName("payment_type")] string PaymentType,
    [property: JsonPropertyName("order_type")] string OrderType,
    [property: JsonPropertyName("is_mobile")] bool IsMobile);
