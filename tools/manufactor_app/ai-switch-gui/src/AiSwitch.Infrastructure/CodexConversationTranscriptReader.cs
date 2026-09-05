using System.Globalization;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Reads only the user-visible message projection from one Codex rollout.
/// Model reasoning, developer instructions and raw tool payloads are never
/// copied into the returned transcript.
/// </summary>
internal static class CodexConversationTranscriptReader
{
    private const int MaxMetadataLines = 64;
    private const int MaxJsonLineCharacters = 4 * 1024 * 1024;
    private const int MaxMessageCharacters = 64 * 1024;
    private const int MaxMessages = 1_000;
    private const int MaxTotalCharacters = 2 * 1024 * 1024;
    private const string TruncationMarker = "\n\n…（此条消息过长，已截断）";

    public static async Task<ConversationTranscript> ReadAsync(
        AppDataPaths paths,
        ConversationRecord conversation,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(paths);
        ArgumentNullException.ThrowIfNull(conversation);

        if (!Directory.Exists(paths.CodexSessionsDirectory))
        {
            return ConversationTranscript.NotFound("未找到 Codex 会话目录。");
        }

        if (!TryNormalizeWorkingDirectory(conversation.OriginalWorkingDirectory, out string expectedWorkingDirectory))
        {
            return ConversationTranscript.NotFound("Codex 会话记录中的项目目录无效。");
        }

        var searchWarnings = new List<string>();
        IReadOnlyList<RolloutFile> rolloutFiles = EnumerateRolloutFiles(
            paths.CodexSessionsDirectory,
            searchWarnings,
            cancellationToken);

        foreach (RolloutFile rolloutFile in rolloutFiles)
        {
            cancellationToken.ThrowIfCancellationRequested();

            CandidateReadResult result = await ReadCandidateAsync(
                rolloutFile.Path,
                conversation,
                expectedWorkingDirectory,
                cancellationToken).ConfigureAwait(false);

            if (!result.MetadataMatched)
            {
                if (result.Warning is { Length: > 0 })
                {
                    AddWarningOnce(searchWarnings, result.Warning);
                }

                continue;
            }

            var warnings = new List<string>(searchWarnings);
            foreach (string warning in result.Warnings)
            {
                AddWarningOnce(warnings, warning);
            }

            if (result.Messages.Count == 0)
            {
                AddWarningOnce(warnings, "Codex 会话文件存在，但没有可安全展示的用户或助手消息。");
            }

            return new ConversationTranscript(true, result.Messages, warnings);
        }

        AddWarningOnce(searchWarnings, "未找到与会话 ID 和项目目录同时匹配的 Codex 历史文件。");
        return new ConversationTranscript(
            false,
            Array.Empty<ConversationTranscriptMessage>(),
            searchWarnings);
    }

