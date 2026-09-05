using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Wpf.Tests;

public sealed class GeminiConversationTranscriptReaderTests
{
    [Fact]
    public async Task ReadAsync_AppliesJsonlUpdatesRewindAndVisibleContentRules()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "33333333-3333-3333-3333-333333333333";
        string sessionFile = fixture.CreateSessionFile("session-current.jsonl");
        File.WriteAllLines(sessionFile,
        [
            Serialize(new
            {
                sessionId,
                projectHash = "project-hash",
                startTime = "2026-07-13T10:00:00Z",
                lastUpdated = "2026-07-13T10:10:00Z",
                kind = "main",
            }),
            Serialize(new { set = new { summary = "摘要不应成为消息" } }).Replace("\"set\"", "\"$set\"", StringComparison.Ordinal),
            Serialize(new
            {
                id = "user-1",
                type = "user",
                timestamp = "2026-07-13T10:00:01Z",
                displayContent = new object[] { new { text = "用户展示文本" } },
                content = new object[] { new { text = "用户原始文本" } },
            }),
            Serialize(new
            {
                id = "user-1",
                type = "user",
                timestamp = "2026-07-13T10:00:02Z",
                displayContent = new object[] { new { text = "用户展示文本" } },
                content = new object[] { new { text = "重复原始文本" } },
            }),
            Serialize(new
            {
                id = "assistant-1",
                type = "gemini",
                timestamp = "2026-07-13T10:00:03Z",
                content = new object[]
                {
                    new { text = "助手答案" },
                    new { text = "隐藏思考", thought = true },
                    new { functionCall = new { name = "read_file", args = new { path = "secret" } } },
                    new { text = "助手补充" },
                },
                thoughts = new[] { new { subject = "隐藏", description = "推理" } },
                toolCalls = new[] { new { name = "Read", resultDisplay = "工具原始输出" } },
            }),
            Serialize(new
            {
                id = "tool-1",
                type = "tool",
                content = new object[] { new { text = "工具消息" } },
            }),
            Serialize(new
            {
                id = "assistant-rewound",
                type = "gemini",
                content = new object[] { new { text = "应被回退" } },
            }),
            Serialize(new { rewindTo = "assistant-rewound" }).Replace("\"rewindTo\"", "\"$rewindTo\"", StringComparison.Ordinal),
            Serialize(new
            {
                id = "assistant-2",
                type = "gemini",
                displayContent = Array.Empty<object>(),
                content = new object[] { new { text = "回退后的回答" } },
            }),
            "{malformed",
        ]);

