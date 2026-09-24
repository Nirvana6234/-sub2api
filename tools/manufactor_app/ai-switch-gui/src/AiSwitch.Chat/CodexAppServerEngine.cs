using System.Collections.Concurrent;
using System.Diagnostics.CodeAnalysis;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace LanAi.Workspace.Chat;

/// <summary>
/// Hosts the official Codex app-server protocol used by graphical Codex clients.
/// The protocol is newline-delimited, bidirectional JSON over stdio.
/// </summary>
public sealed class CodexAppServerEngine : IChatEngine
{
    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(20);
    private static readonly TimeSpan StopTimeout = TimeSpan.FromSeconds(3);

    private readonly CliTerminalCommandFactory _commandFactory;
    private readonly Func<IStructuredCliProcess> _processFactory;
    private readonly ConcurrentDictionary<string, PendingClientRequest> _clientRequests = new();
    private readonly ConcurrentDictionary<string, PendingServerRequest> _serverRequests = new();
    private readonly ConcurrentDictionary<string, string> _toolNames = new();
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly SemaphoreSlim _lifecycleLock = new(1, 1);
    private readonly object _stateGate = new();

    private IStructuredCliProcess? _process;
    private ChatEngineContext? _context;
    private ChatEngineState _state = ChatEngineState.Created;
    private string? _nativeSessionId;
    private string? _currentTurnId;
    private string? _lastErrorLine;
    private long _nextRequestId;
    private bool _disposed;

    public CodexAppServerEngine(CliTerminalCommandFactory commandFactory)
        : this(commandFactory, static () => new StructuredCliProcess())
    {
    }

    internal CodexAppServerEngine(
        CliTerminalCommandFactory commandFactory,
        Func<IStructuredCliProcess> processFactory)
    {
        _commandFactory = commandFactory ?? throw new ArgumentNullException(nameof(commandFactory));
        _processFactory = processFactory ?? throw new ArgumentNullException(nameof(processFactory));
    }

