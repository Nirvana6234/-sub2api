using System.Collections.Specialized;
using System.IO;
using System.Net.Http.Headers;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// Builds the request the local proxy sends to the official API. Pure, so every rule
/// is testable without a socket.
/// </summary>
/// <remarks>
/// <para>
/// <b>Forwarded, not re-wrapped.</b> The request is what Codex or Claude Code already
/// sent; the tools are official clients. What is added is only what the tool leaves out
/// because it is running against a custom endpoint rather than signed in to its vendor:
/// the account's token and, for Codex, the ChatGPT account id; for Claude Code, the OAuth
/// beta. Every rule here mirrors a line in the relay server's own forwarder, named in the
/// remarks, and nothing the server does only because many users share one account is
/// copied (session isolation, fingerprint convergence, identity unification, failover).
/// </para>
/// <para>
/// Headers are a whitelist, not "everything the tool sent": the tool's request also
/// carries this relay's local token and the context filter's statistics headers, neither
/// of which may leave this machine.
/// </para>
/// </remarks>
internal static class LocalProxyRequests
{
    /// <summary>
    /// Codex headers forwarded as-is. The server's <c>openaiPassthroughAllowedHeaders</c>,
    /// plus <c>version</c>: the server rewrites that one itself, this proxy does not, so the
    /// tool's own value travels.
    /// </summary>
    internal static readonly string[] CodexForwardedHeaders =
    [
        "accept", "accept-language", "conversation_id", "openai-beta", "user-agent", "originator",
        "session_id", "version", "x-codex-beta-features", "x-codex-installation-id",
        "x-codex-turn-state", "x-codex-turn-metadata", "x-codex-window-id",
        "x-openai-internal-codex-responses-lite",
    ];

    /// <summary>
    /// Claude Code headers forwarded as-is: the server's <c>allowedHeaders</c> minus
    /// <c>accept-encoding</c>, so the answer comes back uncompressed and its usage can be read.
    /// </summary>
    internal static readonly string[] ClaudeForwardedHeaders =
    [
        "accept", "accept-language", "anthropic-beta", "anthropic-dangerous-direct-browser-access",
        "anthropic-version", "sec-fetch-mode", "user-agent", "x-app", "x-claude-code-session-id",
        "x-client-request-id", "x-stainless-arch", "x-stainless-helper-method", "x-stainless-lang",
        "x-stainless-os", "x-stainless-package-version", "x-stainless-retry-count",
        "x-stainless-runtime", "x-stainless-runtime-version", "x-stainless-timeout",
    ];

    /// <summary>Server <c>openAIRemoteCompactionV2Feature</c>.</summary>
    internal const string RemoteCompactionV2 = "remote_compaction_v2";

    /// <summary>Server <c>claude.BetaOAuth</c>: without it a subscription token is refused.</summary>
    internal const string ClaudeOAuthBeta = "oauth-2025-04-20";

    internal const string ClaudeCodeBeta = "claude-code-20250219";

    internal const string TokenCountingBeta = "token-counting-2024-11-01";

    /// <summary>Server <c>claude.DefaultBetaHeader</c>, for a client that sent none.</summary>
    internal const string DefaultClaudeBeta =
        "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14";

    /// <summary>Server <c>openAIChatGPTInternalUnsupportedFields</c>: the ChatGPT backend 400s on these.</summary>
    private static readonly string[] CodexUnsupportedFields =
    [
        "chat_template_kwargs", "user", "metadata", "prompt_cache_retention", "safety_identifier",
        "stream_options", "truncation", "stop_sequences",
    ];

