using System.Diagnostics;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Deletes native conversations through the official CLI deletion commands.
/// File fallback is intentionally narrow and is used only when an installed
/// client cannot address an already-missing project directory.
/// </summary>
public sealed class OfficialConversationDeletionService : IConversationDeletionService
{
    private static readonly TimeSpan CommandTimeout = TimeSpan.FromMinutes(2);

    private readonly AppDataPaths _paths;
    private readonly IConversationIndexer _indexer;
    private readonly ICliDetector _cliDetector;
    private readonly IOfficialCliCommandRunner _commandRunner;
    private readonly SemaphoreSlim _deleteGate = new(1, 1);

    public OfficialConversationDeletionService(AppDataPaths paths)
        : this(
            paths,
            new CompositeConversationIndexer(paths ?? throw new ArgumentNullException(nameof(paths))),
            new CliDetector(),
            new OfficialCliCommandRunner())
    {
    }

    internal OfficialConversationDeletionService(
        AppDataPaths paths,
        IConversationIndexer indexer,
        ICliDetector cliDetector,
        IOfficialCliCommandRunner commandRunner)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _indexer = indexer ?? throw new ArgumentNullException(nameof(indexer));
        _cliDetector = cliDetector ?? throw new ArgumentNullException(nameof(cliDetector));
        _commandRunner = commandRunner ?? throw new ArgumentNullException(nameof(commandRunner));
    }

    public async Task<ConversationDeletionResult> DeleteProjectConversationsAsync(
        ProjectRecord project,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(project);
        string projectRoot = PathIdentity.Normalize(project.RootPath);

        await _deleteGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            Task<IReadOnlyList<ConversationRecord>> conversationsTask = _indexer.ScanAsync(
                project,
                cancellationToken: cancellationToken);
            Task<IReadOnlyList<CliInstallation>> installationsTask = _cliDetector.DetectAsync(
                cancellationToken: cancellationToken);

            await Task.WhenAll(conversationsTask, installationsTask).ConfigureAwait(false);
            ConversationRecord[] conversations = (await conversationsTask.ConfigureAwait(false))
                // Deletion is destructive, so do not rely solely on an indexer's
                // optional project filter. Recheck the native cwd before passing
                // any session id to an official CLI.
                .Where(conversation => MatchesProjectRoot(projectRoot, conversation))
                .ToArray();
            var installations = (await installationsTask.ConfigureAwait(false))
                .GroupBy(installation => installation.Kind)
                .ToDictionary(
                    group => group.Key,
                    group => group
                        .OrderByDescending(installation => installation.IsInstalled)
                        .ThenByDescending(installation => !string.IsNullOrWhiteSpace(installation.ExecutablePath))
                        .First());

            CliConversationDeletionResult codex = await DeleteCodexAsync(
                    projectRoot,
                    conversations.Where(item => item.NativeClient == CliKind.Codex).ToArray(),
                    installations.GetValueOrDefault(CliKind.Codex),
                    cancellationToken)
                .ConfigureAwait(false);
            CliConversationDeletionResult claude = await DeleteClaudeAsync(
                    projectRoot,
                    conversations.Where(item => item.NativeClient == CliKind.ClaudeCode).ToArray(),
                    installations.GetValueOrDefault(CliKind.ClaudeCode),
                    cancellationToken)
                .ConfigureAwait(false);
            CliConversationDeletionResult gemini = await DeleteGeminiAsync(
                    projectRoot,
                    conversations.Where(item => item.NativeClient == CliKind.GeminiCli).ToArray(),
                    installations.GetValueOrDefault(CliKind.GeminiCli),
                    cancellationToken)
                .ConfigureAwait(false);

            return new ConversationDeletionResult([codex, claude, gemini]);
        }
        finally
        {
            _deleteGate.Release();
        }
    }

    private async Task<CliConversationDeletionResult> DeleteCodexAsync(
        string projectRoot,
        IReadOnlyList<ConversationRecord> conversations,
        CliInstallation? installation,
        CancellationToken cancellationToken)
    {
        ConversationRecord[] sessions = DistinctSessions(conversations);
        if (sessions.Length == 0)
        {
            return Success(CliKind.Codex, 0, 0);
        }

        if (!CanRun(installation))
        {
            return Failure(
                CliKind.Codex,
                sessions.Length,
                0,
                "Codex 删除命令",
                "未检测到支持会话删除的 Codex CLI；项目记录已保留。");
        }

        var issues = new List<ConversationDeletionIssue>();
        int deleted = 0;
        foreach (ConversationRecord session in sessions)
        {
            cancellationToken.ThrowIfCancellationRequested();
            OfficialCliCommandResult result = await _commandRunner.RunAsync(
                    installation!.ExecutablePath!,
                    ["delete", "--force", session.NativeSessionId],
                    projectRoot,
                    environment: null,
                    CommandTimeout,
                    cancellationToken)
                .ConfigureAwait(false);
            if (result.Succeeded)
            {
                deleted++;
                continue;
            }

            issues.Add(new ConversationDeletionIssue(
                CliKind.Codex,
                $"会话 {ShortId(session.NativeSessionId)}",
                SafeFailure(result)));
        }

        return new CliConversationDeletionResult(CliKind.Codex, sessions.Length, deleted, issues);
    }

    private async Task<CliConversationDeletionResult> DeleteClaudeAsync(
        string projectRoot,
        IReadOnlyList<ConversationRecord> conversations,
        CliInstallation? installation,
        CancellationToken cancellationToken)
    {
        ConversationRecord[] sessions = DistinctSessions(conversations);
        if (sessions.Length == 0)
        {
            // Claude's project-level purge may resolve an otherwise unrelated
            // child directory to a configured parent project. With no indexed
            // Claude conversation to delete, invoking it creates collateral risk
            // without removing any user-visible history.
            return Success(CliKind.ClaudeCode, 0, 0);
        }

        if (!CanRun(installation))
        {
            return Failure(
                CliKind.ClaudeCode,
                sessions.Length,
                0,
                "Claude Code 项目状态",
                "未检测到支持 project purge 的 Claude Code CLI；项目记录已保留。");
        }

        OfficialCliCommandResult dryRun = await _commandRunner.RunAsync(
                installation!.ExecutablePath!,
                ["project", "purge", "--dry-run", projectRoot],
                projectRoot,
                environment: null,
                CommandTimeout,
                cancellationToken)
            .ConfigureAwait(false);

        if (!dryRun.Succeeded && IsNoClaudeProjectState(dryRun))
        {
            return sessions.Length == 0
                ? Success(CliKind.ClaudeCode, 0, 0)
                : Failure(
                    CliKind.ClaudeCode,
                    sessions.Length,
                    0,
                    "Claude Code 清理预检",
                    "官方 CLI 报告没有该项目状态，但本机仍索引到 Claude Code 会话；为避免漏删，项目记录已保留。");
        }

        if (!dryRun.Succeeded)
        {
            return Failure(
                CliKind.ClaudeCode,
                sessions.Length,
                0,
                "Claude Code 清理预检",
                SafeFailure(dryRun));
        }

        if (TryFindMismatchedClaudeConfigPath(dryRun, projectRoot, out string? referencedPath))
        {
            return Failure(
                CliKind.ClaudeCode,
                sessions.Length,
                0,
                "Claude Code 清理范围验证",
                $"官方 purge 计划会同时操作另一个项目配置（{referencedPath}）；为避免误删其他项目，当前项目记录已保留。");
        }

        OfficialCliCommandResult purge = await _commandRunner.RunAsync(
                installation.ExecutablePath!,
                ["project", "purge", "--yes", projectRoot],
                projectRoot,
                environment: null,
                CommandTimeout,
                cancellationToken)
            .ConfigureAwait(false);

        if (!purge.Succeeded)
        {
            return Failure(
                CliKind.ClaudeCode,
                sessions.Length,
                0,
                "Claude Code 项目状态",
                SafeFailure(purge));
        }

        OfficialCliCommandResult verification = await _commandRunner.RunAsync(
                installation.ExecutablePath!,
                ["project", "purge", "--dry-run", projectRoot],
                projectRoot,
                environment: null,
                CommandTimeout,
                cancellationToken)
            .ConfigureAwait(false);
        if (!verification.Succeeded && IsNoClaudeProjectState(verification))
        {
            return Success(CliKind.ClaudeCode, sessions.Length, sessions.Length);
        }

        return Failure(
            CliKind.ClaudeCode,
            sessions.Length,
            0,
            "Claude Code 清理验证",
            verification.Succeeded
                ? "执行 project purge 后仍检测到该项目的官方状态；项目记录已保留。"
                : SafeFailure(verification));
    }

    private async Task<CliConversationDeletionResult> DeleteGeminiAsync(
        string projectRoot,
        IReadOnlyList<ConversationRecord> conversations,
        CliInstallation? installation,
        CancellationToken cancellationToken)
    {
        ConversationRecord[] sessions = DistinctSessions(conversations);
        if (sessions.Length == 0)
        {
            return Success(CliKind.GeminiCli, 0, 0);
        }

        if (!Directory.Exists(projectRoot))
        {
            return DeleteGeminiFilesForMissingProject(projectRoot, sessions, cancellationToken);
        }

        if (!CanRun(installation))
        {
            return Failure(
                CliKind.GeminiCli,
                sessions.Length,
                0,
                "Gemini CLI 删除命令",
                "未检测到支持 --delete-session 的 Gemini CLI；项目记录已保留。");
        }

        var issues = new List<ConversationDeletionIssue>();
        int deleted = 0;
        foreach (ConversationRecord session in sessions)
        {
            cancellationToken.ThrowIfCancellationRequested();
            OfficialCliCommandResult result = await _commandRunner.RunAsync(
                    installation!.ExecutablePath!,
                    ["--delete-session", session.NativeSessionId],
                    projectRoot,
                    new Dictionary<string, string?>
                    {
                        // Gemini 0.43 initializes authentication before its local
                        // deletion handler. This child-only placeholder prevents
                        // that check and is never used for a network request.
                        ["GEMINI_API_KEY"] = "local-session-deletion",
                    },
                    CommandTimeout,
                    cancellationToken)
                .ConfigureAwait(false);
            if (result.Succeeded && GeminiDeletionConfirmed(result))
            {
                deleted++;
                continue;
            }

            issues.Add(new ConversationDeletionIssue(
                CliKind.GeminiCli,
                $"会话 {ShortId(session.NativeSessionId)}",
                result.Succeeded
                    ? "Gemini CLI 未确认指定会话已删除；项目记录已保留。"
                    : SafeFailure(result)));
        }

        return new CliConversationDeletionResult(CliKind.GeminiCli, sessions.Length, deleted, issues);
    }

    private CliConversationDeletionResult DeleteGeminiFilesForMissingProject(
        string projectRoot,
        IReadOnlyList<ConversationRecord> sessions,
        CancellationToken cancellationToken)
    {
        var sessionIds = sessions
            .Select(session => session.NativeSessionId)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);
        var matchedFiles = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
        var projectDirectories = new Dictionary<string, HashSet<string>>(StringComparer.OrdinalIgnoreCase);
        var logsFiles = new HashSet<string>(
            OperatingSystem.IsWindows() ? StringComparer.OrdinalIgnoreCase : StringComparer.Ordinal);

        foreach (string filePath in EnumerateFilesSafely(_paths.GeminiProjectsDirectory, "session-*.jsonl"))
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!TryReadGeminiSessionId(filePath, out string? sessionId) ||
                !sessionIds.Contains(sessionId))
            {
                continue;
            }

            string? chatsDirectory = Path.GetDirectoryName(filePath);
            string? projectDirectory = chatsDirectory is null
                ? null
                : Directory.GetParent(chatsDirectory)?.FullName;
            string? storedRoot = projectDirectory is null
                ? null
                : ReadSmallTextFile(Path.Combine(projectDirectory, ".project_root"));
            if (!PathsEqual(projectRoot, storedRoot))
            {
                continue;
            }

            if (!matchedFiles.TryGetValue(sessionId, out List<string>? paths))
            {
                paths = [];
                matchedFiles[sessionId] = paths;
            }

            paths.Add(filePath);
            if (projectDirectory is not null)
            {
                if (!projectDirectories.TryGetValue(sessionId, out HashSet<string>? directories))
                {
                    directories = new HashSet<string>(PathComparer);
                    projectDirectories[sessionId] = directories;
                }

                directories.Add(projectDirectory);
                logsFiles.Add(Path.Combine(projectDirectory, "logs.json"));
            }
        }

        var issues = new List<ConversationDeletionIssue>();
        foreach (string logsFile in logsFiles)
        {
            cancellationToken.ThrowIfCancellationRequested();
            try
            {
                RemoveGeminiLogEntries(logsFile, sessionIds);
            }
            catch (Exception exception) when (
                IsRecoverableFileException(exception) || exception is JsonException)
            {
                issues.Add(new ConversationDeletionIssue(
                    CliKind.GeminiCli,
                    "项目会话索引",
                    exception.Message));
            }
        }

        if (issues.Count > 0)
        {
            return new CliConversationDeletionResult(CliKind.GeminiCli, sessions.Count, 0, issues);
        }

        int deleted = 0;
        foreach (ConversationRecord session in sessions)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!matchedFiles.TryGetValue(session.NativeSessionId, out List<string>? paths) || paths.Count == 0)
            {
                issues.Add(new ConversationDeletionIssue(
                    CliKind.GeminiCli,
                    $"会话 {ShortId(session.NativeSessionId)}",
                    "原项目目录已不存在，且未能安全定位本地会话文件。"));
                continue;
            }

            bool sessionDeleted = true;
            if (projectDirectories.TryGetValue(
                    session.NativeSessionId,
                    out HashSet<string>? sessionProjectDirectories))
            {
                foreach (string projectDirectory in sessionProjectDirectories)
                {
                    try
                    {
                        DeleteGeminiSessionArtifacts(projectDirectory, session.NativeSessionId);
                    }
                    catch (Exception exception) when (IsRecoverableFileException(exception))
                    {
                        sessionDeleted = false;
                        issues.Add(new ConversationDeletionIssue(
                            CliKind.GeminiCli,
                            $"会话 {ShortId(session.NativeSessionId)} 附属文件",
                            exception.Message));
                    }
                }
            }

            foreach (string path in paths)
            {
                try
                {
                    File.Delete(path);
                }
                catch (Exception exception) when (IsRecoverableFileException(exception))
                {
                    sessionDeleted = false;
                    issues.Add(new ConversationDeletionIssue(
                        CliKind.GeminiCli,
                        $"会话 {ShortId(session.NativeSessionId)}",
                        exception.Message));
                }
            }

            if (sessionDeleted)
            {
                deleted++;
            }
        }

        return new CliConversationDeletionResult(CliKind.GeminiCli, sessions.Count, deleted, issues);
    }

    private static void DeleteGeminiSessionArtifacts(string projectDirectory, string sessionId)
    {
        if (!IsSafeFileSegment(sessionId))
        {
            throw new IOException("Gemini 会话标识包含不安全字符，已阻止文件回退删除。");
        }

        DeleteFileWithinRoot(
            projectDirectory,
            Path.Combine(projectDirectory, "logs", $"session-{sessionId}.jsonl"));
        DeleteDirectoryWithinRoot(
            projectDirectory,
            Path.Combine(projectDirectory, "tool-outputs", $"session-{sessionId}"));
        DeleteDirectoryWithinRoot(projectDirectory, Path.Combine(projectDirectory, sessionId));
        DeleteDirectoryWithinRoot(
            projectDirectory,
            Path.Combine(projectDirectory, "chats", sessionId));
    }

    private static void DeleteFileWithinRoot(string root, string path)
    {
        EnsurePathWithinRoot(root, path);
        if (File.Exists(path))
        {
            File.Delete(path);
        }
    }

    private static void DeleteDirectoryWithinRoot(string root, string path)
    {
        EnsurePathWithinRoot(root, path);
        if (Directory.Exists(path))
        {
            Directory.Delete(path, recursive: true);
        }
    }

    private static void EnsurePathWithinRoot(string root, string path)
    {
        string normalizedRoot = Path.TrimEndingDirectorySeparator(Path.GetFullPath(root));
        string normalizedPath = Path.GetFullPath(path);
        StringComparison comparison = OperatingSystem.IsWindows()
            ? StringComparison.OrdinalIgnoreCase
            : StringComparison.Ordinal;
        if (!normalizedPath.StartsWith(normalizedRoot + Path.DirectorySeparatorChar, comparison))
        {
            throw new IOException("Gemini 附属文件路径越出项目历史目录，已阻止删除。");
        }
    }

    private static bool IsSafeFileSegment(string value)
        => value.Length > 0 &&
           value is not "." and not ".." &&
           value.All(character =>
               char.IsAsciiLetterOrDigit(character) || character is '-' or '_' or '.');

    private static void RemoveGeminiLogEntries(string path, IReadOnlySet<string> sessionIds)
    {
        if (!File.Exists(path))
        {
            return;
        }

        string directory = Path.GetDirectoryName(path)
            ?? throw new IOException("Gemini 会话索引路径无效。");
        string temporaryPath = Path.Combine(directory, $".{Path.GetFileName(path)}.{Guid.NewGuid():N}.tmp");
        bool changed = false;
        try
        {
            using (FileStream input = OpenSharedRead(path))
            using (JsonDocument document = JsonDocument.Parse(input))
            {
                if (document.RootElement.ValueKind != JsonValueKind.Array)
                {
                    throw new JsonException("Gemini 会话索引不是预期的数组格式。");
                }

                using var output = new FileStream(
                    temporaryPath,
                    FileMode.CreateNew,
                    FileAccess.Write,
                    FileShare.None);
                using var writer = new Utf8JsonWriter(output, new JsonWriterOptions { Indented = true });
                writer.WriteStartArray();
                foreach (JsonElement entry in document.RootElement.EnumerateArray())
                {
                    string? sessionId = entry.ValueKind == JsonValueKind.Object &&
                                        entry.TryGetProperty("sessionId", out JsonElement value) &&
                                        value.ValueKind == JsonValueKind.String
                        ? value.GetString()
                        : null;
                    if (sessionId is not null && sessionIds.Contains(sessionId))
                    {
                        changed = true;
                        continue;
                    }

                    entry.WriteTo(writer);
                }

                writer.WriteEndArray();
                writer.Flush();
            }

            if (changed)
            {
                File.Move(temporaryPath, path, overwrite: true);
            }
        }
        finally
        {
            try
            {
                if (File.Exists(temporaryPath))
                {
                    File.Delete(temporaryPath);
                }
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                // A stale temporary file is harmless and can be cleaned later.
            }
        }
    }

    private static ConversationRecord[] DistinctSessions(IReadOnlyList<ConversationRecord> conversations)
        => conversations
            .Where(session => !string.IsNullOrWhiteSpace(session.NativeSessionId))
            .GroupBy(session => session.NativeSessionId, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.OrderByDescending(session => session.UpdatedAt).First())
            .ToArray();

    private static bool CanRun(CliInstallation? installation)
        => installation is { IsInstalled: true, ExecutablePath.Length: > 0 };

    private static CliConversationDeletionResult Success(CliKind client, int matched, int deleted)
        => new(client, matched, deleted, Array.Empty<ConversationDeletionIssue>());

    private static CliConversationDeletionResult Failure(
        CliKind client,
        int matched,
        int deleted,
        string item,
        string message)
        => new(client, matched, deleted, [new ConversationDeletionIssue(client, item, message)]);

    private static bool IsNoClaudeProjectState(OfficialCliCommandResult result)
        => CombinedOutput(result).Contains(
            "No Claude Code project state found",
            StringComparison.OrdinalIgnoreCase);

    private static bool TryFindMismatchedClaudeConfigPath(
        OfficialCliCommandResult result,
        string projectRoot,
        out string? mismatchedPath)
    {
        mismatchedPath = null;
        foreach (Match match in Regex.Matches(
                     CombinedOutput(result),
                     "projects\\[\\\"(?<path>(?:\\\\.|[^\\\"])*)\\\"\\]",
                     RegexOptions.CultureInvariant))
        {
            string captured = match.Groups["path"].Value;
            string candidate;
            try
            {
                candidate = JsonSerializer.Deserialize<string>($"\"{captured}\"") ?? captured;
            }
            catch (JsonException)
            {
                candidate = captured;
            }

            if (!PathsEqual(projectRoot, candidate))
            {
                mismatchedPath = candidate;
                return true;
            }
        }

        return false;
    }

    private static bool GeminiDeletionConfirmed(OfficialCliCommandResult result)
        => CombinedOutput(result).Contains("Deleted session", StringComparison.OrdinalIgnoreCase);

    private static string SafeFailure(OfficialCliCommandResult result)
    {
        if (result.TimedOut)
        {
            return "官方 CLI 删除命令执行超时。";
        }

        string? line = CombinedOutput(result)
            .Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .FirstOrDefault();
        if (string.IsNullOrWhiteSpace(line))
        {
            return $"官方 CLI 删除命令失败（退出码 {result.ExitCode}）。";
        }

        return line.Length <= 240 ? line : line[..240] + "…";
    }

    private static string CombinedOutput(OfficialCliCommandResult result)
        => string.Join(Environment.NewLine, result.StandardOutput, result.StandardError);

    private static string ShortId(string value)
        => value.Length <= 8 ? value : value[..8];

    private static bool TryReadGeminiSessionId(string filePath, out string sessionId)
    {
        sessionId = string.Empty;
        try
        {
            using var stream = OpenSharedRead(filePath);
            using var reader = new StreamReader(stream, Encoding.UTF8, true, 4096, false);
            string? line = reader.ReadLine();
            if (!string.IsNullOrWhiteSpace(line))
            {
                using JsonDocument document = JsonDocument.Parse(line);
                if (document.RootElement.ValueKind == JsonValueKind.Object &&
                    document.RootElement.TryGetProperty("sessionId", out JsonElement value) &&
                    value.ValueKind == JsonValueKind.String &&
                    !string.IsNullOrWhiteSpace(value.GetString()))
                {
                    sessionId = value.GetString()!;
                    return true;
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception) || exception is JsonException)
        {
            // Gemini's official filename also carries the exact session id.
            // Fall through to that narrow convention when metadata is absent
            // or partially written.
        }

        const string prefix = "session-";
        string fileName = Path.GetFileNameWithoutExtension(filePath);
        if (!fileName.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        sessionId = fileName[prefix.Length..];
        return IsSafeFileSegment(sessionId);
    }

    private static bool MatchesProjectRoot(string projectRoot, ConversationRecord conversation)
        => !string.IsNullOrWhiteSpace(conversation.OriginalWorkingDirectory) &&
           PathsEqual(projectRoot, conversation.OriginalWorkingDirectory);

    private static StringComparer PathComparer => OperatingSystem.IsWindows()
        ? StringComparer.OrdinalIgnoreCase
        : StringComparer.Ordinal;

    private static string? ReadSmallTextFile(string path)
    {
        try
        {
            if (!File.Exists(path))
            {
                return null;
            }

            using var stream = OpenSharedRead(path);
            using var reader = new StreamReader(stream, Encoding.UTF8, true, 1024, false);
            var buffer = new char[32 * 1024 + 1];
            int count = reader.ReadBlock(buffer, 0, buffer.Length);
            return count > 32 * 1024 ? null : new string(buffer, 0, count).Trim();
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return null;
        }
    }

    private static bool PathsEqual(string left, string? right)
    {
        if (string.IsNullOrWhiteSpace(right))
        {
            return false;
        }

        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows() ? StringComparison.OrdinalIgnoreCase : StringComparison.Ordinal);
        }
        catch (Exception exception) when (exception is ArgumentException or NotSupportedException)
        {
            return false;
        }
    }

    private static IEnumerable<string> EnumerateFilesSafely(string root, string pattern)
    {
        if (!Directory.Exists(root))
        {
            yield break;
        }

        var pending = new Stack<string>();
        pending.Push(root);
        while (pending.Count > 0)
        {
            string directory = pending.Pop();
            string[] files;
            string[] directories;
            try
            {
                files = Directory.GetFiles(directory, pattern, SearchOption.TopDirectoryOnly);
                directories = Directory.GetDirectories(directory, "*", SearchOption.TopDirectoryOnly);
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                continue;
            }

            foreach (string file in files)
            {
                yield return file;
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
                    // Continue with accessible siblings.
                }
            }
        }
    }

    private static FileStream OpenSharedRead(string path)
        => new(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);

    private static bool IsRecoverableFileException(Exception exception)
        => exception is IOException or UnauthorizedAccessException or System.Security.SecurityException;
}

