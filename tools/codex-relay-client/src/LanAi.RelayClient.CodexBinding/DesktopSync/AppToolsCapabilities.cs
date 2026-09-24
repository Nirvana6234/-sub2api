using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>Which of the four calls sync relies on the connected desktop app still accepts.</summary>
public sealed record AppToolsCapabilities(bool CanList, bool CanReadStatus, bool CanSend, bool CanNavigate)
{
    public static AppToolsCapabilities None { get; } = new(false, false, false, false);

    public bool Any => CanList || CanReadStatus || CanSend || CanNavigate;
}

/// <summary>
/// Checks a <c>tools/list</c> answer against the arguments this client sends.
/// </summary>
/// <remarks>
/// <para>
/// The interface is undocumented and ships inside the desktop app, so it can change on
/// any update. The check is structural rather than a hash of the schemas: the
/// description of <c>send_message_to_thread</c> embeds the current model list, and a
/// hash would fail every time a model is added while nothing we depend on changed.
/// </para>
/// <para>
/// A tool passes when it exists, every property we send is still declared, and every
/// property it requires is one we send. Anything else switches that one call off; the
/// rest keep working, which is how "read-only" degradation falls out.
/// </para>
/// </remarks>
public static class AppToolsContract
{
    internal const string ListThreads = "list_threads";
    internal const string ReadThread = "read_thread";
    internal const string SendMessage = "send_message_to_thread";
    internal const string Navigate = "navigate_to_codex_page";

    private static readonly Dictionary<string, string[]> SentProperties = new(StringComparer.Ordinal)
    {
        [ListThreads] = ["limit"],
        [ReadThread] = ["threadId", "turnLimit", "maxOutputCharsPerItem"],
        [SendMessage] = ["threadId", "prompt"],
        [Navigate] = ["threadId"],
    };

    /// <param name="result">The <c>result</c> member of the <c>tools/list</c> response.</param>
    public static AppToolsCapabilities Evaluate(JsonElement result)
    {
        if (result.ValueKind != JsonValueKind.Object ||
            !result.TryGetProperty("tools", out JsonElement tools) ||
            tools.ValueKind != JsonValueKind.Array)
        {
            return AppToolsCapabilities.None;
        }

        var accepted = new HashSet<string>(StringComparer.Ordinal);
        foreach (JsonElement tool in tools.EnumerateArray())
        {
            if (tool.ValueKind == JsonValueKind.Object &&
                tool.TryGetProperty("name", out JsonElement name) &&
                name.ValueKind == JsonValueKind.String &&
                SentProperties.TryGetValue(name.GetString()!, out string[]? sent) &&
                Accepts(tool, sent))
            {
                accepted.Add(name.GetString()!);
            }
        }

        return new AppToolsCapabilities(
            CanList: accepted.Contains(ListThreads),
            CanReadStatus: accepted.Contains(ReadThread),
            CanSend: accepted.Contains(SendMessage),
            CanNavigate: accepted.Contains(Navigate));
    }

    private static bool Accepts(JsonElement tool, string[] sent)
    {
        if (!tool.TryGetProperty("inputSchema", out JsonElement schema) || schema.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        if (!schema.TryGetProperty("properties", out JsonElement properties) || properties.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        foreach (string property in sent)
        {
            if (!properties.TryGetProperty(property, out _))
            {
                return false;
            }
        }

        if (schema.TryGetProperty("required", out JsonElement required) && required.ValueKind == JsonValueKind.Array)
        {
            foreach (JsonElement item in required.EnumerateArray())
            {
                if (item.ValueKind != JsonValueKind.String || !sent.Contains(item.GetString(), StringComparer.Ordinal))
                {
                    return false;
                }
            }
        }

        return true;
    }
}