    internal static HttpRequestMessage BuildCodex(
        string targetUrl,
        NameValueCollection clientHeaders,
        byte[] body,
        string? contentType,
        LocalProxyCredential credential,
        bool compact)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, targetUrl)
        {
            Content = new ByteArrayContent(NormalizeCodexBody(body, compact)),
        };
        request.Content.Headers.ContentType = MediaTypeHeaderValue.Parse(contentType ?? "application/json");
        Forward(clientHeaders, CodexForwardedHeaders, request);

        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", credential.AccessToken);

        // setOpenAIChatGPTAccountHeaders: which ChatGPT workspace the token acts in.
        if (!string.IsNullOrWhiteSpace(credential.ChatGptAccountId))
        {
            request.Headers.TryAddWithoutValidation("chatgpt-account-id", credential.ChatGptAccountId);
        }
        if (credential.FedRamp)
        {
            request.Headers.TryAddWithoutValidation("x-openai-fedramp", "true");
        }

        // stripOpenAILegacyResponsesBeta.
        if (StripLegacyResponsesBeta(clientHeaders["openai-beta"]) is var beta && beta != clientHeaders["openai-beta"])
        {
            request.Headers.Remove("openai-beta");
            if (!string.IsNullOrEmpty(beta))
            {
                request.Headers.TryAddWithoutValidation("openai-beta", beta);
            }
        }

        // applyOpenAICodexBetaFeatures: a client that did not declare features gets the
        // default Codex shape, so remote compaction uses the endpoint that still exists.
        if (string.IsNullOrWhiteSpace(clientHeaders["x-codex-beta-features"]))
        {
            request.Headers.TryAddWithoutValidation("x-codex-beta-features", RemoteCompactionV2);
        }

        // Compact is a unary JSON call; a turn is a stream.
        if (compact)
        {
            request.Headers.Remove("accept");
            request.Headers.TryAddWithoutValidation("accept", "application/json");
        }
        else if (string.IsNullOrWhiteSpace(clientHeaders["accept"]))
        {
            request.Headers.TryAddWithoutValidation("accept", "text/event-stream");
        }

        return request;
    }

    internal static HttpRequestMessage BuildClaude(
        string targetUrl,
        NameValueCollection clientHeaders,
        byte[] body,
        string? contentType,
        LocalProxyCredential credential,
        bool countTokens)
    {
        var request = new HttpRequestMessage(HttpMethod.Post, targetUrl)
        {
            Content = new ByteArrayContent(body),
        };
        request.Content.Headers.ContentType = MediaTypeHeaderValue.Parse(contentType ?? "application/json");
        Forward(clientHeaders, ClaudeForwardedHeaders, request);

        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", credential.AccessToken);

        request.Headers.Remove("anthropic-beta");
        request.Headers.TryAddWithoutValidation("anthropic-beta", WithOAuthBeta(clientHeaders["anthropic-beta"], countTokens));

        if (string.IsNullOrWhiteSpace(clientHeaders["anthropic-version"]))
        {
            request.Headers.TryAddWithoutValidation("anthropic-version", "2023-06-01");
        }

        return request;
    }

    /// <summary>
    /// The client's <c>anthropic-beta</c> with the OAuth beta added — after the Claude Code
    /// beta when present, first otherwise (server <c>getBetaHeader</c>) — and the token
    /// counting beta on a count (<c>computeFinalCountTokensAnthropicBeta</c>).
    /// </summary>
    internal static string WithOAuthBeta(string? clientBeta, bool countTokens)
    {
        List<string> parts = string.IsNullOrWhiteSpace(clientBeta)
            ? [.. DefaultClaudeBeta.Split(',')]
            : [.. clientBeta.Split(',').Select(p => p.Trim()).Where(p => p.Length > 0)];

        if (!parts.Contains(ClaudeOAuthBeta))
        {
            int claudeCode = parts.IndexOf(ClaudeCodeBeta);
            parts.Insert(claudeCode >= 0 ? claudeCode + 1 : 0, ClaudeOAuthBeta);
        }
        if (countTokens && !parts.Contains(TokenCountingBeta))
        {
            parts.Add(TokenCountingBeta);
        }

        return string.Join(",", parts);
    }

    /// <summary>Removes only <c>responses=experimental</c>, keeping any other beta negotiation.</summary>
    internal static string? StripLegacyResponsesBeta(string? value)
    {
        if (string.IsNullOrEmpty(value) || !value.Contains("responses=experimental", StringComparison.OrdinalIgnoreCase))
        {
            return value;
        }

        return string.Join(", ", value
            .Split(',')
            .Select(p => p.Trim())
            .Where(p => p.Length > 0 && !p.Equals("responses=experimental", StringComparison.OrdinalIgnoreCase)));
    }

    /// <summary>
    /// The server's <c>normalizeOpenAIPassthroughOAuthBody</c>, minus what exists only for
    /// shared accounts. Returns the very same array when nothing needed changing, so a
    /// normal Codex request reaches the official API byte for byte.
    /// </summary>
    /// <remarks>
    /// A body that is not a JSON object is passed through: the official API will say what
    /// is wrong with it more precisely than this could.
    /// </remarks>
    internal static byte[] NormalizeCodexBody(byte[] body, bool compact)
    {
        if (body.Length == 0)
        {
            return body;
        }

        JsonObject root;
        try
        {
            if (JsonNode.Parse(body) is not JsonObject parsed)
            {
                return body;
            }
            root = parsed;
        }
        catch (JsonException)
        {
            return body;
        }

        bool changed = false;

        // normalizeOpenAIOAuthResponsesCompatibilityBody: prompt → input, drop commands,
        // drop the internal passthrough metadata on input items.
        if (root.ContainsKey("prompt"))
        {
            JsonNode? prompt = root["prompt"];
            if (prompt is not null && root["input"] is null)
            {
                root["input"] = prompt.DeepClone();
            }
            root.Remove("prompt");
            changed = true;
        }
        if (root.Remove("commands"))
        {
            changed = true;
        }

        // normalizeOpenAIResponsesReasoningMode (not for the gpt-6 astra family, where mode is its own parameter).
        if (root["reasoning"] is JsonObject reasoning &&
            reasoning["mode"] is JsonValue modeValue &&
            modeValue.TryGetValue(out string? mode) &&
            !IsAstraModel(root["model"]))
        {
            string? effort = reasoning["effort"] is JsonValue e && e.TryGetValue(out string? s) ? s : null;
            if (string.IsNullOrWhiteSpace(effort) && string.Equals(mode?.Trim(), "pro", StringComparison.OrdinalIgnoreCase))
            {
                reasoning["effort"] = "max";
            }
            reasoning.Remove("mode");
            if (reasoning.Count == 0)
            {
                root.Remove("reasoning");
            }
            changed = true;
        }

        foreach (string field in CodexUnsupportedFields)
        {
            changed |= root.Remove(field);
        }

        switch (root["input"])
        {
            case JsonValue text when text.TryGetValue(out string? value):
                root["input"] = string.IsNullOrWhiteSpace(value)
                    ? new JsonArray()
                    : new JsonArray(new JsonObject { ["type"] = "message", ["role"] = "user", ["content"] = value });
                changed = true;
                break;
            case JsonObject single:
                root["input"] = new JsonArray(single.DeepClone());
                changed = true;
                break;
            case JsonArray items:
                foreach (JsonNode? item in items)
                {
                    if (item is JsonObject itemObject && itemObject.Remove("internal_chat_message_metadata_passthrough"))
                    {
                        changed = true;
                    }
                }
                break;
        }

        if (compact)
        {
            changed |= root.Remove("store");
            changed |= root.Remove("stream");
        }
        else
        {
            if (!IsBool(root["store"], false))
            {
                root["store"] = false;
                changed = true;
            }
            if (!IsBool(root["stream"], true))
            {
                root["stream"] = true;
                changed = true;
            }
        }

        if (!changed)
        {
            return body;
        }

        using var buffer = new MemoryStream(body.Length);
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions { Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping }))
        {
            root.WriteTo(writer);
        }
        return buffer.ToArray();
    }

    private static bool IsBool(JsonNode? node, bool expected) =>
        node is JsonValue value && value.GetValueKind() is JsonValueKind.True or JsonValueKind.False &&
        value.GetValue<bool>() == expected;

    /// <summary>Server <c>isOpenAIGPT6AstraModel</c>.</summary>
    private static bool IsAstraModel(JsonNode? model)
    {
        string name = model is JsonValue v && v.TryGetValue(out string? s) ? s.Trim().ToLowerInvariant() : string.Empty;
        return name is "gpt-6" or "gpt-6-astra" || name.StartsWith("gpt-6-astra-", StringComparison.Ordinal);
    }

    private static void Forward(NameValueCollection from, string[] names, HttpRequestMessage to)
    {
        foreach (string name in names)
        {
            string[]? values = from.GetValues(name);
            if (values is null)
            {
                continue;
            }
            foreach (string value in values)
            {
                if (!string.IsNullOrWhiteSpace(value))
                {
                    to.Headers.TryAddWithoutValidation(name, value);
                }
            }
        }
    }
}