        ConversationTranscript transcript = await GeminiConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(sessionId),
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Collection(
            transcript.Messages,
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.User, message.Role);
                Assert.Equal("用户展示文本", message.Text);
            },
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.Assistant, message.Role);
                Assert.Equal($"助手答案{Environment.NewLine}助手补充", message.Text);
            },
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.Assistant, message.Role);
                Assert.Equal("回退后的回答", message.Text);
            });
        Assert.Contains(transcript.Warnings, warning => warning.Contains("1 行", StringComparison.Ordinal));
        Assert.DoesNotContain(transcript.Messages, message =>
            message.Text.Contains("隐藏", StringComparison.Ordinal) ||
            message.Text.Contains("工具", StringComparison.Ordinal) ||
            message.Text.Contains("回退", StringComparison.Ordinal) && message.Text != "回退后的回答");
    }

    [Fact]
    public async Task ReadAsync_SupportsLegacyMessagesArray()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "44444444-4444-4444-4444-444444444444";
        string sessionFile = fixture.CreateSessionFile("session-legacy.json");
        File.WriteAllText(sessionFile, JsonSerializer.Serialize(new
        {
            sessionId,
            projectHash = "legacy-project",
            kind = "main",
            startTime = "2026-07-13T09:00:00Z",
            messages = new object[]
            {
                new
                {
                    id = "legacy-user",
                    type = "user",
                    displayContent = new object[] { new { text = "旧版用户消息" } },
                    content = new object[] { new { text = "旧版用户原始消息" } },
                },
                new
                {
                    id = "legacy-assistant",
                    type = "gemini",
                    content = new object[] { new { text = "旧版助手消息" } },
                },
                new
                {
                    id = "legacy-info",
                    type = "info",
                    content = new object[] { new { text = "内部信息" } },
                },
            },
        }, new JsonSerializerOptions { WriteIndented = true }));

        ConversationTranscript transcript = await GeminiConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(sessionId),
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Equal(
            ["旧版用户消息", "旧版助手消息"],
            transcript.Messages.Select(message => message.Text));
        Assert.Equal(
            [ConversationTranscriptRole.User, ConversationTranscriptRole.Assistant],
            transcript.Messages.Select(message => message.Role));
    }

    [Fact]
    public async Task ReadAsync_RejectsMatchingSessionFromDifferentProjectRoot()
    {
        using var fixture = new TemporaryWorkspace(writeMatchingProjectRoot: false);
        const string sessionId = "55555555-5555-5555-5555-555555555555";
        File.WriteAllText(
            fixture.CreateSessionFile("session-wrong-project.jsonl"),
            Serialize(new
            {
                sessionId,
                projectHash = "wrong-project",
                kind = "main",
                id = "user-1",
                type = "user",
                content = "错误项目",
            }) + Environment.NewLine);

        ConversationTranscript transcript = await GeminiConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(sessionId),
            CancellationToken.None);

        Assert.False(transcript.SourceFound);
        Assert.Empty(transcript.Messages);
        Assert.Contains(transcript.Warnings, warning => warning.Contains("工作目录不匹配", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ReadAsync_DeduplicatesByIdAndStopsAtMessageLimit()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "77777777-7777-7777-7777-777777777777";
        string sessionFile = fixture.CreateSessionFile("session-limit.jsonl");
        var lines = new List<string>
        {
            Serialize(new
            {
                sessionId,
                projectHash = "project-hash",
                kind = "main",
            }),
        };
        lines.AddRange(Enumerable.Range(0, 2_005).Select(index => Serialize(new
        {
            id = $"message-{index}",
            type = index % 2 == 0 ? "user" : "gemini",
            content = $"消息 {index}",
        })));
        lines.Insert(2, Serialize(new
        {
            id = "message-0",
            type = "user",
            content = "覆盖后的消息 0",
        }));
        File.WriteAllLines(sessionFile, lines);

        ConversationTranscript transcript = await GeminiConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(sessionId),
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Equal(2_000, transcript.Messages.Count);
        Assert.Equal("覆盖后的消息 0", transcript.Messages[0].Text);
        Assert.Contains(transcript.Warnings, warning => warning.Contains("安全上限", StringComparison.Ordinal));
    }

    private static string Serialize(object value) => JsonSerializer.Serialize(value);

    private sealed class TemporaryWorkspace : IDisposable
    {
        private readonly string _chatsDirectory;

        public TemporaryWorkspace(bool writeMatchingProjectRoot = true)
        {
            Root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.GeminiTranscript.Tests",
                Guid.NewGuid().ToString("N"));
            string user = Directory.CreateDirectory(Path.Combine(Root, "user")).FullName;
            string local = Directory.CreateDirectory(Path.Combine(Root, "local")).FullName;
            Paths = new AppDataPaths(user, local);
            ProjectRoot = Directory.CreateDirectory(Path.Combine(Root, "project")).FullName;
            OtherProjectRoot = Directory.CreateDirectory(Path.Combine(Root, "other-project")).FullName;
            string projectDirectory = Directory.CreateDirectory(
                Path.Combine(Paths.GeminiProjectsDirectory, "project-hash")).FullName;
            Directory.CreateDirectory(Paths.GeminiProjectsDirectory);
            File.WriteAllText(
                Path.Combine(projectDirectory, ".project_root"),
                writeMatchingProjectRoot ? ProjectRoot : OtherProjectRoot);
            _chatsDirectory = Directory.CreateDirectory(Path.Combine(projectDirectory, "chats")).FullName;
        }

        public string Root { get; }

        public AppDataPaths Paths { get; }

        public string ProjectRoot { get; }

        public string OtherProjectRoot { get; }

        public string CreateSessionFile(string fileName) => Path.Combine(_chatsDirectory, fileName);

        public ConversationRecord CreateConversation(string sessionId)
        {
            string fingerprint = PathIdentity.CreateStableId(ProjectRoot);
            return new ConversationRecord
            {
                Id = $"gemini:{sessionId}",
                ProjectId = fingerprint,
                NativeClient = CliKind.GeminiCli,
                NativeSessionId = sessionId,
                OriginalWorkingDirectory = ProjectRoot,
                CreatedAt = DateTimeOffset.Parse("2026-07-13T10:00:00Z"),
                UpdatedAt = DateTimeOffset.Parse("2026-07-13T10:10:00Z"),
            };
        }

        public void Dispose()
        {
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(
                Path.Combine(Path.GetTempPath(), "LanAi.GeminiTranscript.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
