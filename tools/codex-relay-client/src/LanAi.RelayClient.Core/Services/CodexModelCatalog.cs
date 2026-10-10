using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Builds the model list Codex's picker shows for a group, and keeps a request inside it.
/// </summary>
/// <remarks>
/// <para>
/// <b>Entries are copies of one of Codex's own.</b> A catalog entry is not just a label: its
/// fields (which shell tool, how to truncate, which reasoning levels, ...) decide what Codex
/// sends for that model. Writing the schema out here would be a copy that goes stale with
/// every Codex release, so the template is the catalog the installed Codex ships
/// (<c>codex debug models --bundled</c>) and only what identifies the model is replaced.
/// </para>
/// <para>
/// The template is the entry with the fewest OpenAI-only behaviours — no code-mode-only
/// tools, no lite responses, no multi-agent — because a Claude model reached through the
/// bridge is served none of them. The same fields are forced off on the copy, so a Codex
/// release that makes every bundled model exotic still yields a usable entry.
/// </para>
/// </remarks>
internal static class CodexModelCatalog
{
    /// <summary>
    /// The catalog body Codex expects, or null when <paramref name="bundledCatalogJson"/>
    /// has nothing usable to copy from.
    /// </summary>
    public static string? Build(string bundledCatalogJson, CodexGroupModels models)
    {
        ArgumentNullException.ThrowIfNull(models);

        JsonObject? template;
        try
        {
            template = ChooseTemplate(JsonNode.Parse(bundledCatalogJson));
        }
        catch (JsonException)
        {
            return null;
        }

        if (template is null)
        {
            return null;
        }

        var entries = new JsonArray();
        for (int i = 0; i < models.Models.Count; i++)
        {
            entries.Add((JsonNode)Entry(template, models.Models[i], i + 1, models.ClaudeEffort));
        }

        return new JsonObject { ["models"] = entries }.ToJsonString();
    }

    private static JsonObject? ChooseTemplate(JsonNode? root)
    {
        if (root is not JsonObject obj || obj["models"] is not JsonArray array)
        {
            return null;
        }

        List<JsonObject> entries = [.. array.OfType<JsonObject>().Where(e => e["slug"] is JsonValue)];
        List<JsonObject> visible = [.. entries.Where(e => (string?)e["visibility"] == "list")];

        return visible.FirstOrDefault(IsPlain)
            ?? entries.FirstOrDefault(IsPlain)
            ?? visible.OrderBy(e => (int?)e["priority"] ?? int.MaxValue).FirstOrDefault()
            ?? entries.FirstOrDefault();
    }

    private static bool IsPlain(JsonObject entry) =>
        entry["tool_mode"] is null &&
        entry["multi_agent_version"] is null &&
        !(entry["use_responses_lite"] is JsonValue lite && lite.GetValueKind() == JsonValueKind.True);

    private static JsonObject Entry(JsonObject template, string slug, int priority, string? claudeEffort = null)
    {
        var copy = (JsonObject)template.DeepClone();
        copy["slug"] = slug;
        copy["display_name"] = DisplayName(slug);
        copy["description"] = "由当前分组提供";
        copy["priority"] = priority;
        copy["visibility"] = "list";

        // What belongs to the model the template was copied from, not to this one.
        copy["upgrade"] = null;
        copy["availability_nux"] = null;
        copy["model_specialty"] = null;
        copy["service_tiers"] = new JsonArray();
        copy["default_service_tier"] = null;
        copy["additional_speed_tiers"] = new JsonArray();

        // Behaviours only OpenAI's own models are served.
        copy["tool_mode"] = null;
        copy["use_responses_lite"] = false;
        copy["multi_agent_version"] = null;
        copy["multi_agent_reasoning_effort"] = null;

        // What Codex itself assumes for a model it has never heard of. A model reached
        // through the bridge behaved exactly like that until it was listed, and listing it
        // must not change what Codex sends for it — measured against the real binary by
        // diffing the /responses request with and without the entry. Left as copied, the
        // gpt template adds a reasoning effort (which the bridge turns into extended
        // thinking), a verbosity setting, a freeform apply_patch tool and a tool-search tool,
        // none of which a Claude model was ever sent, and shrinks nothing in exchange.
        //
        // Reasoning levels are the exception for an OpenAI-family model (gpt-*, o-series,
        // codex-*): those are served by a real OpenAI-compatible upstream that honours the
        // effort, and without the levels Codex / ChatGPT shows no effort picker and never
        // sends one — the server then logs every request as having no reasoning effort.
        // Only the three reasoning fields keep the template's values; everything else above
        // stays forced off.
        //
        // The other exception is a Claude model when the user has chosen a thinking strength on the
        // 共飞 Codex page (<paramref name="claudeEffort"/>): the picker is then offered, limited to
        // the levels the server's bridge turns into thinking, and starts at that choice. With none
        // chosen ("关闭") nothing is offered and nothing is sent, as before.
        if (!IsOpenAiFamily(slug))
        {
            copy["default_reasoning_level"] = null;
            copy["supported_reasoning_levels"] = new JsonArray();
            copy["supports_reasoning_effort_updates"] = false;

            if (claudeEffort is not null && template["supported_reasoning_levels"] is JsonArray offered)
            {
                // Codex's own list, in its own order and wording, minus what the bridge refuses.
                JsonArray levels = [.. offered
                    .OfType<JsonObject>()
                    .Where(level => level["effort"] is JsonValue effort &&
                                    CodexGroupModels.ClaudeEfforts.Contains((string?)effort ?? string.Empty, StringComparer.OrdinalIgnoreCase))
                    .Select(level => (JsonNode)level.DeepClone())];
                bool offersChoice = levels.OfType<JsonObject>()
                    .Any(level => string.Equals((string?)level["effort"], claudeEffort, StringComparison.OrdinalIgnoreCase));
                if (offersChoice)
                {
                    copy["supported_reasoning_levels"] = levels;
                    copy["default_reasoning_level"] = claudeEffort;
                    copy["supports_reasoning_effort_updates"] = template["supports_reasoning_effort_updates"]?.DeepClone() ?? false;
                }
            }
        }
        copy["supports_reasoning_summary_parameter"] = true;
        copy["default_reasoning_summary"] = "auto";
        copy["support_verbosity"] = false;
        copy["default_verbosity"] = null;
        copy["apply_patch_tool_type"] = null;
        copy["web_search_tool_type"] = "text";
        copy["supports_search_tool"] = false;
        copy["supports_experimental_context"] = false;
        copy["supports_image_detail_original"] = false;
        copy["experimental_supported_tools"] = new JsonArray();
        copy["include_skills_usage_instructions"] = false;
        copy["include_plugin_usage_instructions"] = false;
        copy["include_apps_usage_instructions"] = false;
        return copy;
    }

