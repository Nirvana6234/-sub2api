using System.IO;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using Microsoft.Data.Sqlite;

namespace AiSwitch.Wpf.Tests;

public sealed class ProjectDeletionTests
{
    [Fact]
    public async Task OfficialDeletion_UsesSupportedCliCommandsAndKeepsSourceDirectory()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        ConversationRecord[] conversations =
        [
            CreateConversation(project, CliKind.Codex, "11111111-1111-1111-1111-111111111111"),
            CreateConversation(project, CliKind.ClaudeCode, "22222222-2222-2222-2222-222222222222"),
            CreateConversation(project, CliKind.GeminiCli, "33333333-3333-3333-3333-333333333333"),
        ];
        var runner = CreateSuccessfulCommandRunner();
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(conversations),
            new StubCliDetector(CreateInstallations()),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.True(result.Succeeded);
        Assert.Equal(3, result.DeletedCount);
        Assert.True(Directory.Exists(projectRoot));
        Assert.Contains(runner.Commands, command =>
            command.ExecutablePath == "codex.exe" &&
            command.Arguments.SequenceEqual(
                new[] { "delete", "--force", "11111111-1111-1111-1111-111111111111" }));
        Assert.Contains(runner.Commands, command =>
            command.ExecutablePath == "claude.exe" &&
            command.Arguments.SequenceEqual(new[] { "project", "purge", "--dry-run", projectRoot }));
        Assert.Contains(runner.Commands, command =>
            command.ExecutablePath == "claude.exe" &&
            command.Arguments.SequenceEqual(new[] { "project", "purge", "--yes", projectRoot }));
        Assert.Equal(2, runner.Commands.Count(command =>
            command.ExecutablePath == "claude.exe" &&
            command.Arguments.SequenceEqual(new[] { "project", "purge", "--dry-run", projectRoot })));
        Assert.Contains(runner.Commands, command =>
            command.ExecutablePath == "gemini.exe" &&
            command.Arguments.SequenceEqual(
                new[] { "--delete-session", "33333333-3333-3333-3333-333333333333" }));
    }

    [Fact]
    public async Task OfficialDeletion_DefensivelyRejectsSessionsFromAnotherWorkingDirectory()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        string otherRoot = fixture.CreateDirectory("other-project");
        ProjectRecord project = CreateProject(projectRoot);
        ProjectRecord otherProject = CreateProject(otherRoot);
        const string targetCodex = "11111111-1111-1111-1111-111111111111";
        const string otherCodex = "22222222-2222-2222-2222-222222222222";
        const string targetGemini = "33333333-3333-3333-3333-333333333333";
        const string otherGemini = "44444444-4444-4444-4444-444444444444";
        var runner = CreateSuccessfulCommandRunner();
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            // The stub intentionally ignores the project filter. The destructive
            // service must still reject sessions whose native cwd is different.
            new StubConversationIndexer(
            [
                CreateConversation(project, CliKind.Codex, targetCodex),
                CreateConversation(otherProject, CliKind.Codex, otherCodex),
                CreateConversation(project, CliKind.GeminiCli, targetGemini),
                CreateConversation(otherProject, CliKind.GeminiCli, otherGemini),
            ]),
            new StubCliDetector(CreateInstallations()),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.True(result.Succeeded);
        Assert.Equal(2, result.DeletedCount);
        Assert.Contains(runner.Commands, command => command.Arguments.Contains(targetCodex));
        Assert.Contains(runner.Commands, command => command.Arguments.Contains(targetGemini));
        Assert.DoesNotContain(runner.Commands, command => command.Arguments.Contains(otherCodex));
        Assert.DoesNotContain(runner.Commands, command => command.Arguments.Contains(otherGemini));
    }

    [Fact]
    public async Task ClaudeDryRun_NoStateIsFailureWhenIndexedConversationStillExists()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var runner = new RecordingCommandRunner
        {
            OnRun = _ => new OfficialCliCommandResult(
                1,
                string.Empty,
                $"No Claude Code project state found for {projectRoot}."),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.ClaudeCode, "22222222-2222-2222-2222-222222222222")]),
            new StubCliDetector([CreateInstallation(CliKind.ClaudeCode, "claude.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.False(result.Succeeded);
        CliConversationDeletionResult claude = Assert.Single(
            result.Clients,
            client => client.Client == CliKind.ClaudeCode);
        Assert.Equal(1, claude.MatchedCount);
        Assert.Equal(0, claude.DeletedCount);
        Assert.Single(runner.Commands);
        Assert.DoesNotContain(runner.Commands, command => command.Arguments.Contains("--yes"));
    }

    [Fact]
    public async Task ClaudeDeletion_WithNoIndexedConversationSkipsProjectPurge()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory(Path.Combine("configured-parent", "new-child-project"));
        ProjectRecord project = CreateProject(projectRoot);
        var runner = new RecordingCommandRunner
        {
            OnRun = _ => throw new InvalidOperationException(
                "Claude project purge must not run when no Claude conversation matched."),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(Array.Empty<ConversationRecord>()),
            new StubCliDetector([CreateInstallation(CliKind.ClaudeCode, "claude.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.True(result.Succeeded);
        Assert.Empty(runner.Commands);
        CliConversationDeletionResult claude = Assert.Single(
            result.Clients,
            client => client.Client == CliKind.ClaudeCode);
        Assert.Equal(0, claude.MatchedCount);
        Assert.Equal(0, claude.DeletedCount);
    }

    [Fact]
    public async Task ClaudePurge_MustVerifyOfficialStateIsGone()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var runner = new RecordingCommandRunner
        {
            OnRun = _ => new OfficialCliCommandResult(0, "Purge plan still contains one item.", string.Empty),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.ClaudeCode, "22222222-2222-2222-2222-222222222222")]),
            new StubCliDetector([CreateInstallation(CliKind.ClaudeCode, "claude.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.False(result.Succeeded);
        Assert.Equal(3, runner.Commands.Count);
        Assert.Contains(result.Issues, issue => issue.Item == "Claude Code 清理验证");
    }

    [Fact]
    public async Task ClaudePurge_RejectsPlanThatWouldTouchAnotherProjectConfiguration()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory(Path.Combine("parent", "source-project"));
        string parentRoot = Directory.GetParent(projectRoot)!.FullName;
        ProjectRecord project = CreateProject(projectRoot);
        var runner = new RecordingCommandRunner
        {
            OnRun = _ => new OfficialCliCommandResult(
                0,
                $"Purge plan for {projectRoot}:\n  config: projects[\"{parentRoot.Replace('\\', '/')}\"]",
                string.Empty),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.ClaudeCode, "22222222-2222-2222-2222-222222222222")]),
            new StubCliDetector([CreateInstallation(CliKind.ClaudeCode, "claude.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.False(result.Succeeded);
        Assert.Single(runner.Commands);
        Assert.DoesNotContain(runner.Commands, command => command.Arguments.Contains("--yes"));
        Assert.Contains(result.Issues, issue => issue.Item == "Claude Code 清理范围验证");
    }

    [Fact]
    public async Task GeminiDeletion_RequiresPositiveOfficialConfirmationEvenWithZeroExitCode()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var runner = new RecordingCommandRunner
        {
            OnRun = _ => new OfficialCliCommandResult(0, "No sessions found for this project.", string.Empty),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.GeminiCli, "33333333-3333-3333-3333-333333333333")]),
            new StubCliDetector([CreateInstallation(CliKind.GeminiCli, "gemini.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.False(result.Succeeded);
        Assert.Equal(0, result.DeletedCount);
        Assert.Contains(result.Issues, issue => issue.Client == CliKind.GeminiCli);
    }

    [Fact]
    public async Task GeminiMissingProject_FallbackDeletesOnlyMatchedNativeSessionAndLogEntries()
    {
        using var fixture = new TemporaryWorkspace();
        string missingProject = Path.Combine(fixture.Root, "deleted-source-project");
        ProjectRecord project = CreateProject(missingProject);
        const string targetSession = "33333333-3333-3333-3333-333333333333";
        const string keptSession = "44444444-4444-4444-4444-444444444444";
        string geminiProject = fixture.CreateDirectory(
            Path.Combine("user", ".gemini", "tmp", "project-hash"));
        string chats = Directory.CreateDirectory(Path.Combine(geminiProject, "chats")).FullName;
        File.WriteAllText(Path.Combine(geminiProject, ".project_root"), missingProject);
        string targetFile = Path.Combine(chats, "session-target.jsonl");
        string keptFile = Path.Combine(chats, "session-kept.jsonl");
        File.WriteAllText(targetFile, JsonSerializer.Serialize(new { sessionId = targetSession }) + "\n");
        File.WriteAllText(keptFile, JsonSerializer.Serialize(new { sessionId = keptSession }) + "\n");
        string nativeLog = Path.Combine(geminiProject, "logs", $"session-{targetSession}.jsonl");
        Directory.CreateDirectory(Path.GetDirectoryName(nativeLog)!);
        File.WriteAllText(nativeLog, "{}\n");
        string toolOutputs = Directory.CreateDirectory(
            Path.Combine(geminiProject, "tool-outputs", $"session-{targetSession}")).FullName;
        File.WriteAllText(Path.Combine(toolOutputs, "output.txt"), "test");
        string sessionArtifacts = Directory.CreateDirectory(Path.Combine(geminiProject, targetSession)).FullName;
        string subagents = Directory.CreateDirectory(Path.Combine(chats, targetSession)).FullName;
        File.WriteAllText(
            Path.Combine(geminiProject, "logs.json"),
            JsonSerializer.Serialize(new[]
            {
                new { sessionId = targetSession, message = "target" },
                new { sessionId = keptSession, message = "keep" },
            }));

        var runner = new RecordingCommandRunner
        {
            OnRun = _ => throw new InvalidOperationException("Missing-project fallback must not launch Gemini."),
        };
        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.GeminiCli, targetSession)]),
            new StubCliDetector(
                [CreateInstallation(CliKind.GeminiCli, "gemini.exe")]),
            runner);

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.True(result.Succeeded);
        Assert.Equal(1, result.DeletedCount);
        Assert.False(File.Exists(targetFile));
        Assert.True(File.Exists(keptFile));
        Assert.False(File.Exists(nativeLog));
        Assert.False(Directory.Exists(toolOutputs));
        Assert.False(Directory.Exists(sessionArtifacts));
        Assert.False(Directory.Exists(subagents));
        Assert.False(Directory.Exists(missingProject));
        using JsonDocument logs = JsonDocument.Parse(
            await File.ReadAllTextAsync(Path.Combine(geminiProject, "logs.json")));
        JsonElement remaining = Assert.Single(logs.RootElement.EnumerateArray());
        Assert.Equal(keptSession, remaining.GetProperty("sessionId").GetString());
        Assert.Empty(runner.Commands);
    }

    [Fact]
    public async Task GeminiMissingProject_FallbackUsesOfficialFilenameIdAndCleansDuplicateHistoryDirectories()
    {
        using var fixture = new TemporaryWorkspace();
        string missingProject = Path.Combine(fixture.Root, "deleted-source-project");
        ProjectRecord project = CreateProject(missingProject);
        const string targetSession = "55555555-5555-5555-5555-555555555555";
        var sessionFiles = new List<string>();
        var artifactDirectories = new List<string>();

        foreach (string historyName in new[] { "project-hash-a", "project-hash-b" })
        {
            string geminiProject = fixture.CreateDirectory(
                Path.Combine("user", ".gemini", "tmp", historyName));
            File.WriteAllText(Path.Combine(geminiProject, ".project_root"), missingProject);
            string chats = Directory.CreateDirectory(Path.Combine(geminiProject, "chats")).FullName;
            string sessionFile = Path.Combine(chats, $"session-{targetSession}.jsonl");
            // Some Gemini versions rely on the official session-* filename and
            // omit sessionId from the first metadata object.
            File.WriteAllText(sessionFile, JsonSerializer.Serialize(new { kind = "main" }) + "\n");
            sessionFiles.Add(sessionFile);
            artifactDirectories.Add(Directory.CreateDirectory(
                Path.Combine(geminiProject, "tool-outputs", $"session-{targetSession}")).FullName);
            artifactDirectories.Add(Directory.CreateDirectory(
                Path.Combine(geminiProject, targetSession)).FullName);
        }

        var service = new OfficialConversationDeletionService(
            fixture.Paths,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.GeminiCli, targetSession)]),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            new RecordingCommandRunner());

        ConversationDeletionResult result = await service.DeleteProjectConversationsAsync(project);

        Assert.True(result.Succeeded);
        Assert.Equal(1, result.DeletedCount);
        Assert.All(sessionFiles, path => Assert.False(File.Exists(path)));
        Assert.All(artifactDirectories, path => Assert.False(Directory.Exists(path)));
        Assert.False(Directory.Exists(missingProject));
    }

    [Fact]
    public async Task Load_DoesNotRediscoverConversationWhoseWorkingDirectoryWasDeleted()
    {
        using var fixture = new TemporaryWorkspace();
        string missingProject = Path.Combine(fixture.Root, "missing-project");
        ProjectRecord project = CreateProject(missingProject);
        var repository = new SqliteProjectRepository(fixture.Paths);
        using var legacy = new LegacyProfileReader(fixture.Paths);
        using var service = new WorkspaceDataService(
            repository,
            new StubConversationIndexer(
                [CreateConversation(project, CliKind.Codex, "session")]),
            new StubConversationDeletionService(succeeded: true),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            legacy);

        WorkspaceDataSnapshot snapshot = await service.LoadAsync();

        Assert.Empty(snapshot.Projects);
        Assert.Equal(0, snapshot.DiscoveredProjectCount);
        Assert.Null(await repository.GetByIdAsync(project.Id));
    }

    [Fact]
    public async Task DeleteProject_KeepsSqliteRecordWhenAnyOfficialHistoryDeletionFails()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var repository = new SqliteProjectRepository(fixture.Paths);
        await repository.UpsertAsync(project);
        using var legacy = new LegacyProfileReader(fixture.Paths);
        using var service = new WorkspaceDataService(
            repository,
            new StubConversationIndexer(Array.Empty<ConversationRecord>()),
            new StubConversationDeletionService(succeeded: false),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            legacy);

        ProjectDeletionResult result = await service.DeleteProjectAsync(project);

        Assert.False(result.Succeeded);
        Assert.False(result.ProjectRecordDeleted);
        Assert.NotNull(await repository.GetByIdAsync(project.Id));
        Assert.True(Directory.Exists(projectRoot));
    }

    [Fact]
    public async Task DeleteProject_RemovesOnlySqliteRecordAfterOfficialHistoryDeletionSucceeds()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var repository = new SqliteProjectRepository(fixture.Paths);
        await repository.UpsertAsync(project);
        using var legacy = new LegacyProfileReader(fixture.Paths);
        using var service = new WorkspaceDataService(
            repository,
            new StubConversationIndexer(Array.Empty<ConversationRecord>()),
            new StubConversationDeletionService(succeeded: true),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            legacy);

        ProjectDeletionResult result = await service.DeleteProjectAsync(project);

        Assert.True(result.Succeeded);
        Assert.True(result.ProjectRecordDeleted);
        Assert.Null(await repository.GetByIdAsync(project.Id));
        Assert.True(Directory.Exists(projectRoot));
    }

    [Fact]
    public async Task DeleteProject_KeepsSqliteRecordWhenDeletionResultOmitsAnOfficialClient()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var repository = new SqliteProjectRepository(fixture.Paths);
        await repository.UpsertAsync(project);
        var incompleteResult = new ConversationDeletionResult(
        [
            new CliConversationDeletionResult(
                CliKind.Codex,
                0,
                0,
                Array.Empty<ConversationDeletionIssue>()),
            new CliConversationDeletionResult(
                CliKind.ClaudeCode,
                0,
                0,
                Array.Empty<ConversationDeletionIssue>()),
        ]);
        using var legacy = new LegacyProfileReader(fixture.Paths);
        using var service = new WorkspaceDataService(
            repository,
            new StubConversationIndexer(Array.Empty<ConversationRecord>()),
            new StubConversationDeletionService(incompleteResult),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            legacy);

        ProjectDeletionResult result = await service.DeleteProjectAsync(project);

        Assert.False(result.Succeeded);
        Assert.False(result.ProjectRecordDeleted);
        Assert.NotNull(await repository.GetByIdAsync(project.Id));
    }

    [Fact]
    public async Task DeleteProject_KeepsSqliteRecordWhenMatchedAndDeletedCountsDifferWithoutIssue()
    {
        using var fixture = new TemporaryWorkspace();
        string projectRoot = fixture.CreateDirectory("source-project");
        ProjectRecord project = CreateProject(projectRoot);
        var repository = new SqliteProjectRepository(fixture.Paths);
        await repository.UpsertAsync(project);
        var mismatchedResult = new ConversationDeletionResult(
        [
            new CliConversationDeletionResult(
                CliKind.Codex,
                1,
                0,
                Array.Empty<ConversationDeletionIssue>()),
            new CliConversationDeletionResult(
                CliKind.ClaudeCode,
                0,
                0,
                Array.Empty<ConversationDeletionIssue>()),
            new CliConversationDeletionResult(
                CliKind.GeminiCli,
                0,
                0,
                Array.Empty<ConversationDeletionIssue>()),
        ]);
        using var legacy = new LegacyProfileReader(fixture.Paths);
        using var service = new WorkspaceDataService(
            repository,
            new StubConversationIndexer(Array.Empty<ConversationRecord>()),
            new StubConversationDeletionService(mismatchedResult),
            new StubCliDetector(Array.Empty<CliInstallation>()),
            legacy);

        ProjectDeletionResult result = await service.DeleteProjectAsync(project);

        Assert.False(result.Succeeded);
        Assert.False(result.ProjectRecordDeleted);
        Assert.NotNull(await repository.GetByIdAsync(project.Id));
    }

    private static ProjectRecord CreateProject(string rootPath)
    {
        string normalized = PathIdentity.Normalize(rootPath);
        string id = PathIdentity.CreateStableId(normalized);
        return new ProjectRecord
        {
            Id = id,
            DisplayName = "测试项目",
            RootPath = normalized,
            PathFingerprint = id,
            CreatedAt = DateTimeOffset.UtcNow,
        };
    }

    private static ConversationRecord CreateConversation(
        ProjectRecord project,
        CliKind client,
        string sessionId)
        => new()
        {
            Id = $"{client}:{sessionId}",
            ProjectId = project.PathFingerprint,
            NativeClient = client,
            NativeSessionId = sessionId,
            OriginalWorkingDirectory = project.RootPath,
            CreatedAt = DateTimeOffset.UtcNow.AddMinutes(-5),
            UpdatedAt = DateTimeOffset.UtcNow,
        };

    private static CliInstallation[] CreateInstallations()
        =>
        [
            CreateInstallation(CliKind.Codex, "codex.exe"),
            CreateInstallation(CliKind.ClaudeCode, "claude.exe"),
            CreateInstallation(CliKind.GeminiCli, "gemini.exe"),
        ];

    private static CliInstallation CreateInstallation(CliKind client, string path)
        => new()
        {
            Kind = client,
            IsInstalled = true,
            ExecutablePath = path,
            Version = "test",
            DetectedAt = DateTimeOffset.UtcNow,
        };

    private static RecordingCommandRunner CreateSuccessfulCommandRunner()
    {
        bool claudePurged = false;
        return new RecordingCommandRunner
        {
            OnRun = command =>
            {
                if (command.ExecutablePath == "claude.exe" && command.Arguments.Contains("--yes"))
                {
                    claudePurged = true;
                    return new OfficialCliCommandResult(0, "Purged project state.", string.Empty);
                }

                if (command.ExecutablePath == "claude.exe" &&
                    command.Arguments.Contains("--dry-run") &&
                    claudePurged)
                {
                    return new OfficialCliCommandResult(
                        1,
                        string.Empty,
                        "No Claude Code project state found for requested path.");
                }

                if (command.ExecutablePath == "gemini.exe")
                {
                    return new OfficialCliCommandResult(0, "Deleted session 1.", string.Empty);
                }

                return new OfficialCliCommandResult(0, "Command succeeded.", string.Empty);
            },
        };
    }

    private sealed class StubConversationIndexer : IConversationIndexer
    {
        private readonly IReadOnlyList<ConversationRecord> _conversations;

        public StubConversationIndexer(IReadOnlyList<ConversationRecord> conversations)
            => _conversations = conversations;

        public Task<IReadOnlyList<ConversationRecord>> ScanAsync(
            ProjectRecord? project = null,
            CliKind? client = null,
            CancellationToken cancellationToken = default)
        {
            IReadOnlyList<ConversationRecord> result = _conversations
                .Where(conversation => client is null || conversation.NativeClient == client)
                .ToArray();
            return Task.FromResult(result);
        }
    }

    private sealed class StubCliDetector : ICliDetector
    {
        private readonly IReadOnlyList<CliInstallation> _installations;

        public StubCliDetector(IReadOnlyList<CliInstallation> installations)
            => _installations = installations;

        public Task<IReadOnlyList<CliInstallation>> DetectAsync(
            CliKind? cli = null,
            CancellationToken cancellationToken = default)
        {
            IReadOnlyList<CliInstallation> result = _installations
                .Where(installation => cli is null || installation.Kind == cli)
                .ToArray();
            return Task.FromResult(result);
        }
    }

    private sealed class StubConversationDeletionService : IConversationDeletionService
    {
        private readonly ConversationDeletionResult _result;

        public StubConversationDeletionService(bool succeeded)
        {
            IReadOnlyList<ConversationDeletionIssue> issues = succeeded
                ? Array.Empty<ConversationDeletionIssue>()
                : [new ConversationDeletionIssue(CliKind.Codex, "会话 test", "模拟失败")];
            _result = new ConversationDeletionResult(
            [
                new CliConversationDeletionResult(CliKind.Codex, 1, succeeded ? 1 : 0, issues),
                new CliConversationDeletionResult(
                    CliKind.ClaudeCode,
                    0,
                    0,
                    Array.Empty<ConversationDeletionIssue>()),
                new CliConversationDeletionResult(
                    CliKind.GeminiCli,
                    0,
                    0,
                    Array.Empty<ConversationDeletionIssue>()),
            ]);
        }

        public StubConversationDeletionService(ConversationDeletionResult result)
            => _result = result;

        public Task<ConversationDeletionResult> DeleteProjectConversationsAsync(
            ProjectRecord project,
            CancellationToken cancellationToken = default)
            => Task.FromResult(_result);
    }

    private sealed class RecordingCommandRunner : IOfficialCliCommandRunner
    {
        public List<RecordedCommand> Commands { get; } = [];

        public Func<RecordedCommand, OfficialCliCommandResult>? OnRun { get; init; }

        public Task<OfficialCliCommandResult> RunAsync(
            string executablePath,
            IReadOnlyList<string> arguments,
            string workingDirectory,
            IReadOnlyDictionary<string, string?>? environment,
            TimeSpan timeout,
            CancellationToken cancellationToken)
        {
            var command = new RecordedCommand(
                executablePath,
                arguments.ToArray(),
                workingDirectory,
                environment);
            Commands.Add(command);
            return Task.FromResult(OnRun?.Invoke(command) ?? new OfficialCliCommandResult(0, "deleted", string.Empty));
        }
    }

    private sealed record RecordedCommand(
        string ExecutablePath,
        IReadOnlyList<string> Arguments,
        string WorkingDirectory,
        IReadOnlyDictionary<string, string?>? Environment);

    private sealed class TemporaryWorkspace : IDisposable
    {
        public TemporaryWorkspace()
        {
            Root = Path.Combine(Path.GetTempPath(), "LanAi.Workspace.Tests", Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
            string user = CreateDirectory("user");
            string local = CreateDirectory("local");
            Paths = new AppDataPaths(user, local);
        }

        public string Root { get; }

        public AppDataPaths Paths { get; }

        public string CreateDirectory(string relativePath)
            => Directory.CreateDirectory(Path.Combine(Root, relativePath)).FullName;

        public void Dispose()
        {
            SqliteConnection.ClearAllPools();
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "LanAi.Workspace.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