    private static async Task<CandidateReadResult> ReadCandidateAsync(
        string filePath,
        ConversationRecord conversation,
        string expectedWorkingDirectory,
        CancellationToken cancellationToken)
    {
        var eventMessages = new MessageCollector();
        var fallbackMessages = new MessageCollector();
        var warnings = new List<string>();
        bool metadataMatched = false;
        int lineNumber = 0;

        try
        {
            await using var stream = new FileStream(
                filePath,
                FileMode.Open,
                FileAccess.Read,
                FileShare.ReadWrite | FileShare.Delete,
                bufferSize: 16 * 1024,
                FileOptions.Asynchronous | FileOptions.SequentialScan);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false) is { } line)
            {
                lineNumber++;
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                if (line.Length > MaxJsonLineCharacters)
                {
                    if (metadataMatched)
                    {
                        AddWarningOnce(warnings, "Codex 历史中存在超大记录，已安全跳过。");
                    }

                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;

                    if (!metadataMatched)
                    {
                        MetadataMatch metadataMatch = MatchSessionMetadata(
                            root,
                            conversation.NativeSessionId,
                            expectedWorkingDirectory);
                        if (metadataMatch == MetadataMatch.Mismatch)
                        {
                            return CandidateReadResult.NotMatched();
                        }

                        if (metadataMatch == MetadataMatch.Match)
                        {
                            metadataMatched = true;
                            continue;
                        }

                        if (lineNumber >= MaxMetadataLines)
                        {
                            return CandidateReadResult.NotMatched();
                        }

                        continue;
                    }

                    DateTimeOffset? sourceTimestamp = ReadTimestamp(root);
                    DateTimeOffset timestamp = sourceTimestamp
                        ?? (conversation.CreatedAt == default
                            ? DateTimeOffset.UnixEpoch
                            : conversation.CreatedAt);

                    if (TryReadEventMessage(
                            root,
                            conversation.NativeSessionId,
                            lineNumber,
                            timestamp,
                            sourceTimestamp,
                            out ParsedMessage? eventMessage))
                    {
                        eventMessages.Add(eventMessage!);
                    }
                    else if (TryReadFallbackMessage(
                                 root,
                                 conversation.NativeSessionId,
                                 lineNumber,
                                 timestamp,
                                 sourceTimestamp,
                                 out ParsedMessage? fallbackMessage))
                    {
                        fallbackMessages.Add(fallbackMessage!);
                    }

                    if (eventMessages.IsSaturated)
                    {
                        break;
                    }
                }
                catch (JsonException)
                {
                    AddWarningOnce(warnings, "Codex 历史中存在未完成或无法识别的记录，已跳过。");
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            if (!metadataMatched)
            {
                return CandidateReadResult.NotMatched("部分 Codex 历史文件当前无法读取，已继续查找。");
            }

            AddWarningOnce(warnings, "Codex 会话读取到一半时文件发生变化，已展示成功读取的内容。");
        }

        if (!metadataMatched)
        {
            return CandidateReadResult.NotMatched();
        }

        MessageCollector selected = eventMessages.Count > 0
            ? eventMessages
            : fallbackMessages;
        foreach (string warning in selected.CreateLimitWarnings())
        {
            AddWarningOnce(warnings, warning);
        }

        return CandidateReadResult.Matched(selected.Messages, warnings);
    }

    private static MetadataMatch MatchSessionMetadata(
        JsonElement root,
        string expectedSessionId,
        string expectedWorkingDirectory)
    {
        if (!GetString(root, "type").Equals("session_meta", StringComparison.OrdinalIgnoreCase))
        {
            return MetadataMatch.NotMetadata;
        }

        if (!TryGetObject(root, "payload", out JsonElement payload))
        {
            return MetadataMatch.Mismatch;
        }

        string? sessionId = GetString(payload, "id");
        string? workingDirectory = GetString(payload, "cwd");
        if (string.IsNullOrWhiteSpace(sessionId) ||
            !sessionId.Equals(expectedSessionId, StringComparison.OrdinalIgnoreCase) ||
            !TryNormalizeWorkingDirectory(workingDirectory, out string normalizedWorkingDirectory))
        {
            return MetadataMatch.Mismatch;
        }

        StringComparison pathComparison = OperatingSystem.IsWindows()
            ? StringComparison.OrdinalIgnoreCase
            : StringComparison.Ordinal;
        return normalizedWorkingDirectory.Equals(expectedWorkingDirectory, pathComparison)
            ? MetadataMatch.Match
            : MetadataMatch.Mismatch;
    }

