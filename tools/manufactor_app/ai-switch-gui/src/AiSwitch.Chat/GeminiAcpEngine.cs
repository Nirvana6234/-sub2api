using System.Collections.Concurrent;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Chat;

public sealed class GeminiAcpEngine : IChatEngine
{
    private static readonly TimeSpan StopTimeout = TimeSpan.FromSeconds(3);
    private readonly CliTerminalCommandFactory _commandFactory;
    private readonly Func<IStructuredCliProcess> _processFactory;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly object _stateGate = new();
    private readonly object _assistantGate = new();
    private readonly StringBuilder _assistantBuffer = new();
    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonElement>> _pendingResponses = new();
    private readonly ConcurrentDictionary<string, PendingApproval> _pendingApprovals = new();
    private readonly ConcurrentDictionary<string, string> _toolNames = new();
    private IStructuredCliProcess? _process;
    private ChatEngineState _state = ChatEngineState.Created;
    private string? _nativeSessionId;
    private long _nextRequestId;
    private bool _disposed;

    public GeminiAcpEngine(CliTerminalCommandFactory commandFactory)
        : this(commandFactory, static () => new StructuredCliProcess())
    {
    }

    internal GeminiAcpEngine(
        CliTerminalCommandFactory commandFactory,
        Func<IStructuredCliProcess> processFactory)
    {
        _commandFactory = commandFactory ?? throw new ArgumentNullException(nameof(commandFactory));
        _processFactory = processFactory ?? throw new ArgumentNullException(nameof(processFactory));
    }

    public CliKind Kind => CliKind.GeminiCli;

    public ChatEngineState State
    {
        get
        {
            lock (_stateGate)
            {
                return _state;
            }
        }
    }

    public string? NativeSessionId
    {
        get
        {
            lock (_stateGate)
            {
                return _nativeSessionId;
            }
        }
    }

    public event EventHandler<ChatEvent>? EventReceived;

    public async Task StartAsync(
        ChatEngineContext context,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(context);
        ObjectDisposedException.ThrowIf(_disposed, this);
        if (context.LaunchRequest.Cli != Kind || context.Installation.Kind != Kind)
        {
            throw new ArgumentException("聊天上下文不是 Gemini CLI。", nameof(context));
        }

        lock (_stateGate)
        {
            if (_state is not ChatEngineState.Created and not ChatEngineState.Stopped)
            {
                throw new InvalidOperationException("Gemini 聊天引擎已经启动。");
            }

            _nativeSessionId = null;
        }
        _pendingApprovals.Clear();
        _toolNames.Clear();
        lock (_assistantGate)
        {
            _assistantBuffer.Clear();
        }

        Transition(ChatEngineState.Starting, "正在启动 Gemini ACP 会话…");
        IStructuredCliProcess process = _processFactory();
        process.OutputLineReceived += Process_OnOutputLineReceived;
        process.ErrorLineReceived += Process_OnErrorLineReceived;
        process.Exited += Process_OnExited;
        _process = process;

        try
        {
            CliLaunchRequest factoryRequest = context.LaunchRequest with
            {
                Mode = CliLaunchMode.New,
                NativeSessionId = null,
            };
            TerminalCommand command = await _commandFactory.CreateAsync(
                factoryRequest,
                context.Installation,
                context.Connection,
                cancellationToken).ConfigureAwait(false);
            command = BuildAcpCommand(command, context.PermissionMode);
            await process.StartAsync(command, cancellationToken).ConfigureAwait(false);

            await SendRequestAsync(
                "initialize",
                new
                {
                    protocolVersion = 1,
                    clientCapabilities = new
                    {
                        auth = new { terminal = false },
                        fs = new { readTextFile = false, writeTextFile = false },
                        terminal = false,
                    },
                    clientInfo = new
                    {
                        name = "lan-ai-workspace",
                        title = "局域网 AI 工作台",
                        version = "1.0",
                    },
                },
                cancellationToken).ConfigureAwait(false);

            string? requestedSessionId = context.LaunchRequest.Mode == CliLaunchMode.Resume
                ? context.LaunchRequest.NativeSessionId
                : null;
            string nativeSessionId;
            if (!string.IsNullOrWhiteSpace(requestedSessionId))
            {
                await SendRequestAsync(
                    "session/load",
                    new
                    {
                        sessionId = requestedSessionId,
                        cwd = context.LaunchRequest.WorkingDirectory,
                        mcpServers = Array.Empty<object>(),
                    },
                    cancellationToken).ConfigureAwait(false);
                nativeSessionId = requestedSessionId;
            }
            else
            {
                JsonElement response = await SendRequestAsync(
                    "session/new",
                    new
                    {
                        cwd = context.LaunchRequest.WorkingDirectory,
                        mcpServers = Array.Empty<object>(),
                    },
                    cancellationToken).ConfigureAwait(false);
                nativeSessionId = GetString(response, "sessionId")
                    ?? throw new InvalidOperationException("Gemini ACP 没有返回 sessionId。");
            }

            lock (_stateGate)
            {
                _nativeSessionId = nativeSessionId;
            }
            Publish(new ChatSessionStartedEvent(nativeSessionId, DateTimeOffset.UtcNow));
            Transition(ChatEngineState.Ready, "Gemini ACP 已就绪。");
        }
        catch
        {
            Transition(ChatEngineState.Faulted, "Gemini ACP 启动失败。");
            FailPendingResponses(new InvalidOperationException("Gemini ACP 启动失败。"));
            await DisposeProcessAsync(process).ConfigureAwait(false);
            if (ReferenceEquals(_process, process))
            {
                _process = null;
            }

            throw;
        }
    }