    /// <summary>Whether <paramref name="slug"/> is a model an OpenAI-compatible upstream serves with reasoning effort.</summary>
    internal static bool IsOpenAiFamily(string slug)
    {
        if (string.IsNullOrWhiteSpace(slug))
        {
            return false;
        }

        string id = slug.Trim();
        return id.StartsWith("gpt-", StringComparison.OrdinalIgnoreCase) ||
               id.StartsWith("codex-", StringComparison.OrdinalIgnoreCase) ||
               (id.Length >= 2 && (id[0] is 'o' or 'O') && char.IsDigit(id[1]));
    }

    /// <summary><c>claude-sonnet-5</c> as <c>Claude Sonnet 5</c>.</summary>
    internal static string DisplayName(string slug)
    {
        var words = slug.Split(['-', '_'], StringSplitOptions.RemoveEmptyEntries);
        return string.Join(' ', words.Select(w => char.ToUpperInvariant(w[0]) + w[1..]));
    }

    /// <summary>
    /// Replaces the request's top-level <c>model</c> when <paramref name="models"/> does not
    /// serve it.
    /// </summary>
    /// <returns>
    /// The rewritten body, or null when nothing needed changing — the body has no string
    /// <c>model</c>, the group serves it, or the body is not JSON. Null leaves the caller's
    /// bytes untouched, which is the right outcome for anything not understood.
    /// </returns>
    /// <remarks>
    /// Splices bytes rather than parsing into a tree: a Responses request carries the whole
    /// conversation, easily megabytes, and this runs on every turn. Only the one value
    /// changes, so the rest of the request reaches the server exactly as Codex sent it.
    /// </remarks>
    public static byte[]? SubstituteUnservedModel(byte[] body, CodexGroupModels models, out string? original)
    {
        original = null;
        if (!TryFindModel(body, out int start, out int end, out string? requested) ||
            requested is null ||
            models.Contains(requested))
        {
            return null;
        }

        byte[] replacement = Encoding.UTF8.GetBytes(JsonValue.Create(models.DefaultModel)!.ToJsonString());
        var result = new byte[body.Length - (end - start) + replacement.Length];
        Buffer.BlockCopy(body, 0, result, 0, start);
        Buffer.BlockCopy(replacement, 0, result, start, replacement.Length);
        Buffer.BlockCopy(body, end, result, start + replacement.Length, body.Length - end);
        original = requested;
        return result;
    }

    private static bool TryFindModel(byte[] body, out int start, out int end, out string? value)
    {
        start = end = 0;
        value = null;
        try
        {
            var reader = new Utf8JsonReader(body, new JsonReaderOptions { CommentHandling = JsonCommentHandling.Skip });
            int depth = 0;
            while (reader.Read())
            {
                switch (reader.TokenType)
                {
                    case JsonTokenType.StartObject:
                    case JsonTokenType.StartArray:
                        depth++;
                        break;
                    case JsonTokenType.EndObject:
                    case JsonTokenType.EndArray:
                        depth--;
                        break;
                    case JsonTokenType.PropertyName when depth == 1 && reader.ValueTextEquals("model"u8):
                        if (!reader.Read() || reader.TokenType != JsonTokenType.String)
                        {
                            return false;
                        }

                        start = checked((int)reader.TokenStartIndex);
                        end = checked((int)reader.BytesConsumed);
                        value = reader.GetString();
                        return true;
                }
            }
        }
        catch (JsonException)
        {
        }

        return false;
    }
}