    private static bool TryReadEventMessage(
        JsonElement root,
        string nativeSessionId,
        int lineNumber,
        DateTimeOffset timestamp,
        DateTimeOffset? sourceTimestamp,
        out ParsedMessage? message)
    {
        message = null;
        if (!GetString(root, "type").Equals("event_msg", StringComparison.OrdinalIgnoreCase) ||
            !TryGetObject(root, "payload", out JsonElement payload))
        {
            return false;
        }

        string eventType = GetString(payload, "type");
        ConversationTranscriptRole role;
        if (eventType.Equals("user_message", StringComparison.OrdinalIgnoreCase))
        {
            role = ConversationTranscriptRole.User;
        }
        else if (eventType.Equals("agent_message", StringComparison.OrdinalIgnoreCase))
        {
            if (IsHiddenPhase(GetString(payload, "phase")))
            {
                return false;
            }

            role = ConversationTranscriptRole.Assistant;
        }
        else
        {
            return false;
        }

        string text = NormalizeVisibleText(GetString(payload, "message"));
        if (text.Length == 0)
        {
            return false;
        }

        string? sourceId = GetString(payload, "client_id") ?? GetString(payload, "id");
        message = new ParsedMessage(
            CreateMessageId(nativeSessionId, lineNumber, role, sourceId),
            role,
            text,
            timestamp,
            sourceTimestamp);
        return true;
    }

