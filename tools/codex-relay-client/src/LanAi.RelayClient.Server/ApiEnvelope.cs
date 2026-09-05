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
    [JsonPropertyName("code")]
    public int Code { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }

    [JsonPropertyName("reason")]
    public string? Reason { get; init; }

    [JsonPropertyName("metadata")]
    public Dictionary<string, string>? Metadata { get; init; }

    [JsonPropertyName("data")]
    public T? Data { get; init; }

    /// <summary>
    /// Whether the envelope reports success.
    /// </summary>
    /// <remarks>
    /// A 2xx status is not sufficient on its own — the code field is the
    /// authoritative signal, and only <c>0</c> means success.
    /// </remarks>
    public bool IsSuccess => Code == 0;
}
