using System.Globalization;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

internal static class GeminiConversationTranscriptReader
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
        if (conversation.NativeClient != CliKind.GeminiCli ||
            string.IsNullOrWhiteSpace(conversation.NativeSessionId) ||
            !TryNormalizePath(conversation.OriginalWorkingDirectory, out string expectedWorkingDirectory))
        {
            return ConversationTranscript.NotFound("Gemini 会话标识或工作目录无效。");
        }

        if (!Directory.Exists(paths.GeminiProjectsDirectory))
        {
            return ConversationTranscript.NotFound("未找到 Gemini CLI 官方历史目录。");
        }

        bool projectDirectoryFound = false;
        foreach (string projectDirectory in EnumerateProjectDirectories(paths.GeminiProjectsDirectory))
        {
            cancellationToken.ThrowIfCancellationRequested();
            string? projectRoot = await ReadSmallTextFileAsync(
                    Path.Combine(projectDirectory, ".project_root"),
                    cancellationToken)
                .ConfigureAwait(false);
            if (!PathsEqual(projectRoot, expectedWorkingDirectory))
            {
                continue;
            }

            projectDirectoryFound = true;
            string chatsDirectory = Path.Combine(projectDirectory, "chats");
            foreach (string filePath in EnumerateSessionFilesSafely(chatsDirectory))
            {
                cancellationToken.ThrowIfCancellationRequested();
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
                    continue;
                }

                GeminiReadResult result = await ReadFileAsync(filePath, conversation, cancellationToken)
                    .ConfigureAwait(false);
                if (result.IdentityMatched)
                {
                    return new ConversationTranscript(true, result.Messages, result.Warnings);
                }
            }
        }

        return ConversationTranscript.NotFound(
            projectDirectoryFound
                ? "未找到该 Gemini CLI 官方会话文件。"
                : "Gemini 官方历史与该会话的工作目录不匹配。");
    }

    private static async Task<GeminiReadResult> ReadFileAsync(
        string filePath,
        ConversationRecord conversation,
        CancellationToken cancellationToken)
    {
        if (Path.GetExtension(filePath).Equals(".json", StringComparison.OrdinalIgnoreCase))
        {
            return await ReadLegacyDocumentAsync(filePath, conversation, cancellationToken)
                .ConfigureAwait(false);
        }

        var warnings = new List<string>();
        var currentMessages = new OrderedMessageStore(MaximumMessages);
        List<StoredMessage>? legacyMessages = null;
        string? sessionId = null;
        string? kind = null;
        DateTimeOffset fallbackTimestamp = conversation.CreatedAt;
        int malformedLines = 0;
        bool limitReached = false;
        int generatedId = 0;

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

                    ApplyMetadata(root, ref sessionId, ref kind, ref fallbackTimestamp);
                    if (root.TryGetProperty("$set", out JsonElement metadataUpdate) &&
                        metadataUpdate.ValueKind == JsonValueKind.Object)
                    {
                        ApplyMetadata(metadataUpdate, ref sessionId, ref kind, ref fallbackTimestamp);
                        if (metadataUpdate.TryGetProperty("messages", out JsonElement updatedMessages) &&
                            updatedMessages.ValueKind == JsonValueKind.Array)
                        {
                            legacyMessages = ReadLegacyMessages(
                                updatedMessages,
                                fallbackTimestamp,
                                ref generatedId,
                                out bool legacyLimitReached);
                            limitReached |= legacyLimitReached;
                        }
                        continue;
                    }

                    if (root.TryGetProperty("messages", out JsonElement messagesArray) &&
                        messagesArray.ValueKind == JsonValueKind.Array)
                    {
                        legacyMessages = ReadLegacyMessages(
                            messagesArray,
                            fallbackTimestamp,
                            ref generatedId,
                            out bool legacyLimitReached);
                        limitReached |= legacyLimitReached;
                    }

                    string? rewindId = GetString(root, "$rewindTo");
                    if (!string.IsNullOrWhiteSpace(rewindId))
                    {
                        currentMessages.RewindTo(rewindId);
                        continue;
                    }

                    string? id = GetString(root, "id");
                    string? type = GetString(root, "type");
                    if (string.IsNullOrWhiteSpace(id) || type is not "user" and not "gemini")
                    {
                        continue;
                    }

                    StoredMessage? message = CreateStoredMessage(root, id, type, fallbackTimestamp);
                    if (message is not null && !currentMessages.AddOrReplace(message))
                    {
                        limitReached = true;
                    }
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            warnings.Add($"Gemini 官方历史读取失败：{exception.Message}");
        }

        IReadOnlyList<StoredMessage> selected = legacyMessages is { Count: > 0 }
            ? legacyMessages
            : currentMessages.Messages;
        return BuildResult(
            sessionId,
            kind,
            selected,
            warnings,
            malformedLines,
            limitReached,
            conversation,
            cancellationToken);
    }

    private static async Task<GeminiReadResult> ReadLegacyDocumentAsync(
        string filePath,
        ConversationRecord conversation,
        CancellationToken cancellationToken)
    {
        var warnings = new List<string>();
        try
        {
            await using var stream = OpenSharedRead(filePath);
            using JsonDocument document = await JsonDocument.ParseAsync(
                    stream,
                    cancellationToken: cancellationToken)
                .ConfigureAwait(false);
            JsonElement root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object)
            {
                return new GeminiReadResult(false, Array.Empty<ConversationTranscriptMessage>(), warnings);
            }

            string? sessionId = null;
            string? kind = null;
            DateTimeOffset fallbackTimestamp = conversation.CreatedAt;
            ApplyMetadata(root, ref sessionId, ref kind, ref fallbackTimestamp);
            if (!root.TryGetProperty("messages", out JsonElement messages) ||
                messages.ValueKind != JsonValueKind.Array)
            {
                return BuildResult(
                    sessionId,
                    kind,
                    Array.Empty<StoredMessage>(),
                    warnings,
                    malformedLines: 0,
                    limitReached: false,
                    conversation,
                    cancellationToken);
            }

            int generatedId = 0;
            List<StoredMessage> legacyMessages = ReadLegacyMessages(
                messages,
                fallbackTimestamp,
                ref generatedId,
                out bool limitReached);
            return BuildResult(
                sessionId,
                kind,
                legacyMessages,
                warnings,
                malformedLines: 0,
                limitReached,
                conversation,
                cancellationToken);
        }
        catch (JsonException)
        {
            warnings.Add("Gemini 旧版历史文件不是有效 JSON，已安全跳过。");
            return new GeminiReadResult(false, Array.Empty<ConversationTranscriptMessage>(), warnings);
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            warnings.Add($"Gemini 官方历史读取失败：{exception.Message}");
            return new GeminiReadResult(false, Array.Empty<ConversationTranscriptMessage>(), warnings);
        }
    }

    private static GeminiReadResult BuildResult(
        string? sessionId,
        string? kind,
        IReadOnlyList<StoredMessage> selected,
        List<string> warnings,
        int malformedLines,
        bool limitReached,
        ConversationRecord conversation,
        CancellationToken cancellationToken)
    {
        bool identityMatched = string.Equals(
                                   sessionId,
                                   conversation.NativeSessionId,
                                   StringComparison.OrdinalIgnoreCase) &&
                               (string.IsNullOrWhiteSpace(kind) ||
                                string.Equals(kind, "main", StringComparison.OrdinalIgnoreCase));
        if (!identityMatched)
        {
            return new GeminiReadResult(false, Array.Empty<ConversationTranscriptMessage>(), warnings);
        }

        var visible = new List<ConversationTranscriptMessage>(Math.Min(selected.Count, MaximumMessages));
        var seen = new HashSet<string>(StringComparer.Ordinal);
        int totalCharacters = 0;
        foreach (StoredMessage message in selected)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (message.Text.Length == 0)
            {
                continue;
            }

            string deduplicationKey = $"{message.Role}:{message.Id}:{message.Text}";
            if (!seen.Add(deduplicationKey))
            {
                continue;
            }
            if (visible.Count >= MaximumMessages ||
                totalCharacters + message.Text.Length > MaximumVisibleCharacters)
            {
                limitReached = true;
                break;
            }

            visible.Add(new ConversationTranscriptMessage(
                $"gemini:{message.Role.ToString().ToLowerInvariant()}:{message.Id}",
                message.Role,
                message.Text,
                message.Timestamp));
            totalCharacters += message.Text.Length;
        }

        if (malformedLines > 0)
        {
            warnings.Add($"已跳过 {malformedLines} 行不完整的 Gemini 历史记录。");
        }
        if (limitReached)
        {
            warnings.Add("Gemini 历史过长，仅显示安全上限内的消息。");
        }

        return new GeminiReadResult(true, visible, warnings);
    }

    private static List<StoredMessage> ReadLegacyMessages(
        JsonElement messages,
        DateTimeOffset fallbackTimestamp,
        ref int generatedId,
        out bool limitReached)
    {
        var result = new OrderedMessageStore(MaximumMessages);
        limitReached = false;
        foreach (JsonElement message in messages.EnumerateArray())
        {
            if (message.ValueKind != JsonValueKind.Object)
            {
                continue;
            }

            string? type = GetString(message, "type");
            if (type is not "user" and not "gemini")
            {
                continue;
            }

            string id = GetString(message, "id") ?? $"legacy-{generatedId++}";
            StoredMessage? stored = CreateStoredMessage(message, id, type, fallbackTimestamp);
            if (stored is not null && !result.AddOrReplace(stored))
            {
                limitReached = true;
                break;
            }
        }
        return result.Messages.ToList();
    }

    private static StoredMessage? CreateStoredMessage(
        JsonElement message,
        string id,
        string type,
        DateTimeOffset fallbackTimestamp)
    {
        string text = string.Empty;
        if (message.TryGetProperty("displayContent", out JsonElement displayContent))
        {
            text = ExtractVisibleText(displayContent);
        }
        if (text.Length == 0 && message.TryGetProperty("content", out JsonElement content))
        {
            text = ExtractVisibleText(content);
        }
        if (text.Length == 0)
        {
            return null;
        }

        return new StoredMessage(
            id,
            type == "user" ? ConversationTranscriptRole.User : ConversationTranscriptRole.Assistant,
            text,
            GetTimestamp(message, "timestamp") ?? fallbackTimestamp);
    }

    private static string ExtractVisibleText(JsonElement value)
    {
        switch (value.ValueKind)
        {
            case JsonValueKind.String:
                return SanitizeText(value.GetString());
            case JsonValueKind.Array:
            {
                var parts = new List<string>();
                foreach (JsonElement item in value.EnumerateArray())
                {
                    string text = ExtractVisibleText(item);
                    if (text.Length > 0)
                    {
                        parts.Add(text);
                    }
                }
                return string.Join(Environment.NewLine, parts);
            }
            case JsonValueKind.Object:
            {
                if (GetBoolean(value, "thought") ||
                    GetString(value, "type") is "thought" or "thinking" ||
                    value.TryGetProperty("functionCall", out _) ||
                    value.TryGetProperty("functionResponse", out _) ||
                    value.TryGetProperty("executableCode", out _) ||
                    value.TryGetProperty("codeExecutionResult", out _))
                {
                    return string.Empty;
                }

                string? text = GetString(value, "text");
                if (!string.IsNullOrWhiteSpace(text))
                {
                    return SanitizeText(text);
                }
                if (value.TryGetProperty("parts", out JsonElement parts))
                {
                    return ExtractVisibleText(parts);
                }
                return string.Empty;
            }
            default:
                return string.Empty;
        }
    }

    private static void ApplyMetadata(
        JsonElement metadata,
        ref string? sessionId,
        ref string? kind,
        ref DateTimeOffset fallbackTimestamp)
    {
        sessionId = GetString(metadata, "sessionId") ?? sessionId;
        kind = GetString(metadata, "kind") ?? kind;
        fallbackTimestamp = GetTimestamp(metadata, "startTime") ??
                            GetTimestamp(metadata, "lastUpdated") ??
                            fallbackTimestamp;
    }

    private static IEnumerable<string> EnumerateProjectDirectories(string root)
    {
        string[] directories;
        try
        {
            directories = Directory.GetDirectories(root, "*", SearchOption.TopDirectoryOnly);
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            yield break;
        }

        foreach (string directory in directories)
        {
            bool include;
            try
            {
                include = (File.GetAttributes(directory) & FileAttributes.ReparsePoint) == 0;
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                // Continue with accessible project directories.
                continue;
            }

            if (include)
            {
                yield return directory;
            }
        }
    }

    private static IEnumerable<string> EnumerateSessionFilesSafely(string chatsDirectory)
    {
        if (!Directory.Exists(chatsDirectory))
        {
            yield break;
        }

        string[] files;
        try
        {
            files = Directory.GetFiles(chatsDirectory, "session-*.*", SearchOption.TopDirectoryOnly);
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            yield break;
        }

        foreach (string file in files)
        {
            string extension = Path.GetExtension(file);
            if (extension.Equals(".jsonl", StringComparison.OrdinalIgnoreCase) ||
                extension.Equals(".json", StringComparison.OrdinalIgnoreCase))
            {
                yield return file;
            }
        }
    }

    private static async Task<string?> ReadSmallTextFileAsync(
        string path,
        CancellationToken cancellationToken)
    {
        if (!File.Exists(path))
        {
            return null;
        }

        try
        {
            await using var stream = OpenSharedRead(path);
            if (stream.Length > 32 * 1024)
            {
                return null;
            }
            using var reader = new StreamReader(stream, Encoding.UTF8, true, 1024, false);
            return (await reader.ReadToEndAsync(cancellationToken).ConfigureAwait(false)).Trim();
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return null;
        }
    }

    private static FileStream OpenSharedRead(string path) => new(
        path,
        FileMode.Open,
        FileAccess.Read,
        FileShare.ReadWrite | FileShare.Delete,
        bufferSize: 16 * 1024,
        FileOptions.Asynchronous | FileOptions.SequentialScan);

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

    private static DateTimeOffset? GetTimestamp(JsonElement element, string propertyName)
    {
        if (!element.TryGetProperty(propertyName, out JsonElement value))
        {
            return null;
        }
        if (value.ValueKind == JsonValueKind.String && DateTimeOffset.TryParse(
                value.GetString(),
                CultureInfo.InvariantCulture,
                DateTimeStyles.AllowWhiteSpaces,
                out DateTimeOffset timestamp))
        {
            return timestamp;
        }
        return null;
    }

    private static bool IsRecoverableFileException(Exception exception) =>
        exception is IOException or UnauthorizedAccessException or System.Security.SecurityException;

    private sealed class OrderedMessageStore
    {
        private readonly int _maximumMessages;
        private readonly List<StoredMessage> _messages = [];
        private readonly Dictionary<string, int> _indexes = new(StringComparer.Ordinal);

        public OrderedMessageStore(int maximumMessages) => _maximumMessages = maximumMessages;

        public IReadOnlyList<StoredMessage> Messages => _messages;

        public bool AddOrReplace(StoredMessage message)
        {
            if (_indexes.TryGetValue(message.Id, out int index))
            {
                _messages[index] = message;
                return true;
            }
            if (_messages.Count >= _maximumMessages)
            {
                return false;
            }

            _indexes[message.Id] = _messages.Count;
            _messages.Add(message);
            return true;
        }

        public void RewindTo(string id)
        {
            if (!_indexes.TryGetValue(id, out int index))
            {
                _messages.Clear();
                _indexes.Clear();
                return;
            }

            for (int current = _messages.Count - 1; current >= index; current--)
            {
                _indexes.Remove(_messages[current].Id);
                _messages.RemoveAt(current);
            }
        }
    }

    private sealed record StoredMessage(
        string Id,
        ConversationTranscriptRole Role,
        string Text,
        DateTimeOffset Timestamp);

    private sealed record GeminiReadResult(
        bool IdentityMatched,
        IReadOnlyList<ConversationTranscriptMessage> Messages,
        IReadOnlyList<string> Warnings);
}
