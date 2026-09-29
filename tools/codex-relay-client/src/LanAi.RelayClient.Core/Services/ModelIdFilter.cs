using System.Text.Json;
using System.Text.RegularExpressions;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Decides which entries of a group's model whitelist can be models Codex is offered.
/// </summary>
/// <remarks>
/// <para>
/// A group's list is whatever the operator typed or the accounts' mappings happen to name, and a
/// lot of it is not something a coding agent can talk to: Codex's own review model, internal
/// aliases, image and audio models, placeholders, notes written in Chinese. Offered in the
/// picker, each is a choice that can only fail — and because the default is taken from the list,
/// one of them could become the model a new conversation starts on.
/// </para>
/// <para>
/// Two kinds of pattern, both case-insensitive wildcards (<c>*</c>, <c>?</c>): <b>deny</b> patterns
/// remove entries, <b>allow</b> patterns — empty unless configured — keep only entries they match.
/// The built-in deny list was drawn from the names actually present in production groups; it can
/// be added to, or replaced, from <c>model-filter.json</c> next to the client's other files:
/// </para>
/// <code>
/// { "allow": ["gpt-*", "claude-*"], "deny": ["gpt-5.3-*"], "useDefaultDeny": true }
/// </code>
/// <para>
/// Anything with Chinese characters is always dropped, whatever the file says. An entry that is
/// itself a wildcard (<c>gpt-5*</c>) stays in the list the user reads — it says what the group
/// admits — but names no one model, so Codex's picker never gets it (see
/// <see cref="CodexGroupModels.IsConcrete"/>).
/// </para>
/// </remarks>
internal sealed class ModelIdFilter
{
    /// <summary>Not chat models, or not models a client should ever name. Matched case-insensitively.</summary>
    internal static readonly string[] BuiltInDeny =
    [
        "codex-auto-review",     // Codex's own review model
        "*-openai-compact",      // internal aliases for the compaction route
        "gpt-reserve",           // a placeholder
        "*image*",               // gpt-image-*, gemini-*-image-*, step-image-edit-*
        "seedream*",
        "dall-e*",
        "*audio*",               // gpt-4o-audio-preview
        "*realtime*",            // gpt-4o-realtime-preview
        "*tts*",
        "*transcribe*",
        "*whisper*",
        "*asr*",
        "*embedding*",
        "*moderation*",
    ];

    private readonly Regex[] _allow;
    private readonly Regex[] _deny;

    public ModelIdFilter(IEnumerable<string>? allow = null, IEnumerable<string>? deny = null, bool useDefaultDeny = true)
    {
        _allow = [.. (allow ?? []).Select(Compile)];
        _deny = [.. (useDefaultDeny ? BuiltInDeny : []).Concat(deny ?? []).Select(Compile)];
    }

    /// <summary>The filter in force. The built-in one until the application loads the user's file.</summary>
    public static ModelIdFilter Current { get; set; } = new();

    /// <summary>Whether <paramref name="model"/> may be offered.</summary>
    public bool IsSelectable(string? model)
    {
        if (string.IsNullOrWhiteSpace(model))
        {
            return false;
        }

        string id = model.Trim();
        if (id.Any(IsCjk))
        {
            return false;
        }

        if (_deny.Any(pattern => pattern.IsMatch(id)))
        {
            return false;
        }

        return _allow.Length == 0 || _allow.Any(pattern => pattern.IsMatch(id));
    }

    /// <summary>
    /// The filter described by <paramref name="json"/>; the built-in one when it is missing or is
    /// not what it should be. A file the user is editing by hand is bound to be wrong sometimes,
    /// and models disappearing from every list over a stray comma would be worse than ignoring it.
    /// </summary>
    public static ModelIdFilter Parse(string? json, Action<string>? warn = null)
    {
        if (string.IsNullOrWhiteSpace(json))
        {
            return new ModelIdFilter();
        }

        try
        {
            using JsonDocument doc = JsonDocument.Parse(json, new JsonDocumentOptions { AllowTrailingCommas = true, CommentHandling = JsonCommentHandling.Skip });
            JsonElement root = doc.RootElement;
            if (root.ValueKind != JsonValueKind.Object)
            {
                warn?.Invoke("model-filter.json 的顶层不是对象，已忽略");
                return new ModelIdFilter();
            }

            bool useDefaultDeny = !root.TryGetProperty("useDefaultDeny", out JsonElement flag) || flag.ValueKind != JsonValueKind.False;
            return new ModelIdFilter(Strings(root, "allow"), Strings(root, "deny"), useDefaultDeny);
        }
        catch (JsonException ex)
        {
            warn?.Invoke("model-filter.json 无法解析，已忽略：" + ex.Message);
            return new ModelIdFilter();
        }
    }

    /// <summary>Reads <paramref name="path"/> if it exists; the built-in filter otherwise.</summary>
    public static ModelIdFilter Load(string path, Action<string>? warn = null)
    {
        try
        {
            return File.Exists(path) ? Parse(File.ReadAllText(path), warn) : new ModelIdFilter();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            warn?.Invoke("读取 model-filter.json 失败，已忽略：" + ex.Message);
            return new ModelIdFilter();
        }
    }

    private static IEnumerable<string> Strings(JsonElement root, string name) =>
        root.TryGetProperty(name, out JsonElement list) && list.ValueKind == JsonValueKind.Array
            ? list.EnumerateArray().Where(e => e.ValueKind == JsonValueKind.String).Select(e => e.GetString()!).Where(s => s.Length > 0).ToArray()
            : [];

    private static Regex Compile(string wildcard) =>
        new(
            "^" + Regex.Escape(wildcard.Trim()).Replace("\\*", ".*", StringComparison.Ordinal).Replace("\\?", ".", StringComparison.Ordinal) + "$",
            RegexOptions.IgnoreCase | RegexOptions.CultureInvariant | RegexOptions.Singleline);

    /// <summary>Chinese characters, Chinese punctuation and full-width forms.</summary>
    internal static bool IsCjk(char c) =>
        c is >= '㐀' and <= '䶿'
        or >= '一' and <= '鿿'
        or >= '豈' and <= '﫿'
        or >= '　' and <= '〿'
        or >= '＀' and <= '￯';
}
