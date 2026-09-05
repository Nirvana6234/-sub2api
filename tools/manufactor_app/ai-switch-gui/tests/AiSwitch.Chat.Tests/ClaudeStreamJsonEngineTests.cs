using System.Text.Json;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace AiSwitch.Chat.Tests;

public sealed class ClaudeStreamJsonEngineTests
{
    [Fact]
    public async Task StartAndSend_UseBidirectionalJsonAndCaptureSessionId()
    {
        var process = new FakeStructuredCliProcess();
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider("claude-secret"));
        await using var engine = new ClaudeStreamJsonEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);
        ConnectionProfile connection = new()
        {
            Id = "connection-1",
            Name = "测试连接",
            BaseUrl = "https://claude.example.test",
        };

        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.ClaudeCode, connection: connection));

        Assert.NotNull(process.StartedCommand);
        Assert.Contains("stream-json", process.StartedCommand!.Arguments);
        Assert.Contains("--permission-prompt-tool", process.StartedCommand.Arguments);
        Assert.Equal("sdk-ts", process.StartedCommand.Environment!["CLAUDE_CODE_ENTRYPOINT"]);
        Assert.Equal("claude-secret", process.StartedCommand.Environment["ANTHROPIC_API_KEY"]);
        using (JsonDocument initialize = ChatEngineTestSupport.ParseWritten(process, 0))
        {
            Assert.Equal("control_request", initialize.RootElement.GetProperty("type").GetString());
            Assert.Equal("initialize", initialize.RootElement.GetProperty("request").GetProperty("subtype").GetString());
        }

        process.EmitOutput("{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"claude-session-1\",\"cwd\":\"C:/project\"}");

        Assert.Equal("claude-session-1", engine.NativeSessionId);
        Assert.Contains(events, item => item is ChatSessionStartedEvent { NativeSessionId: "claude-session-1" });

        await engine.SendMessageAsync("你好 Claude");
        using JsonDocument userMessage = ChatEngineTestSupport.ParseWritten(process, 1);
        Assert.Equal("user", userMessage.RootElement.GetProperty("type").GetString());
        Assert.Equal(
            "你好 Claude",
            userMessage.RootElement.GetProperty("message").GetProperty("content")[0].GetProperty("text").GetString());

        int errorCount = events.OfType<ChatErrorEvent>().Count();
        process.EmitOutput("{\"type\":\"future_event\",\"new_field\":true}");
        Assert.Equal(errorCount, events.OfType<ChatErrorEvent>().Count());
    }

    [Fact]
    public async Task PermissionAndCancel_WriteClaudeControlProtocol()
    {
        var process = new FakeStructuredCliProcess();
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new ClaudeStreamJsonEngine(factory, () => process);
        ChatApprovalRequestedEvent? approval = null;
        engine.EventReceived += (_, chatEvent) => approval ??= chatEvent as ChatApprovalRequestedEvent;
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.ClaudeCode));
        await engine.SendMessageAsync("检查项目");

        process.EmitOutput(
            "{\"type\":\"control_request\",\"request_id\":\"permission-1\",\"request\":{\"subtype\":\"can_use_tool\",\"tool_name\":\"Bash\",\"input\":{\"command\":\"git status\"},\"permission_suggestions\":[{\"type\":\"addRules\",\"rules\":[],\"behavior\":\"allow\",\"destination\":\"session\"}],\"tool_use_id\":\"tool-1\"}}");

        Assert.NotNull(approval);
        Assert.Equal("permission-1", approval!.RequestId);
        Assert.Equal(ChatEngineState.WaitingForApproval, engine.State);

        await engine.RespondToApprovalAsync("permission-1", ChatApprovalDecision.AllowForSession);
        using (JsonDocument response = ChatEngineTestSupport.ParseWritten(process, 2))
        {
            JsonElement body = response.RootElement.GetProperty("response");
            Assert.Equal("permission-1", body.GetProperty("request_id").GetString());
            Assert.Equal("allow", body.GetProperty("response").GetProperty("behavior").GetString());
            Assert.True(body.GetProperty("response").TryGetProperty("updatedPermissions", out _));
        }

        await engine.CancelTurnAsync();
        using JsonDocument cancel = ChatEngineTestSupport.ParseWritten(process, 3);
        Assert.Equal("control_request", cancel.RootElement.GetProperty("type").GetString());
        Assert.Equal("interrupt", cancel.RootElement.GetProperty("request").GetProperty("subtype").GetString());
    }

    [Fact]
    public async Task ProtocolEvents_AreNormalizedWithoutFailingOnTools()
    {
        var process = new FakeStructuredCliProcess();
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new ClaudeStreamJsonEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.ClaudeCode));
        await engine.SendMessageAsync("执行检查");

        process.EmitOutput("{\"type\":\"stream_event\",\"event\":{\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"正在检查\"}}}");
        process.EmitOutput("{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"检查完成\"},{\"type\":\"tool_use\",\"id\":\"tool-2\",\"name\":\"Read\",\"input\":{\"file_path\":\"README.md\"}}]}}");
        process.EmitOutput("{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"tool-2\",\"content\":\"ok\"}]}}");
        process.EmitOutput("{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":6}}");

        Assert.Contains(events, item => item is ChatAssistantDeltaEvent { Text: "正在检查" });
        Assert.Contains(events, item => item is ChatAssistantMessageEvent { Text: "检查完成" });
        Assert.Contains(events, item => item is ChatToolStartedEvent { ToolCallId: "tool-2", ToolName: "Read" });
        Assert.Contains(events, item => item is ChatToolCompletedEvent { ToolCallId: "tool-2", Succeeded: true });
        Assert.Contains(events, item => item is ChatUsageEvent { InputTokens: 10, OutputTokens: 4, CachedInputTokens: 2, CacheCreationTokens: 6 });
        Assert.Contains(events, item => item is ChatTurnCompletedEvent { Succeeded: true });
        Assert.Equal(ChatEngineState.Ready, engine.State);
    }
}