    public async Task SendMessageAsync(
        string message,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(message))
        {
            throw new ArgumentException("消息不能为空。", nameof(message));
        }

        EnsureState(ChatEngineState.Ready);
        string sessionId = NativeSessionId
            ?? throw new InvalidOperationException("Gemini ACP 会话尚未建立。");
        lock (_assistantGate)
        {
            _assistantBuffer.Clear();
        }
        Transition(ChatEngineState.RunningTurn, "Gemini 正在回复…");

        try
        {
            JsonElement response = await SendRequestAsync(
                "session/prompt",
                new
                {
                    sessionId,
                    prompt = new[] { new { type = "text", text = message } },
                },
                cancellationToken).ConfigureAwait(false);
            PublishGeminiUsage(response);

            string finalText;
            lock (_assistantGate)
            {
                finalText = _assistantBuffer.ToString();
            }
            if (!string.IsNullOrEmpty(finalText))
            {
                Publish(new ChatAssistantMessageEvent(finalText, DateTimeOffset.UtcNow));
            }

            string stopReason = GetString(response, "stopReason") ?? "end_turn";
            bool succeeded = stopReason == "end_turn";
            string? error = succeeded ? null : stopReason;
            Publish(new ChatTurnCompletedEvent(succeeded, error, DateTimeOffset.UtcNow));
            Transition(ChatEngineState.Ready, succeeded ? "Gemini 回复完成。" : "Gemini 本轮回复已停止。");
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            try
            {
                await CancelTurnAsync(CancellationToken.None).ConfigureAwait(false);
            }
            catch
            {
                // The process may have exited while cancellation was requested.
            }
            Publish(new ChatTurnCompletedEvent(false, "cancelled", DateTimeOffset.UtcNow));
            Transition(ChatEngineState.Ready, "Gemini 本轮回复已取消。");
            throw;
        }
        catch (Exception exception)
        {
            Publish(new ChatTurnCompletedEvent(false, exception.Message, DateTimeOffset.UtcNow));
            Publish(new ChatErrorEvent("GEMINI_PROMPT_ERROR", exception.Message, DateTimeOffset.UtcNow));
            Transition(ChatEngineState.Ready, "Gemini 本轮回复失败，可以重试。");
            throw;
        }
    }

    public async Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default)
    {
        if (!_pendingApprovals.TryRemove(requestId, out PendingApproval? pending))
        {
            throw new KeyNotFoundException($"找不到 Gemini 权限请求：{requestId}");
        }

        PermissionOption? selected = decision switch
        {
            ChatApprovalDecision.AllowOnce => pending.Options.FirstOrDefault(option => option.Kind == "allow_once"),
            ChatApprovalDecision.AllowForSession => pending.Options.FirstOrDefault(option => option.Kind == "allow_always"),
            ChatApprovalDecision.Deny => pending.Options.FirstOrDefault(option =>
                option.Kind is "reject_once" or "reject_always"),
            _ => throw new ArgumentOutOfRangeException(nameof(decision)),
        };

        object outcome = selected is null
            ? new { outcome = "cancelled" }
            : new { outcome = "selected", optionId = selected.OptionId };
        await WriteJsonAsync(new
        {
            jsonrpc = "2.0",
            id = pending.RpcId,
            result = new { outcome },
        }, cancellationToken).ConfigureAwait(false);
        Transition(ChatEngineState.RunningTurn, "已提交 Gemini 工具权限选择。");
    }

    public Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default) =>
        throw new NotSupportedException("Gemini ACP 当前通过工具权限请求收集交互，不提供独立用户输入响应。");

    public async Task CancelTurnAsync(CancellationToken cancellationToken = default)
    {
        if (State is not ChatEngineState.RunningTurn and not ChatEngineState.WaitingForApproval)
        {
            return;
        }

        string? sessionId = NativeSessionId;
        if (string.IsNullOrWhiteSpace(sessionId))
        {
            return;
        }

        await WriteJsonAsync(new
        {
            jsonrpc = "2.0",
            method = "session/cancel",
            @params = new { sessionId },
        }, cancellationToken).ConfigureAwait(false);
        Publish(new ChatEngineStateEvent(
            ChatEngineState.RunningTurn,
            "已请求 Gemini 取消当前回复。",
            DateTimeOffset.UtcNow));
    }

    public async Task StopAsync(CancellationToken cancellationToken = default)
    {
        IStructuredCliProcess? process = _process;
        if (process is null)
        {
            Transition(ChatEngineState.Stopped, "Gemini ACP 已停止。");
            return;
        }

        Transition(ChatEngineState.Stopping, "正在停止 Gemini ACP…");
        FailPendingResponses(new OperationCanceledException("Gemini ACP 已停止。"));
        await process.StopAsync(StopTimeout, cancellationToken).ConfigureAwait(false);
        await DisposeProcessAsync(process).ConfigureAwait(false);
        if (ReferenceEquals(_process, process))
        {
            _process = null;
        }

        Transition(ChatEngineState.Stopped, "Gemini ACP 已停止。");
    }

    internal static TerminalCommand BuildAcpCommand(
        TerminalCommand command,
        ChatPermissionMode permissionMode)
    {
        var arguments = new List<string>(command.Arguments)
        {
            "--acp",
            "--approval-mode",
            permissionMode switch
            {
                ChatPermissionMode.ReadOnly => "plan",
                ChatPermissionMode.WorkspaceWrite => "default",
                ChatPermissionMode.FullAccess => "yolo",
                _ => throw new ArgumentOutOfRangeException(nameof(permissionMode)),
            },
        };
        return command with { Arguments = arguments };
    }

    private async Task<JsonElement> SendRequestAsync(
        string method,
        object parameters,
        CancellationToken cancellationToken)
    {
        long id = Interlocked.Increment(ref _nextRequestId);
        var completion = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        if (!_pendingResponses.TryAdd(id, completion))
        {
            throw new InvalidOperationException("无法登记 Gemini ACP 请求。");
        }

        try
        {
            await WriteJsonAsync(new
            {
                jsonrpc = "2.0",
                id,
                method,
                @params = parameters,
            }, cancellationToken).ConfigureAwait(false);
            return await completion.Task.WaitAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _pendingResponses.TryRemove(id, out _);
        }
    }

    private async Task WriteJsonAsync(object value, CancellationToken cancellationToken)
    {
        IStructuredCliProcess process = _process is { IsRunning: true } running
            ? running
            : throw new InvalidOperationException("Gemini ACP 进程未运行。");
        string json = JsonSerializer.Serialize(value);
        await _writeLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await process.WriteLineAsync(json, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private void Process_OnOutputLineReceived(object? sender, string line)
    {
        try
        {
            using JsonDocument document = JsonDocument.Parse(line);
            JsonElement root = document.RootElement;
            if (root.TryGetProperty("method", out JsonElement methodElement) &&
                methodElement.ValueKind == JsonValueKind.String)
            {
                HandleAgentMessage(root, methodElement.GetString() ?? string.Empty);
                return;
            }

            HandleRpcResponse(root);
        }
        catch (JsonException exception)
        {
            Publish(new ChatErrorEvent(
                "GEMINI_ACP_PARSE_ERROR",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
        catch (Exception exception)
        {
            Publish(new ChatErrorEvent(
                "GEMINI_ACP_PROTOCOL_ERROR",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
    }

    private void HandleRpcResponse(JsonElement root)
    {
        if (!root.TryGetProperty("id", out JsonElement idElement) ||
            !idElement.TryGetInt64(out long id) ||
            !_pendingResponses.TryGetValue(id, out TaskCompletionSource<JsonElement>? completion))
        {
            return;
        }

        if (root.TryGetProperty("error", out JsonElement error))
        {
            string message = GetString(error, "message") ?? error.GetRawText();
            completion.TrySetException(new InvalidOperationException(message));
        }
        else if (root.TryGetProperty("result", out JsonElement result))
        {
            completion.TrySetResult(result.Clone());
        }
        else
        {
            completion.TrySetResult(JsonSerializer.SerializeToElement(new Dictionary<string, object?>()));
        }
    }

    private void HandleAgentMessage(JsonElement root, string method)
    {
        if (method == "session/update")
        {
            if (root.TryGetProperty("params", out JsonElement parameters) &&
                parameters.TryGetProperty("update", out JsonElement update))
            {
                HandleSessionUpdate(update);
            }
            return;
        }

        if (method == "session/request_permission" && root.TryGetProperty("id", out JsonElement id))
        {
            HandlePermissionRequest(root, id);
            return;
        }

        if (root.TryGetProperty("id", out JsonElement unknownId))
        {
            _ = RespondMethodNotFoundAsync(unknownId.Clone());
        }
    }

    private void HandleSessionUpdate(JsonElement update)
    {
        string? updateType = GetString(update, "sessionUpdate");
        switch (updateType)
        {
            case "agent_message_chunk":
                if (update.TryGetProperty("content", out JsonElement content))
                {
                    string? text = GetContentText(content);
                    if (!string.IsNullOrEmpty(text))
                    {
                        if (State == ChatEngineState.RunningTurn)
                        {
                            lock (_assistantGate)
                            {
                                _assistantBuffer.Append(text);
                            }
                        }
                        Publish(new ChatAssistantDeltaEvent(text, DateTimeOffset.UtcNow));
                    }
                }
                break;
            case "tool_call":
            case "tool_call_update":
                HandleToolUpdate(update);
                break;
            case "agent_thought_chunk":
                // The common contract intentionally has no thought event yet.
                break;
                // Future ACP session updates are ignored without faulting the session.
        }
    }

    private void HandleToolUpdate(JsonElement update)
    {
        string toolCallId = GetString(update, "toolCallId") ?? string.Empty;
        string title = GetString(update, "title") ??
                       (_toolNames.TryGetValue(toolCallId, out string? knownName) ? knownName : "Tool");
        string status = GetString(update, "status") ?? "in_progress";
        string? summary = update.TryGetProperty("content", out JsonElement content)
            ? SummarizeToolContent(content)
            : null;

        if (_toolNames.TryAdd(toolCallId, title))
        {
            Publish(new ChatToolStartedEvent(
                toolCallId,
                title,
                summary,
                DateTimeOffset.UtcNow));
        }

        if (status is "completed" or "failed")
        {
            _toolNames.TryRemove(toolCallId, out _);
            Publish(new ChatToolCompletedEvent(
                toolCallId,
                title,
                status == "completed",
                summary,
                DateTimeOffset.UtcNow));
        }
        else if (!string.IsNullOrWhiteSpace(summary))
        {
            Publish(new ChatToolProgressEvent(
                toolCallId,
                summary,
                DateTimeOffset.UtcNow));
        }
    }

    private void HandlePermissionRequest(JsonElement root, JsonElement id)
    {
        if (!root.TryGetProperty("params", out JsonElement parameters))
        {
            return;
        }

        string requestId = ToExternalRequestId(id);
        var options = new List<PermissionOption>();
        if (parameters.TryGetProperty("options", out JsonElement rawOptions) &&
            rawOptions.ValueKind == JsonValueKind.Array)
        {
            foreach (JsonElement option in rawOptions.EnumerateArray())
            {
                string? optionId = GetString(option, "optionId");
                if (!string.IsNullOrWhiteSpace(optionId))
                {
                    options.Add(new PermissionOption(
                        optionId,
                        GetString(option, "kind") ?? string.Empty,
                        GetString(option, "name") ?? optionId));
                }
            }
        }

        JsonElement toolCall = parameters.TryGetProperty("toolCall", out JsonElement rawToolCall)
            ? rawToolCall
            : default;
        string title = toolCall.ValueKind == JsonValueKind.Object
            ? GetString(toolCall, "title") ?? "Gemini 工具"
            : "Gemini 工具";
        string detail = toolCall.ValueKind == JsonValueKind.Object &&
                        toolCall.TryGetProperty("content", out JsonElement content)
            ? SummarizeToolContent(content) ?? title
            : title;
        var decisions = new List<ChatApprovalDecision> { ChatApprovalDecision.Deny };
        if (options.Any(option => option.Kind == "allow_once"))
        {
            decisions.Add(ChatApprovalDecision.AllowOnce);
        }
        if (options.Any(option => option.Kind == "allow_always"))
        {
            decisions.Add(ChatApprovalDecision.AllowForSession);
        }

        _pendingApprovals[requestId] = new PendingApproval(id.Clone(), options);
        Transition(ChatEngineState.WaitingForApproval, $"Gemini 请求使用 {title}。");
        Publish(new ChatApprovalRequestedEvent(
            requestId,
            title,
            detail,
            decisions,
            DateTimeOffset.UtcNow));
    }

    private async Task RespondMethodNotFoundAsync(JsonElement id)
    {
        try
        {
            await WriteJsonAsync(new
            {
                jsonrpc = "2.0",
                id,
                error = new { code = -32601, message = "Method not supported by Lan AI Workspace." },
            }, CancellationToken.None).ConfigureAwait(false);
        }
        catch
        {
            // The process may already be shutting down.
        }
    }

    private void PublishGeminiUsage(JsonElement response)
    {
        if (!response.TryGetProperty("_meta", out JsonElement metadata) ||
            !metadata.TryGetProperty("quota", out JsonElement quota) ||
            !quota.TryGetProperty("token_count", out JsonElement tokenCount))
        {
            return;
        }

        Publish(new ChatUsageEvent(
            GetInt64(tokenCount, "input_tokens"),
            GetInt64(tokenCount, "output_tokens"),
            null,
            DateTimeOffset.UtcNow));
    }

    private void Process_OnErrorLineReceived(object? sender, string line)
    {
        if (!string.IsNullOrWhiteSpace(line))
        {
            Publish(new ChatErrorEvent("GEMINI_STDERR", line, DateTimeOffset.UtcNow));
        }
    }

    private void Process_OnExited(object? sender, int exitCode)
    {
        if (State is ChatEngineState.Stopping or ChatEngineState.Stopped)
        {
            return;
        }

        var exception = new InvalidOperationException($"Gemini ACP 进程意外退出，代码 {exitCode}。");
        FailPendingResponses(exception);
        Transition(ChatEngineState.Faulted, exception.Message);
        Publish(new ChatErrorEvent("GEMINI_PROCESS_EXITED", exception.Message, DateTimeOffset.UtcNow));
    }

    private void FailPendingResponses(Exception exception)
    {
        foreach (TaskCompletionSource<JsonElement> completion in _pendingResponses.Values)
        {
            completion.TrySetException(exception);
        }
        _pendingResponses.Clear();
    }

    private static string ToExternalRequestId(JsonElement id) => id.ValueKind switch
    {
        JsonValueKind.String => id.GetString() ?? string.Empty,
        JsonValueKind.Number => id.GetRawText(),
        _ => id.GetRawText(),
    };

    private static string? GetContentText(JsonElement content)
    {
        if (content.ValueKind == JsonValueKind.String)
        {
            return content.GetString();
        }
        return GetString(content, "text");
    }

    private static string? SummarizeToolContent(JsonElement content)
    {
        if (content.ValueKind == JsonValueKind.String)
        {
            return content.GetString();
        }
        if (content.ValueKind != JsonValueKind.Array)
        {
            return content.GetRawText();
        }

        var parts = new List<string>();
        foreach (JsonElement item in content.EnumerateArray())
        {
            string? type = GetString(item, "type");
            if (type == "content" && item.TryGetProperty("content", out JsonElement nested))
            {
                string? text = GetContentText(nested);
                if (!string.IsNullOrWhiteSpace(text))
                {
                    parts.Add(text);
                }
            }
            else if (type == "diff")
            {
                string path = GetString(item, "path") ?? "文件";
                parts.Add($"修改 {path}");
            }
        }

        return parts.Count == 0 ? content.GetRawText() : string.Join(Environment.NewLine, parts);
    }

    private static string? GetString(JsonElement element, string propertyName) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static long? GetInt64(JsonElement element, string propertyName) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.TryGetInt64(out long result)
            ? result
            : null;

    private void EnsureState(ChatEngineState expected)
    {
        if (State != expected)
        {
            throw new InvalidOperationException($"Gemini 聊天引擎当前状态为 {State}，需要 {expected}。");
        }
    }

    private void Transition(ChatEngineState state, string message)
    {
        lock (_stateGate)
        {
            _state = state;
        }
        Publish(new ChatEngineStateEvent(state, message, DateTimeOffset.UtcNow));
    }

    private void Publish(ChatEvent chatEvent)
    {
        EventHandler<ChatEvent>? handlers = EventReceived;
        if (handlers is null)
        {
            return;
        }

        foreach (EventHandler<ChatEvent> handler in handlers.GetInvocationList())
        {
            try
            {
                handler(this, chatEvent);
            }
            catch
            {
                // A UI subscriber must not terminate the protocol reader.
            }
        }
    }

    private async ValueTask DisposeProcessAsync(IStructuredCliProcess process)
    {
        process.OutputLineReceived -= Process_OnOutputLineReceived;
        process.ErrorLineReceived -= Process_OnErrorLineReceived;
        process.Exited -= Process_OnExited;
        await process.DisposeAsync().ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        await StopAsync().ConfigureAwait(false);
        _disposed = true;
        _writeLock.Dispose();
    }

    private sealed record PermissionOption(string OptionId, string Kind, string Name);

    private sealed record PendingApproval(JsonElement RpcId, IReadOnlyList<PermissionOption> Options);
}
