using System.IO;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Wpf.Tests;

public sealed class CodexConversationTranscriptReaderTests
{
    [Fact]
    public async Task ReadAsync_PrefersDisplaySafeEventMessages()
    {
        using var fixture = new TemporaryCodexHome();
        ConversationRecord conversation = fixture.CreateConversation("session-body");
        fixture.WriteRollout(
            "session-body",
            fixture.ProjectDirectory,
            Event("2026-07-13T10:00:00Z", "user_message", "你好"),
            ResponseMessage("2026-07-13T10:00:00Z", "user", "input_text", "你好"),
            Event("2026-07-13T10:00:01Z", "agent_message", "你好，我来帮你。"),
            ResponseMessage("2026-07-13T10:00:01Z", "assistant", "output_text", "你好，我来帮你。"));

        ConversationTranscript transcript = await CodexConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Collection(
            transcript.Messages,
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.User, message.Role);
                Assert.Equal("你好", message.Text);
            },
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.Assistant, message.Role);
                Assert.Equal("你好，我来帮你。", message.Text);
            });
    }

    [Fact]
    public async Task ReadAsync_DeduplicatesSameMessageButKeepsRealRepeat()
    {
        using var fixture = new TemporaryCodexHome();
        ConversationRecord conversation = fixture.CreateConversation("session-dedupe");
        fixture.WriteRollout(
            "session-dedupe",
            fixture.ProjectDirectory,
            Event("2026-07-13T10:00:00Z", "user_message", "再试一次"),
            Event("2026-07-13T10:00:00Z", "user_message", "再试一次"),
            Event("2026-07-13T10:01:00Z", "user_message", "再试一次"));

        ConversationTranscript transcript = await CodexConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Equal(2, transcript.Messages.Count);
        Assert.All(transcript.Messages, message => Assert.Equal("再试一次", message.Text));
    }

    [Fact]
    public async Task ReadAsync_FallbackSkipsDeveloperReasoningAndRawToolOutput()
    {
        using var fixture = new TemporaryCodexHome();
        ConversationRecord conversation = fixture.CreateConversation("session-sensitive");
        fixture.WriteRollout(
            "session-sensitive",
            fixture.ProjectDirectory,
            ResponseMessage("2026-07-13T10:00:00Z", "developer", "input_text", "DEVELOPER_SECRET"),
            Record("2026-07-13T10:00:01Z", "response_item", new
            {
                type = "reasoning",
                summary = new[] { new { type = "summary_text", text = "REASONING_SECRET" } },
            }),
            Record("2026-07-13T10:00:02Z", "response_item", new
            {
                type = "function_call_output",
                call_id = "tool-1",
                output = "TOOL_SECRET",
            }),
            Record("2026-07-13T10:00:03Z", "event_msg", new
            {
                type = "patch_apply_end",
                stdout = "PATCH_SECRET",
            }),
            ResponseMessage("2026-07-13T10:00:04Z", "user", "input_text", "可见问题"),
            ResponseMessage("2026-07-13T10:00:05Z", "assistant", "output_text", "可见回答"));

        ConversationTranscript transcript = await CodexConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Equal(new[] { "可见问题", "可见回答" }, transcript.Messages.Select(message => message.Text));
        string visibleText = string.Join("\n", transcript.Messages.Select(message => message.Text));
        Assert.DoesNotContain("SECRET", visibleText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ReadAsync_IgnoresMalformedLinesAndContinues()
    {
        using var fixture = new TemporaryCodexHome();
        ConversationRecord conversation = fixture.CreateConversation("session-corrupt");
        fixture.WriteRolloutWithRawLine(
            "session-corrupt",
            fixture.ProjectDirectory,
            "{this is incomplete",
            Event("2026-07-13T10:00:00Z", "user_message", "仍然可见"));

        ConversationTranscript transcript = await CodexConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Single(transcript.Messages);
        Assert.Equal("仍然可见", transcript.Messages[0].Text);
        Assert.Contains(transcript.Warnings, warning => warning.Contains("已跳过", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ReadAsync_RequiresExactSessionAndWorkingDirectoryAndToleratesMissingSource()
    {
        using var fixture = new TemporaryCodexHome();
        ConversationRecord conversation = fixture.CreateConversation("wanted-session");
        fixture.WriteRollout(
            "wanted-session",
            fixture.OtherProjectDirectory,
            Event("2026-07-13T10:00:00Z", "user_message", "错误目录"));
        fixture.WriteRollout(
            "other-session",
            fixture.ProjectDirectory,
            Event("2026-07-13T10:00:00Z", "user_message", "错误会话"));

        ConversationTranscript mismatch = await CodexConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.False(mismatch.SourceFound);
        Assert.Empty(mismatch.Messages);

        using var missingFixture = new TemporaryCodexHome(createSessionsDirectory: false);
        ConversationTranscript missing = await CodexConversationTranscriptReader.ReadAsync(
            missingFixture.Paths,
            missingFixture.CreateConversation("missing-session"),
            CancellationToken.None);

        Assert.False(missing.SourceFound);
        Assert.Empty(missing.Messages);
        Assert.NotEmpty(missing.Warnings);
    }

    private static object Event(string timestamp, string eventType, string message) =>
        Record(timestamp, "event_msg", new
        {
            type = eventType,
            message,
            phase = eventType == "agent_message" ? "final_answer" : null,
        });

    private static object ResponseMessage(
        string timestamp,
        string role,
        string contentType,
        string text) =>
        Record(timestamp, "response_item", new
        {
            type = "message",
            role,
            content = new[] { new { type = contentType, text } },
        });

    private static object Record(string timestamp, string type, object payload) => new
    {
        timestamp,
        type,
        payload,
    };

    private sealed class TemporaryCodexHome : IDisposable
    {
        private readonly string _safeParent;

        public TemporaryCodexHome(bool createSessionsDirectory = true)
        {
            _safeParent = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "LanAi.CodexTranscript.Tests"));
            Root = Path.Combine(_safeParent, Guid.NewGuid().ToString("N"));
            UserProfile = Path.Combine(Root, "user");
            LocalAppData = Path.Combine(Root, "local");
            ProjectDirectory = Path.Combine(Root, "project");
            OtherProjectDirectory = Path.Combine(Root, "other-project");
            Directory.CreateDirectory(UserProfile);
            Directory.CreateDirectory(LocalAppData);
            Directory.CreateDirectory(ProjectDirectory);
            Directory.CreateDirectory(OtherProjectDirectory);
            Paths = new AppDataPaths(UserProfile, LocalAppData);
            if (createSessionsDirectory)
            {
                Directory.CreateDirectory(Paths.CodexSessionsDirectory);
            }
        }

        public string Root { get; }

        public string UserProfile { get; }

        public string LocalAppData { get; }

        public string ProjectDirectory { get; }

        public string OtherProjectDirectory { get; }

        public AppDataPaths Paths { get; }

        public ConversationRecord CreateConversation(string nativeSessionId) => new()
        {
            Id = $"codex:{nativeSessionId}",
            ProjectId = PathIdentity.CreateStableId(ProjectDirectory),
            NativeClient = CliKind.Codex,
            NativeSessionId = nativeSessionId,
            Title = "测试会话",
            OriginalWorkingDirectory = ProjectDirectory,
            CreatedAt = DateTimeOffset.Parse("2026-07-13T10:00:00Z"),
            UpdatedAt = DateTimeOffset.Parse("2026-07-13T10:01:00Z"),
        };

        public void WriteRollout(
            string sessionId,
            string workingDirectory,
            params object[] records) =>
            WriteRolloutCore(sessionId, workingDirectory, records.Select(Serialize));

        public void WriteRolloutWithRawLine(
            string sessionId,
            string workingDirectory,
            string rawLine,
            params object[] records) =>
            WriteRolloutCore(
                sessionId,
                workingDirectory,
                new[] { rawLine }.Concat(records.Select(Serialize)));

        private void WriteRolloutCore(
            string sessionId,
            string workingDirectory,
            IEnumerable<string> bodyLines)
        {
            string dayDirectory = Path.Combine(Paths.CodexSessionsDirectory, "2026", "07", "13");
            Directory.CreateDirectory(dayDirectory);
            string filePath = Path.Combine(
                dayDirectory,
                $"rollout-{Guid.NewGuid():N}-{sessionId}.jsonl");
            object metadata = Record(
                "2026-07-13T09:59:59Z",
                "session_meta",
                new
                {
                    id = sessionId,
                    cwd = workingDirectory,
                });
            File.WriteAllLines(filePath, new[] { Serialize(metadata) }.Concat(bodyLines));
        }

        private static string Serialize(object value) =>
            JsonSerializer.Serialize(value);

        public void Dispose()
        {
            string resolvedRoot = Path.GetFullPath(Root);
            string safePrefix = _safeParent.TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
            if (resolvedRoot.StartsWith(safePrefix, StringComparison.OrdinalIgnoreCase) &&
                Directory.Exists(resolvedRoot))
            {
                Directory.Delete(resolvedRoot, recursive: true);
            }
        }
    }
}
