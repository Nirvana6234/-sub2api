using System.Globalization;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Builds a read-only metadata index over official CLI storage. It deliberately
/// does not return, persist, log or fingerprint message bodies.
/// </summary>
public sealed class CompositeConversationIndexer : IConversationIndexer
{
    private const int MaxMetadataLines = 16;
    private readonly AppDataPaths _paths;

    public CompositeConversationIndexer(AppDataPaths paths)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
    }

    public async Task<IReadOnlyList<ConversationRecord>> ScanAsync(
        ProjectRecord? project = null,
        CliKind? client = null,
        CancellationToken cancellationToken = default)
    {
        var scans = new List<Task<IReadOnlyList<ConversationRecord>>>(3);

        if (client is null or CliKind.Codex)
        {
            scans.Add(Task.Run<IReadOnlyList<ConversationRecord>>(
                () => ScanCodex(project, cancellationToken),
                cancellationToken));
        }

        if (client is null or CliKind.ClaudeCode)
        {
            scans.Add(Task.Run<IReadOnlyList<ConversationRecord>>(
                () => ScanClaude(project, cancellationToken),
                cancellationToken));
        }

        if (client is null or CliKind.GeminiCli)
        {
            scans.Add(Task.Run<IReadOnlyList<ConversationRecord>>(
                () => ScanGemini(project, cancellationToken),
                cancellationToken));
        }

        if (scans.Count == 0)
        {
            return Array.Empty<ConversationRecord>();
        }

        IReadOnlyList<ConversationRecord>[] batches = await Task.WhenAll(scans).ConfigureAwait(false);
        cancellationToken.ThrowIfCancellationRequested();

        return batches
            .SelectMany(batch => batch)
            .GroupBy(record => record.Id, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.OrderByDescending(record => record.UpdatedAt).First())
            .OrderByDescending(record => record.UpdatedAt)
            .ThenBy(record => record.NativeClient)
            .ToArray();
    }

    private IReadOnlyList<ConversationRecord> ScanCodex(
        ProjectRecord? project,
        CancellationToken cancellationToken)
    {
        string[] roots =
        [
            _paths.CodexSessionsDirectory,
            _paths.CodexArchivedSessionsDirectory,
        ];
        if (!roots.Any(Directory.Exists))
        {
            return Array.Empty<ConversationRecord>();
        }

        Dictionary<string, CodexIndexEntry> index = ReadCodexSessionIndex(cancellationToken);
        var conversations = new List<ConversationRecord>();

        foreach (string filePath in roots
                     .Where(Directory.Exists)
                     .SelectMany(root => EnumerateFilesSafely(root, "*.jsonl"))
                     .Distinct(StringComparer.OrdinalIgnoreCase))
        {
            cancellationToken.ThrowIfCancellationRequested();

            CodexRolloutMetadata? metadata = ReadCodexRolloutMetadata(filePath);
            if (metadata is null ||
                metadata.IsSubagent ||
                string.IsNullOrWhiteSpace(metadata.NativeSessionId) ||
                !TryNormalizeWorkingDirectory(metadata.WorkingDirectory, out string workingDirectory))
            {
                continue;
            }

            string projectFingerprint = PathIdentity.CreateStableId(workingDirectory);
            if (!MatchesProject(project, workingDirectory, projectFingerprint))
            {
                continue;
            }

            if (!TryGetFileTimes(filePath, out DateTimeOffset fileCreatedAt, out DateTimeOffset fileUpdatedAt))
            {
                continue;
            }

            index.TryGetValue(metadata.NativeSessionId, out CodexIndexEntry? indexEntry);

            DateTimeOffset createdAt = metadata.CreatedAt ?? fileCreatedAt;
            DateTimeOffset updatedAt = indexEntry?.UpdatedAt ?? fileUpdatedAt;
            if (updatedAt < createdAt)
            {
                updatedAt = createdAt;
            }

            conversations.Add(CreateConversation(
                CliKind.Codex,
                metadata.NativeSessionId,
                projectFingerprint,
                workingDirectory,
                SanitizeTitle(indexEntry?.Title) ?? $"Codex 会话 {ShortId(metadata.NativeSessionId)}",
                createdAt,
                updatedAt,
                filePath));
        }

        return conversations;
    }

    private IReadOnlyList<ConversationRecord> ScanClaude(
        ProjectRecord? project,
        CancellationToken cancellationToken)
    {
        if (!Directory.Exists(_paths.ClaudeProjectsDirectory))
        {
            return Array.Empty<ConversationRecord>();
        }

        var conversations = new List<ConversationRecord>();
        foreach (string filePath in EnumerateFilesSafely(_paths.ClaudeProjectsDirectory, "*.jsonl"))
        {
            cancellationToken.ThrowIfCancellationRequested();

            ClaudeMetadata? metadata = ReadClaudeMetadata(filePath);
            if (metadata is null ||
                metadata.IsSidechain ||
                string.IsNullOrWhiteSpace(metadata.NativeSessionId) ||
                !TryNormalizeWorkingDirectory(metadata.WorkingDirectory, out string workingDirectory))
            {
                continue;
            }

            string projectFingerprint = PathIdentity.CreateStableId(workingDirectory);
            if (!MatchesProject(project, workingDirectory, projectFingerprint) ||
                !TryGetFileTimes(filePath, out DateTimeOffset fileCreatedAt, out DateTimeOffset fileUpdatedAt))
            {
                continue;
            }

            DateTimeOffset createdAt = metadata.CreatedAt ?? fileCreatedAt;
            DateTimeOffset updatedAt = fileUpdatedAt < createdAt ? createdAt : fileUpdatedAt;
            string title = SanitizeTitle(metadata.Title)
                ?? $"Claude 会话 {ShortId(metadata.NativeSessionId)}";

            conversations.Add(CreateConversation(
                CliKind.ClaudeCode,
                metadata.NativeSessionId,
                projectFingerprint,
                workingDirectory,
                title,
                createdAt,
                updatedAt,
                filePath));
        }

        return conversations;
    }

    private IReadOnlyList<ConversationRecord> ScanGemini(
        ProjectRecord? project,
        CancellationToken cancellationToken)
    {
        if (!Directory.Exists(_paths.GeminiProjectsDirectory))
        {
            return Array.Empty<ConversationRecord>();
        }

        var conversations = new List<ConversationRecord>();
        foreach (string filePath in new[] { "session-*.jsonl", "session-*.json" }
                     .SelectMany(pattern => EnumerateFilesSafely(_paths.GeminiProjectsDirectory, pattern))
                     .Distinct(StringComparer.OrdinalIgnoreCase))
        {
            cancellationToken.ThrowIfCancellationRequested();

            GeminiMetadata? metadata = ReadGeminiMetadata(filePath);
            if (metadata is null ||
                (!string.IsNullOrWhiteSpace(metadata.Kind) &&
                 !string.Equals(metadata.Kind, "main", StringComparison.OrdinalIgnoreCase)) ||
                string.IsNullOrWhiteSpace(metadata.NativeSessionId))
            {
                continue;
            }

            string? chatsDirectory = Path.GetDirectoryName(filePath);
            string? projectDirectory = chatsDirectory is null
                ? null
                : Directory.GetParent(chatsDirectory)?.FullName;
            string? projectRoot = projectDirectory is null
                ? null
                : ReadSmallTextFile(Path.Combine(projectDirectory, ".project_root"), 32 * 1024);

            if (!TryNormalizeWorkingDirectory(projectRoot, out string workingDirectory))
            {
                continue;
            }

            string projectFingerprint = PathIdentity.CreateStableId(workingDirectory);
            if (!MatchesProject(project, workingDirectory, projectFingerprint) ||
                !TryGetFileTimes(filePath, out DateTimeOffset fileCreatedAt, out DateTimeOffset fileUpdatedAt))
            {
                continue;
            }

            DateTimeOffset createdAt = metadata.CreatedAt ?? fileCreatedAt;
            DateTimeOffset updatedAt = metadata.UpdatedAt ?? fileUpdatedAt;
            if (updatedAt < createdAt)
            {
                updatedAt = createdAt;
            }

            conversations.Add(CreateConversation(
                CliKind.GeminiCli,
                metadata.NativeSessionId,
                projectFingerprint,
                workingDirectory,
                SanitizeTitle(metadata.Title) ?? $"Gemini 会话 {ShortId(metadata.NativeSessionId)}",
                createdAt,
                updatedAt,
                filePath));
        }

        return conversations;
    }

    private Dictionary<string, CodexIndexEntry> ReadCodexSessionIndex(
        CancellationToken cancellationToken)
    {
        var entries = new Dictionary<string, CodexIndexEntry>(StringComparer.OrdinalIgnoreCase);
        if (!File.Exists(_paths.CodexSessionIndexPath))
        {
            return entries;
        }

        try
        {
            using var stream = OpenSharedRead(_paths.CodexSessionIndexPath);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (reader.ReadLine() is { } line)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    string? id = GetString(root, "id");
                    if (string.IsNullOrWhiteSpace(id))
                    {
                        continue;
                    }

                    entries[id] = new CodexIndexEntry(
                        SanitizeTitle(GetString(root, "thread_name")),
                        GetTimestamp(root, "updated_at"));
                }
                catch (JsonException)
                {
                    // A concurrently appended partial line is safe to ignore.
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            // Missing/locked metadata should not hide other clients' history.
        }

        return entries;
    }

    private static CodexRolloutMetadata? ReadCodexRolloutMetadata(string filePath)
    {
        foreach (SafeJsonMetadata metadata in ReadSafeMetadata(filePath, MaxMetadataLines))
        {
            if (!string.Equals(metadata.Type, "session_meta", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            string? id = metadata.PayloadNativeSessionId;
            string? cwd = metadata.PayloadWorkingDirectory;
            if (string.IsNullOrWhiteSpace(id) || string.IsNullOrWhiteSpace(cwd))
            {
                return null;
            }

            return new CodexRolloutMetadata(
                id,
                cwd,
                metadata.PayloadTimestamp ?? metadata.Timestamp,
                metadata.PayloadIsSubagent);
        }

        return null;
    }

    private static ClaudeMetadata? ReadClaudeMetadata(string filePath)
    {
        string? nativeSessionId = Path.GetFileNameWithoutExtension(filePath);
        string? workingDirectory = null;
        string? title = null;
        DateTimeOffset? createdAt = null;
        bool isSidechain = filePath.Contains(
            $"{Path.DirectorySeparatorChar}subagents{Path.DirectorySeparatorChar}",
            StringComparison.OrdinalIgnoreCase);

        int lineNumber = 0;
        foreach (SafeJsonMetadata metadata in ReadSafeMetadata(filePath, MaxMetadataLines))
        {
            lineNumber++;
            nativeSessionId = NullIfWhiteSpace(metadata.NativeSessionId) ?? nativeSessionId;
            workingDirectory ??= NullIfWhiteSpace(metadata.WorkingDirectory);
            title ??= NullIfWhiteSpace(metadata.Title);
            isSidechain |= metadata.IsSidechain;

            DateTimeOffset? timestamp = metadata.Timestamp;
            if (timestamp is { } value && (createdAt is null || value < createdAt))
            {
                createdAt = value;
            }

            if (lineNumber >= 4 && workingDirectory is not null && title is not null)
            {
                break;
            }
        }

        return string.IsNullOrWhiteSpace(nativeSessionId) || string.IsNullOrWhiteSpace(workingDirectory)
            ? null
            : new ClaudeMetadata(nativeSessionId, workingDirectory, title, createdAt, isSidechain);
    }

    private static GeminiMetadata? ReadGeminiMetadata(string filePath)
    {
        try
        {
            if (Path.GetExtension(filePath).Equals(".json", StringComparison.OrdinalIgnoreCase))
            {
                using FileStream stream = OpenSharedRead(filePath);
                using JsonDocument document = JsonDocument.Parse(stream);
                return ReadGeminiMetadata(document.RootElement, filePath, allowFileNameFallback: true);
            }

            using FileStream jsonlStream = OpenSharedRead(filePath);
            using var reader = new StreamReader(
                jsonlStream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);
            GeminiMetadata? metadata = null;
            for (int lineNumber = 0; lineNumber < MaxMetadataLines && reader.ReadLine() is { } line; lineNumber++)
            {
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    metadata = MergeGeminiMetadata(
                        metadata,
                        ReadGeminiMetadata(root, filePath, allowFileNameFallback: false));
                    if (TryGetProperty(root, "$set", out JsonElement update) &&
                        update.ValueKind == JsonValueKind.Object)
                    {
                        metadata = MergeGeminiMetadata(
                            metadata,
                            ReadGeminiMetadata(update, filePath, allowFileNameFallback: false));
                    }
                }
                catch (JsonException)
                {
                    // A concurrently appended partial JSONL line is safe to ignore.
                }
            }

            return metadata ?? new GeminiMetadata(
                Path.GetFileNameWithoutExtension(filePath)
                    .Replace("session-", string.Empty, StringComparison.OrdinalIgnoreCase),
                null,
                null,
                null,
                null);
        }
        catch (Exception exception) when (IsRecoverableFileException(exception) || exception is JsonException)
        {
            return null;
        }
    }

    private static GeminiMetadata? ReadGeminiMetadata(
        JsonElement root,
        string filePath,
        bool allowFileNameFallback)
    {
        if (root.ValueKind != JsonValueKind.Object)
        {
            return null;
        }

        string? id = NullIfWhiteSpace(GetString(root, "sessionId"));
        if (id is null && allowFileNameFallback)
        {
            id = Path.GetFileNameWithoutExtension(filePath)
                .Replace("session-", string.Empty, StringComparison.OrdinalIgnoreCase);
        }
        if (string.IsNullOrWhiteSpace(id))
        {
            return null;
        }

        string? title = NullIfWhiteSpace(GetString(root, "aiTitle") ?? GetString(root, "title"));
        if (title is null &&
            TryGetProperty(root, "messages", out JsonElement messages) &&
            messages.ValueKind == JsonValueKind.Array)
        {
            title = messages.EnumerateArray()
                .Where(message => message.ValueKind == JsonValueKind.Object &&
                                  string.Equals(GetString(message, "type"), "user", StringComparison.OrdinalIgnoreCase))
                .Select(ExtractGeminiTitleText)
                .FirstOrDefault(candidate => !string.IsNullOrWhiteSpace(candidate));
        }

        return new GeminiMetadata(
            id,
            title,
            GetTimestamp(root, "startTime"),
            GetTimestamp(root, "lastUpdated"),
            GetString(root, "kind"));
    }

    private static GeminiMetadata? MergeGeminiMetadata(GeminiMetadata? current, GeminiMetadata? candidate)
    {
        if (candidate is null)
        {
            return current;
        }

        if (current is null)
        {
            return candidate;
        }

        return new GeminiMetadata(
            NullIfWhiteSpace(candidate.NativeSessionId) ?? current.NativeSessionId,
            NullIfWhiteSpace(candidate.Title) ?? current.Title,
            candidate.CreatedAt ?? current.CreatedAt,
            candidate.UpdatedAt ?? current.UpdatedAt,
            NullIfWhiteSpace(candidate.Kind) ?? current.Kind);
    }

    private static string? ExtractGeminiTitleText(JsonElement message)
    {
        if (!TryGetProperty(message, "content", out JsonElement content))
        {
            return null;
        }

        if (content.ValueKind == JsonValueKind.String)
        {
            return NullIfWhiteSpace(content.GetString());
        }

        if (content.ValueKind != JsonValueKind.Array)
        {
            return null;
        }

        string joined = string.Join(
            " ",
            content.EnumerateArray()
                .Select(item => item.ValueKind switch
                {
                    JsonValueKind.String => item.GetString(),
                    JsonValueKind.Object => GetString(item, "text"),
                    _ => null,
                })
                .Where(value => !string.IsNullOrWhiteSpace(value)));
        return NullIfWhiteSpace(joined);
    }

    private static IReadOnlyList<SafeJsonMetadata> ReadSafeMetadata(string filePath, int maximumLines)
    {
        var metadata = new List<SafeJsonMetadata>(Math.Min(maximumLines, 8));
        try
        {
            using var stream = OpenSharedRead(filePath);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            for (int lineNumber = 0; lineNumber < maximumLines && reader.ReadLine() is { } line; lineNumber++)
            {
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    JsonElement payload = default;
                    bool hasPayload = TryGetProperty(root, "payload", out payload) &&
                                      payload.ValueKind == JsonValueKind.Object;

                    // Only this explicit metadata allow-list survives disposal of
                    // the JsonDocument. Message/body fields are never copied.
                    metadata.Add(new SafeJsonMetadata(
                        GetString(root, "type"),
                        GetString(root, "sessionId"),
                        GetString(root, "cwd"),
                        GetString(root, "aiTitle") ?? GetString(root, "title"),
                        GetTimestamp(root, "timestamp"),
                        GetTimestamp(root, "startTime"),
                        GetTimestamp(root, "lastUpdated"),
                        GetString(root, "kind"),
                        GetBoolean(root, "isSidechain"),
                        hasPayload ? GetString(payload, "id") : null,
                        hasPayload ? GetString(payload, "cwd") : null,
                        hasPayload ? GetTimestamp(payload, "timestamp") : null,
                        hasPayload && IsCodexSubagentSource(payload)));
                }
                catch (JsonException)
                {
                    // Ignore partial or future-format lines.
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return Array.Empty<SafeJsonMetadata>();
        }

        return metadata;
    }

    private static ConversationRecord CreateConversation(
        CliKind client,
        string nativeSessionId,
        string projectFingerprint,
        string workingDirectory,
        string title,
        DateTimeOffset createdAt,
        DateTimeOffset updatedAt,
        string nativeFilePath)
    {
        string clientName = client switch
        {
            CliKind.Codex => "codex",
            CliKind.ClaudeCode => "claude",
            CliKind.GeminiCli => "gemini",
            _ => client.ToString().ToLowerInvariant(),
        };

        return new ConversationRecord
        {
            Id = $"{clientName}:{nativeSessionId}",
            ProjectId = projectFingerprint,
            NativeClient = client,
            NativeSessionId = nativeSessionId,
            Title = title,
            OriginalWorkingDirectory = workingDirectory,
            CreatedAt = createdAt,
            UpdatedAt = updatedAt,
            ResumePolicy = ResumePolicy.CurrentConnection,
            StorageMode = ConversationStorageMode.NativeIndex,
            NativeFileFingerprint = CreateNativeFileFingerprint(nativeFilePath),
            Status = ConversationStatus.Available,
        };
    }

    private static bool MatchesProject(
        ProjectRecord? project,
        string workingDirectory,
        string projectFingerprint)
    {
        if (project is null)
        {
            return true;
        }

        if (!string.IsNullOrWhiteSpace(project.PathFingerprint) &&
            string.Equals(project.PathFingerprint, projectFingerprint, StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        try
        {
            return string.Equals(
                PathIdentity.Normalize(project.RootPath),
                workingDirectory,
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
        {
            return false;
        }
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

    private static string CreateNativeFileFingerprint(string filePath)
    {
        try
        {
            var file = new FileInfo(filePath);
            string identity = string.Join(
                "|",
                Path.GetFullPath(filePath),
                file.Exists ? file.Length.ToString(CultureInfo.InvariantCulture) : "missing",
                file.Exists ? file.LastWriteTimeUtc.Ticks.ToString(CultureInfo.InvariantCulture) : "0");
            return Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(identity))).ToLowerInvariant();
        }
        catch (Exception exception) when (IsRecoverableFileException(exception) || exception is ArgumentException)
        {
            return Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(filePath))).ToLowerInvariant();
        }
    }

    private static bool TryGetFileTimes(
        string filePath,
        out DateTimeOffset createdAt,
        out DateTimeOffset updatedAt)
    {
        createdAt = default;
        updatedAt = default;
        try
        {
            var file = new FileInfo(filePath);
            file.Refresh();
            if (!file.Exists)
            {
                return false;
            }

            createdAt = new DateTimeOffset(file.CreationTimeUtc, TimeSpan.Zero);
            updatedAt = new DateTimeOffset(file.LastWriteTimeUtc, TimeSpan.Zero);
            return true;
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return false;
        }
    }

    private static FileStream OpenSharedRead(string path)
        => new(
            path,
            FileMode.Open,
            FileAccess.Read,
            FileShare.ReadWrite | FileShare.Delete,
            bufferSize: 16 * 1024,
            FileOptions.SequentialScan);

    private static IEnumerable<string> EnumerateFilesSafely(string root, string pattern)
    {
        var pending = new Stack<string>();
        pending.Push(root);

        while (pending.Count > 0)
        {
            string directory = pending.Pop();
            string[] files;
            try
            {
                files = Directory.GetFiles(directory, pattern, SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                continue;
            }

            foreach (string file in files)
            {
                yield return file;
            }

            string[] directories;
            try
            {
                directories = Directory.GetDirectories(directory, "*", SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                continue;
            }

            foreach (string child in directories)
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
                    // Skip inaccessible directories and continue with siblings.
                }
            }
        }
    }

    private static string? ReadSmallTextFile(string path, int maximumCharacters)
    {
        if (!File.Exists(path))
        {
            return null;
        }

        try
        {
            using var stream = OpenSharedRead(path);
            using var reader = new StreamReader(stream, Encoding.UTF8, true, 1024, false);
            var buffer = new char[maximumCharacters + 1];
            int read = reader.ReadBlock(buffer, 0, buffer.Length);
            if (read > maximumCharacters)
            {
                return null;
            }

            return NullIfWhiteSpace(new string(buffer, 0, read));
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return null;
        }
    }

    private static DateTimeOffset? GetTimestamp(JsonElement element, string propertyName)
    {
        if (!TryGetProperty(element, propertyName, out JsonElement value))
        {
            return null;
        }

        if (value.ValueKind == JsonValueKind.String)
        {
            string? text = value.GetString();
            if (DateTimeOffset.TryParse(
                    text,
                    CultureInfo.InvariantCulture,
                    DateTimeStyles.AllowWhiteSpaces,
                    out DateTimeOffset timestamp))
            {
                return timestamp;
            }
        }
        else if (value.ValueKind == JsonValueKind.Number && value.TryGetInt64(out long numeric))
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

    private static bool TryGetProperty(JsonElement element, string name, out JsonElement value)
    {
        if (element.ValueKind == JsonValueKind.Object && element.TryGetProperty(name, out value))
        {
            return true;
        }

        if (element.ValueKind == JsonValueKind.Object)
        {
            foreach (JsonProperty property in element.EnumerateObject())
            {
                if (string.Equals(property.Name, name, StringComparison.OrdinalIgnoreCase))
                {
                    value = property.Value;
                    return true;
                }
            }
        }

        value = default;
        return false;
    }

    private static string? GetString(JsonElement element, string propertyName)
    {
        return TryGetProperty(element, propertyName, out JsonElement value) &&
               value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;
    }

    private static bool GetBoolean(JsonElement element, string propertyName)
        => TryGetProperty(element, propertyName, out JsonElement value) &&
           value.ValueKind == JsonValueKind.True;

    private static bool IsCodexSubagentSource(JsonElement payload) =>
        TryGetProperty(payload, "source", out JsonElement source) &&
        source.ValueKind == JsonValueKind.Object &&
        TryGetProperty(source, "subagent", out _);

    private static string? SanitizeTitle(string? title)
    {
        if (string.IsNullOrWhiteSpace(title))
        {
            return null;
        }

        string singleLine = string.Join(
            " ",
            title.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries));
        const int maximumLength = 240;
        return singleLine.Length <= maximumLength ? singleLine : singleLine[..maximumLength];
    }

    private static string ShortId(string id)
        => id.Length <= 8 ? id : id[..8];

    private static string? NullIfWhiteSpace(string? value)
        => string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    private static bool IsRecoverableFileException(Exception exception)
        => exception is IOException or UnauthorizedAccessException or System.Security.SecurityException;

    private sealed record CodexIndexEntry(string? Title, DateTimeOffset? UpdatedAt);

    private sealed record CodexRolloutMetadata(
        string NativeSessionId,
        string WorkingDirectory,
        DateTimeOffset? CreatedAt,
        bool IsSubagent);

    private sealed record ClaudeMetadata(
        string NativeSessionId,
        string WorkingDirectory,
        string? Title,
        DateTimeOffset? CreatedAt,
        bool IsSidechain);

    private sealed record GeminiMetadata(
        string NativeSessionId,
        string? Title,
        DateTimeOffset? CreatedAt,
        DateTimeOffset? UpdatedAt,
        string? Kind);

    private sealed record SafeJsonMetadata(
        string? Type,
        string? NativeSessionId,
        string? WorkingDirectory,
        string? Title,
        DateTimeOffset? Timestamp,
        DateTimeOffset? StartTime,
        DateTimeOffset? LastUpdated,
        string? Kind,
        bool IsSidechain,
        string? PayloadNativeSessionId,
        string? PayloadWorkingDirectory,
        DateTimeOffset? PayloadTimestamp,
        bool PayloadIsSubagent);
}
