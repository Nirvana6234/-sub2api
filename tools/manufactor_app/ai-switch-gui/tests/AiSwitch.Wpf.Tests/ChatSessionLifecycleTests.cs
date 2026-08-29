using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ChatSessionLifecycleTests
{
    [Fact]
    public async Task DisposeAsync_CancelsInFlightSend_ThenStopsAndDisposesSequentially()
    {
        var engine = new BlockingChatEngine();
        var profiles = new DisposableProfileReader();
        var controller = CreateController(engine, profiles, ownsProfileReader: true);
        Task send = controller.SendAsync(CreateIntent(), "hello");
        await engine.SendStarted.Task.WaitAsync(TimeSpan.FromSeconds(1));

        Task shutdown = controller.DisposeAsync().AsTask();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => send);
        await shutdown.WaitAsync(TimeSpan.FromSeconds(1));
        Assert.Equal(1, engine.StopCalls);
        Assert.Equal(1, engine.DisposeCalls);
        Assert.False(engine.DisposeOverlappedStop);
        Assert.True(profiles.IsDisposed);
    }

    [Fact]
    public async Task ResetAsync_CancelsInFlightSendAndClearsActiveProject()
    {
        var engine = new BlockingChatEngine();
        await using var controller = CreateController(
            engine,
            new DisposableProfileReader(),
            ownsProfileReader: false);
        Task send = controller.SendAsync(CreateIntent(), "hello");
        await engine.SendStarted.Task.WaitAsync(TimeSpan.FromSeconds(1));

        Task reset = controller.ResetAsync();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => send);
        await reset.WaitAsync(TimeSpan.FromSeconds(1));
        Assert.Null(controller.ActiveProjectFingerprint);
        Assert.Equal(ChatEngineState.Created, controller.State);
        Assert.Equal(1, engine.StopCalls);
        Assert.Equal(1, engine.DisposeCalls);
        Assert.False(engine.DisposeOverlappedStop);
    }

    [Fact]
    public async Task ResetAsync_CancelsEngineStartupThatStillOwnsLifecycleGate()
    {
        var engine = new BlockingChatEngine(blockStart: true);
        await using var controller = CreateController(
            engine,
            new DisposableProfileReader(),
            ownsProfileReader: false);
        Task send = controller.SendAsync(CreateIntent(), "hello");
        await engine.StartStarted.Task.WaitAsync(TimeSpan.FromSeconds(1));

        Task reset = controller.ResetAsync();

        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => send);
        await reset.WaitAsync(TimeSpan.FromSeconds(1));
        Assert.Null(controller.ActiveProjectFingerprint);
        Assert.Equal(0, engine.StopCalls);
        Assert.Equal(1, engine.DisposeCalls);
    }

    [Fact]
    public async Task DisposeAsync_IsIdempotentAndRejectsNewOperations()
    {
        var engine = new BlockingChatEngine(blockSend: false);
        var controller = CreateController(
            engine,
            new DisposableProfileReader(),
            ownsProfileReader: false);
        await controller.SendAsync(CreateIntent(), "hello");

        await controller.DisposeAsync();
        await controller.DisposeAsync();

        Assert.Equal(1, engine.StopCalls);
        Assert.Equal(1, engine.DisposeCalls);
        await Assert.ThrowsAsync<ObjectDisposedException>(() =>
            controller.SendAsync(CreateIntent(), "after shutdown"));
        await Assert.ThrowsAsync<ObjectDisposedException>(() => controller.ResetAsync());
        await Assert.ThrowsAsync<ObjectDisposedException>(() => controller.CancelTurnAsync());
    }

    private static ChatSessionController CreateController(
        BlockingChatEngine engine,
        IConnectionProfileReader profiles,
        bool ownsProfileReader) =>
        new(
            _ => engine,
            new InstalledCliDetector(),
            profiles,
            ownsProfileReader);

    private static ChatLaunchIntent CreateIntent() => new(
        new ProjectRecord
        {
            Id = "project-1",
            DisplayName = "Project",
            RootPath = Environment.CurrentDirectory,
            PathFingerprint = "project-fingerprint",
            DefaultCli = CliKind.Codex,
            CreatedAt = DateTimeOffset.UtcNow,
        },
        CliKind.Codex,
        ConnectionProfileId: "test-source",
        ConnectionLabel: "测试来源",
        Model: null,
        Conversation: null,
        ChatPermissionMode.WorkspaceWrite);

    private sealed class InstalledCliDetector : ICliDetector
    {
        public Task<IReadOnlyList<CliInstallation>> DetectAsync(
            CliKind? cli = null,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            IReadOnlyList<CliInstallation> result =
            [
                new CliInstallation
                {
                    Kind = cli ?? CliKind.Codex,
                    IsInstalled = true,
                    ExecutablePath = "official-cli.exe",
                    Version = "test",
                    DetectedAt = DateTimeOffset.UtcNow,
                },
            ];
            return Task.FromResult(result);
        }
    }

    private sealed class DisposableProfileReader : IConnectionProfileReader, IDisposable
    {
        public bool IsDisposed { get; private set; }

        public Task<IReadOnlyList<ConnectionProfile>> GetAllAsync(
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult<IReadOnlyList<ConnectionProfile>>(
            [
                new ConnectionProfile
                {
                    Id = "test-source",
                    Name = "测试来源",
                    BaseUrl = "https://test.example/v1",
                },
            ]);
        }

        public Task<ConnectionProfile?> GetByIdAsync(
            string id,
            CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            return Task.FromResult<ConnectionProfile?>(
                string.Equals(id, "test-source", StringComparison.OrdinalIgnoreCase)
                    ? new ConnectionProfile
                    {
                        Id = "test-source",
                        Name = "测试来源",
                        BaseUrl = "https://test.example/v1",
                    }
                    : null);
        }

        public void Dispose() => IsDisposed = true;
    }

    private sealed class BlockingChatEngine(
        bool blockSend = true,
        bool blockStart = false) : IChatEngine
    {
        private readonly bool _blockSend = blockSend;
        private readonly bool _blockStart = blockStart;
        private int _stopInProgress;

        public CliKind Kind => CliKind.Codex;

        public ChatEngineState State { get; private set; } = ChatEngineState.Created;

        public string? NativeSessionId { get; private set; }

        public int StopCalls { get; private set; }

        public int DisposeCalls { get; private set; }

        public bool DisposeOverlappedStop { get; private set; }

        public TaskCompletionSource SendStarted { get; } = new(
            TaskCreationOptions.RunContinuationsAsynchronously);

        public TaskCompletionSource StartStarted { get; } = new(
            TaskCreationOptions.RunContinuationsAsynchronously);

        public event EventHandler<ChatEvent>? EventReceived;

        public async Task StartAsync(
            ChatEngineContext context,
            CancellationToken cancellationToken = default)
        {
            State = ChatEngineState.Starting;
            StartStarted.TrySetResult();
            if (_blockStart)
            {
                await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            }

            NativeSessionId = "native-session";
            State = ChatEngineState.Ready;
            EventReceived?.Invoke(
                this,
                new ChatSessionStartedEvent(NativeSessionId, DateTimeOffset.UtcNow));
        }

        public async Task SendMessageAsync(
            string message,
            CancellationToken cancellationToken = default)
        {
            State = ChatEngineState.RunningTurn;
            SendStarted.TrySetResult();
            if (!_blockSend)
            {
                State = ChatEngineState.Ready;
                return;
            }

            try
            {
                await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            }
            finally
            {
                State = ChatEngineState.Ready;
            }
        }

        public Task RespondToApprovalAsync(
            string requestId,
            ChatApprovalDecision decision,
            CancellationToken cancellationToken = default) => Task.CompletedTask;

        public Task RespondToUserInputAsync(
            string requestId,
            string response,
            CancellationToken cancellationToken = default) => Task.CompletedTask;

        public Task CancelTurnAsync(CancellationToken cancellationToken = default) =>
            Task.CompletedTask;

        public async Task StopAsync(CancellationToken cancellationToken = default)
        {
            StopCalls++;
            Interlocked.Exchange(ref _stopInProgress, 1);
            try
            {
                await Task.Delay(30, cancellationToken);
                State = ChatEngineState.Stopped;
            }
            finally
            {
                Interlocked.Exchange(ref _stopInProgress, 0);
            }
        }

        public ValueTask DisposeAsync()
        {
            DisposeCalls++;
            DisposeOverlappedStop |= Volatile.Read(ref _stopInProgress) != 0;
            State = ChatEngineState.Stopped;
            return ValueTask.CompletedTask;
        }
    }
}
