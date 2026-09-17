using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>
/// The relay's uniform response wrapper.
/// </summary>
/// <remarks>
/// Mirrors <c>internal/pkg/response/response.go</c>: success replies carry
/// <c>code = 0</c>, failures carry the HTTP status in <c>code</c> plus an
/// optional machine-readable <c>reason</c>.
/// </remarks>
internal sealed class ApiEnvelope<T>
{
    /// <summary>
    /// The envelope's <c>code</c>, kept raw because the relay has two shapes for it.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Handlers answer through <c>internal/pkg/response</c>, whose <c>code</c> is a
    /// number. Middleware answers through its own <c>ErrorResponse</c>
    /// (<c>internal/server/middleware/middleware.go</c>), whose <c>code</c> is a
    /// <b>string</b> naming the reason — <c>SESSION_BINDING_MISMATCH</c>,
    /// <c>TOKEN_EXPIRED</c>, <c>TOKEN_REVOKED</c>, and about twenty more.
    /// </para>
    /// <para>
    /// Typed as <c>int</c>, every one of those rejections failed to deserialize, so
    /// the whole middleware layer was invisible to this client: a 401 that said
    /// exactly why arrived as "服务器返回了无法识别的内容（HTTP 401）" with
    /// <see cref="RelayApiException.Reason"/> null and the failure classified as
    /// <see cref="RelayFailure.MalformedResponse"/> rather than
    /// <see cref="RelayFailure.Unauthenticated"/>. Seen in a real session log.
    /// </para>
    /// </remarks>
    [JsonPropertyName("code")]
    public JsonElement Code { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }

    [JsonPropertyName("reason")]
    public string? Reason { get; init; }

    [JsonPropertyName("metadata")]
    public Dictionary<string, string>? Metadata { get; init; }

    [JsonPropertyName("data")]
    public T? Data { get; init; }

    /// <summary>The reason code, when the relay sent the string-shaped envelope.</summary>
    public string? CodeText => Code.ValueKind == JsonValueKind.String ? Code.GetString() : null;

    /// <summary>
    /// Whether the envelope reports success.
    /// </summary>
    /// <remarks>
    /// A 2xx status is not sufficient on its own — the code field is the
    /// authoritative signal, and only <c>0</c> means success. A missing field counts
    /// as success because that is what this property did while <c>code</c> was an
    /// <c>int</c> defaulting to zero, and some success replies omit it. A string code
    /// is only ever produced by the middleware error envelope, so it is never one.
    /// </remarks>
    public bool IsSuccess => Code.ValueKind switch
    {
        JsonValueKind.Undefined => true,
        JsonValueKind.Number => Code.TryGetInt32(out int code) && code == 0,
        _ => false,
    };
}
