using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>What projecting a line needs to know about the lines before it.</summary>
public sealed class ProjectionState(string? openTurnId, string? cwd)
{
    /// <summary>The turn started and not yet ended. Tool-call lines carry no turn id of their own.</summary>
    public string? OpenTurnId { get; set; } = openTurnId;

    public string? Cwd { get; set; } = cwd;

    /// <summary>
    /// <c>shell_command</c> calls awaiting their output, by call id. Codex never writes a
    /// completed item for them (0 of 9,432 measured, current version included), so the
    /// command's result has to be assembled from its call and its output.
    /// </summary>
    internal Dictionary<string, (string? TurnId, string? Command)> PendingShellCalls { get; } = new(StringComparer.Ordinal);
}

/// <summary>
/// Turns one rollout line into what the phone shows, or nothing.
/// </summary>
/// <remarks>
/// <para>
/// A rollout holds most things twice: <c>event_msg</c>/<c>item_completed</c> for the
/// desktop UI and <c>response_item</c> for the model (with encrypted reasoning). The UI
/// copy is the one projected. The single exception is the tool-call line, which the
/// rollout writes the moment a command starts — the completed item only arrives when it
/// finishes, so without it the phone could not show what is running.
/// </para>
/// <para>
/// Every field name here was read off real rollouts from Codex desktop 26.903, not from
/// a schema; none is published.
/// </para>
/// </remarks>
public static class RolloutProjector
{
    public const int OutputPreviewLines = 20;
    public const int OutputPreviewChars = 2 * 1024;

    private static readonly Regex CodeModeCommand =
        new(@"\b(?:cmd|command)\s*:\s*(""(?:[^""\\]|\\.)*"")", RegexOptions.CultureInvariant);

    private static readonly Regex ShellExitCode = new(@"^Exit code: (-?\d+)", RegexOptions.CultureInvariant);

    internal const string ShellOutputMarker = "Output:\n";

    private static readonly Regex DelegationSource =
        new(@"<source_thread_id>(.*?)</source_thread_id>", RegexOptions.CultureInvariant | RegexOptions.Singleline);

    private static readonly Regex DelegationInput =
        new(@"<input>(.*)</input>", RegexOptions.CultureInvariant | RegexOptions.Singleline);

    /// <summary>Projects one line. <paramref name="state"/> is advanced for the next line.</summary>
    public static SyncItem? Project(JsonElement line, long seq, ProjectionState state)
    {
        string? type = Str(line, "type");
        if (!line.TryGetProperty("payload", out JsonElement payload) || payload.ValueKind != JsonValueKind.Object)
        {
            return null;
        }

        switch (type)
        {
            case "session_meta":
            case "turn_context":
                if (Str(payload, "cwd") is string cwd)
                {
                    state.Cwd = CodexStateDatabase.StripLongPathPrefix(cwd);
                }

                return null;

            case "event_msg":
                return ProjectEvent(payload, seq, state);

            case "response_item":
                return ProjectToolCall(payload, seq, state);

            default:
                return null;
        }
    }

    private static SyncItem? ProjectEvent(JsonElement payload, long seq, ProjectionState state)
    {
        string? turnId = Str(payload, "turn_id");
        switch (Str(payload, "type"))
        {
            case "task_started" when turnId is not null:
                state.OpenTurnId = turnId;
                return new SyncItem(seq, turnId, $"turn-start:{turnId}", SyncItemKind.TurnStarted);

            case "task_complete" when turnId is not null:
                CloseTurn(turnId, state);
                string? error = ReadError(payload);
                return new SyncItem(seq, turnId, $"turn-end:{turnId}", SyncItemKind.TurnEnded)
                {
                    Outcome = error is null ? TurnOutcome.Completed : TurnOutcome.Failed,
                    Text = error,
                    DurationMs = Long(payload, "duration_ms"),
                };

            case "turn_aborted" when turnId is not null:
                CloseTurn(turnId, state);
                return new SyncItem(seq, turnId, $"turn-end:{turnId}", SyncItemKind.TurnEnded)
                {
                    Outcome = TurnOutcome.Aborted,
                    Text = Str(payload, "reason"),
                    DurationMs = Long(payload, "duration_ms"),
                };

            case "item_completed" when payload.TryGetProperty("item", out JsonElement item) && item.ValueKind == JsonValueKind.Object:
                long? duration = Long(payload, "completed_at_ms") - Long(payload, "started_at_ms");
                return ProjectItem(item, seq, turnId, duration, state);

            default:
                return null;
        }
    }

