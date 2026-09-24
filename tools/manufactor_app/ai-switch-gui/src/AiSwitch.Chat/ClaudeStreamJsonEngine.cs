using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Chat;

public sealed class ClaudeStreamJsonEngine : IChatEngine
{
    private static readonly TimeSpan StopTimeout = TimeSpan.FromSeconds(3);
    private readonly CliTerminalCommandFactory _commandFactory;
    private readonly Func<IStructuredCliProcess> _processFactory;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly object _stateGate = new();
    private readonly ConcurrentDictionary<string, PendingPermission> _pendingPermissions = new();
    private readonly ConcurrentDictionary<string, PendingUserInput> _pendingUserInputs = new();
    private readonly ConcurrentDictionary<string, string> _toolNames = new();
    private IStructuredCliProcess? _process;
    private ChatEngineState _state = ChatEngineState.Created;
    private string? _nativeSessionId;
    private bool _disposed;

    public ClaudeStreamJsonEngine(CliTerminalCommandFactory commandFactory)
        : this(commandFactory, static () => new StructuredCliProcess())
    {
    }

    internal ClaudeStreamJsonEngine(
        CliTerminalCommandFactory commandFactory,
        Func<IStructuredCliProcess> processFactory)
    {
        _commandFactory = commandFactory ?? throw new ArgumentNullException(nameof(commandFactory));
        _processFactory = processFactory ?? throw new ArgumentNullException(nameof(processFactory));
    }