    private static bool TryReadFallbackMessage(
        JsonElement root,
        string nativeSessionId,
        int lineNumber,
        DateTimeOffset timestamp,
        DateTimeOffset? sourceTimestamp,
        out ParsedMessage? message)
    {
        message = null;
        if (!GetString(root, "type").Equals("response_item", StringComparison.OrdinalIgnoreCase) ||
            !TryGetObject(root, "payload", out JsonElement payload) ||
            !GetString(payload, "type").Equals("message", StringComparison.OrdinalIgnoreCase) ||
            IsHiddenPhase(GetString(payload, "phase")))
        {
            return false;
        }

        string rawRole = GetString(payload, "role");
        ConversationTranscriptRole role;
        string expectedContentType;
        if (rawRole.Equals("user", StringComparison.OrdinalIgnoreCase))
        {
            role = ConversationTranscriptRole.User;
            expectedContentType = "input_text";
        }
        else if (rawRole.Equals("assistant", StringComparison.OrdinalIgnoreCase))
        {
            role = ConversationTranscriptRole.Assistant;
            expectedContentType = "output_text";
        }
        else
        {
            // Includes developer/system messages. They are model-visible but
            // must never be shown as user chat history.
            return false;
        }

        if (!payload.TryGetProperty("content", out JsonElement content) ||
            content.ValueKind != JsonValueKind.Array)
        {
            return false;
        }

        var textParts = new List<string>();
        foreach (JsonElement contentItem in content.EnumerateArray())
        {
            if (contentItem.ValueKind != JsonValueKind.Object ||
                !GetString(contentItem, "type").Equals(expectedContentType, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            string textPart = NormalizeVisibleText(GetString(contentItem, "text"));
            if (textPart.Length > 0)
            {
                textParts.Add(textPart);
            }
        }

        string text = NormalizeVisibleText(string.Join("\n\n", textParts));
        if (text.Length == 0)
        {
            return false;
        }

        message = new ParsedMessage(
            CreateMessageId(nativeSessionId, lineNumber, role, GetString(payload, "id")),
            role,
            text,
            timestamp,
            sourceTimestamp);
        return true;
    }

    private static bool IsHiddenPhase(string phase) =>
        phase.Equals("analysis", StringComparison.OrdinalIgnoreCase) ||
        phase.Equals("reasoning", StringComparison.OrdinalIgnoreCase);

    private static DateTimeOffset? ReadTimestamp(JsonElement root)
    {
        if (!root.TryGetProperty("timestamp", out JsonElement value))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.String &&
            DateTimeOffset.TryParse(
                value.GetString(),
                CultureInfo.InvariantCulture,
                DateTimeStyles.AllowWhiteSpaces | DateTimeStyles.AssumeUniversal,
                out DateTimeOffset parsed))
        {
            return parsed;
        }

        if (value.ValueKind == JsonValueKind.Number && value.TryGetInt64(out long numeric))
        {
            try
            {
                return numeric > 10_000_000_000
                    ? DateTimeOffset.FromUnixTimeMilliseconds(numeric)
                    : DateTimeOffset.FromUnixTimeSeconds(numeric);
            }
            catch (ArgumentOutOfRangeException)
            {
                return null;
            }
        }

        return null;
    }

    private static string NormalizeVisibleText(string? value) =>
        string.IsNullOrWhiteSpace(value)
            ? string.Empty
            : value.Replace("\0", string.Empty, StringComparison.Ordinal)
                .Replace("\r\n", "\n", StringComparison.Ordinal)
                .Replace('\r', '\n')
                .Trim();

    private static string CreateMessageId(
        string nativeSessionId,
        int lineNumber,
        ConversationTranscriptRole role,
        string? sourceId) =>
        !string.IsNullOrWhiteSpace(sourceId)
            ? $"codex:{nativeSessionId}:{sourceId}"
            : $"codex:{nativeSessionId}:{lineNumber.ToString(CultureInfo.InvariantCulture)}:{role.ToString().ToLowerInvariant()}";

    private static IReadOnlyList<RolloutFile> EnumerateRolloutFiles(
        string root,
        ICollection<string> warnings,
        CancellationToken cancellationToken)
    {
        var files = new List<RolloutFile>();
        var pending = new Stack<string>();
        pending.Push(root);

        while (pending.Count > 0)
        {
            cancellationToken.ThrowIfCancellationRequested();
            string directory = pending.Pop();
            string[] directoryFiles;
            try
            {
                directoryFiles = Directory.GetFiles(directory, "rollout-*.jsonl", SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                AddWarningOnce(warnings, "部分 Codex 历史目录当前无法读取，已继续查找。");
                continue;
            }

            foreach (string filePath in directoryFiles)
            {
                cancellationToken.ThrowIfCancellationRequested();
                try
                {
                    files.Add(new RolloutFile(filePath, File.GetLastWriteTimeUtc(filePath)));
                }
                catch (Exception exception) when (IsRecoverableFileException(exception))
                {
                    AddWarningOnce(warnings, "部分 Codex 历史文件当前无法读取，已继续查找。");
                }
            }

            string[] childDirectories;
            try
            {
                childDirectories = Directory.GetDirectories(directory, "*", SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                AddWarningOnce(warnings, "部分 Codex 历史目录当前无法读取，已继续查找。");
                continue;
            }

            foreach (string childDirectory in childDirectories)
            {
                cancellationToken.ThrowIfCancellationRequested();
                try
                {
                    if ((File.GetAttributes(childDirectory) & FileAttributes.ReparsePoint) == 0)
                    {
                        pending.Push(childDirectory);
                    }
                }
                catch (Exception exception) when (IsRecoverableFileException(exception))
                {
                    AddWarningOnce(warnings, "部分 Codex 历史目录当前无法读取，已继续查找。");
                }
            }
        }

        return files
            .OrderByDescending(file => file.LastWriteTimeUtc)
            .ThenBy(file => file.Path, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    private static bool TryNormalizeWorkingDirectory(string? value, out string normalized)
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
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
        {
            return false;
        }
    }

    private static bool TryGetObject(JsonElement element, string propertyName, out JsonElement value)
    {
        if (element.ValueKind == JsonValueKind.Object &&
            element.TryGetProperty(propertyName, out value) &&
            value.ValueKind == JsonValueKind.Object)
        {
            return true;
        }

        value = default;
        return false;
    }

    private static string GetString(JsonElement element, string propertyName)
    {
        if (element.ValueKind != JsonValueKind.Object ||
            !element.TryGetProperty(propertyName, out JsonElement value) ||
            value.ValueKind != JsonValueKind.String)
        {
            return string.Empty;
        }

        return value.GetString() ?? string.Empty;
    }

    private static bool IsRecoverableFileException(Exception exception) =>
        exception is IOException or UnauthorizedAccessException;

    private static void AddWarningOnce(ICollection<string> warnings, string warning)
    {
        if (!warnings.Contains(warning, StringComparer.Ordinal))
        {
            warnings.Add(warning);
        }
    }

    private sealed class MessageCollector
    {
        private readonly List<ConversationTranscriptMessage> _messages = new();
        private readonly HashSet<MessageDedupKey> _deduplicationKeys = new();
        private int _totalCharacters;

        public IReadOnlyList<ConversationTranscriptMessage> Messages => _messages;

        public int Count => _messages.Count;

        public bool HitMessageLimit { get; private set; }

        public bool HitCharacterLimit { get; private set; }

        public bool TruncatedMessage { get; private set; }

        public bool IsSaturated => HitMessageLimit || HitCharacterLimit;

        public void Add(ParsedMessage candidate)
        {
            var deduplicationKey = new MessageDedupKey(
                candidate.Role,
                candidate.SourceTimestamp?.UtcTicks,
                candidate.Text);
            if (!_deduplicationKeys.Add(deduplicationKey))
            {
                return;
            }

            if (_messages.Count >= MaxMessages)
            {
                HitMessageLimit = true;
                return;
            }

            int remainingCharacters = MaxTotalCharacters - _totalCharacters;
            if (remainingCharacters <= TruncationMarker.Length)
            {
                HitCharacterLimit = true;
                return;
            }

            int maximumCharacters = Math.Min(MaxMessageCharacters, remainingCharacters);
            string text = candidate.Text;
            if (text.Length > maximumCharacters)
            {
                int bodyLength = maximumCharacters - TruncationMarker.Length;
                if (bodyLength > 0 &&
                    bodyLength < text.Length &&
                    char.IsHighSurrogate(text[bodyLength - 1]))
                {
                    bodyLength--;
                }

                text = text[..bodyLength] + TruncationMarker;
                TruncatedMessage = true;
                if (remainingCharacters <= MaxMessageCharacters)
                {
                    HitCharacterLimit = true;
                }
            }

            _messages.Add(new ConversationTranscriptMessage(
                candidate.Id,
                candidate.Role,
                text,
                candidate.Timestamp));
            _totalCharacters += text.Length;

            if (_messages.Count >= MaxMessages)
            {
                HitMessageLimit = true;
            }

            if (_totalCharacters >= MaxTotalCharacters)
            {
                HitCharacterLimit = true;
            }
        }

        public IReadOnlyList<string> CreateLimitWarnings()
        {
            var warnings = new List<string>(3);
            if (TruncatedMessage)
            {
                warnings.Add("部分超长消息已截断后展示。");
            }

            if (HitMessageLimit)
            {
                warnings.Add($"为保证界面流畅，本次最多展示 {MaxMessages.ToString(CultureInfo.InvariantCulture)} 条消息。");
            }

            if (HitCharacterLimit)
            {
                warnings.Add("为保证界面流畅，本次历史正文已达到安全容量上限。");
            }

            return warnings;
        }
    }

    private enum MetadataMatch
    {
        NotMetadata,
        Match,
        Mismatch,
    }

    private sealed record RolloutFile(string Path, DateTime LastWriteTimeUtc);

    private sealed record ParsedMessage(
        string Id,
        ConversationTranscriptRole Role,
        string Text,
        DateTimeOffset Timestamp,
        DateTimeOffset? SourceTimestamp);

    private readonly record struct MessageDedupKey(
        ConversationTranscriptRole Role,
        long? TimestampTicks,
        string Text);

    private sealed record CandidateReadResult(
        bool MetadataMatched,
        IReadOnlyList<ConversationTranscriptMessage> Messages,
        IReadOnlyList<string> Warnings,
        string? Warning)
    {
        public static CandidateReadResult NotMatched(string? warning = null) =>
            new(false, Array.Empty<ConversationTranscriptMessage>(), Array.Empty<string>(), warning);

        public static CandidateReadResult Matched(
            IReadOnlyList<ConversationTranscriptMessage> messages,
            IReadOnlyList<string> warnings) =>
            new(true, messages, warnings, null);
    }
}
