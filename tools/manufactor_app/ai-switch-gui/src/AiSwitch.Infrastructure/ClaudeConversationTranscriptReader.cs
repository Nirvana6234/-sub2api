using System.Globalization;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

internal static class ClaudeConversationTranscriptReader
{
    private const long MaximumFileBytes = 32L * 1024 * 1024;
    private const int MaximumMessages = 2_000;
    private const int MaximumVisibleCharacters = 4 * 1024 * 1024;

    public static async Task<ConversationTranscript> ReadAsync(
        AppDataPaths paths,
        ConversationRecord conversation,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(paths);
        ArgumentNullException.ThrowIfNull(conversation);
        if (conversation.NativeClient != CliKind.ClaudeCode ||
            string.IsNullOrWhiteSpace(conversation.NativeSessionId) ||
            !TryNormalizePath(conversation.OriginalWorkingDirectory, out string expectedWorkingDirectory))
        {
            return ConversationTranscript.NotFound("Claude 会话标识或工作目录无效。");
        }

        if (!Directory.Exists(paths.ClaudeProjectsDirectory))
        {
            return ConversationTranscript.NotFound("未找到 Claude Code 官方历史目录。");
        }

        string expectedFileName = conversation.NativeSessionId + ".jsonl";
        bool matchingFileNameFound = false;
        foreach (string filePath in EnumerateFilesSafely(paths.ClaudeProjectsDirectory, expectedFileName))
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (IsSubagentPath(filePath))
            {
                continue;
            }

            matchingFileNameFound = true;
            FileInfo file = new(filePath);
            try
            {
                file.Refresh();
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                continue;
            }

            if (!file.Exists)
            {
                continue;
            }

            if (file.Length > MaximumFileBytes)
            {
                return new ConversationTranscript(
                    false,
                    Array.Empty<ConversationTranscriptMessage>(),
                    [$"Claude 官方历史超过安全读取上限（{MaximumFileBytes / 1024 / 1024} MB）。"]);
            }

            ClaudeReadResult result = await ReadFileAsync(
                    filePath,
                    conversation,
                    expectedWorkingDirectory,
                    cancellationToken)
                .ConfigureAwait(false);
            if (result.IdentityMatched)
            {
                return new ConversationTranscript(true, result.Messages, result.Warnings);
            }
        }

