using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>The <c>state</c> sent to Jev (docs §5.3): only what the judgement needs.</summary>
internal sealed record JevState
{
    [JsonPropertyName("relationship")]
    public string Relationship { get; init; } = "私聊";

    [JsonPropertyName("conversation")]
    public IReadOnlyList<JevTurn> Conversation { get; init; } = [];

    [JsonPropertyName("latest_from_them")]
    public IReadOnlyList<string> LatestFromThem { get; init; } = [];
}

internal sealed record JevTurn
{
    /// <summary><c>them</c> or <c>me</c>.</summary>
    [JsonPropertyName("speaker")]
    public string Speaker { get; init; } = string.Empty;

    [JsonPropertyName("text")]
    public string Text { get; init; } = string.Empty;
}

/// <summary>A Jev response (docs §5.2), as the API reference documents it.</summary>
internal sealed record JevResponse
{
    /// <summary>The versioned model that answered, e.g. <c>jev-1.13.0</c>.</summary>
    [JsonPropertyName("model")]
    public string Model { get; init; } = string.Empty;

    [JsonPropertyName("answers")]
    public Dictionary<string, JevAnswer> Answers { get; init; } = [];

    [JsonPropertyName("usage")]
    public JevUsage? Usage { get; init; }
}

internal sealed record JevUsage
{
    [JsonPropertyName("input_tokens")]
    public long InputTokens { get; init; }

    [JsonPropertyName("output_tokens")]
    public long OutputTokens { get; init; }
}

/// <summary>One answer. Which fields are set depends on <see cref="Type"/>.</summary>
internal sealed record JevAnswer
{
    [JsonPropertyName("type")]
    public string Type { get; init; } = string.Empty;

    /// <summary>Choice: the most probable option.</summary>
    [JsonPropertyName("choice")]
    public string? Choice { get; init; }

    /// <summary>Choice: option → probability. Score: level index as a string → probability.</summary>
    [JsonPropertyName("probabilities")]
    public Dictionary<string, double>? Probabilities { get; init; }

    /// <summary>Choice and Score: how peaked the distribution is, 0–1.</summary>
    [JsonPropertyName("confidence")]
    public double? Confidence { get; init; }

    /// <summary>Score: probability-weighted level. Only for thresholds, never interpolated (§5.5).</summary>
    [JsonPropertyName("score")]
    public double? Score { get; init; }

    /// <summary>Noul: probability of yes.</summary>
    [JsonPropertyName("noul")]
    public double? Noul { get; init; }
}

/// <summary>The error body TypeSafe returns, e.g. <c>{"detail":{"error_type":…,"message":"Unknown model: …"}}</c>.</summary>
internal sealed record JevErrorBody
{
    [JsonPropertyName("detail")]
    public JevErrorDetail? Detail { get; init; }
}

internal sealed record JevErrorDetail
{
    [JsonPropertyName("error_type")]
    public string? ErrorType { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }
}

internal sealed record JevModelList
{
    [JsonPropertyName("models")]
    public List<JevModelEntry> Models { get; init; } = [];
}

internal sealed record JevModelEntry
{
    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;
}

[JsonSourceGenerationOptions(DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(JevState))]
[JsonSerializable(typeof(JevResponse))]
[JsonSerializable(typeof(JevErrorBody))]
[JsonSerializable(typeof(JevModelList))]
internal sealed partial class JevJsonContext : JsonSerializerContext;

/// <summary>Builds the request body: the state through the source-generated context, the fixed questions spliced in raw.</summary>
internal static class JevRequestBody
{
    public static byte[] Build(string model, JevState state)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream))
        {
            writer.WriteStartObject();
            writer.WriteString("model", model);
            writer.WritePropertyName("state");
            JsonSerializer.Serialize(writer, state, JevJsonContext.Default.JevState);
            writer.WritePropertyName("questions");
            writer.WriteRawValue(WeChatIntentQuestions.Json, skipInputValidation: false);
            writer.WriteEndObject();
        }

        return stream.ToArray();
    }

    /// <summary>The request for a judgement: the last ten messages, and the batch being answered.</summary>
    public static JevState StateFor(IReadOnlyList<ChatItem> recent, IReadOnlyList<ChatItem> latest) => new()
    {
        Conversation = recent
            .Where(i => i.Speaker != ChatSpeaker.Time)
            .Select(i => new JevTurn { Speaker = i.Speaker == ChatSpeaker.Me ? "me" : "them", Text = i.Text })
            .ToList(),
        LatestFromThem = latest.Select(i => i.Text).ToList(),
    };

    internal static string ToText(byte[] body) => Encoding.UTF8.GetString(body);
}