    public CliKind Kind => CliKind.Codex;

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
            throw new ArgumentException("聊天上下文不是 Codex。", nameof(context));
        }

        string workingDirectory = Path.GetFullPath(context.LaunchRequest.WorkingDirectory);
        if (!Directory.Exists(workingDirectory))
        {
            throw new DirectoryNotFoundException(
                $"项目目录已经不存在：{workingDirectory}。请从项目列表删除该失效项目，或重新选择目录。");
        }

        if (context.LaunchRequest.Mode != CliLaunchMode.New &&
            string.IsNullOrWhiteSpace(context.LaunchRequest.NativeSessionId))
        {
            throw new ArgumentException("恢复或分叉 Codex 会话时必须提供原生会话 ID。", nameof(context));
        }

        await _lifecycleLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (State is not ChatEngineState.Created and not ChatEngineState.Stopped)
            {
                throw new InvalidOperationException("Codex 聊天引擎已经启动。");
            }

            ResetForStart(context);
            Transition(ChatEngineState.Starting, "正在启动 Codex 图形会话服务…");

            IStructuredCliProcess process = _processFactory();
            Subscribe(process);
            _process = process;

            try
            {
                TerminalCommand command = await CreateAppServerCommandAsync(
                    context,
                    cancellationToken).ConfigureAwait(false);
                await process.StartAsync(command, cancellationToken).ConfigureAwait(false);

                await SendRequestAsync(
                    "initialize",
                    new
                    {
                        clientInfo = new
                        {
                            name = "lan-ai-workspace",
                            title = "局域网 AI 工作台",
                            version = typeof(CodexAppServerEngine).Assembly.GetName().Version?.ToString() ?? "1.0.0",
                        },
                        capabilities = new
                        {
                            // Keep the experimental request surface disabled until
                            // every experimental server request has a matching UI
                            // handler.  The supported app-server handshake and
                            // thread APIs do not require it.
                            experimentalApi = false,
                            mcpServerOpenaiFormElicitation = true,
                            requestAttestation = false,
                        },
                    },
                    cancellationToken).ConfigureAwait(false);

                JsonElement threadResult = await StartOrResumeThreadAsync(
                    context,
                    workingDirectory,
                    cancellationToken).ConfigureAwait(false);
                string sessionId = ReadThreadId(threadResult) ??
                    throw new InvalidDataException("Codex app-server 未返回 thread.id。");

                SetNativeSessionId(sessionId);
                Transition(ChatEngineState.Ready, "Codex 已就绪，可以直接对话。");
            }
            catch
            {
                Transition(ChatEngineState.Faulted, "Codex 图形会话服务启动失败。");
                await DisposeProcessAsync(process).ConfigureAwait(false);
                if (ReferenceEquals(_process, process))
                {
                    _process = null;
                }

                throw;
            }
        }
        finally
        {
            _lifecycleLock.Release();
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

        ChatEngineContext context = _context ??
            throw new InvalidOperationException("Codex 聊天引擎尚未启动。");
        string threadId = NativeSessionId ??
            throw new InvalidOperationException("Codex 会话 ID 尚未建立。");
        EnsureState(ChatEngineState.Ready);

        Transition(ChatEngineState.RunningTurn, "Codex 正在回复…");
        try
        {
            var parameters = new Dictionary<string, object?>
            {
                ["threadId"] = threadId,
                ["input"] = new[]
                {
                    new Dictionary<string, object?>
                    {
                        ["type"] = "text",
                        ["text"] = message,
                    },
                },
                ["cwd"] = Path.GetFullPath(context.LaunchRequest.WorkingDirectory),
            };
            AddIfNotBlank(parameters, "model", context.LaunchRequest.Model);

            JsonElement result = await SendRequestAsync(
                "turn/start",
                parameters,
                cancellationToken).ConfigureAwait(false);
            string? turnId = ReadNestedString(result, "turn", "id");
            if (!string.IsNullOrWhiteSpace(turnId))
            {
                lock (_stateGate)
                {
                    _currentTurnId ??= turnId;
                }
            }
        }
        catch
        {
            lock (_stateGate)
            {
                _currentTurnId = null;
            }

            Transition(ChatEngineState.Ready, "消息发送失败，可以重试。");
            throw;
        }
    }

    public async Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default)
    {
        if (!_serverRequests.TryRemove(requestId, out PendingServerRequest? request) ||
            request.Kind is not PendingServerRequestKind.CommandApproval and
                not PendingServerRequestKind.FileApproval and
                not PendingServerRequestKind.PermissionsApproval)
        {
            throw new KeyNotFoundException($"找不到 Codex 权限请求：{requestId}");
        }

        object result = request.Kind switch
        {
            PendingServerRequestKind.CommandApproval or PendingServerRequestKind.FileApproval =>
                new Dictionary<string, object?>
                {
                    ["decision"] = ToCodexApprovalDecision(decision),
                },
            PendingServerRequestKind.PermissionsApproval =>
                BuildPermissionsResponse(request.Params, decision),
            _ => throw new InvalidOperationException("未知 Codex 权限请求。"),
        };

        await WriteProtocolLineAsync(
            CodexAppServerProtocol.SerializeResponse(request.Id, result),
            cancellationToken).ConfigureAwait(false);
        RestoreRunningStateAfterServerResponse("已提交 Codex 权限选择。");
    }

    public async Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default)
    {
        if (!_serverRequests.TryRemove(requestId, out PendingServerRequest? request) ||
            request.Kind is not PendingServerRequestKind.UserInput and
                not PendingServerRequestKind.McpElicitation)
        {
            throw new KeyNotFoundException($"找不到 Codex 用户输入请求：{requestId}");
        }

        object result = request.Kind == PendingServerRequestKind.UserInput
            ? BuildUserInputResponse(request.QuestionIds, response)
            : BuildMcpElicitationResponse(request, response);
        await WriteProtocolLineAsync(
            CodexAppServerProtocol.SerializeResponse(request.Id, result),
            cancellationToken).ConfigureAwait(false);
        RestoreRunningStateAfterServerResponse("已提交 Codex 所需的信息。");
    }

    public async Task CancelTurnAsync(CancellationToken cancellationToken = default)
    {
        string? threadId = NativeSessionId;
        string? turnId;
        lock (_stateGate)
        {
            turnId = _currentTurnId;
        }

        if (string.IsNullOrWhiteSpace(threadId) || string.IsNullOrWhiteSpace(turnId) ||
            State is not ChatEngineState.RunningTurn and not ChatEngineState.WaitingForApproval)
        {
            return;
        }

        await DeclinePendingRequestsAsync(cancellationToken).ConfigureAwait(false);
        await SendRequestAsync(
            "turn/interrupt",
            new { threadId, turnId },
            cancellationToken).ConfigureAwait(false);
        Publish(new ChatEngineStateEvent(
            State,
            "已请求 Codex 取消当前回复。",
            DateTimeOffset.UtcNow));
    }

    public async Task StopAsync(CancellationToken cancellationToken = default)
    {
        await _lifecycleLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            IStructuredCliProcess? process = _process;
            if (process is null)
            {
                Transition(ChatEngineState.Stopped, "Codex 已停止。");
                return;
            }

            Transition(ChatEngineState.Stopping, "正在停止 Codex 图形会话服务…");
            FailAllClientRequests(new OperationCanceledException("Codex app-server 正在停止。"));
            _serverRequests.Clear();
            await process.StopAsync(StopTimeout, cancellationToken).ConfigureAwait(false);
            await DisposeProcessAsync(process).ConfigureAwait(false);
            if (ReferenceEquals(_process, process))
            {
                _process = null;
            }

            lock (_stateGate)
            {
                _currentTurnId = null;
            }

            Transition(ChatEngineState.Stopped, "Codex 已停止。");
        }
        finally
        {
            _lifecycleLock.Release();
        }
    }

    /// <summary>
    /// Builds the one non-interactive command used by the graphical Codex
    /// client.  npm's Windows <c>codex.cmd</c> shim has a companion
    /// <c>codex.ps1</c>; the normal terminal intentionally uses that shim, but
    /// PowerShell's <c>-File</c> host can wait for redirected stdin to reach EOF
    /// before it forwards the app-server protocol.  When the canonical npm
    /// package layout is present, launch its JavaScript entry point through the
    /// resolved node.exe instead.  All other command paths keep the normal
    /// factory output as a safe fallback.
    /// </summary>
    internal static TerminalCommand BuildAppServerCommand(
        TerminalCommand command,
        string? installationExecutablePath = null)
    {
        ArgumentNullException.ThrowIfNull(command);

        return TryRewriteNpmPowerShellShim(
            command,
            installationExecutablePath,
            out TerminalCommand? directNodeCommand)
            ? AppendAppServerArguments(directNodeCommand)
            : AppendAppServerArguments(command);
    }

    private async Task<TerminalCommand> CreateAppServerCommandAsync(
        ChatEngineContext context,
        CancellationToken cancellationToken)
    {
        CliLaunchRequest request = context.LaunchRequest with
        {
            Mode = CliLaunchMode.New,
            Model = null,
            NativeSessionId = null,
            AdditionalArguments = Array.Empty<string>(),
        };
        TerminalCommand command = await _commandFactory.CreateAsync(
            request,
            context.Installation,
            context.Connection,
            cancellationToken).ConfigureAwait(false);

        if (context.LaunchRequest.AdditionalArguments.Count > 0)
        {
            command = command with
            {
                Arguments = [.. command.Arguments, .. context.LaunchRequest.AdditionalArguments],
            };
        }

        return BuildAppServerCommand(command, context.Installation.ExecutablePath);
    }

    private static TerminalCommand AppendAppServerArguments(TerminalCommand command) =>
        command with
        {
            Arguments = [.. command.Arguments, "app-server", "--stdio"],
            DisplayName = "Codex · 图形会话服务",
        };

    private static bool TryRewriteNpmPowerShellShim(
        TerminalCommand command,
        string? installationExecutablePath,
        [NotNullWhen(true)] out TerminalCommand? directNodeCommand)
    {
        directNodeCommand = null;
        if (!OperatingSystem.IsWindows() ||
            string.IsNullOrWhiteSpace(installationExecutablePath) ||
            !IsPowerShell(command.FileName) ||
            !TryExtractPowerShellFileArguments(command.Arguments, out string? scriptPath, out IReadOnlyList<string>? cliArguments) ||
            !IsNpmCodexShimPair(installationExecutablePath, scriptPath) ||
            !TryResolveNpmCodexEntryPoint(scriptPath, out string? entryPoint))
        {
            return false;
        }

        string? shimDirectory = Path.GetDirectoryName(scriptPath);
        if (string.IsNullOrWhiteSpace(shimDirectory) ||
            !TryResolveNodeRuntime(shimDirectory, out string? nodeRuntime))
        {
            return false;
        }

        // Reuse the exact environment dictionary created by CliTerminalCommandFactory.
        // In particular, OPENAI_API_KEY remains a process environment variable and
        // is never moved into the command line or logged here.
        directNodeCommand = command with
        {
            FileName = nodeRuntime,
            Arguments = [entryPoint, .. cliArguments],
        };
        return true;
    }

    private static bool IsPowerShell(string fileName)
    {
        string executable = Path.GetFileNameWithoutExtension(fileName);
        return string.Equals(executable, "powershell", StringComparison.OrdinalIgnoreCase) ||
               string.Equals(executable, "pwsh", StringComparison.OrdinalIgnoreCase);
    }

    private static bool TryExtractPowerShellFileArguments(
        IReadOnlyList<string> arguments,
        [NotNullWhen(true)] out string? scriptPath,
        [NotNullWhen(true)] out IReadOnlyList<string>? cliArguments)
    {
        scriptPath = null;
        cliArguments = null;
        for (int index = 0; index + 1 < arguments.Count; index++)
        {
            if (!string.Equals(arguments[index], "-File", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            string candidate = arguments[index + 1];
            if (!candidate.EndsWith(".ps1", StringComparison.OrdinalIgnoreCase) ||
                !string.Equals(Path.GetFileNameWithoutExtension(candidate), "codex", StringComparison.OrdinalIgnoreCase))
            {
                return false;
            }

            try
            {
                scriptPath = Path.GetFullPath(candidate);
                cliArguments = arguments.Skip(index + 2).ToArray();
                return true;
            }
            catch (Exception exception) when (
                exception is ArgumentException or NotSupportedException or PathTooLongException)
            {
                return false;
            }
        }

        return false;
    }

    private static bool IsNpmCodexShimPair(
        string installationExecutablePath,
        string scriptPath)
    {
        try
        {
            string installationPath = Path.GetFullPath(installationExecutablePath);
            string extension = Path.GetExtension(installationPath);
            if (!string.Equals(Path.GetFileNameWithoutExtension(installationPath), "codex", StringComparison.OrdinalIgnoreCase) ||
                (extension.Length > 0 &&
                 !extension.Equals(".cmd", StringComparison.OrdinalIgnoreCase) &&
                 !extension.Equals(".bat", StringComparison.OrdinalIgnoreCase) &&
                 !extension.Equals(".ps1", StringComparison.OrdinalIgnoreCase)))
            {
                return false;
            }

            string? installationDirectory = Path.GetDirectoryName(installationPath);
            string? scriptDirectory = Path.GetDirectoryName(scriptPath);
            return !string.IsNullOrWhiteSpace(installationDirectory) &&
                   !string.IsNullOrWhiteSpace(scriptDirectory) &&
                   string.Equals(
                       installationDirectory,
                       scriptDirectory,
                       StringComparison.OrdinalIgnoreCase);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }

    private static bool TryResolveNpmCodexEntryPoint(
        string scriptPath,
        [NotNullWhen(true)] out string? entryPoint)
    {
        entryPoint = null;
        string? shimDirectory = Path.GetDirectoryName(scriptPath);
        if (string.IsNullOrWhiteSpace(shimDirectory))
        {
            return false;
        }

        string candidate = Path.Combine(
            shimDirectory,
            "node_modules",
            "@openai",
            "codex",
            "bin",
            "codex.js");
        if (!File.Exists(candidate))
        {
            return false;
        }

        entryPoint = candidate;
        return true;
    }

    private static bool TryResolveNodeRuntime(
        string shimDirectory,
        [NotNullWhen(true)] out string? nodeRuntime)
    {
        nodeRuntime = null;
        string localNode = Path.Combine(shimDirectory, "node.exe");
        if (File.Exists(localNode))
        {
            nodeRuntime = localNode;
            return true;
        }

        string? path = Environment.GetEnvironmentVariable("PATH");
        if (string.IsNullOrWhiteSpace(path))
        {
            return false;
        }

        foreach (string entry in path.Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            try
            {
                string candidate = Path.Combine(Environment.ExpandEnvironmentVariables(entry.Trim('"')), "node.exe");
                if (File.Exists(candidate))
                {
                    nodeRuntime = candidate;
                    return true;
                }
            }
            catch (Exception exception) when (
                exception is ArgumentException or NotSupportedException or PathTooLongException)
            {
                // A malformed PATH component must not prevent the normal shim
                // fallback from starting.
            }
        }

        return false;
    }

    private async Task<JsonElement> StartOrResumeThreadAsync(
        ChatEngineContext context,
        string workingDirectory,
        CancellationToken cancellationToken)
    {
        var parameters = new Dictionary<string, object?>
        {
            ["cwd"] = workingDirectory,
            ["approvalPolicy"] = "on-request",
            ["approvalsReviewer"] = "user",
            ["sandbox"] = ToSandboxMode(context.PermissionMode),
        };
        // runtimeWorkspaceRoots is an experimental-only app-server field.
        // The client deliberately does not negotiate experimentalApi because it
        // does not implement the whole experimental server-request surface.
        // cwd already establishes the workspace for the supported v2
        // thread/start, thread/resume and thread/fork APIs.
        AddIfNotBlank(parameters, "model", context.LaunchRequest.Model);

        string method;
        switch (context.LaunchRequest.Mode)
        {
            case CliLaunchMode.New:
                method = "thread/start";
                break;
            case CliLaunchMode.Resume:
                method = "thread/resume";
                parameters["threadId"] = context.LaunchRequest.NativeSessionId;
                break;
            case CliLaunchMode.Fork:
                method = "thread/fork";
                parameters["threadId"] = context.LaunchRequest.NativeSessionId;
                break;
            default:
                throw new ArgumentOutOfRangeException(nameof(context.LaunchRequest.Mode));
        }

        return await SendRequestAsync(method, parameters, cancellationToken).ConfigureAwait(false);
    }

    private async Task<JsonElement> SendRequestAsync(
        string method,
        object? parameters,
        CancellationToken cancellationToken)
    {
        string requestId = Interlocked.Increment(ref _nextRequestId).ToString();
        var pending = new PendingClientRequest(method);
        if (!_clientRequests.TryAdd(requestId, pending))
        {
            throw new InvalidOperationException($"Codex 请求 ID 冲突：{requestId}");
        }

        try
        {
            await WriteProtocolLineAsync(
                CodexAppServerProtocol.SerializeRequest(requestId, method, parameters),
                cancellationToken).ConfigureAwait(false);
            return await pending.Completion.Task
                .WaitAsync(RequestTimeout, cancellationToken)
                .ConfigureAwait(false);
        }
        finally
        {
            _clientRequests.TryRemove(requestId, out _);
        }
    }

    private async Task WriteProtocolLineAsync(
        string line,
        CancellationToken cancellationToken)
    {
        IStructuredCliProcess process = _process is { IsRunning: true } running
            ? running
            : throw new InvalidOperationException("Codex app-server 进程未运行。");

        await _writeLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await process.WriteLineAsync(line, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private void Process_OnOutputLineReceived(object? sender, string line)
    {
        if (!CodexAppServerProtocol.TryParse(line, out CodexProtocolMessage? message, out string? error))
        {
            Publish(new ChatErrorEvent(
                "CODEX_PROTOCOL_PARSE_ERROR",
                error ?? "无法解析 Codex app-server 消息。",
                DateTimeOffset.UtcNow));
            return;
        }

        try
        {
            switch (message!.Kind)
            {
                case CodexProtocolMessageKind.Response:
                case CodexProtocolMessageKind.ErrorResponse:
                    HandleResponse(message);
                    break;
                case CodexProtocolMessageKind.ServerRequest:
                    HandleServerRequest(message);
                    break;
                case CodexProtocolMessageKind.Notification:
                    HandleNotification(message.Method!, message.Params);
                    break;
            }
        }
        catch (Exception exception)
        {
            Publish(new ChatErrorEvent(
                "CODEX_PROTOCOL_ERROR",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
    }

    private void HandleResponse(CodexProtocolMessage message)
    {
        if (message.Id is not JsonElement id)
        {
            return;
        }

        string requestId = CodexAppServerProtocol.NormalizeId(id);
        if (!_clientRequests.TryRemove(requestId, out PendingClientRequest? pending))
        {
            return;
        }

        if (message.Kind == CodexProtocolMessageKind.ErrorResponse)
        {
            pending.Completion.TrySetException(new CodexAppServerException(
                pending.Method,
                message.ErrorCode,
                message.ErrorMessage ?? "未知错误"));
            return;
        }

        pending.Completion.TrySetResult(message.Result ?? JsonSerializer.SerializeToElement(new { }));
    }

    private void HandleServerRequest(CodexProtocolMessage message)
    {
        if (message.Id is not JsonElement id || message.Method is null)
        {
            return;
        }

        JsonElement parameters = message.Params ?? JsonSerializer.SerializeToElement(new { });
        string requestId = CodexAppServerProtocol.NormalizeId(id);
        switch (message.Method)
        {
            case "item/commandExecution/requestApproval":
                AddApprovalRequest(
                    requestId,
                    id,
                    message.Method,
                    parameters,
                    PendingServerRequestKind.CommandApproval,
                    "执行命令",
                    BuildCommandApprovalDetail(parameters));
                break;
            case "item/fileChange/requestApproval":
                AddApprovalRequest(
                    requestId,
                    id,
                    message.Method,
                    parameters,
                    PendingServerRequestKind.FileApproval,
                    "修改文件",
                    BuildFileApprovalDetail(parameters));
                break;
            case "item/permissions/requestApproval":
                AddApprovalRequest(
                    requestId,
                    id,
                    message.Method,
                    parameters,
                    PendingServerRequestKind.PermissionsApproval,
                    "请求额外权限",
                    BuildPermissionsApprovalDetail(parameters));
                break;
            case "item/tool/requestUserInput":
                AddUserInputRequest(requestId, id, message.Method, parameters);
                break;
            case "mcpServer/elicitation/request":
                AddMcpElicitationRequest(requestId, id, message.Method, parameters);
                break;
            default:
                _ = RejectUnsupportedServerRequestAsync(id, message.Method);
                break;
        }
    }

    private void HandleNotification(string method, JsonElement? rawParams)
    {
        JsonElement parameters = rawParams ?? JsonSerializer.SerializeToElement(new { });
        switch (method)
        {
            case "thread/started":
                SetNativeSessionId(ReadNestedString(parameters, "thread", "id"));
                break;
            case "turn/started":
            {
                string? turnId = ReadNestedString(parameters, "turn", "id");
                lock (_stateGate)
                {
                    _currentTurnId = turnId;
                }
                Transition(ChatEngineState.RunningTurn, "Codex 正在回复…");
                break;
            }
            case "item/agentMessage/delta":
            {
                string? delta = GetString(parameters, "delta");
                if (!string.IsNullOrEmpty(delta))
                {
                    Publish(new ChatAssistantDeltaEvent(delta, DateTimeOffset.UtcNow));
                }
                break;
            }
            case "item/started":
                if (TryGetObject(parameters, "item", out JsonElement startedItem))
                {
                    PublishToolStarted(startedItem);
                }
                break;
            case "item/completed":
                if (TryGetObject(parameters, "item", out JsonElement completedItem))
                {
                    PublishItemCompleted(completedItem);
                }
                break;
            case "item/commandExecution/outputDelta":
            case "item/fileChange/outputDelta":
                PublishToolProgress(parameters, GetString(parameters, "delta"));
                break;
            case "item/mcpToolCall/progress":
                PublishToolProgress(parameters, GetString(parameters, "message"));
                break;
            case "thread/tokenUsage/updated":
                PublishUsage(parameters);
                break;
            case "turn/completed":
                PublishTurnCompleted(parameters);
                break;
            case "error":
                PublishCodexError(parameters);
                break;
            case "serverRequest/resolved":
            {
                string? requestId = GetFlexibleId(parameters, "requestId");
                if (!string.IsNullOrWhiteSpace(requestId))
                {
                    _serverRequests.TryRemove(requestId, out _);
                }
                break;
            }
            case "warning":
            case "configWarning":
            case "deprecationNotice":
            case "guardianWarning":
            {
                string warning = GetString(parameters, "message") ?? parameters.GetRawText();
                Publish(new ChatErrorEvent("CODEX_WARNING", warning, DateTimeOffset.UtcNow));
                break;
            }
        }
    }

    private void AddApprovalRequest(
        string requestId,
        JsonElement id,
        string method,
        JsonElement parameters,
        PendingServerRequestKind kind,
        string title,
        string detail)
    {
        var pending = new PendingServerRequest(
            id.Clone(),
            method,
            kind,
            parameters.Clone(),
            Array.Empty<string>(),
            null);
        _serverRequests[requestId] = pending;
        Transition(ChatEngineState.WaitingForApproval, $"Codex 正在等待：{title}。");
        Publish(new ChatApprovalRequestedEvent(
            requestId,
            title,
            detail,
            GetAllowedApprovalDecisions(parameters),
            DateTimeOffset.UtcNow));
    }

    private void AddUserInputRequest(
        string requestId,
        JsonElement id,
        string method,
        JsonElement parameters)
    {
        var questionIds = new List<string>();
        var prompts = new List<string>();
        var options = new List<string>();
        if (parameters.TryGetProperty("questions", out JsonElement questions) &&
            questions.ValueKind == JsonValueKind.Array)
        {
            foreach (JsonElement question in questions.EnumerateArray())
            {
                string questionId = GetString(question, "id") ?? $"question-{questionIds.Count + 1}";
                questionIds.Add(questionId);
                string header = GetString(question, "header") ?? string.Empty;
                string prompt = GetString(question, "question") ?? "Codex 需要补充信息。";
                prompts.Add(string.IsNullOrWhiteSpace(header) ? prompt : $"{header}：{prompt}");

                if (questionIds.Count == 1 &&
                    question.TryGetProperty("options", out JsonElement rawOptions) &&
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
            }
        }

        _serverRequests[requestId] = new PendingServerRequest(
            id.Clone(),
            method,
            PendingServerRequestKind.UserInput,
            parameters.Clone(),
            questionIds,
            null);
        Transition(ChatEngineState.WaitingForApproval, "Codex 正在等待补充信息。");
        Publish(new ChatUserInputRequestedEvent(
            requestId,
            prompts.Count == 0 ? "Codex 需要补充信息。" : string.Join(Environment.NewLine, prompts),
            options,
            DateTimeOffset.UtcNow));
    }

    private void AddMcpElicitationRequest(
        string requestId,
        JsonElement id,
        string method,
        JsonElement parameters)
    {
        string prompt = GetString(parameters, "message") ?? "MCP 工具需要补充信息。";
        string? contentKey = null;
        if (parameters.TryGetProperty("requestedSchema", out JsonElement schema) &&
            schema.ValueKind == JsonValueKind.Object &&
            schema.TryGetProperty("properties", out JsonElement properties) &&
            properties.ValueKind == JsonValueKind.Object)
        {
            contentKey = properties.EnumerateObject().Select(property => property.Name).FirstOrDefault();
        }

        _serverRequests[requestId] = new PendingServerRequest(
            id.Clone(),
            method,
            PendingServerRequestKind.McpElicitation,
            parameters.Clone(),
            Array.Empty<string>(),
            contentKey);
        Transition(ChatEngineState.WaitingForApproval, "MCP 工具正在等待补充信息。");
        Publish(new ChatUserInputRequestedEvent(
            requestId,
            prompt,
            Array.Empty<string>(),
            DateTimeOffset.UtcNow));
    }

    private void PublishToolStarted(JsonElement item)
    {
        string? type = GetString(item, "type");
        string? itemId = GetString(item, "id");
        if (string.IsNullOrWhiteSpace(type) || string.IsNullOrWhiteSpace(itemId))
        {
            return;
        }

        (string toolName, string? summary) = DescribeToolItem(item, type);
        if (toolName.Length == 0)
        {
            return;
        }

        _toolNames[itemId] = toolName;
        Publish(new ChatToolStartedEvent(
            itemId,
            toolName,
            TrimSummary(summary),
            DateTimeOffset.UtcNow));
    }

    private void PublishItemCompleted(JsonElement item)
    {
        string? type = GetString(item, "type");
        if (type == "agentMessage")
        {
            string? text = GetString(item, "text");
            if (!string.IsNullOrEmpty(text))
            {
                Publish(new ChatAssistantMessageEvent(text, DateTimeOffset.UtcNow));
            }
            return;
        }

        string? itemId = GetString(item, "id");
        if (string.IsNullOrWhiteSpace(type) || string.IsNullOrWhiteSpace(itemId))
        {
            return;
        }

        (string describedName, string? summary) = DescribeToolItem(item, type);
        string toolName = _toolNames.TryRemove(itemId, out string? knownName)
            ? knownName
            : describedName;
        if (string.IsNullOrWhiteSpace(toolName))
        {
            return;
        }

        bool succeeded = IsSuccessfulToolItem(item, type);
        Publish(new ChatToolCompletedEvent(
            itemId,
            toolName,
            succeeded,
            TrimSummary(summary),
            DateTimeOffset.UtcNow));
    }

    private void PublishToolProgress(JsonElement parameters, string? message)
    {
        string? itemId = GetString(parameters, "itemId");
        if (string.IsNullOrWhiteSpace(itemId) || string.IsNullOrEmpty(message))
        {
            return;
        }

        Publish(new ChatToolProgressEvent(itemId, message, DateTimeOffset.UtcNow));
    }

    private void PublishUsage(JsonElement parameters)
    {
        if (!TryGetObject(parameters, "tokenUsage", out JsonElement usage) ||
            !TryGetObject(usage, "last", out JsonElement last))
        {
            return;
        }

        Publish(new ChatUsageEvent(
            GetInt64(last, "inputTokens"),
            GetInt64(last, "outputTokens"),
            GetInt64(last, "cachedInputTokens"),
            DateTimeOffset.UtcNow));
    }

    private void PublishTurnCompleted(JsonElement parameters)
    {
        if (!TryGetObject(parameters, "turn", out JsonElement turn))
        {
            return;
        }

        string status = GetString(turn, "status") ?? "failed";
        bool succeeded = status.Equals("completed", StringComparison.OrdinalIgnoreCase);
        string? errorMessage = null;
        if (TryGetObject(turn, "error", out JsonElement error))
        {
            errorMessage = GetString(error, "message");
        }
        errorMessage ??= succeeded ? null : status;

        lock (_stateGate)
        {
            _currentTurnId = null;
        }
        _serverRequests.Clear();
        Publish(new ChatTurnCompletedEvent(succeeded, errorMessage, DateTimeOffset.UtcNow));
        Transition(
            ChatEngineState.Ready,
            succeeded ? "Codex 回复完成。" : "Codex 本轮回复已结束。" );
    }

    private void PublishCodexError(JsonElement parameters)
    {
        string code = "CODEX_ERROR";
        string message = "Codex 返回了未知错误。";
        if (TryGetObject(parameters, "error", out JsonElement error))
        {
            message = GetString(error, "message") ?? message;
            if (error.TryGetProperty("codexErrorInfo", out JsonElement info))
            {
                code = info.ValueKind switch
                {
                    JsonValueKind.String => info.GetString() ?? code,
                    JsonValueKind.Object => info.EnumerateObject().Select(property => property.Name).FirstOrDefault() ?? code,
                    _ => code,
                };
            }
        }

        Publish(new ChatErrorEvent(code, message, DateTimeOffset.UtcNow));
    }

    private async Task DeclinePendingRequestsAsync(CancellationToken cancellationToken)
    {
        PendingServerRequest[] requests = _serverRequests.Values.ToArray();
        _serverRequests.Clear();
        foreach (PendingServerRequest request in requests)
        {
            object result = request.Kind switch
            {
                PendingServerRequestKind.CommandApproval or PendingServerRequestKind.FileApproval =>
                    new Dictionary<string, object?> { ["decision"] = "decline" },
                PendingServerRequestKind.PermissionsApproval =>
                    new Dictionary<string, object?>
                    {
                        ["permissions"] = new Dictionary<string, object?>(),
                        ["scope"] = "turn",
                    },
                PendingServerRequestKind.UserInput => BuildUserInputResponse(request.QuestionIds, string.Empty),
                PendingServerRequestKind.McpElicitation =>
                    new Dictionary<string, object?> { ["action"] = "decline" },
                _ => new Dictionary<string, object?>(),
            };
            await WriteProtocolLineAsync(
                CodexAppServerProtocol.SerializeResponse(request.Id, result),
                cancellationToken).ConfigureAwait(false);
        }
    }

    private async Task RejectUnsupportedServerRequestAsync(JsonElement id, string method)
    {
        try
        {
            await WriteProtocolLineAsync(
                CodexAppServerProtocol.SerializeErrorResponse(
                    id,
                    -32601,
                    $"局域网 AI 工作台暂不支持 Codex 服务请求：{method}"),
                CancellationToken.None).ConfigureAwait(false);
            Publish(new ChatErrorEvent(
                "CODEX_UNSUPPORTED_SERVER_REQUEST",
                $"Codex 请求了尚未接入的交互：{method}",
                DateTimeOffset.UtcNow));
        }
        catch (Exception exception)
        {
            Publish(new ChatErrorEvent(
                "CODEX_SERVER_RESPONSE_FAILED",
                exception.Message,
                DateTimeOffset.UtcNow));
        }
    }

    private void Process_OnErrorLineReceived(object? sender, string line)
    {
        if (!string.IsNullOrWhiteSpace(line))
        {
            lock (_stateGate)
            {
                _lastErrorLine = line;
            }
        }
    }

    private void Process_OnExited(object? sender, int exitCode)
    {
        if (State is ChatEngineState.Stopping or ChatEngineState.Stopped)
        {
            return;
        }

        string? lastError;
        lock (_stateGate)
        {
            lastError = _lastErrorLine;
        }
        var exception = new InvalidOperationException(
            string.IsNullOrWhiteSpace(lastError)
                ? $"Codex app-server 意外退出，代码 {exitCode}。"
                : $"Codex app-server 意外退出，代码 {exitCode}：{lastError}");
        FailAllClientRequests(exception);
        Transition(ChatEngineState.Faulted, "Codex 图形会话服务意外退出。");
        Publish(new ChatErrorEvent(
            "CODEX_PROCESS_EXITED",
            exception.Message,
            DateTimeOffset.UtcNow));
    }

    private void ResetForStart(ChatEngineContext context)
    {
        _context = context;
        _clientRequests.Clear();
        _serverRequests.Clear();
        _toolNames.Clear();
        lock (_stateGate)
        {
            _nativeSessionId = null;
            _currentTurnId = null;
            _lastErrorLine = null;
        }
    }

    private void RestoreRunningStateAfterServerResponse(string message)
    {
        ChatEngineState next;
        lock (_stateGate)
        {
            next = _serverRequests.Count > 0
                ? ChatEngineState.WaitingForApproval
                : _currentTurnId is null
                    ? ChatEngineState.Ready
                    : ChatEngineState.RunningTurn;
        }
        Transition(next, message);
    }

    private void SetNativeSessionId(string? sessionId)
    {
        if (string.IsNullOrWhiteSpace(sessionId))
        {
            return;
        }

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

    private static string? ReadThreadId(JsonElement result) =>
        ReadNestedString(result, "thread", "id");

    private static string ToSandboxMode(ChatPermissionMode mode) => mode switch
    {
        ChatPermissionMode.ReadOnly => "read-only",
        ChatPermissionMode.WorkspaceWrite => "workspace-write",
        ChatPermissionMode.FullAccess => "danger-full-access",
        _ => throw new ArgumentOutOfRangeException(nameof(mode)),
    };

    private static string ToCodexApprovalDecision(ChatApprovalDecision decision) => decision switch
    {
        ChatApprovalDecision.Deny => "decline",
        ChatApprovalDecision.AllowOnce => "accept",
        ChatApprovalDecision.AllowForSession => "acceptForSession",
        _ => throw new ArgumentOutOfRangeException(nameof(decision)),
    };

    private static object BuildPermissionsResponse(
        JsonElement parameters,
        ChatApprovalDecision decision)
    {
        object permissions = new Dictionary<string, object?>();
        if (decision != ChatApprovalDecision.Deny &&
            parameters.TryGetProperty("permissions", out JsonElement requestedPermissions))
        {
            permissions = requestedPermissions.Clone();
        }

        return new Dictionary<string, object?>
        {
            ["permissions"] = permissions,
            ["scope"] = decision == ChatApprovalDecision.AllowForSession ? "session" : "turn",
        };
    }

    private static object BuildUserInputResponse(
        IReadOnlyList<string> questionIds,
        string response)
    {
        var answers = new Dictionary<string, object?>();
        for (int index = 0; index < questionIds.Count; index++)
        {
            answers[questionIds[index]] = new Dictionary<string, object?>
            {
                ["answers"] = index == 0 && !string.IsNullOrEmpty(response)
                    ? new[] { response }
                    : Array.Empty<string>(),
            };
        }

        return new Dictionary<string, object?> { ["answers"] = answers };
    }

    private static object BuildMcpElicitationResponse(
        PendingServerRequest request,
        string response)
    {
        var result = new Dictionary<string, object?> { ["action"] = "accept" };
        if (!string.IsNullOrWhiteSpace(request.ContentKey))
        {
            result["content"] = new Dictionary<string, object?>
            {
                [request.ContentKey] = response,
            };
        }
        else
        {
            result["content"] = new Dictionary<string, object?>();
        }

        return result;
    }

    private static IReadOnlyList<ChatApprovalDecision> GetAllowedApprovalDecisions(
        JsonElement parameters)
    {
        if (!parameters.TryGetProperty("availableDecisions", out JsonElement available) ||
            available.ValueKind != JsonValueKind.Array)
        {
            return
            [
                ChatApprovalDecision.Deny,
                ChatApprovalDecision.AllowOnce,
                ChatApprovalDecision.AllowForSession,
            ];
        }

        var decisions = new List<ChatApprovalDecision>();
        var raw = available.EnumerateArray()
            .Where(value => value.ValueKind == JsonValueKind.String)
            .Select(value => value.GetString())
            .Where(value => value is not null)
            .ToHashSet(StringComparer.Ordinal);
        if (raw.Contains("decline") || raw.Contains("cancel"))
        {
            decisions.Add(ChatApprovalDecision.Deny);
        }
        if (raw.Contains("accept"))
        {
            decisions.Add(ChatApprovalDecision.AllowOnce);
        }
        if (raw.Contains("acceptForSession"))
        {
            decisions.Add(ChatApprovalDecision.AllowForSession);
        }

        return decisions.Count == 0
            ? [ChatApprovalDecision.Deny]
            : decisions;
    }

    private static string BuildCommandApprovalDetail(JsonElement parameters)
    {
        var parts = new List<string>();
        AddPart(parts, GetString(parameters, "reason"));
        AddPart(parts, GetString(parameters, "command"));
        string? cwd = GetString(parameters, "cwd");
        AddPart(parts, string.IsNullOrWhiteSpace(cwd) ? null : $"目录：{cwd}");
        return parts.Count == 0 ? "Codex 请求执行一条命令。" : string.Join(Environment.NewLine, parts);
    }

    private static string BuildFileApprovalDetail(JsonElement parameters)
    {
        var parts = new List<string>();
        AddPart(parts, GetString(parameters, "reason"));
        string? root = GetString(parameters, "grantRoot");
        AddPart(parts, string.IsNullOrWhiteSpace(root) ? null : $"写入范围：{root}");
        return parts.Count == 0 ? "Codex 请求修改项目文件。" : string.Join(Environment.NewLine, parts);
    }

    private static string BuildPermissionsApprovalDetail(JsonElement parameters)
    {
        var parts = new List<string>();
        AddPart(parts, GetString(parameters, "reason"));
        string? cwd = GetString(parameters, "cwd");
        AddPart(parts, string.IsNullOrWhiteSpace(cwd) ? null : $"目录：{cwd}");
        if (parameters.TryGetProperty("permissions", out JsonElement permissions))
        {
            AddPart(parts, $"权限：{permissions.GetRawText()}");
        }
        return parts.Count == 0 ? "Codex 请求额外的文件或网络权限。" : string.Join(Environment.NewLine, parts);
    }

    private static (string Name, string? Summary) DescribeToolItem(JsonElement item, string type) =>
        type switch
        {
            "commandExecution" => ("终端命令", GetString(item, "command") ?? GetString(item, "aggregatedOutput")),
            "fileChange" => ("文件修改", DescribeFileChanges(item)),
            "mcpToolCall" => ($"MCP · {GetString(item, "server") ?? "server"}/{GetString(item, "tool") ?? "tool"}", GetRawProperty(item, "arguments")),
            "dynamicToolCall" => ($"工具 · {GetString(item, "tool") ?? "dynamic"}", GetRawProperty(item, "arguments")),
            "collabAgentToolCall" => ($"协作 · {GetString(item, "tool") ?? "agent"}", GetString(item, "prompt")),
            "webSearch" => ("网页搜索", GetString(item, "query")),
            "imageView" => ("查看图片", GetString(item, "path")),
            "imageGeneration" => ("生成图片", GetString(item, "revisedPrompt")),
            _ => (string.Empty, null),
        };

    private static bool IsSuccessfulToolItem(JsonElement item, string type)
    {
        string? status = GetString(item, "status");
        if (type == "commandExecution")
        {
            long? exitCode = GetInt64(item, "exitCode");
            return status == "completed" && (exitCode is null or 0);
        }
        if (type == "dynamicToolCall" && item.TryGetProperty("success", out JsonElement success))
        {
            return success.ValueKind == JsonValueKind.True;
        }
        if (type == "mcpToolCall" && item.TryGetProperty("error", out JsonElement error) &&
            error.ValueKind is not JsonValueKind.Null and not JsonValueKind.Undefined)
        {
            return false;
        }

        return status is null or "completed";
    }

    private static string? DescribeFileChanges(JsonElement item)
    {
        if (!item.TryGetProperty("changes", out JsonElement changes) ||
            changes.ValueKind != JsonValueKind.Array)
        {
            return null;
        }

        string[] paths = changes.EnumerateArray()
            .Select(change => GetString(change, "path"))
            .Where(path => !string.IsNullOrWhiteSpace(path))
            .Cast<string>()
            .Take(8)
            .ToArray();
        return paths.Length == 0 ? changes.GetRawText() : string.Join(Environment.NewLine, paths);
    }

    private static string? GetRawProperty(JsonElement element, string propertyName) =>
        element.TryGetProperty(propertyName, out JsonElement value) ? value.GetRawText() : null;

    private static string? TrimSummary(string? value)
    {
        const int maxLength = 4000;
        return value is null || value.Length <= maxLength
            ? value
            : value[..maxLength] + "…";
    }

    private static void AddPart(ICollection<string> parts, string? value)
    {
        if (!string.IsNullOrWhiteSpace(value))
        {
            parts.Add(value);
        }
    }

    private static void AddIfNotBlank(
        IDictionary<string, object?> target,
        string key,
        string? value)
    {
        if (!string.IsNullOrWhiteSpace(value))
        {
            target[key] = value;
        }
    }

    private static bool TryGetObject(
        JsonElement element,
        string propertyName,
        out JsonElement value)
    {
        if (element.ValueKind == JsonValueKind.Object &&
            element.TryGetProperty(propertyName, out value) &&
            value.ValueKind == JsonValueKind.Object)
        {
            return true;
        }

        value = default;
        return false;
    }

    private static string? ReadNestedString(
        JsonElement element,
        string objectName,
        string propertyName) =>
        TryGetObject(element, objectName, out JsonElement nested)
            ? GetString(nested, propertyName)
            : null;

    private static string? GetString(JsonElement element, string propertyName) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static string? GetFlexibleId(JsonElement element, string propertyName)
    {
        if (element.ValueKind != JsonValueKind.Object ||
            !element.TryGetProperty(propertyName, out JsonElement value))
        {
            return null;
        }

        return CodexAppServerProtocol.NormalizeId(value);
    }

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
            throw new InvalidOperationException($"Codex 聊天引擎当前状态为 {State}，需要 {expected}。");
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

    private void FailAllClientRequests(Exception exception)
    {
        foreach ((string id, PendingClientRequest pending) in _clientRequests.ToArray())
        {
            if (_clientRequests.TryRemove(id, out _))
            {
                pending.Completion.TrySetException(exception);
            }
        }
    }

    private void Subscribe(IStructuredCliProcess process)
    {
        process.OutputLineReceived += Process_OnOutputLineReceived;
        process.ErrorLineReceived += Process_OnErrorLineReceived;
        process.Exited += Process_OnExited;
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
        _lifecycleLock.Dispose();
    }

    private sealed class PendingClientRequest
    {
        public PendingClientRequest(string method)
        {
            Method = method;
        }

        public string Method { get; }

        public TaskCompletionSource<JsonElement> Completion { get; } =
            new(TaskCreationOptions.RunContinuationsAsynchronously);
    }

    private sealed record PendingServerRequest(
        JsonElement Id,
        string Method,
        PendingServerRequestKind Kind,
        JsonElement Params,
        IReadOnlyList<string> QuestionIds,
        string? ContentKey);

    private enum PendingServerRequestKind
    {
        CommandApproval,
        FileApproval,
        PermissionsApproval,
        UserInput,
        McpElicitation,
    }
}
