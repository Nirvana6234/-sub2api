using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Terminal;
using LanAi.Workspace.Wpf.Controls;
using LanAi.Workspace.Wpf.ViewModels;
using System.Runtime.ExceptionServices;

namespace LanAi.Workspace.Wpf.Services;

internal sealed class ChatSessionController : IChatSessionController
{
    private readonly Func<CliKind, IChatEngine> _engineFactory;
    private readonly ICliDetector _cliDetector;
    private readonly IConnectionProfileReader _profileReader;
    private readonly IDisposable? _ownedProfileReader;
    private readonly SemaphoreSlim _lifecycleGate = new(1, 1);
    private readonly CancellationTokenSource _shutdownCancellation = new();
    private readonly object _engineStateGate = new();
    private CancellationTokenSource _sessionOperationCancellation = new();
    private IChatEngine? _engine;
    private CancellationTokenSource? _engineCancellation;
    private ChatContextIdentity? _activeContext;
    private int _disposeState;

    public ChatSessionController(
        Func<CliKind, IChatEngine> engineFactory,
        ICliDetector? cliDetector = null,
        IConnectionProfileReader? profileReader = null,
        bool ownsProfileReader = false)
    {
        _engineFactory = engineFactory ?? throw new ArgumentNullException(nameof(engineFactory));
        _cliDetector = cliDetector ?? new CliDetector();
        if (profileReader is null)
        {
            var reader = new LegacyProfileReader(AppDataPaths.CreateDefault());
            _profileReader = reader;
            _ownedProfileReader = reader;
        }
        else
        {
            _profileReader = profileReader;
            _ownedProfileReader = ownsProfileReader
                ? profileReader as IDisposable
                    ?? throw new ArgumentException(
                        "被控制器托管的连接读取器必须实现 IDisposable。",
                        nameof(profileReader))
                : null;
        }
    }

    public ChatEngineState State
    {
        get
        {
            lock (_engineStateGate)
            {
                return _engine?.State ?? ChatEngineState.Created;
            }
        }
    }

    public string? NativeSessionId
    {
        get
        {
            lock (_engineStateGate)
            {
                return _engine?.NativeSessionId;
            }
        }
    }

    public string? ActiveProjectFingerprint
    {
        get
        {
            lock (_engineStateGate)
            {
                return _activeContext?.ProjectFingerprint;
            }
        }
    }

    public event EventHandler<ChatEvent>? EventReceived;