    public CliKind Kind => CliKind.ClaudeCode;

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
            throw new ArgumentException("聊天上下文不是 Claude Code。", nameof(context));
        }

        lock (_stateGate)
        {
            if (_state is not ChatEngineState.Created and not ChatEngineState.Stopped)
            {
                throw new InvalidOperationException("Claude 聊天引擎已经启动。");
            }

            _nativeSessionId = null;
        }
        _pendingPermissions.Clear();
        _pendingUserInputs.Clear();
        _toolNames.Clear();

        Transition(ChatEngineState.Starting, "正在启动 Claude Code 结构化会话…");
        IStructuredCliProcess process = _processFactory();
        process.OutputLineReceived += Process_OnOutputLineReceived;
        process.ErrorLineReceived += Process_OnErrorLineReceived;
        process.Exited += Process_OnExited;
        _process = process;

        try
        {
            TerminalCommand command = await _commandFactory.CreateAsync(
                context.LaunchRequest,
                context.Installation,
                context.Connection,
                cancellationToken).ConfigureAwait(false);
            command = BuildStructuredCommand(command, context.PermissionMode);
            await process.StartAsync(command, cancellationToken).ConfigureAwait(false);

            await WriteJsonAsync(new
            {
                type = "control_request",
                request_id = $"init-{Guid.NewGuid():N}",
                request = new { subtype = "initialize" },
            }, cancellationToken).ConfigureAwait(false);

            Transition(ChatEngineState.Ready, "Claude Code 已就绪。");
        }
        catch
        {
            Transition(ChatEngineState.Faulted, "Claude Code 启动失败。");
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
        Transition(ChatEngineState.RunningTurn, "Claude 正在回复…");
        try
        {
            await WriteJsonAsync(new
            {
                type = "user",
                session_id = string.Empty,
                message = new
                {
                    role = "user",
                    content = new[] { new { type = "text", text = message } },
                },
                parent_tool_use_id = (string?)null,
            }, cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            Transition(ChatEngineState.Ready, "消息发送失败，可以重试。");
            throw;
        }
    }

    public async Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default)
    {
        if (!_pendingPermissions.TryRemove(requestId, out PendingPermission? pending))
        {
            throw new KeyNotFoundException($"找不到 Claude 权限请求：{requestId}");
        }

        object response = decision switch
        {
            ChatApprovalDecision.Deny => new Dictionary<string, object?>
            {
                ["behavior"] = "deny",
                ["message"] = "User denied this tool request.",
                ["interrupt"] = false,
                ["toolUseID"] = pending.ToolUseId,
            },
            ChatApprovalDecision.AllowOnce => BuildClaudeAllowResponse(pending, includeSuggestions: false),
            ChatApprovalDecision.AllowForSession => BuildClaudeAllowResponse(pending, includeSuggestions: true),
            _ => throw new ArgumentOutOfRangeException(nameof(decision)),
        };

        await WriteControlResponseAsync(requestId, response, cancellationToken).ConfigureAwait(false);
        Transition(ChatEngineState.RunningTurn, "已提交 Claude 工具权限选择。");
    }

    public async Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default)
    {
        if (!_pendingUserInputs.TryRemove(requestId, out PendingUserInput? pending))
        {
            throw new KeyNotFoundException($"找不到 Claude 用户输入请求：{requestId}");
        }

        JsonObject updatedInput = JsonNode.Parse(pending.Input.GetRawText()) as JsonObject ?? new JsonObject();
        updatedInput["answers"] = new JsonObject { [pending.Question] = response };
        var permissionResponse = new Dictionary<string, object?>
        {
            ["behavior"] = "allow",
            ["updatedInput"] = updatedInput,
            ["toolUseID"] = pending.ToolUseId,
        };

        await WriteControlResponseAsync(requestId, permissionResponse, cancellationToken).ConfigureAwait(false);
        Transition(ChatEngineState.RunningTurn, "已提交 Claude 所需的信息。");
    }

    public async Task CancelTurnAsync(CancellationToken cancellationToken = default)
    {
        if (State is not ChatEngineState.RunningTurn and not ChatEngineState.WaitingForApproval)
        {
            return;
        }

        await WriteJsonAsync(new
        {
            type = "control_request",
            request_id = $"interrupt-{Guid.NewGuid():N}",
            request = new { subtype = "interrupt" },
        }, cancellationToken).ConfigureAwait(false);
        Publish(new ChatEngineStateEvent(
            ChatEngineState.RunningTurn,
            "已请求 Claude 取消当前回复。",
            DateTimeOffset.UtcNow));
    }

    public async Task StopAsync(CancellationToken cancellationToken = default)
    {
        IStructuredCliProcess? process = _process;
        if (process is null)
        {
            Transition(ChatEngineState.Stopped, "Claude Code 已停止。");
            return;
        }

        Transition(ChatEngineState.Stopping, "正在停止 Claude Code…");
        await process.StopAsync(StopTimeout, cancellationToken).ConfigureAwait(false);
        await DisposeProcessAsync(process).ConfigureAwait(false);
        if (ReferenceEquals(_process, process))
        {
            _process = null;
        }

        Transition(ChatEngineState.Stopped, "Claude Code 已停止。");
    }

    internal static TerminalCommand BuildStructuredCommand(
        TerminalCommand command,
        ChatPermissionMode permissionMode)
    {
        var arguments = new List<string>(command.Arguments)
        {
            "--output-format", "stream-json",
            "--verbose",
            "--input-format", "stream-json",
            "--include-partial-messages",
            "--permission-prompt-tool", "stdio",
            "--permission-mode", ToClaudePermissionMode(permissionMode),
        };

        if (permissionMode == ChatPermissionMode.FullAccess)
        {
            arguments.Add("--allow-dangerously-skip-permissions");
        }

        var environment = command.Environment is null
            ? new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase)
            : new Dictionary<string, string?>(command.Environment, StringComparer.OrdinalIgnoreCase);
        environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-ts";

        return command with
        {
            Arguments = arguments,
            Environment = environment,
        };
    }

    private static string ToClaudePermissionMode(ChatPermissionMode mode) => mode switch
    {
        ChatPermissionMode.ReadOnly => "plan",
        ChatPermissionMode.WorkspaceWrite => "default",
        ChatPermissionMode.FullAccess => "bypassPermissions",
        _ => throw new ArgumentOutOfRangeException(nameof(mode)),
    };

    private static Dictionary<string, object?> BuildClaudeAllowResponse(
        PendingPermission pending,
        bool includeSuggestions)
    {
        var response = new Dictionary<string, object?>
        {
            ["behavior"] = "allow",
            ["updatedInput"] = pending.Input,
            ["toolUseID"] = pending.ToolUseId,
        };
        if (includeSuggestions && pending.Suggestions is { } suggestions)
        {
            response["updatedPermissions"] = suggestions;
        }

        return response;
    }

    private async Task WriteControlResponseAsync(
        string requestId,
        object response,
        CancellationToken cancellationToken)
    {
        await WriteJsonAsync(new
        {
            type = "control_response",
            response = new
            {
                subtype = "success",
                request_id = requestId,
                response,
            },
        }, cancellationToken).ConfigureAwait(false);
    }

    private async Task WriteJsonAsync(object value, CancellationToken cancellationToken)
    {
        IStructuredCliProcess process = _process is { IsRunning: true } running
            ? running
            : throw new InvalidOperationException("Claude Code 结构化进程未运行。");
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
            HandleProtocolMessage(document.RootElement);
        }
        catch (JsonException exception)
        {
            Publish(new ChatErrorEvent(
                "CLAUDE_PROTOCOL_PARSE_ERROR",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
        catch (Exception exception)
        {
            Publish(new ChatErrorEvent(
                "CLAUDE_PROTOCOL_ERROR",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
    }

    private void HandleProtocolMessage(JsonElement root)
    {
        string? type = GetString(root, "type");
        switch (type)
        {
            case "control_request":
                HandleControlRequest(root);
                break;
            case "system":
                HandleSystemMessage(root);
                break;
            case "stream_event":
                HandleStreamEvent(root);
                break;
            case "assistant":
                HandleAssistantMessage(root);
                break;
            case "user":
                HandleToolResults(root);
                break;
            case "tool_progress":
                HandleToolProgress(root);
                break;
            case "result":
                HandleResult(root);
                break;
            case "auth_status":
                if (root.TryGetProperty("error", out JsonElement authError) &&
                    authError.ValueKind == JsonValueKind.String)
                {
                    Publish(new ChatErrorEvent(
                        "CLAUDE_AUTH_ERROR",
                        authError.GetString() ?? "Claude 身份验证失败。",
                        DateTimeOffset.UtcNow));
                }
                break;
                // control_response, keep_alive and future message kinds are intentionally ignored.
        }
    }

    private void HandleControlRequest(JsonElement root)
    {
        if (!root.TryGetProperty("request", out JsonElement request) ||
            GetString(request, "subtype") != "can_use_tool")
        {
            return;
        }

        string requestId = GetString(root, "request_id") ?? string.Empty;
        string toolName = GetString(request, "tool_name") ?? "Tool";
        string toolUseId = GetString(request, "tool_use_id") ?? requestId;
        JsonElement input = request.TryGetProperty("input", out JsonElement rawInput)
            ? rawInput.Clone()
            : JsonSerializer.SerializeToElement(new Dictionary<string, object?>());
        JsonElement? suggestions = request.TryGetProperty("permission_suggestions", out JsonElement rawSuggestions)
            ? rawSuggestions.Clone()
            : null;

        if (toolName.Equals("AskUserQuestion", StringComparison.OrdinalIgnoreCase))
        {
            (string question, IReadOnlyList<string> options) = ExtractClaudeQuestion(input);
            _pendingUserInputs[requestId] = new PendingUserInput(input, toolUseId, question);
            Transition(ChatEngineState.WaitingForApproval, "Claude 正在等待补充信息。");
            Publish(new ChatUserInputRequestedEvent(
                requestId,
                question,
                options,
                DateTimeOffset.UtcNow));
            return;
        }

        _pendingPermissions[requestId] = new PendingPermission(
            toolName,
            input,
            suggestions,
            toolUseId);
        string detail = BuildPermissionDetail(request, input);
        Transition(ChatEngineState.WaitingForApproval, $"Claude 请求使用 {toolName}。");
        Publish(new ChatApprovalRequestedEvent(
            requestId,
            toolName,
            detail,
            [
                ChatApprovalDecision.Deny,
                ChatApprovalDecision.AllowOnce,
                ChatApprovalDecision.AllowForSession,
            ],
            DateTimeOffset.UtcNow));
    }

    private void HandleSystemMessage(JsonElement root)
    {
        string? subtype = GetString(root, "subtype");
        if (subtype == "init")
        {
            string? sessionId = GetString(root, "session_id");
            if (!string.IsNullOrWhiteSpace(sessionId))
            {
                bool changed;
                lock (_stateGate)
                {
                    changed = !string.Equals(_nativeSessionId, sessionId, StringComparison.Ordinal);
                    _nativeSessionId = sessionId;
                }

                if (changed)
                {
                    Publish(new ChatSessionStartedEvent(sessionId, DateTimeOffset.UtcNow));
                }
            }
        }
        else if (subtype == "status")
        {
            string status = GetString(root, "status") ?? "ready";
            Publish(new ChatEngineStateEvent(State, $"Claude 状态：{status}", DateTimeOffset.UtcNow));
        }
    }

    private void HandleStreamEvent(JsonElement root)
    {
        if (!root.TryGetProperty("event", out JsonElement streamEvent) ||
            GetString(streamEvent, "type") != "content_block_delta" ||
            !streamEvent.TryGetProperty("delta", out JsonElement delta) ||
            GetString(delta, "type") != "text_delta")
        {
            return;
        }

        string? text = GetString(delta, "text");
        if (!string.IsNullOrEmpty(text))
        {
            Publish(new ChatAssistantDeltaEvent(text, DateTimeOffset.UtcNow));
        }
    }

    private void HandleAssistantMessage(JsonElement root)
    {
        if (!root.TryGetProperty("message", out JsonElement message) ||
            !message.TryGetProperty("content", out JsonElement content) ||
            content.ValueKind != JsonValueKind.Array)
        {
            return;
        }

        var textParts = new List<string>();
        foreach (JsonElement block in content.EnumerateArray())
        {
            string? blockType = GetString(block, "type");
            if (blockType == "text")
            {
                string? text = GetString(block, "text");
                if (!string.IsNullOrEmpty(text))
                {
                    textParts.Add(text);
                }
            }
            else if (blockType is "tool_use" or "server_tool_use")
            {
                string toolCallId = GetString(block, "id") ?? Guid.NewGuid().ToString("N");
                string toolName = GetString(block, "name") ?? "Tool";
                _toolNames[toolCallId] = toolName;
                string? summary = block.TryGetProperty("input", out JsonElement input)
                    ? input.GetRawText()
                    : null;
                Publish(new ChatToolStartedEvent(
                    toolCallId,
                    toolName,
                    summary,
                    DateTimeOffset.UtcNow));
            }
        }

        if (textParts.Count > 0)
        {
            Publish(new ChatAssistantMessageEvent(
                string.Concat(textParts),
                DateTimeOffset.UtcNow));
        }
    }

    private void HandleToolResults(JsonElement root)
    {
        if (!root.TryGetProperty("message", out JsonElement message) ||
            !message.TryGetProperty("content", out JsonElement content) ||
            content.ValueKind != JsonValueKind.Array)
        {
            return;
        }

        foreach (JsonElement block in content.EnumerateArray())
        {
            if (GetString(block, "type") != "tool_result")
            {
                continue;
            }

            string toolCallId = GetString(block, "tool_use_id") ?? string.Empty;
            string toolName = _toolNames.TryRemove(toolCallId, out string? knownName)
                ? knownName
                : "Tool";
            bool failed = block.TryGetProperty("is_error", out JsonElement isError) &&
                          isError.ValueKind == JsonValueKind.True;
            string? summary = block.TryGetProperty("content", out JsonElement resultContent)
                ? ExtractText(resultContent)
                : null;
            Publish(new ChatToolCompletedEvent(
                toolCallId,
                toolName,
                !failed,
                summary,
                DateTimeOffset.UtcNow));
        }
    }

    private void HandleToolProgress(JsonElement root)
    {
        string toolCallId = GetString(root, "tool_use_id") ?? string.Empty;
        string toolName = GetString(root, "tool_name") ??
                          (_toolNames.TryGetValue(toolCallId, out string? knownName) ? knownName : "Tool");
        long elapsed = GetInt64(root, "elapsed_time_seconds") ?? 0;
        Publish(new ChatToolProgressEvent(
            toolCallId,
            $"{toolName} 已运行 {elapsed} 秒",
            DateTimeOffset.UtcNow));
    }

    private void HandleResult(JsonElement root)
    {
        if (root.TryGetProperty("usage", out JsonElement usage))
        {
            Publish(new ChatUsageEvent(
                GetInt64(usage, "input_tokens"),
                GetInt64(usage, "output_tokens"),
                GetInt64(usage, "cache_read_input_tokens"),
                DateTimeOffset.UtcNow,
                GetInt64(usage, "cache_creation_input_tokens")));
        }

        string subtype = GetString(root, "subtype") ?? string.Empty;
        bool isError = root.TryGetProperty("is_error", out JsonElement rawError) &&
                       rawError.ValueKind == JsonValueKind.True;
        bool succeeded = subtype == "success" && !isError;
        string? errorMessage = succeeded ? null : ExtractResultError(root, subtype);
        Publish(new ChatTurnCompletedEvent(succeeded, errorMessage, DateTimeOffset.UtcNow));
        Transition(ChatEngineState.Ready, succeeded ? "Claude 回复完成。" : "Claude 本轮回复未成功。");
    }

    private void Process_OnErrorLineReceived(object? sender, string line)
    {
        if (!string.IsNullOrWhiteSpace(line))
        {
            Publish(new ChatErrorEvent("CLAUDE_STDERR", line, DateTimeOffset.UtcNow));
        }
    }

    private void Process_OnExited(object? sender, int exitCode)
    {
        if (State is ChatEngineState.Stopping or ChatEngineState.Stopped)
        {
            return;
        }

        Transition(ChatEngineState.Faulted, $"Claude Code 已退出（代码 {exitCode}）。");
        Publish(new ChatErrorEvent(
            "CLAUDE_PROCESS_EXITED",
            $"Claude Code 进程意外退出，代码 {exitCode}。",
            DateTimeOffset.UtcNow));
    }

    private static string BuildPermissionDetail(JsonElement request, JsonElement input)
    {
        var parts = new List<string>();
        string? reason = GetString(request, "decision_reason");
        string? blockedPath = GetString(request, "blocked_path");
        if (!string.IsNullOrWhiteSpace(reason))
        {
            parts.Add(reason);
        }

        if (!string.IsNullOrWhiteSpace(blockedPath))
        {
            parts.Add($"路径：{blockedPath}");
        }

        parts.Add(input.GetRawText());
        return string.Join(Environment.NewLine, parts);
    }

    private static (string Question, IReadOnlyList<string> Options) ExtractClaudeQuestion(JsonElement input)
    {
        if (!input.TryGetProperty("questions", out JsonElement questions) ||
            questions.ValueKind != JsonValueKind.Array ||
            questions.GetArrayLength() == 0)
        {
            return ("Claude 需要你补充信息。", Array.Empty<string>());
        }

        JsonElement question = questions[0];
        string prompt = GetString(question, "question") ?? "Claude 需要你补充信息。";
        var options = new List<string>();
        if (question.TryGetProperty("options", out JsonElement rawOptions) &&
            rawOptions.ValueKind == JsonValueKind.Array)
        {
            foreach (JsonElement option in rawOptions.EnumerateArray())
            {
                string? label = GetString(option, "label");
                if (!string.IsNullOrWhiteSpace(label))
                {
                    options.Add(label);
                }
            }
        }

        return (prompt, options);
    }

    private static string? ExtractResultError(JsonElement root, string subtype)
    {
        if (root.TryGetProperty("errors", out JsonElement errors) &&
            errors.ValueKind == JsonValueKind.Array)
        {
            string[] messages = errors.EnumerateArray()
                .Where(item => item.ValueKind == JsonValueKind.String)
                .Select(item => item.GetString())
                .Where(item => !string.IsNullOrWhiteSpace(item))
                .Cast<string>()
                .ToArray();
            if (messages.Length > 0)
            {
                return string.Join(Environment.NewLine, messages);
            }
        }

        return string.IsNullOrWhiteSpace(subtype) ? "Claude 返回了未知错误。" : subtype;
    }

    private static string? ExtractText(JsonElement value)
    {
        if (value.ValueKind == JsonValueKind.String)
        {
            return value.GetString();
        }

        if (value.ValueKind == JsonValueKind.Array)
        {
            var parts = new List<string>();
            foreach (JsonElement item in value.EnumerateArray())
            {
                string? text = item.ValueKind == JsonValueKind.String
                    ? item.GetString()
                    : GetString(item, "text");
                if (!string.IsNullOrEmpty(text))
                {
                    parts.Add(text);
                }
            }

            return parts.Count == 0 ? value.GetRawText() : string.Join(Environment.NewLine, parts);
        }

        return value.GetRawText();
    }

    private static string? GetString(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static long? GetInt64(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) && value.TryGetInt64(out long result)
            ? result
            : null;

    private void EnsureState(ChatEngineState expected)
    {
        if (State != expected)
        {
            throw new InvalidOperationException($"Claude 聊天引擎当前状态为 {State}，需要 {expected}。");
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

    private sealed record PendingPermission(
        string ToolName,
        JsonElement Input,
        JsonElement? Suggestions,
        string ToolUseId);

    private sealed record PendingUserInput(
        JsonElement Input,
        string ToolUseId,
        string Question);
}