internal interface IOfficialCliCommandRunner
{
    Task<OfficialCliCommandResult> RunAsync(
        string executablePath,
        IReadOnlyList<string> arguments,
        string workingDirectory,
        IReadOnlyDictionary<string, string?>? environment,
        TimeSpan timeout,
        CancellationToken cancellationToken);
}

internal sealed record OfficialCliCommandResult(
    int ExitCode,
    string StandardOutput,
    string StandardError,
    bool TimedOut = false)
{
    public bool Succeeded => !TimedOut && ExitCode == 0;
}

internal sealed class OfficialCliCommandRunner : IOfficialCliCommandRunner
{
    public async Task<OfficialCliCommandResult> RunAsync(
        string executablePath,
        IReadOnlyList<string> arguments,
        string workingDirectory,
        IReadOnlyDictionary<string, string?>? environment,
        TimeSpan timeout,
        CancellationToken cancellationToken)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(executablePath);
        if (timeout <= TimeSpan.Zero)
        {
            throw new ArgumentOutOfRangeException(nameof(timeout));
        }

        var process = new Process
        {
            StartInfo = CreateStartInfo(executablePath, arguments, workingDirectory, environment),
        };

        try
        {
            if (!process.Start())
            {
                return new OfficialCliCommandResult(-1, string.Empty, "无法启动官方 CLI。");
            }

            Task<string> outputTask = process.StandardOutput.ReadToEndAsync(cancellationToken);
            Task<string> errorTask = process.StandardError.ReadToEndAsync(cancellationToken);
            using var timeoutSource = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeoutSource.CancelAfter(timeout);

            try
            {
                await process.WaitForExitAsync(timeoutSource.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                TryKill(process);
                return new OfficialCliCommandResult(
                    -1,
                    await ObserveOutputAsync(outputTask).ConfigureAwait(false),
                    await ObserveOutputAsync(errorTask).ConfigureAwait(false),
                    TimedOut: true);
            }

            return new OfficialCliCommandResult(
                process.ExitCode,
                await outputTask.ConfigureAwait(false),
                await errorTask.ConfigureAwait(false));
        }
        catch (OperationCanceledException)
        {
            TryKill(process);
            throw;
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or System.ComponentModel.Win32Exception or IOException)
        {
            TryKill(process);
            return new OfficialCliCommandResult(-1, string.Empty, exception.Message);
        }
        finally
        {
            process.Dispose();
        }
    }

