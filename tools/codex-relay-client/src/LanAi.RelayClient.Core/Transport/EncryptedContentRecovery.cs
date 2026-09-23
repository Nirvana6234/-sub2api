using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// Codex history the local proxy's account cannot read: encrypted reasoning and compaction
/// items made by a different account — typically the relay server's, when a conversation
/// that started there is continued on a local proxy.
/// </summary>
/// <remarks>
/// <para>
/// The same recovery the server runs when its pool changes accounts
/// (<c>openai_encrypted_content_lineage.go</c>): the official API answers such a turn with
/// 400 <c>invalid_encrypted_content</c>; the turn is retried once with every encrypted item
/// stripped, and the rejected items are remembered by digest so later turns — whose
/// history Codex keeps sending unchanged — have just those stripped up front. Items the
/// account itself produced have other digests and are left alone.
/// </para>
/// <para>
/// Without it the conversation is stuck: every later turn carries the same unreadable
/// items and is refused the same way. Stripping loses the old turns' hidden reasoning,
/// not their messages or tool results.
/// </para>
/// </remarks>
internal static class EncryptedContentRecovery
{
    /// <summary>Server <c>isOpenAIInvalidEncryptedContentHTTPResponse</c>.</summary>
    internal static bool IsInvalidEncryptedContent(int status, string? body)
    {
        if (status != 400 || string.IsNullOrEmpty(body))
        {
            return false;
        }

        string text = body.ToLowerInvariant();
        if (text.Contains("invalid_encrypted_content", StringComparison.Ordinal))
        {
            return true;
        }

        return (text.Contains("encrypted content", StringComparison.Ordinal) ||
                text.Contains("encrypted_content", StringComparison.Ordinal)) &&
               (text.Contains("could not be verified", StringComparison.Ordinal) ||
                text.Contains("decrypt", StringComparison.Ordinal) ||
                text.Contains("could not be parsed", StringComparison.Ordinal));
    }

    /// <summary>Server <c>collectOpenAIEncryptedContentDigestsRaw</c>: digests of the encrypted items in <c>input</c>.</summary>
    internal static IReadOnlyList<string> CollectDigests(byte[] body)
    {
        var digests = new List<string>();
        if (Parse(body)?["input"] is { } input)
        {
            foreach (JsonObject item in Items(input))
            {
                if (IsCoveredType(item) && EncryptedOf(item) is { } encrypted)
                {
                    digests.Add(Digest(encrypted));
                }
            }
        }
        return digests;
    }

    /// <summary>Server <c>trimOpenAIEncryptedReasoningItems</c>: every encrypted item stripped; null when there is none.</summary>
    internal static byte[]? StripAll(byte[] body) => Strip(body, _ => true);

    /// <summary>Server <c>stripOpenAIInvalidEncryptedContentRaw</c>: only the items already rejected; null when none is present.</summary>
    internal static byte[]? StripKnown(byte[] body, IReadOnlySet<string> rejected) =>
        rejected.Count == 0 ? null : Strip(body, rejected.Contains);

    internal static string Digest(string encrypted) =>
        Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(encrypted))).ToLowerInvariant();

    private static byte[]? Strip(byte[] body, Func<string, bool> matches)
    {
        if (Parse(body) is not { } root || root["input"] is not { } input)
        {
            return null;
        }

        bool changed = false;
        switch (input)
        {
            case JsonArray items:
                for (int i = items.Count - 1; i >= 0; i--)
                {
                    if (items[i] is JsonObject item && Sanitize(item, matches) is var (itemChanged, keep) && itemChanged)
                    {
                        changed = true;
                        if (!keep)
                        {
                            items.RemoveAt(i);
                        }
                    }
                }
                if (changed && items.Count == 0)
                {
                    root.Remove("input");
                }
                break;
            case JsonObject single:
                if (Sanitize(single, matches) is var (singleChanged, singleKeep) && singleChanged)
                {
                    changed = true;
                    if (!singleKeep)
                    {
                        root.Remove("input");
                    }
                }
                break;
        }

        if (!changed)
        {
            return null;
        }

        using var buffer = new MemoryStream(body.Length);
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions { Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping }))
        {
            root.WriteTo(writer);
        }
        return buffer.ToArray();
    }

    /// <summary>
    /// Server <c>sanitizeEncryptedReasoningInputItem</c>: a compaction item goes whole; a
    /// reasoning item loses its encrypted_content (and a null content), and goes whole if
    /// nothing else is left.
    /// </summary>
    private static (bool Changed, bool Keep) Sanitize(JsonObject item, Func<string, bool> matches)
    {
        if (!IsCoveredType(item) || EncryptedOf(item) is not { } encrypted || !matches(Digest(encrypted)))
        {
            return (false, true);
        }

        if (TypeOf(item) is "compaction" or "compaction_summary")
        {
            return (true, false);
        }

        item.Remove("encrypted_content");
        if (item.TryGetPropertyValue("content", out JsonNode? content) && content is null)
        {
            item.Remove("content");
        }
        return (true, item.Count > 1);
    }

    private static bool IsCoveredType(JsonObject item) =>
        TypeOf(item) is "reasoning" or "compaction" or "compaction_summary";

    private static string? TypeOf(JsonObject item) =>
        item["type"] is JsonValue v && v.TryGetValue(out string? s) ? s.Trim() : null;

    private static string? EncryptedOf(JsonObject item) =>
        item["encrypted_content"] is JsonValue v && v.TryGetValue(out string? s) && s.Length > 0 ? s : null;

    private static IEnumerable<JsonObject> Items(JsonNode input) => input switch
    {
        JsonArray items => items.OfType<JsonObject>(),
        JsonObject single => [single],
        _ => [],
    };

    private static JsonObject? Parse(byte[] body)
    {
        if (body.Length == 0)
        {
            return null;
        }

        try
        {
            return JsonNode.Parse(body) as JsonObject;
        }
        catch (JsonException)
        {
            return null;
        }
    }
}

/// <summary>
/// The encrypted items each of the user's accounts has refused, by digest. One relay,
/// one user: keyed by account rather than by conversation, since an item one account
/// cannot read stays unreadable to it in every conversation.
/// </summary>
internal sealed class RejectedEncryptedContent
{
    /// <summary>Per account; past this the set starts over, which costs at most one more refused turn.</summary>
    internal const int Capacity = 4096;

    private readonly Dictionary<long, HashSet<string>> _byAccount = [];
    private readonly object _gate = new();

    internal void Remember(long accountId, IEnumerable<string> digests)
    {
        lock (_gate)
        {
            if (!_byAccount.TryGetValue(accountId, out HashSet<string>? set) || set.Count >= Capacity)
            {
                _byAccount[accountId] = set = [];
            }
            set.UnionWith(digests);
        }
    }

    internal IReadOnlySet<string> For(long accountId)
    {
        lock (_gate)
        {
            return _byAccount.TryGetValue(accountId, out HashSet<string>? set) ? new HashSet<string>(set) : [];
        }
    }
}
