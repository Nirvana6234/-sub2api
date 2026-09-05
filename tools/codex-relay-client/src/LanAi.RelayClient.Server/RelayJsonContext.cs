using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>
/// Source-generated serialization metadata for every type that crosses the wire.
/// </summary>
/// <remarks>
/// <para>
/// <b>NOT WIRED IN YET — one decision is outstanding.</b> The type inventory below is
/// complete and verified against every generic argument the transport binds, but
/// <c>RelayServerClient</c> still uses the reflection-based serializer. Switching it
/// over changes observable behaviour, so the choice belongs to a human.
/// </para>
/// <para>
/// <b>The blocker.</b> Every contract here is a record whose properties are
/// <c>init</c>-only with a default (<c>= string.Empty</c>, <c>= Array.Empty&lt;T&gt;()</c>).
/// Source generation cannot assign <c>init</c>-only properties — it fails with
/// <i>"Setting init-only properties is not supported in source generation mode"</i> —
/// so it routes these types through the parameterized-constructor converter instead.
/// On that path a field the server omits arrives as <c>default</c> (<c>null</c>), and
/// the property initializer never runs.
/// </para>
/// <para>
/// Measured, not assumed. For <c>{"access_token":"at","token_type":"Bearer"}</c>:
/// reflection yields <c>RefreshToken == ""</c>, source generation yields
/// <c>RefreshToken == null</c>. The regression test
/// <c>AnAccessTokenWithoutARefreshTokenIsAccepted</c> pins the former, and it is the
/// only test that caught this — it is the one place an omitted field is asserted.
/// <b>41 properties across these contracts depend on that initializer</b>
/// (38 strings, 3 collections), and a null collection is worse than a null string.
/// </para>
/// <para>
/// Options, in the order they were considered:
/// <list type="number">
/// <item><description>
/// <c>init</c> → <c>set</c> on all 41 properties. Smallest diff, restores the
/// parameterless-constructor path exactly. Cost: the contracts stop being immutable.
/// </description></item>
/// <item><description>
/// Convert to positional records with defaulted parameters
/// (<c>record AuthTokens(string AccessToken = "", …)</c>). Source generation honours
/// constructor defaults, so behaviour is preserved <i>and</i> immutability survives.
/// Larger rewrite of all 26 contracts.
/// </description></item>
/// <item><description>
/// Give up on trimming. Not viable — see §2.3 of the macOS plan: reflection-based
/// serialization is what blocks <c>PublishTrimmed</c>, and trimming is the only
/// .NET route to the ~20 MB target.
/// </description></item>
/// </list>
/// </para>
/// <para>
/// This exists to make the client trimmable. Reflection-based
/// <see cref="JsonSerializer"/> is the single largest obstacle to
/// <c>PublishTrimmed</c>: the trimmer removes property accessors and constructors
/// that nothing references statically, and a reflection binder references nothing
/// statically. The failure mode is the dangerous one — not an exception, but a
/// contract that binds to an empty object.
/// </para>
/// <para>
/// That is not a hypothetical. This client already shipped one bug of exactly that
/// shape: <c>AuthTokens</c> gives every property a default value, so a response of
/// the wrong shape bound cleanly to an empty token object, the client reported
/// "login succeeded", and every later call returned 401. Trimming would reintroduce
/// that failure across all of the contracts at once.
/// </para>
/// <para>
/// Registering a root type also registers everything reachable from it, so nested
/// contracts (<c>SubscriptionSummaryItem</c>, <c>ModelUsage</c>, <c>UsageTrendPoint</c>,
/// <c>PeakWindow</c>, …) do not need their own entries.
/// </para>
/// <para>
/// <b>Adding an endpoint means adding its type here.</b> A type that is missing
/// throws <see cref="NotSupportedException"/> at the call, which the transport
/// already classifies as a malformed response — loud, and caught by the endpoint's
/// own test rather than discovered by a user.
/// </para>
/// </remarks>
[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase,
    PropertyNameCaseInsensitive = true,
    NumberHandling = JsonNumberHandling.AllowReadingFromString)]
// The envelope every response arrives in. Bound with JsonElement as its payload
// so the data field can be inspected before it is committed to a contract.
[JsonSerializable(typeof(ApiEnvelope<JsonElement>))]
[JsonSerializable(typeof(JsonElement))]
// Responses.
[JsonSerializable(typeof(PublicSettings))]
[JsonSerializable(typeof(RelayUser))]
[JsonSerializable(typeof(AuthTokens))]
[JsonSerializable(typeof(TotpChallengePayload))]
[JsonSerializable(typeof(VerifyCodeDispatch))]
[JsonSerializable(typeof(AnnouncementHead))]
[JsonSerializable(typeof(RelayAnnouncement[]))]
[JsonSerializable(typeof(RelayGroup[]))]
[JsonSerializable(typeof(DashboardStats))]
[JsonSerializable(typeof(RelayApiKey))]
[JsonSerializable(typeof(PagedResult<RelayApiKey>))]
[JsonSerializable(typeof(SubscriptionSummaryResponse))]
[JsonSerializable(typeof(UsageSnapshot))]
[JsonSerializable(typeof(ModelUsageResponse))]
[JsonSerializable(typeof(ClaudePreferenceDto))]
[JsonSerializable(typeof(PaymentCheckoutInfo))]
[JsonSerializable(typeof(PaymentOrderCreateResult))]
[JsonSerializable(typeof(PaymentOrder))]
// Per-group rate overrides arrive as a bare map, not a contract object.
[JsonSerializable(typeof(Dictionary<long, double>))]
// Requests with a fixed shape.
[JsonSerializable(typeof(LoginBody))]
[JsonSerializable(typeof(TwoFactorBody))]
[JsonSerializable(typeof(RefreshTokenBody))]
[JsonSerializable(typeof(VerifyOrderBody))]
[JsonSerializable(typeof(BalanceOrderBody))]
// Requests whose field *presence* is the contract.
//
// These stay dictionaries rather than becoming records, and that is a safety
// property rather than a style choice. PUT /keys/:id reads expires_at in three
// states: absent keeps the current expiry, "" clears it (making the key permanent),
// and an RFC3339 value sets it. A record with a defaulted property would serialize
// the field as "" and silently convert a one-day lease into a key that never
// expires. The same rule governs the optional registration fields, where an empty
// string means "supplied but blank" rather than "not supplied".
[JsonSerializable(typeof(Dictionary<string, object?>))]
// Value types reachable as dictionary values, needed because those dictionaries
// are declared with object values.
[JsonSerializable(typeof(string))]
[JsonSerializable(typeof(int))]
[JsonSerializable(typeof(long))]
[JsonSerializable(typeof(decimal))]
[JsonSerializable(typeof(bool))]
internal sealed partial class RelayJsonContext : JsonSerializerContext;
