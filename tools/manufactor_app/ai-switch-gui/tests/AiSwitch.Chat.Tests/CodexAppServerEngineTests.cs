using System.Text.Json;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace AiSwitch.Chat.Tests;

public sealed class CodexAppServerEngineTests
{
    [Fact]
    public async Task Start_UsesOfficialAppServerAndCreatesStructuredThread()
    {
        var process = CreateHandshakeProcess();
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider("codex-secret"));
        await using var engine = new CodexAppServerEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);
        ConnectionProfile connection = new()
        {
            Id = "connection-1",
            Name = "测试连接",
            BaseUrl = "https://codex.example.test/v1",
        };

        await engine.StartAsync(ChatEngineTestSupport.CreateContext(
            CliKind.Codex,
            connection: connection));

        Assert.Equal(ChatEngineState.Ready, engine.State);
        Assert.Equal("codex-thread-1", engine.NativeSessionId);
        Assert.NotNull(process.StartedCommand);
        Assert.Contains("app-server", process.StartedCommand!.Arguments);
        Assert.Contains("--stdio", process.StartedCommand.Arguments);
        Assert.DoesNotContain("resume", process.StartedCommand.Arguments);
        Assert.Equal("codex-secret", process.StartedCommand.Environment!["OPENAI_API_KEY"]);
        Assert.Contains(
            "model_providers.lan_ai_workspace.base_url=\"https://codex.example.test/v1\"",
            process.StartedCommand.Arguments);
        Assert.Contains(events, item =>
            item is ChatSessionStartedEvent { NativeSessionId: "codex-thread-1" });

        using JsonDocument initialize = ChatEngineTestSupport.ParseWritten(process, 0);
        Assert.Equal("initialize", initialize.RootElement.GetProperty("method").GetString());
        Assert.Equal(
            "lan-ai-workspace",
            initialize.RootElement.GetProperty("params").GetProperty("clientInfo").GetProperty("name").GetString());
        Assert.False(
            initialize.RootElement.GetProperty("params").GetProperty("capabilities").GetProperty("experimentalApi").GetBoolean());

        using JsonDocument start = ChatEngineTestSupport.ParseWritten(process, 1);
        Assert.Equal("thread/start", start.RootElement.GetProperty("method").GetString());
        JsonElement parameters = start.RootElement.GetProperty("params");
        Assert.Equal("workspace-write", parameters.GetProperty("sandbox").GetString());
        Assert.Equal("on-request", parameters.GetProperty("approvalPolicy").GetString());
        Assert.Equal("test-model", parameters.GetProperty("model").GetString());
        Assert.Equal(Environment.CurrentDirectory, parameters.GetProperty("cwd").GetString());
        Assert.False(parameters.TryGetProperty("runtimeWorkspaceRoots", out _));
    }

    [Fact]
    public async Task Resume_UsesThreadResumeInsteadOfInteractiveCliResume()
    {
        var process = CreateHandshakeProcess(resumedThreadId: "existing-thread");
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new CodexAppServerEngine(factory, () => process);

        await engine.StartAsync(ChatEngineTestSupport.CreateContext(
            CliKind.Codex,
            CliLaunchMode.Resume,
            "existing-thread"));

        Assert.Equal("existing-thread", engine.NativeSessionId);
        Assert.DoesNotContain("resume", process.StartedCommand!.Arguments);
        using JsonDocument resume = ChatEngineTestSupport.ParseWritten(process, 1);
        Assert.Equal("thread/resume", resume.RootElement.GetProperty("method").GetString());
        Assert.Equal(
            "existing-thread",
            resume.RootElement.GetProperty("params").GetProperty("threadId").GetString());
        Assert.False(
            resume.RootElement.GetProperty("params").TryGetProperty(
                "runtimeWorkspaceRoots",
                out _));
    }

    [Fact]
    public async Task Start_NpmPowerShellShimUsesDirectNodeOnlyForStructuredAppServer()
    {
        using var layout = new NpmCodexShimLayout(includeEntryPoint: true);
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider("codex-secret"));
        ChatEngineContext context = ChatEngineTestSupport.CreateContext(
            CliKind.Codex,
            connection: new ConnectionProfile
            {
                Id = "npm-shim-source",
                Name = "npm shim source",
                BaseUrl = "https://codex.example.test/v1",
            }) with
        {
            Installation = new CliInstallation
            {
                Kind = CliKind.Codex,
                IsInstalled = true,
                ExecutablePath = layout.CommandShimPath,
                Version = "test",
                DetectedAt = DateTimeOffset.UtcNow,
            },
        };

        // The normal terminal path deliberately retains the PowerShell npm shim.
        // Only the structured app-server route is allowed to bypass it.
        TerminalCommand normalTerminalCommand = await factory.CreateAsync(
            context.LaunchRequest,
            context.Installation,
            context.Connection);
        Assert.Equal(
            "powershell",
            Path.GetFileNameWithoutExtension(normalTerminalCommand.FileName),
            StringComparer.OrdinalIgnoreCase);
        Assert.Contains(layout.PowerShellShimPath, normalTerminalCommand.Arguments);

        var process = CreateHandshakeProcess();
        await using var engine = new CodexAppServerEngine(factory, () => process);
        await engine.StartAsync(context);

        TerminalCommand graphicalCommand = Assert.IsType<TerminalCommand>(process.StartedCommand);
        Assert.Equal(layout.NodeExecutablePath, graphicalCommand.FileName);
        Assert.Equal(layout.EntryPointPath, graphicalCommand.Arguments[0]);
        Assert.DoesNotContain("-File", graphicalCommand.Arguments, StringComparer.OrdinalIgnoreCase);
        Assert.Contains("app-server", graphicalCommand.Arguments);
        Assert.Contains("--stdio", graphicalCommand.Arguments);
        Assert.Equal("codex-secret", graphicalCommand.Environment!["OPENAI_API_KEY"]);
    }

    [Fact]
    public void BuildAppServerCommand_NpmLayoutUnavailableFallsBackToPowerShellShim()
    {
        using var layout = new NpmCodexShimLayout(includeEntryPoint: false);
        var environment = new Dictionary<string, string?>
        {
            ["OPENAI_API_KEY"] = "not-on-command-line",
        };
        var command = new TerminalCommand(
            @"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
            ["-NoLogo", "-File", layout.PowerShellShimPath, "-c", "model_provider=\"lan_ai_workspace\""],
            Environment.CurrentDirectory,
            environment);

        TerminalCommand fallback = CodexAppServerEngine.BuildAppServerCommand(
            command,
            layout.CommandShimPath);

        Assert.Equal(command.FileName, fallback.FileName);
        Assert.Equal(
            ["-NoLogo", "-File", layout.PowerShellShimPath, "-c", "model_provider=\"lan_ai_workspace\"", "app-server", "--stdio"],
            fallback.Arguments);
        Assert.Same(environment, fallback.Environment);
    }

    [Fact]
    public async Task Notifications_AreNormalizedIntoGraphicalChatEvents()
    {
        var process = CreateHandshakeProcess(respondToTurnStart: true);
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new CodexAppServerEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.Codex));

        await engine.SendMessageAsync("检查项目");
        process.EmitOutput(
            "{\"method\":\"turn/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turn\":{\"id\":\"turn-1\",\"items\":[],\"status\":\"inProgress\"}}}");
        process.EmitOutput(
            "{\"method\":\"item/agentMessage/delta\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"message-1\",\"delta\":\"正在检查\"}}");
        process.EmitOutput(
            "{\"method\":\"item/started\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"startedAtMs\":1,\"item\":{\"id\":\"tool-1\",\"type\":\"commandExecution\",\"command\":\"git status\",\"commandActions\":[],\"cwd\":\"C:/project\",\"status\":\"inProgress\"}}}");
        process.EmitOutput(
            "{\"method\":\"item/commandExecution/outputDelta\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"tool-1\",\"delta\":\"working tree clean\"}}");
        process.EmitOutput(
            "{\"method\":\"item/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"completedAtMs\":2,\"item\":{\"id\":\"tool-1\",\"type\":\"commandExecution\",\"command\":\"git status\",\"commandActions\":[],\"cwd\":\"C:/project\",\"aggregatedOutput\":\"working tree clean\",\"exitCode\":0,\"status\":\"completed\"}}}");
        process.EmitOutput(
            "{\"method\":\"item/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"completedAtMs\":3,\"item\":{\"id\":\"message-1\",\"type\":\"agentMessage\",\"text\":\"检查完成\"}}}");
        process.EmitOutput(
            "{\"method\":\"thread/tokenUsage/updated\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"tokenUsage\":{\"last\":{\"inputTokens\":12,\"cachedInputTokens\":3,\"outputTokens\":5,\"reasoningOutputTokens\":1,\"totalTokens\":21},\"total\":{\"inputTokens\":12,\"cachedInputTokens\":3,\"outputTokens\":5,\"reasoningOutputTokens\":1,\"totalTokens\":21}}}}");
        process.EmitOutput(
            "{\"method\":\"turn/completed\",\"params\":{\"threadId\":\"codex-thread-1\",\"turn\":{\"id\":\"turn-1\",\"items\":[],\"status\":\"completed\"}}}");

        Assert.Contains(events, item => item is ChatAssistantDeltaEvent { Text: "正在检查" });
        Assert.Contains(events, item => item is ChatAssistantMessageEvent { Text: "检查完成" });
        Assert.Contains(events, item =>
            item is ChatToolStartedEvent { ToolCallId: "tool-1", ToolName: "终端命令" });
        Assert.Contains(events, item =>
            item is ChatToolProgressEvent { ToolCallId: "tool-1", Message: "working tree clean" });
        Assert.Contains(events, item =>
            item is ChatToolCompletedEvent { ToolCallId: "tool-1", Succeeded: true });
        Assert.Contains(events, item =>
            item is ChatUsageEvent { InputTokens: 12, CachedInputTokens: 3, OutputTokens: 5 });
        Assert.Contains(events, item => item is ChatTurnCompletedEvent { Succeeded: true });
        Assert.Equal(ChatEngineState.Ready, engine.State);
    }

    [Fact]
    public async Task ApprovalUserInputAndCancel_UseBidirectionalAppServerResponses()
    {
        var process = CreateHandshakeProcess(respondToTurnStart: true);
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new CodexAppServerEngine(factory, () => process);
        var events = new List<ChatEvent>();
        engine.EventReceived += (_, chatEvent) => events.Add(chatEvent);
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.Codex));
        await engine.SendMessageAsync("执行检查");

        process.EmitOutput(
            "{\"id\":\"approval-1\",\"method\":\"item/commandExecution/requestApproval\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"tool-1\",\"startedAtMs\":1,\"command\":\"git status\",\"cwd\":\"C:/project\",\"availableDecisions\":[\"accept\",\"acceptForSession\",\"decline\"]}}");

        ChatApprovalRequestedEvent approval = Assert.Single(
            events.OfType<ChatApprovalRequestedEvent>());
        Assert.Equal("approval-1", approval.RequestId);
        Assert.Equal(ChatEngineState.WaitingForApproval, engine.State);
        await engine.RespondToApprovalAsync(
            "approval-1",
            ChatApprovalDecision.AllowForSession);
        using (JsonDocument response = ChatEngineTestSupport.ParseWritten(process, 3))
        {
            Assert.Equal("approval-1", response.RootElement.GetProperty("id").GetString());
            Assert.Equal(
                "acceptForSession",
                response.RootElement.GetProperty("result").GetProperty("decision").GetString());
        }

        process.EmitOutput(
            "{\"id\":\"input-1\",\"method\":\"item/tool/requestUserInput\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"ask-1\",\"questions\":[{\"id\":\"choice\",\"header\":\"方式\",\"question\":\"选择检查方式\",\"options\":[{\"label\":\"快速\",\"description\":\"只检查关键项\"},{\"label\":\"完整\",\"description\":\"检查全部\"}]}]}}");
        ChatUserInputRequestedEvent input = Assert.Single(
            events.OfType<ChatUserInputRequestedEvent>());
        Assert.Equal(new[] { "快速", "完整" }, input.Options);
        await engine.RespondToUserInputAsync("input-1", "完整");
        using (JsonDocument response = ChatEngineTestSupport.ParseWritten(process, 4))
        {
            JsonElement answers = response.RootElement
                .GetProperty("result")
                .GetProperty("answers")
                .GetProperty("choice")
                .GetProperty("answers");
            Assert.Equal("完整", answers[0].GetString());
        }

        await engine.CancelTurnAsync();
        using JsonDocument cancel = ChatEngineTestSupport.ParseWritten(process, 5);
        Assert.Equal("turn/interrupt", cancel.RootElement.GetProperty("method").GetString());
        Assert.Equal(
            "turn-1",
            cancel.RootElement.GetProperty("params").GetProperty("turnId").GetString());
    }

    [Fact]
    public async Task PermissionRequest_DenyReturnsEmptyTurnScopedGrant()
    {
        var process = CreateHandshakeProcess(respondToTurnStart: true);
        var factory = new CliTerminalCommandFactory(new StubCredentialProvider());
        await using var engine = new CodexAppServerEngine(factory, () => process);
        await engine.StartAsync(ChatEngineTestSupport.CreateContext(CliKind.Codex));
        await engine.SendMessageAsync("访问网络");

        process.EmitOutput(
            "{\"id\":8,\"method\":\"item/permissions/requestApproval\",\"params\":{\"threadId\":\"codex-thread-1\",\"turnId\":\"turn-1\",\"itemId\":\"permission-1\",\"startedAtMs\":1,\"cwd\":\"C:/project\",\"permissions\":{\"network\":{\"enabled\":true}},\"reason\":\"需要联网\"}}");

        await engine.RespondToApprovalAsync("8", ChatApprovalDecision.Deny);
        using JsonDocument response = ChatEngineTestSupport.ParseWritten(process, 3);
        Assert.Equal(8, response.RootElement.GetProperty("id").GetInt32());
        JsonElement result = response.RootElement.GetProperty("result");
        Assert.Equal("turn", result.GetProperty("scope").GetString());
        Assert.Empty(result.GetProperty("permissions").EnumerateObject());
    }

    [Fact]
    public void ProtocolParser_AcceptsResponseRequestAndNotificationAndRejectsGarbage()
    {
        Assert.True(CodexAppServerProtocol.TryParse(
            "{\"id\":\"1\",\"result\":{}}",
            out CodexProtocolMessage? response,
            out _));
        Assert.Equal(CodexProtocolMessageKind.Response, response!.Kind);

        Assert.True(CodexAppServerProtocol.TryParse(
            "{\"id\":2,\"method\":\"item/tool/requestUserInput\",\"params\":{}}",
            out CodexProtocolMessage? request,
            out _));
        Assert.Equal(CodexProtocolMessageKind.ServerRequest, request!.Kind);

        Assert.True(CodexAppServerProtocol.TryParse(
            "{\"method\":\"turn/started\",\"params\":{}}",
            out CodexProtocolMessage? notification,
            out _));
        Assert.Equal(CodexProtocolMessageKind.Notification, notification!.Kind);

        Assert.False(CodexAppServerProtocol.TryParse("not-json", out _, out string? error));
        Assert.Contains("JSON", error, StringComparison.OrdinalIgnoreCase);
    }

    private static FakeStructuredCliProcess CreateHandshakeProcess(
        string resumedThreadId = "codex-thread-1",
        bool respondToTurnStart = false)
    {
        return new FakeStructuredCliProcess
        {
            AutoResponder = message =>
            {
                if (!message.TryGetProperty("id", out JsonElement id) ||
                    !message.TryGetProperty("method", out JsonElement methodElement))
                {
                    return Array.Empty<string>();
                }

                string method = methodElement.GetString() ?? string.Empty;
                string rawId = id.GetRawText();
                return method switch
                {
                    "initialize" =>
                    [
                        $"{{\"id\":{rawId},\"result\":{{\"codexHome\":\"C:/Users/test/.codex\",\"platformFamily\":\"windows\",\"platformOs\":\"windows\",\"userAgent\":\"codex-cli/0.144.1\"}}}}",
                    ],
                    "thread/start" or "thread/resume" or "thread/fork" =>
                    [
                        $"{{\"id\":{rawId},\"result\":{{\"thread\":{{\"id\":\"{resumedThreadId}\"}}}}}}",
                    ],
                    "turn/start" when respondToTurnStart =>
                    [
                        $"{{\"id\":{rawId},\"result\":{{\"turn\":{{\"id\":\"turn-1\",\"items\":[],\"status\":\"inProgress\"}}}}}}",
                    ],
                    "turn/interrupt" => [$"{{\"id\":{rawId},\"result\":{{}}}}"],
                    _ => Array.Empty<string>(),
                };
            },
        };
    }

    private sealed class NpmCodexShimLayout : IDisposable
    {
        public NpmCodexShimLayout(bool includeEntryPoint)
        {
            RootPath = Path.Combine(
                Path.GetTempPath(),
                "LanAi.CodexAppServerEngine.Tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(RootPath);

            CommandShimPath = Path.Combine(RootPath, "codex.cmd");
            PowerShellShimPath = Path.Combine(RootPath, "codex.ps1");
            NodeExecutablePath = Path.Combine(RootPath, "node.exe");
            EntryPointPath = Path.Combine(
                RootPath,
                "node_modules",
                "@openai",
                "codex",
                "bin",
                "codex.js");

            File.WriteAllText(CommandShimPath, "@echo off");
            File.WriteAllText(PowerShellShimPath, "# npm shim");
            File.WriteAllText(NodeExecutablePath, string.Empty);
            if (includeEntryPoint)
            {
                Directory.CreateDirectory(Path.GetDirectoryName(EntryPointPath)!);
                File.WriteAllText(EntryPointPath, "#!/usr/bin/env node");
            }
        }

        public string RootPath { get; }

        public string CommandShimPath { get; }

        public string PowerShellShimPath { get; }

        public string NodeExecutablePath { get; }

        public string EntryPointPath { get; }

        public void Dispose()
        {
            if (Directory.Exists(RootPath))
            {
                Directory.Delete(RootPath, recursive: true);
            }
        }
    }
}