    public async Task ConnectAsync(
        ChatLaunchIntent intent,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(intent);
        ThrowIfDisposed();

        using var controllerOperationCancellation = CreateControllerOperation(cancellationToken);
        CancellationToken operationToken = controllerOperationCancellation.Token;
        await _lifecycleGate.WaitAsync(operationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            await EnsureConnectedEngineAsync(intent, operationToken).ConfigureAwait(false);
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    public async Task SendAsync(
        ChatLaunchIntent intent,
        string message,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(intent);
        ArgumentException.ThrowIfNullOrWhiteSpace(message);
        ThrowIfDisposed();

        using var controllerOperationCancellation = CreateControllerOperation(cancellationToken);
        CancellationToken operationToken = controllerOperationCancellation.Token;
        await _lifecycleGate.WaitAsync(operationToken).ConfigureAwait(false);
        try
        {
            ThrowIfDisposed();
            await EnsureConnectedEngineAsync(intent, operationToken).ConfigureAwait(false);

            (IChatEngine engine, CancellationTokenSource engineOperationCancellation) =
                CreateEngineOperation(operationToken);
            using (engineOperationCancellation)
            {
                await engine.SendMessageAsync(message, engineOperationCancellation.Token)
                    .ConfigureAwait(false);
            }
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    private async Task EnsureConnectedEngineAsync(
        ChatLaunchIntent intent,
        CancellationToken cancellationToken)
    {
        ChatContextIdentity identity = ChatContextIdentity.From(intent);
        IChatEngine? engine;
        ChatContextIdentity? activeContext;
        bool engineCancellationRequested;
        lock (_engineStateGate)
        {
            engine = _engine;
            activeContext = _activeContext;
            engineCancellationRequested = _engineCancellation?.IsCancellationRequested == true;
        }

        if (engine is null || activeContext != identity ||
            engineCancellationRequested ||
            engine.State is ChatEngineState.Stopped or ChatEngineState.Faulted)
        {
            await ReplaceEngineAsync(intent, identity, cancellationToken).ConfigureAwait(false);
        }
    }

    public Task RespondToApprovalAsync(
        string requestId,
        ChatApprovalDecision decision,
        CancellationToken cancellationToken = default) =>
        InvokeEngineAsync(
            (engine, token) => engine.RespondToApprovalAsync(requestId, decision, token),
            cancellationToken);

    public Task RespondToUserInputAsync(
        string requestId,
        string response,
        CancellationToken cancellationToken = default) =>
        InvokeEngineAsync(
            (engine, token) => engine.RespondToUserInputAsync(requestId, response, token),
            cancellationToken);

    public async Task CancelTurnAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        (IChatEngine? engine, CancellationTokenSource? operationCancellation) =
            TryCreateEngineOperation(cancellationToken);
        if (engine is null || operationCancellation is null)
        {
            return;
        }

        using (operationCancellation)
        {
            await engine.CancelTurnAsync(operationCancellation.Token).ConfigureAwait(false);
        }
    }

    public async Task ResetAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        using var linkedCancellation = CancellationTokenSource.CreateLinkedTokenSource(
            cancellationToken,
            _shutdownCancellation.Token);
        RequestCurrentOperationsCancellation();
        bool gateEntered = false;
        try
        {
            await _lifecycleGate.WaitAsync(linkedCancellation.Token).ConfigureAwait(false);
            gateEntered = true;
            ThrowIfDisposed();
            await DisposeCurrentEngineAsync(linkedCancellation.Token).ConfigureAwait(false);
        }
        finally
        {
            RenewSessionOperationCancellation();
            if (gateEntered)
            {
                _lifecycleGate.Release();
            }
        }
    }

    private void RequestCurrentOperationsCancellation()
    {
        CancellationTokenSource? engineCancellation;
        CancellationTokenSource sessionOperationCancellation;
        lock (_engineStateGate)
        {
            engineCancellation = _engineCancellation;
            sessionOperationCancellation = _sessionOperationCancellation;
        }

        sessionOperationCancellation.Cancel();
        engineCancellation?.Cancel();
    }

    private void RenewSessionOperationCancellation()
    {
        CancellationTokenSource previous;
        lock (_engineStateGate)
        {
            if (Volatile.Read(ref _disposeState) != 0)
            {
                return;
            }

            previous = _sessionOperationCancellation;
            _sessionOperationCancellation = new CancellationTokenSource();
        }

        previous.Dispose();
    }

    private CancellationTokenSource CreateControllerOperation(
        CancellationToken cancellationToken)
    {
        lock (_engineStateGate)
        {
            return CancellationTokenSource.CreateLinkedTokenSource(
                cancellationToken,
                _shutdownCancellation.Token,
                _sessionOperationCancellation.Token);
        }
    }

    private async Task ReplaceEngineAsync(
        ChatLaunchIntent intent,
        ChatContextIdentity identity,
        CancellationToken cancellationToken)
    {
        await DisposeCurrentEngineAsync(cancellationToken).ConfigureAwait(false);

        if (TerminalHost.Shared.IsRunning)
        {
            try
            {
                await TerminalHost.Shared.StopAsync(cancellationToken)
                    .WaitAsync(TimeSpan.FromSeconds(3), cancellationToken)
                    .ConfigureAwait(false);
            }
            catch (TimeoutException)
            {
                throw new InvalidOperationException("高级终端尚未停止，请稍后重试图形对话。");
            }
        }

        CliInstallation installation = (await _cliDetector
                .DetectAsync(intent.Cli, cancellationToken)
                .ConfigureAwait(false))
            .FirstOrDefault()
            ?? throw new InvalidOperationException($"未检测到 {DisplayCli(intent.Cli)}。请先安装官方 CLI。");
        if (!installation.IsInstalled || string.IsNullOrWhiteSpace(installation.ExecutablePath))
        {
            throw new InvalidOperationException($"未检测到 {DisplayCli(intent.Cli)}。请先安装官方 CLI。");
        }

        IReadOnlyList<ConnectionProfile> profiles = await _profileReader
            .GetAllAsync(cancellationToken)
            .ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(intent.ConnectionProfileId))
        {
            throw new InvalidOperationException("连接中心未提供有效来源，无法启动图形对话。");
        }

        ConnectionProfile? connection = ResolveConnection(profiles, intent);
        if (connection is null)
        {
            throw new InvalidOperationException(
                $"连接“{intent.ConnectionLabel}”不存在或未配置 {DisplayCli(intent.Cli)}。请前往连接中心选择或完善该来源。");
        }

        var request = new CliLaunchRequest
        {
            ProjectId = intent.Project.Id,
            Cli = intent.Cli,
            WorkingDirectory = intent.Project.RootPath,
            Mode = intent.Conversation is null ? CliLaunchMode.New : CliLaunchMode.Resume,
            ConnectionProfileId = connection?.Id,
            Model = intent.Model,
            ConversationId = intent.Conversation?.Id,
            NativeSessionId = intent.Conversation?.NativeSessionId,
            ResumePolicy = intent.Conversation?.ResumePolicy ?? intent.Project.ResumePolicy,
        };
        var context = new ChatEngineContext
        {
            LaunchRequest = request,
            Installation = installation,
            Connection = connection,
            PermissionMode = intent.PermissionMode,
        };

        IChatEngine engine = _engineFactory(intent.Cli);
        engine.EventReceived += Engine_OnEventReceived;
        try
        {
            await engine.StartAsync(context, cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            engine.EventReceived -= Engine_OnEventReceived;
            await engine.DisposeAsync().ConfigureAwait(false);
            throw;
        }

        lock (_engineStateGate)
        {
            _engine = engine;
            _engineCancellation = new CancellationTokenSource();
            _activeContext = identity;
        }
    }

    private static ConnectionProfile? ResolveConnection(
        IReadOnlyList<ConnectionProfile> profiles,
        ChatLaunchIntent intent)
    {
        ConnectionProfile? exact = profiles.FirstOrDefault(profile =>
            string.Equals(profile.Id, intent.ConnectionProfileId, StringComparison.OrdinalIgnoreCase));
        return exact is not null && SupportsClient(exact, intent.Cli) ? exact : null;
    }

    private static bool SupportsClient(ConnectionProfile profile, CliKind cli) =>
        profile.EnabledClients.Count == 0 ||
        profile.EnabledClients.Contains(cli) ||
        profile.ClientBaseUrls.ContainsKey(cli);

    private async Task InvokeEngineAsync(
        Func<IChatEngine, CancellationToken, Task> operation,
        CancellationToken cancellationToken)
    {
        ThrowIfDisposed();
        (IChatEngine engine, CancellationTokenSource operationCancellation) =
            CreateEngineOperation(cancellationToken);
        using (operationCancellation)
        {
            await operation(engine, operationCancellation.Token).ConfigureAwait(false);
        }
    }

    private (IChatEngine Engine, CancellationTokenSource OperationCancellation)
        CreateEngineOperation(CancellationToken cancellationToken)
    {
        (IChatEngine? engine, CancellationTokenSource? operationCancellation) =
            TryCreateEngineOperation(cancellationToken);
        return engine is not null && operationCancellation is not null
            ? (engine, operationCancellation)
            : throw new InvalidOperationException("当前没有正在运行的图形会话。");
    }

    private (IChatEngine? Engine, CancellationTokenSource? OperationCancellation)
        TryCreateEngineOperation(CancellationToken cancellationToken)
    {
        lock (_engineStateGate)
        {
            if (_engine is null || _engineCancellation is null)
            {
                return (null, null);
            }

            return (
                _engine,
                CancellationTokenSource.CreateLinkedTokenSource(
                    cancellationToken,
                    _shutdownCancellation.Token,
                    _sessionOperationCancellation.Token,
                    _engineCancellation.Token));
        }
    }

    private void Engine_OnEventReceived(object? sender, ChatEvent chatEvent) =>
        EventReceived?.Invoke(this, chatEvent);

    private async Task DisposeCurrentEngineAsync(CancellationToken cancellationToken)
    {
        IChatEngine? engine;
        CancellationTokenSource? engineCancellation;
        lock (_engineStateGate)
        {
            engine = _engine;
            engineCancellation = _engineCancellation;
            _engine = null;
            _engineCancellation = null;
            _activeContext = null;
        }

        engineCancellation?.Cancel();
        if (engine is null)
        {
            engineCancellation?.Dispose();
            return;
        }

        engine.EventReceived -= Engine_OnEventReceived;
        Exception? stopFailure = null;
        try
        {
            await engine.StopAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception exception)
        {
            stopFailure = exception;
        }

        try
        {
            await engine.DisposeAsync().ConfigureAwait(false);
        }
        catch (Exception disposeFailure) when (stopFailure is not null)
        {
            throw new AggregateException(
                "图形会话停止和释放均失败。",
                stopFailure,
                disposeFailure);
        }
        finally
        {
            engineCancellation?.Dispose();
        }

        if (stopFailure is not null)
        {
            ExceptionDispatchInfo.Capture(stopFailure).Throw();
        }
    }

    private static string DisplayCli(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude Code",
        CliKind.GeminiCli => "Gemini CLI",
        _ => cli.ToString(),
    };

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposeState, 1) != 0)
        {
            return;
        }

        _shutdownCancellation.Cancel();
        RequestCurrentOperationsCancellation();
        await _lifecycleGate.WaitAsync().ConfigureAwait(false);
        try
        {
            await DisposeCurrentEngineAsync(CancellationToken.None).ConfigureAwait(false);
        }
        finally
        {
            try
            {
                _ownedProfileReader?.Dispose();
            }
            finally
            {
                _lifecycleGate.Release();
            }
        }
    }

    private void ThrowIfDisposed() =>
        ObjectDisposedException.ThrowIf(Volatile.Read(ref _disposeState) != 0, this);

    private sealed record ChatContextIdentity(
        string ProjectFingerprint,
        CliKind Cli,
        string? ConnectionProfileId,
        string? Model,
        string? NativeSessionId,
        ChatPermissionMode PermissionMode)
    {
        public static ChatContextIdentity From(ChatLaunchIntent intent) => new(
            intent.Project.PathFingerprint,
            intent.Cli,
            intent.ConnectionProfileId,
            intent.Model,
            intent.Conversation?.NativeSessionId,
            intent.PermissionMode);
    }
}