    private static ProcessStartInfo CreateStartInfo(
        string executablePath,
        IReadOnlyList<string> arguments,
        string workingDirectory,
        IReadOnlyDictionary<string, string?>? environment)
    {
        bool commandScript = OperatingSystem.IsWindows() &&
            (executablePath.EndsWith(".cmd", StringComparison.OrdinalIgnoreCase) ||
             executablePath.EndsWith(".bat", StringComparison.OrdinalIgnoreCase));
        var startInfo = new ProcessStartInfo
        {
            FileName = commandScript
                ? Environment.GetEnvironmentVariable("ComSpec") ?? "cmd.exe"
                : executablePath,
            WorkingDirectory = Directory.Exists(workingDirectory)
                ? workingDirectory
                : Environment.CurrentDirectory,
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            RedirectStandardInput = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8,
        };

        if (commandScript)
        {
            string commandLine = string.Join(
                " ",
                new[] { executablePath }.Concat(arguments).Select(QuoteForCmd));
            startInfo.Arguments = $"/d /s /c \"{commandLine}\"";
        }
        else
        {
            foreach (string argument in arguments)
            {
                startInfo.ArgumentList.Add(argument);
            }
        }

        if (environment is not null)
        {
            foreach ((string key, string? value) in environment)
            {
                startInfo.Environment[key] = value;
            }
        }

        if (OperatingSystem.IsWindows() && string.IsNullOrWhiteSpace(startInfo.Environment["WINDIR"]))
        {
            startInfo.Environment["WINDIR"] = Environment.GetFolderPath(Environment.SpecialFolder.Windows);
        }

        return startInfo;
    }

    private static string QuoteForCmd(string value)
        => $"\"{value.Replace("%", "%%", StringComparison.Ordinal).Replace("\"", "\"\"", StringComparison.Ordinal)}\"";

    private static async Task<string> ObserveOutputAsync(Task<string> outputTask)
    {
        try
        {
            return await outputTask.WaitAsync(TimeSpan.FromSeconds(2)).ConfigureAwait(false);
        }
        catch
        {
            return string.Empty;
        }
    }

    private static void TryKill(Process process)
    {
        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
        }
        catch
        {
            // The process may have exited between the check and the kill call.
        }
    }
}
