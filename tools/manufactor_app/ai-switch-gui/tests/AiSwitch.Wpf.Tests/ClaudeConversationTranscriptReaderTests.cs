using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Wpf.Tests;

public sealed class ClaudeConversationTranscriptReaderTests
{
    [Fact]
    public async Task ReadAsync_ReturnsOnlyExactVisibleUserAndAssistantText()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "11111111-1111-1111-1111-111111111111";
        string sessionDirectory = Directory.CreateDirectory(
            Path.Combine(fixture.Paths.ClaudeProjectsDirectory, "project-hash")).FullName;
        string sessionFile = Path.Combine(sessionDirectory, sessionId + ".jsonl");
        string userLine = Serialize(new
        {
            type = "user",
            sessionId,
            cwd = fixture.ProjectRoot,
            uuid = "user-1",
            timestamp = "2026-07-13T10:00:00Z",
            message = new { role = "user", content = "用户问题" },
        });
        File.WriteAllLines(sessionFile,
        [
            Serialize(new
            {
                type = "user",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "meta-user",
                isMeta = true,
                message = new { role = "user", content = "隐藏元数据" },
            }),
            userLine,
            userLine,
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "assistant-thinking",
                message = new
                {
                    id = "assistant-1",
                    role = "assistant",
                    content = new object[] { new { type = "thinking", thinking = "隐藏推理" } },
                },
            }),
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "assistant-text-1",
                timestamp = "2026-07-13T10:00:01Z",
                message = new
                {
                    id = "assistant-1",
                    role = "assistant",
                    content = new object[] { new { type = "text", text = "第一段" } },
                },
            }),
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "assistant-duplicate",
                message = new
                {
                    id = "assistant-1",
                    role = "assistant",
                    content = new object[] { new { type = "text", text = "第一段" } },
                },
            }),
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "assistant-tool",
                message = new
                {
                    id = "assistant-1",
                    role = "assistant",
                    content = new object[] { new { type = "tool_use", name = "Read", input = new { path = "secret" } } },
                },
            }),
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "assistant-text-2",
                message = new
                {
                    id = "assistant-1",
                    role = "assistant",
                    content = new object[] { new { type = "text", text = "第二段" } },
                },
            }),
            Serialize(new
            {
                type = "user",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "tool-result",
                sourceToolAssistantUUID = "assistant-tool",
                toolUseResult = new { raw = "不得展示" },
                message = new
                {
                    role = "user",
                    content = new object[] { new { type = "tool_result", content = "原始工具输出" } },
                },
            }),
            Serialize(new
            {
                type = "assistant",
                sessionId,
                cwd = fixture.ProjectRoot,
                uuid = "sidechain",
                isSidechain = true,
                message = new
                {
                    id = "sidechain-message",
                    role = "assistant",
                    content = new object[] { new { type = "text", text = "子代理正文" } },
                },
            }),
            Serialize(new
            {
                type = "attachment",
                sessionId,
                cwd = fixture.ProjectRoot,
                attachment = new { content = "附件原文" },
            }),
            Serialize(new
            {
                type = "user",
                sessionId,
                cwd = fixture.OtherProjectRoot,
                uuid = "wrong-cwd",
                message = new { role = "user", content = "其他项目" },
            }),
            "{malformed",
        ]);
        ConversationRecord conversation = fixture.CreateConversation(CliKind.ClaudeCode, sessionId);

        ConversationTranscript transcript = await ClaudeConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            conversation,
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Collection(
            transcript.Messages,
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.User, message.Role);
                Assert.Equal("用户问题", message.Text);
            },
            message =>
            {
                Assert.Equal(ConversationTranscriptRole.Assistant, message.Role);
                Assert.Equal($"第一段{Environment.NewLine}第二段", message.Text);
            });
        Assert.Contains(transcript.Warnings, warning => warning.Contains("1 行", StringComparison.Ordinal));
        Assert.DoesNotContain(transcript.Messages, message =>
            message.Text.Contains("隐藏", StringComparison.Ordinal) ||
            message.Text.Contains("工具", StringComparison.Ordinal) ||
            message.Text.Contains("附件", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ReadAsync_RejectsSameSessionFileFromDifferentWorkingDirectory()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "22222222-2222-2222-2222-222222222222";
        string sessionDirectory = Directory.CreateDirectory(
            Path.Combine(fixture.Paths.ClaudeProjectsDirectory, "project-hash")).FullName;
        File.WriteAllText(
            Path.Combine(sessionDirectory, sessionId + ".jsonl"),
            Serialize(new
            {
                type = "user",
                sessionId,
                cwd = fixture.OtherProjectRoot,
                uuid = "user-1",
                message = new { role = "user", content = "错误项目" },
            }) + Environment.NewLine);

        ConversationTranscript transcript = await ClaudeConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(CliKind.ClaudeCode, sessionId),
            CancellationToken.None);

        Assert.False(transcript.SourceFound);
        Assert.Empty(transcript.Messages);
        Assert.Contains(transcript.Warnings, warning => warning.Contains("工作目录不匹配", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ReadAsync_StopsAtMessageLimitAndReportsWarning()
    {
        using var fixture = new TemporaryWorkspace();
        const string sessionId = "66666666-6666-6666-6666-666666666666";
        string sessionDirectory = Directory.CreateDirectory(
            Path.Combine(fixture.Paths.ClaudeProjectsDirectory, "project-hash")).FullName;
        string sessionFile = Path.Combine(sessionDirectory, sessionId + ".jsonl");
        IEnumerable<string> lines = Enumerable.Range(0, 2_005).Select(index => Serialize(new
        {
            type = "user",
            sessionId,
            cwd = fixture.ProjectRoot,
            uuid = $"user-{index}",
            message = new { role = "user", content = $"消息 {index}" },
        }));
        File.WriteAllLines(sessionFile, lines);

        ConversationTranscript transcript = await ClaudeConversationTranscriptReader.ReadAsync(
            fixture.Paths,
            fixture.CreateConversation(CliKind.ClaudeCode, sessionId),
            CancellationToken.None);

        Assert.True(transcript.SourceFound);
        Assert.Equal(2_000, transcript.Messages.Count);
        Assert.Contains(transcript.Warnings, warning => warning.Contains("安全上限", StringComparison.Ordinal));
    }

    private static string Serialize(object value) => JsonSerializer.Serialize(value);

    private sealed class TemporaryWorkspace : IDisposable
    {
        public TemporaryWorkspace()
        {
            Root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.ClaudeTranscript.Tests",
                Guid.NewGuid().ToString("N"));
            string user = Directory.CreateDirectory(Path.Combine(Root, "user")).FullName;
            string local = Directory.CreateDirectory(Path.Combine(Root, "local")).FullName;
            Paths = new AppDataPaths(user, local);
            ProjectRoot = Directory.CreateDirectory(Path.Combine(Root, "project")).FullName;
            OtherProjectRoot = Directory.CreateDirectory(Path.Combine(Root, "other-project")).FullName;
            Directory.CreateDirectory(Paths.ClaudeProjectsDirectory);
        }

        public string Root { get; }

        public AppDataPaths Paths { get; }

        public string ProjectRoot { get; }

        public string OtherProjectRoot { get; }

        public ConversationRecord CreateConversation(CliKind client, string sessionId)
        {
            string fingerprint = PathIdentity.CreateStableId(ProjectRoot);
            return new ConversationRecord
            {
                Id = $"claude:{sessionId}",
                ProjectId = fingerprint,
                NativeClient = client,
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
                Path.Combine(Path.GetTempPath(), "LanAi.ClaudeTranscript.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}
