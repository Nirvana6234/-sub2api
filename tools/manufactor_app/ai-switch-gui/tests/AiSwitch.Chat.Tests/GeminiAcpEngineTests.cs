using System.Text.Json;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace AiSwitch.Chat.Tests;

public sealed class GeminiAcpEngineTests
{
    [Fact]
    public async Task StartAndPrompt_RunAcpHandshakeAndNormalizeUpdates()
    {
        var process = new FakeStructuredCliProcess
        {
            AutoResponder = message =>
            {
                IReadOnlyList<string> handshake = ChatEngineTestSupport.GeminiHandshakeResponder(message);
                if (handshake.Count > 0)
                {
                    return handshake;
                }

                if (message.TryGetProperty("method", out JsonElement method) &&
                    method.GetString() == "session/prompt")
                {
                    long id = message.GetProperty("id").GetInt64();
                    return
                    [
                        "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"gemini-session-1\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"Gemini 回复\"}}}}",
                        $"{{\"jsonrpc\":\"2.0\",\"id\":{id},\"result\":{{\"stopReason\":\"end_turn\",\"_meta\":{{\"quota\":{{\"token_count\":{{\"input_tokens\":8,\"output_tokens\":3}}}}}}}}}}",
                    ];
                }

                return Array.Empty<string>();
            },
        };
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider("gemini-secret"));
        await using var engine = new GeminiAcpEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);

        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.GeminiCli));

        Assert.Equal("gemini-session-1", engine.NativeSessionId);
        Assert.Contains("--acp", process.StartedCommand!.Arguments);
        Assert.DoesNotContain("--resume", process.StartedCommand.Arguments);
        Assert.Contains(events, item => item is ChatSessionStartedEvent { NativeSessionId: "gemini-session-1" });

        await engine.SendMessageAsync("你好 Gemini");

        Assert.Contains(events, item => item is ChatAssistantDeltaEvent { Text: "Gemini 回复" });
        Assert.Contains(events, item => item is ChatAssistantMessageEvent { Text: "Gemini 回复" });
        Assert.Contains(events, item => item is ChatUsageEvent { InputTokens: 8, OutputTokens: 3 });
        Assert.Contains(events, item => item is ChatTurnCompletedEvent { Succeeded: true });
        Assert.Equal(ChatEngineState.Ready, engine.State);

        using JsonDocument initialize = ChatEngineTestSupport.ParseWritten(process, 0);
        using JsonDocument newSession = ChatEngineTestSupport.ParseWritten(process, 1);
        using JsonDocument prompt = ChatEngineTestSupport.ParseWritten(process, 2);
        Assert.Equal("initialize", initialize.RootElement.GetProperty("method").GetString());
        Assert.Equal("session/new", newSession.RootElement.GetProperty("method").GetString());
        Assert.Equal("session/prompt", prompt.RootElement.GetProperty("method").GetString());

        int errorCount = events.OfType<ChatErrorEvent>().Count();
        process.EmitOutput("{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"future_update\",\"value\":1}}}");
        Assert.Equal(errorCount, events.OfType<ChatErrorEvent>().Count());
    }

    [Fact]
    public async Task Resume_UsesAcpSessionLoadInsteadOfCliResumeArgument()
    {
        var process = new FakeStructuredCliProcess
        {
            AutoResponder = ChatEngineTestSupport.GeminiHandshakeResponder,
        };
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new GeminiAcpEngine(factory, () => process);

        await engine.StartAsync(ChatEngineTestSupport.CreateContext(
            CliKind.GeminiCli,
            CliLaunchMode.Resume,
            "existing-session"));

        Assert.Equal("existing-session", engine.NativeSessionId);
        Assert.DoesNotContain("--resume", process.StartedCommand!.Arguments);
        using JsonDocument load = ChatEngineTestSupport.ParseWritten(process, 1);
        Assert.Equal("session/load", load.RootElement.GetProperty("method").GetString());
        Assert.Equal(
            "existing-session",
            load.RootElement.GetProperty("params").GetProperty("sessionId").GetString());
    }

    [Fact]
    public async Task PermissionAndCancel_UseAcpResponseAndNotification()
    {
        var process = new FakeStructuredCliProcess
        {
            AutoResponder = ChatEngineTestSupport.GeminiHandshakeResponder,
        };
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new GeminiAcpEngine(factory, () => process);
        ChatApprovalRequestedEvent? approval = null;
        engine.EventReceived += (_, chatEvent) => approval ??= chatEvent as ChatApprovalRequestedEvent;
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.GeminiCli));

        Task turn = engine.SendMessageAsync("执行命令");
        process.EmitOutput(
            "{\"jsonrpc\":\"2.0\",\"id\":77,\"method\":\"session/request_permission\",\"params\":{\"sessionId\":\"gemini-session-1\",\"options\":[{\"optionId\":\"once\",\"name\":\"Allow once\",\"kind\":\"allow_once\"},{\"optionId\":\"always\",\"name\":\"Allow session\",\"kind\":\"allow_always\"},{\"optionId\":\"reject\",\"name\":\"Reject\",\"kind\":\"reject_once\"}],\"toolCall\":{\"toolCallId\":\"tool-1\",\"status\":\"pending\",\"title\":\"运行命令\",\"content\":[{\"type\":\"content\",\"content\":{\"type\":\"text\",\"text\":\"git status\"}}]}}}");

        Assert.NotNull(approval);
        Assert.Equal("77", approval!.RequestId);
        Assert.Equal(ChatEngineState.WaitingForApproval, engine.State);

        await engine.RespondToApprovalAsync("77", ChatApprovalDecision.AllowForSession);
        using (JsonDocument permissionResponse = ChatEngineTestSupport.ParseWritten(process, 3))
        {
            Assert.Equal(77, permissionResponse.RootElement.GetProperty("id").GetInt32());
            JsonElement outcome = permissionResponse.RootElement.GetProperty("result").GetProperty("outcome");
            Assert.Equal("selected", outcome.GetProperty("outcome").GetString());
            Assert.Equal("always", outcome.GetProperty("optionId").GetString());
        }

        await engine.CancelTurnAsync();
        using (JsonDocument cancel = ChatEngineTestSupport.ParseWritten(process, 4))
        {
            Assert.Equal("session/cancel", cancel.RootElement.GetProperty("method").GetString());
            Assert.False(cancel.RootElement.TryGetProperty("id", out _));
        }

        process.EmitOutput("{\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"stopReason\":\"cancelled\"}}");
        await turn;
        Assert.Equal(ChatEngineState.Ready, engine.State);
    }
}