    private static void CloseTurn(string turnId, ProjectionState state)
    {
        if (state.OpenTurnId == turnId)
        {
            state.OpenTurnId = null;
        }
    }

    private static SyncItem? ProjectItem(JsonElement item, long seq, string? turnId, long? duration, ProjectionState state)
    {
        string itemType = Str(item, "type") ?? "?";
        string itemId = Str(item, "id") ?? $"line:{seq}";
        var entry = new SyncItem(seq, turnId, itemId, SyncItemKind.Unknown);

        switch (itemType)
        {
            case "UserMessage":
                return entry with
                {
                    Kind = SyncItemKind.User,
                    Origin = UserMessageOrigin.Desktop,
                    Text = JoinText(item, "content", "text", "text"),
                    ImageCount = CountContent(item, "local_image") + CountContent(item, "image"),
                };

            case "FunctionCallOutput" when Str(item, "name") == "send_message_to_thread" && ReadOutput(item) is string output &&
                                           output.TrimStart().StartsWith("<codex_delegation>", StringComparison.Ordinal):
                return entry with
                {
                    Kind = SyncItemKind.User,
                    Origin = UserMessageOrigin.Delegated,
                    // Kept verbatim: nothing observed shows the wrapper escaping the text, and
                    // decoding entities would alter what the user typed.
                    Text = DelegationInput.Match(output) is { Success: true } input ? input.Groups[1].Value : output,
                    SourceThreadId = DelegationSource.Match(output) is { Success: true } source ? source.Groups[1].Value.Trim() : null,
                };

            case "FunctionCallOutput":
                return entry with { Kind = SyncItemKind.Tool, Text = Str(item, "name") ?? "tool" };

            case "AgentMessage":
                string? phase = Str(item, "phase");
                return entry with
                {
                    Kind = phase == "final_answer" ? SyncItemKind.Reply : SyncItemKind.Progress,
                    PhaseMissing = phase is null,
                    Text = JoinText(item, "content", "Text", "text"),
                };

            case "Reasoning":
                string? summary = JoinStrings(item, "summary_text");
                return string.IsNullOrWhiteSpace(summary) ? null : entry with { Kind = SyncItemKind.Thinking, Text = summary };

            case "CommandExecution":
                (string? preview, bool truncated) = Preview(Str(item, "aggregated_output") ?? Str(item, "stdout") + Str(item, "stderr"));
                return entry with
                {
                    Kind = SyncItemKind.Command,
                    Command = DisplayCommand(item),
                    Status = Str(item, "status"),
                    ExitCode = item.TryGetProperty("exit_code", out JsonElement exit) && exit.TryGetInt32(out int code) ? code : null,
                    DurationMs = duration,
                    OutputPreview = preview,
                    OutputTruncated = truncated,
                };

            case "FileChange":
                return entry with { Kind = SyncItemKind.FileChange, Status = Str(item, "status"), Files = ReadFileChanges(item, state.Cwd) };

            case "McpToolCall":
                return entry with { Kind = SyncItemKind.Tool, Text = $"{Str(item, "server")}.{Str(item, "tool")}", Status = Str(item, "status") };

            case "WebSearch":
                return entry with { Kind = SyncItemKind.Tool, Text = $"搜索：{Str(item, "query")}" };

            case "Extension":
                string? query = Str(item, "query") ?? (item.TryGetProperty("action", out JsonElement action) ? JoinStrings(action, "queries") : null);
                return entry with { Kind = SyncItemKind.Tool, Text = $"{Str(item, "kind")}：{query}" };

            case "SubAgentActivity":
                return entry with { Kind = SyncItemKind.Tool, Text = $"子代理 {Str(item, "kind")}：{Str(item, "agent_path")}" };

            case "ImageView":
                return entry with { Kind = SyncItemKind.Image, ImageCount = 1 };

            case "ContextCompaction":
                return entry with { Kind = SyncItemKind.Notice, Text = "上下文已压缩" };

            default:
                return entry with { Text = itemType };
        }
    }