        return ConversationTranscript.NotFound(
            matchingFileNameFound
                ? "Claude 官方历史与该会话的工作目录不匹配。"
                : "未找到该 Claude Code 官方会话文件。");
    }

    private static async Task<ClaudeReadResult> ReadFileAsync(
        string filePath,
        ConversationRecord conversation,
        string expectedWorkingDirectory,
        CancellationToken cancellationToken)
    {
        var warnings = new List<string>();
        var messages = new List<MessageBuilder>();
        var messageIndexes = new Dictionary<string, int>(StringComparer.Ordinal);
        int malformedLines = 0;
        int totalCharacters = 0;
        int sequence = 0;
        bool identityMatched = false;
        bool limitReached = false;

        try
        {
            await using var stream = OpenSharedRead(filePath);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false) is { } line)
            {
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                JsonDocument document;
                try
                {
                    document = JsonDocument.Parse(line);
                }
                catch (JsonException)
                {
                    malformedLines++;
                    continue;
                }

                using (document)
                {
                    JsonElement root = document.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    string? sessionId = GetString(root, "sessionId");
                    string? workingDirectory = GetString(root, "cwd");
                    bool exactSession = string.Equals(
                        sessionId,
                        conversation.NativeSessionId,
                        StringComparison.OrdinalIgnoreCase);
                    bool exactWorkingDirectory = PathsEqual(workingDirectory, expectedWorkingDirectory);
                    if (exactSession && exactWorkingDirectory)
                    {
                        identityMatched = true;
                    }

                    if (!exactSession || !exactWorkingDirectory ||
                        GetBoolean(root, "isMeta") || GetBoolean(root, "isSidechain"))
                    {
                        continue;
                    }

                    string? type = GetString(root, "type");
                    if (type is not "user" and not "assistant" ||
                        !root.TryGetProperty("message", out JsonElement message) ||
                        message.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    ConversationTranscriptRole role = type == "user"
                        ? ConversationTranscriptRole.User
                        : ConversationTranscriptRole.Assistant;
                    if (!string.Equals(
                            GetString(message, "role"),
                            type,
                            StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }

                    if (role == ConversationTranscriptRole.User &&
                        (HasNonEmptyValue(root, "sourceToolAssistantUUID") ||
                         HasNonEmptyValue(root, "toolUseResult")))
                    {
                        continue;
                    }

                    if (!message.TryGetProperty("content", out JsonElement content))
                    {
                        continue;
                    }

                    string text = ExtractHumanText(content);
                    if (text.Length == 0)
                    {
                        continue;
                    }

                    string nativeId = role == ConversationTranscriptRole.Assistant
                        ? FirstNonEmpty(GetString(message, "id"), GetString(root, "uuid"))
                        : FirstNonEmpty(
                            GetString(root, "uuid"),
                            GetString(root, "promptId"),
                            GetString(message, "id"));
                    if (nativeId.Length == 0)
                    {
                        nativeId = sequence.ToString(CultureInfo.InvariantCulture);
                    }

                    string deduplicationKey = $"{role}:{nativeId}";
                    if (!messageIndexes.TryGetValue(deduplicationKey, out int index))
                    {
                        if (messages.Count >= MaximumMessages)
                        {
                            limitReached = true;
                            break;
                        }

                        index = messages.Count;
                        messageIndexes[deduplicationKey] = index;
                        messages.Add(new MessageBuilder(
                            $"claude:{role.ToString().ToLowerInvariant()}:{nativeId}",
                            role,
                            GetTimestamp(root, "timestamp") ?? conversation.CreatedAt,
                            sequence++));
                    }

                    MessageBuilder builder = messages[index];
                    if (builder.Contains(text))
                    {
                        continue;
                    }

                    if (totalCharacters + text.Length > MaximumVisibleCharacters)
                    {
                        limitReached = true;
                        break;
                    }

                    builder.Add(text);
                    totalCharacters += text.Length;
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            warnings.Add($"Claude 官方历史读取失败：{exception.Message}");
        }

        if (malformedLines > 0)
        {
            warnings.Add($"已跳过 {malformedLines} 行不完整的 Claude 历史记录。");
        }
        if (limitReached)
        {
            warnings.Add("Claude 历史过长，仅显示安全上限内的消息。");
        }

        ConversationTranscriptMessage[] visibleMessages = messages
            .Where(message => message.HasText)
            .OrderBy(message => message.Sequence)
            .Select(message => message.Build())
            .ToArray();
        return new ClaudeReadResult(identityMatched, visibleMessages, warnings);
    }

    private static string ExtractHumanText(JsonElement content)
    {
        if (content.ValueKind == JsonValueKind.String)
        {
            return SanitizeText(content.GetString());
        }
        if (content.ValueKind != JsonValueKind.Array)
        {
            return string.Empty;
        }

        var parts = new List<string>();
        foreach (JsonElement block in content.EnumerateArray())
        {
            if (block.ValueKind != JsonValueKind.Object ||
                !string.Equals(GetString(block, "type"), "text", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            string text = SanitizeText(GetString(block, "text"));
            if (text.Length > 0)
            {
                parts.Add(text);
            }
        }
        return string.Join(Environment.NewLine, parts);
    }

    private static string SanitizeText(string? text)
    {
        if (string.IsNullOrWhiteSpace(text))
        {
            return string.Empty;
        }

        var builder = new StringBuilder(text.Length);
        foreach (char character in text)
        {
            if (!char.IsControl(character) || character is '\r' or '\n' or '\t')
            {
                builder.Append(character);
            }
        }
        return builder.ToString().Trim();
    }

    private static IEnumerable<string> EnumerateFilesSafely(string root, string expectedFileName)
    {
        var pending = new Stack<string>();
        pending.Push(root);
        while (pending.Count > 0)
        {
            string directory = pending.Pop();
            string candidate = Path.Combine(directory, expectedFileName);
            if (File.Exists(candidate))
            {
                yield return candidate;
            }

            string[] children;
            try
            {
                children = Directory.GetDirectories(directory, "*", SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                continue;
            }

            foreach (string child in children)
            {
                try
                {
                    if ((File.GetAttributes(child) & FileAttributes.ReparsePoint) == 0)
                    {
                        pending.Push(child);
                    }
                }
                catch (Exception exception) when (IsRecoverableFileException(exception))
                {
                    // Continue with accessible sibling directories.
                }
            }
        }
    }

    private static bool IsSubagentPath(string filePath) => filePath.Contains(
        $"{Path.DirectorySeparatorChar}subagents{Path.DirectorySeparatorChar}",
        StringComparison.OrdinalIgnoreCase);

    private static FileStream OpenSharedRead(string path) => new(
        path,
        FileMode.Open,
        FileAccess.Read,
        FileShare.ReadWrite | FileShare.Delete,
        bufferSize: 16 * 1024,
        FileOptions.Asynchronous | FileOptions.SequentialScan);

    private static bool PathsEqual(string? value, string expected) =>
        TryNormalizePath(value, out string normalized) &&
        string.Equals(
            normalized,
            expected,
            OperatingSystem.IsWindows() ? StringComparison.OrdinalIgnoreCase : StringComparison.Ordinal);

    private static bool TryNormalizePath(string? value, out string normalized)
    {
        normalized = string.Empty;
        if (string.IsNullOrWhiteSpace(value) || !Path.IsPathFullyQualified(value))
        {
            return false;
        }

        try
        {
            normalized = PathIdentity.Normalize(value);
            return true;
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }

    private static string? GetString(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static bool GetBoolean(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind == JsonValueKind.True;

    private static bool HasNonEmptyValue(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind switch
        {
            JsonValueKind.String => !string.IsNullOrWhiteSpace(value.GetString()),
            JsonValueKind.Null or JsonValueKind.Undefined => false,
            _ => true,
        };

    private static DateTimeOffset? GetTimestamp(JsonElement element, string propertyName)
    {
        string? value = GetString(element, propertyName);
        return DateTimeOffset.TryParse(
            value,
            CultureInfo.InvariantCulture,
            DateTimeStyles.AllowWhiteSpaces,
            out DateTimeOffset timestamp)
            ? timestamp
            : null;
    }

    private static string FirstNonEmpty(params string?[] values) =>
        values.FirstOrDefault(value => !string.IsNullOrWhiteSpace(value))?.Trim() ?? string.Empty;

    private static bool IsRecoverableFileException(Exception exception) =>
        exception is IOException or UnauthorizedAccessException or System.Security.SecurityException;

    private sealed class MessageBuilder
    {
        private readonly List<string> _parts = [];
        private readonly HashSet<string> _uniqueParts = new(StringComparer.Ordinal);

        public MessageBuilder(
            string id,
            ConversationTranscriptRole role,
            DateTimeOffset timestamp,
            int sequence)
        {
            Id = id;
            Role = role;
            Timestamp = timestamp;
            Sequence = sequence;
        }

        public string Id { get; }

        public ConversationTranscriptRole Role { get; }

        public DateTimeOffset Timestamp { get; }

        public int Sequence { get; }

        public bool HasText => _parts.Count > 0;

        public bool Contains(string text) => _uniqueParts.Contains(text);

        public void Add(string text)
        {
            if (_uniqueParts.Add(text))
            {
                _parts.Add(text);
            }
        }

        public ConversationTranscriptMessage Build() => new(
            Id,
            Role,
            string.Join(Environment.NewLine, _parts),
            Timestamp);
    }

    private sealed record ClaudeReadResult(
        bool IdentityMatched,
        IReadOnlyList<ConversationTranscriptMessage> Messages,
        IReadOnlyList<string> Warnings);
}