    /// <summary>
    /// The "running: …" card, from the line written as a command starts; and, for
    /// <c>shell_command</c>, the finished command, assembled from its output line.
    /// </summary>
    /// <remarks>
    /// <c>exec_command</c> finishes as a <c>CommandExecution</c> item and needs nothing
    /// here. <c>shell_command</c> — still used by the current version, and the only command
    /// tool in older rollouts — never does, so without this its results would never show.
    /// </remarks>
    private static SyncItem? ProjectToolCall(JsonElement payload, long seq, ProjectionState state)
    {
        if (Str(payload, "call_id") is not string callId)
        {
            return null;
        }

        string? type = Str(payload, "type");
        if (type == "function_call_output")
        {
            return state.PendingShellCalls.Remove(callId, out var call) ? ShellResult(payload, seq, callId, call) : null;
        }

        if (state.OpenTurnId is null)
        {
            return null;
        }

        string? name = Str(payload, "name");
        string? command = (type, name) switch
        {
            ("function_call", "exec_command") => ReadArgument(Str(payload, "arguments"), "cmd"),
            ("function_call", "shell_command") => ReadArgument(Str(payload, "arguments"), "command"),

            // Conversations in code mode run commands from a JavaScript cell; the command
            // is a string literal inside it. Unreadable means "some script", not nothing.
            ("custom_tool_call", "exec") => ReadCodeModeCommand(Str(payload, "input")) ?? string.Empty,
            _ => null,
        };

        if (command is null)
        {
            return null;
        }

        string? shown = command.Length == 0 ? null : command;
        if (name == "shell_command")
        {
            state.PendingShellCalls[callId] = (state.OpenTurnId, shown);
        }

        return new SyncItem(seq, state.OpenTurnId, $"running:{callId}", SyncItemKind.Running) { Command = shown };
    }

    /// <summary>
    /// A <c>shell_command</c> output: <c>Exit code: N</c>, <c>Wall time: …</c>, <c>Output:</c>
    /// and the text; or <c>execution error: …</c>; or <c>exec command rejected by user</c>.
    /// These are the three first lines seen across 9,432 real calls.
    /// </summary>
    private static SyncItem ShellResult(JsonElement payload, long seq, string callId, (string? TurnId, string? Command) call)
    {
        string output = ReadOutput(payload) ?? string.Empty;
        Match exit = ShellExitCode.Match(output);
        (string? preview, bool truncated) = Preview(ShellOutputBody(output));

        return new SyncItem(seq, call.TurnId, callId, SyncItemKind.Command)
        {
            Command = call.Command,
            ExitCode = exit.Success ? int.Parse(exit.Groups[1].Value, System.Globalization.CultureInfo.InvariantCulture) : null,
            Status = exit.Success ? "completed"
                : output.StartsWith("exec command rejected", StringComparison.Ordinal) ? "declined"
                : "failed",
            OutputPreview = preview,
            OutputTruncated = truncated,
        };
    }

    /// <summary>The text after <see cref="ShellOutputMarker"/> in a <c>shell_command</c> output, or the whole of it.</summary>
    internal static string ShellOutputBody(string output)
    {
        int marker = output.IndexOf(ShellOutputMarker, StringComparison.Ordinal);
        return ShellExitCode.IsMatch(output) && marker >= 0 ? output[(marker + ShellOutputMarker.Length)..] : output;
    }

    private static string? ReadArgument(string? arguments, string name)
    {
        if (arguments is null)
        {
            return null;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(arguments);
            return Str(document.RootElement, name) ?? string.Empty;
        }
        catch (JsonException)
        {
            return string.Empty;
        }
    }

    private static string? ReadCodeModeCommand(string? input)
    {
        if (input is null || CodeModeCommand.Match(input) is not { Success: true } match)
        {
            return null;
        }

        try
        {
            // A JS double-quoted literal without template tricks is also a JSON string.
            using JsonDocument literal = JsonDocument.Parse(match.Groups[1].Value);
            return literal.RootElement.GetString();
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <summary>
    /// What the user would recognise: the parsed command, else the last argument of a
    /// <c>pwsh -Command …</c> wrapper, else the whole argv.
    /// </summary>
    internal static string? DisplayCommand(JsonElement item)
    {
        if (item.TryGetProperty("parsed_cmd", out JsonElement parsed) && parsed.ValueKind == JsonValueKind.Array)
        {
            string[] commands = parsed.EnumerateArray().Select(p => Str(p, "cmd")).OfType<string>().ToArray();
            if (commands.Length > 0)
            {
                return string.Join(" ; ", commands);
            }
        }

        if (item.TryGetProperty("command", out JsonElement argv) && argv.ValueKind == JsonValueKind.Array)
        {
            string[] parts = argv.EnumerateArray().Where(a => a.ValueKind == JsonValueKind.String).Select(a => a.GetString()!).ToArray();
            int wrapper = Array.FindIndex(parts, p => p is "-Command" or "-c" or "/c" or "/C");
            return wrapper >= 0 && wrapper == parts.Length - 2 ? parts[^1] : string.Join(' ', parts);
        }

        return null;
    }

    internal static (string? Preview, bool Truncated) Preview(string? output)
    {
        if (string.IsNullOrEmpty(output))
        {
            return (null, false);
        }

        string trimmed = output.TrimEnd();
        string[] lines = trimmed.Split('\n');
        bool truncated = false;
        if (lines.Length > OutputPreviewLines)
        {
            trimmed = string.Join('\n', lines[^OutputPreviewLines..]);
            truncated = true;
        }

        if (trimmed.Length > OutputPreviewChars)
        {
            trimmed = trimmed[^OutputPreviewChars..];
            truncated = true;
        }

        return (trimmed, truncated);
    }

    private static IReadOnlyList<SyncFileChange> ReadFileChanges(JsonElement item, string? cwd)
    {
        var files = new List<SyncFileChange>();
        if (!item.TryGetProperty("changes", out JsonElement changes) || changes.ValueKind != JsonValueKind.Object)
        {
            return files;
        }

        foreach (JsonProperty change in changes.EnumerateObject())
        {
            (int added, int removed) = CountDiff(Str(change.Value, "unified_diff"));
            files.Add(new SyncFileChange(RelativeTo(cwd, change.Name), Str(change.Value, "type") ?? "update", added, removed));
        }

        return files;
    }

    internal static (int Added, int Removed) CountDiff(string? diff)
    {
        if (diff is null)
        {
            return (0, 0);
        }

        int added = 0;
        int removed = 0;
        foreach (string line in diff.Split('\n'))
        {
            if (line.StartsWith("+++", StringComparison.Ordinal) || line.StartsWith("---", StringComparison.Ordinal))
            {
                continue;
            }

            if (line.StartsWith('+'))
            {
                added++;
            }
            else if (line.StartsWith('-'))
            {
                removed++;
            }
        }

        return (added, removed);
    }

    private static string RelativeTo(string? cwd, string path)
    {
        if (string.IsNullOrEmpty(cwd))
        {
            return path;
        }

        string root = cwd.TrimEnd('\\', '/') + Path.DirectorySeparatorChar;
        return path.StartsWith(root, StringComparison.OrdinalIgnoreCase) ? path[root.Length..] : path;
    }

    /// <summary>The error of a failed turn; its message is often itself JSON with a <c>detail</c>.</summary>
    private static string? ReadError(JsonElement payload)
    {
        if (!payload.TryGetProperty("error", out JsonElement error) || error.ValueKind != JsonValueKind.Object)
        {
            return null;
        }

        string message = Str(error, "message") ?? "error";
        try
        {
            using JsonDocument inner = JsonDocument.Parse(message);
            return Str(inner.RootElement, "detail") ?? message;
        }
        catch (JsonException)
        {
            return message;
        }
    }

    private static string? ReadOutput(JsonElement item) =>
        item.TryGetProperty("output", out JsonElement output) ? output.ValueKind switch
        {
            JsonValueKind.String => output.GetString(),
            JsonValueKind.Object => Str(output, "text"),
            _ => null,
        } : null;

    private static string? JoinText(JsonElement item, string array, string contentType, string field)
    {
        if (!item.TryGetProperty(array, out JsonElement content) || content.ValueKind != JsonValueKind.Array)
        {
            return null;
        }

        var text = new StringBuilder();
        foreach (JsonElement part in content.EnumerateArray())
        {
            if (Str(part, "type") == contentType && Str(part, field) is string value)
            {
                text.Append(value);
            }
        }

        return text.Length == 0 ? null : text.ToString();
    }

    private static int CountContent(JsonElement item, string contentType) =>
        item.TryGetProperty("content", out JsonElement content) && content.ValueKind == JsonValueKind.Array
            ? content.EnumerateArray().Count(part => Str(part, "type") == contentType)
            : 0;

    private static string? JoinStrings(JsonElement element, string property) =>
        element.TryGetProperty(property, out JsonElement array) && array.ValueKind == JsonValueKind.Array
            ? string.Join('\n', array.EnumerateArray().Where(v => v.ValueKind == JsonValueKind.String).Select(v => v.GetString()))
            : null;

    internal static string? Str(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(property, out JsonElement value) &&
        value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static long? Long(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(property, out JsonElement value) &&
        value.TryGetInt64(out long number)
            ? number
            : null;
}
