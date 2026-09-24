using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.DesktopSync;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>The 「同步会话」 page: what the user can select, and what they are warned about.</summary>
public sealed class DesktopSyncViewModelTests : IAsyncDisposable
{
    private readonly Tools _tools = new();
    private readonly Dictionary<string, CodexThreadRecord> _records = [];
    private readonly SyncAuditLog _audit = new(persist: false);
    private readonly DesktopSyncAgent _agent;
    private readonly DesktopSyncLink _link;
    private readonly DesktopSyncViewModel _page;
    private readonly List<string> _asked = [];
    private bool _answer = true;

    public DesktopSyncViewModelTests()
    {
        CodexThreadRecord? Find(string id) => _records.GetValueOrDefault(id);
        _agent = new DesktopSyncAgent(_tools, new SessionContentSync(Find), Find, new Store(), _audit);
        _link = new DesktopSyncLink(new Uri("http://127.0.0.1:1/"), "pc-test0001", "pc", _ => Task.FromResult("t"), _agent,
            _ => Task.FromResult("{}"u8.ToArray()));
        _page = new DesktopSyncViewModel(_agent, _link, _tools, Find, _audit, action => action())
        {
            Confirm = (message, _) =>
            {
                _asked.Add(message);
                return Task.FromResult(_answer);
            },
        };
    }

    public async ValueTask DisposeAsync()
    {
        await _link.DisposeAsync();
        _agent.Dispose();
    }

    private void GivenConversations(params (string Id, string Mode)[] conversations)
    {
        foreach ((string id, string mode) in conversations)
        {
            _tools.Threads.Add(new DesktopThread(id, "idle", @"C:\w", $"会话 {id}", DateTimeOffset.UtcNow.ToUnixTimeSeconds()));
            (string sandbox, string approval) = mode == "full" ? ("""{"type":"disabled"}""", "never") : ("""{"type":"managed"}""", "on-request");
            _records[id] = new CodexThreadRecord(id, @"C:\none.jsonl", @"C:\w", $"会话 {id}", "gpt-5.5", sandbox, approval, false);
        }
    }

    /// <summary>A full-access conversation runs phone messages as the user; selecting one is confirmed.</summary>
    [Fact]
    public async Task SelectingAFullAccessConversationAsksFirst()
    {
        GivenConversations(("a", "full"));
        await _page.RefreshSessionsAsync();
        _answer = false;

        await _page.ToggleAsync(_page.Sessions[0]);

        Assert.Single(_asked);
        Assert.Contains("完全访问", _asked[0]);
        Assert.Empty(_agent.State.Sessions);
    }

    [Fact]
    public async Task AnAutoConversationIsSelectedWithoutAsking()
    {
        GivenConversations(("a", "auto"));
        await _page.RefreshSessionsAsync();

        await _page.ToggleAsync(_page.Sessions[0]);

        Assert.Empty(_asked);
        Assert.True(_page.Sessions[0].IsSelected);
        Assert.Equal("自动 · 越界操作需在电脑上确认", _page.Sessions[0].PermissionText);
    }

    /// <summary>Once five are chosen the rest are disabled, and the reason is on screen.</summary>
    [Fact]
    public async Task AFullSelectionDisablesTheRest()
    {
        GivenConversations(("1", "auto"), ("2", "auto"), ("3", "auto"), ("4", "auto"), ("5", "auto"), ("6", "auto"));
        await _page.RefreshSessionsAsync();

        foreach (SyncSessionItem item in _page.Sessions.Take(5).ToList())
        {
            await _page.ToggleAsync(item);
        }

        Assert.Equal("已选 5 / 5", _page.SelectedCountText);
        Assert.False(_page.Sessions[5].CanToggle);
        Assert.True(_page.Sessions[0].CanToggle, "a selected one can still be removed");
    }

    [Fact]
    public async Task ASelectedConversationGoneFromCodexIsMarkedAndRemovable()
    {
        _agent.Select("gone", "旧会话");

        await _page.RefreshSessionsAsync();

        SyncSessionItem item = Assert.Single(_page.Sessions);
        Assert.True(item.IsMissing);
        await _page.ToggleAsync(item);
        Assert.Empty(_agent.State.Sessions);
    }

    [Fact]
    public async Task AClosedDesktopAppIsSaidSo()
    {
        _tools.Unavailable = true;

        await _page.RefreshSessionsAsync();

        Assert.Equal("Codex 桌面版未运行", _page.DesktopStatus);
        Assert.True(_page.IsDesktopOffline, "the page offers 启动 ChatGPT");
    }

    /// <summary>After 启动 ChatGPT the list fills in once the app answers, without 刷新.</summary>
    [Fact]
    public async Task WaitingForTheDesktopAppStopsWhenItAnswers()
    {
        GivenConversations(("a", "auto"));
        _tools.Unavailable = true;
        await _page.RefreshSessionsAsync();
        _tools.AvailableAfterCalls = 2;

        await _page.WaitForDesktopAsync(TimeSpan.FromSeconds(10), TimeSpan.FromMilliseconds(1));

        Assert.False(_page.IsDesktopOffline);
        Assert.False(_page.IsWaitingForDesktop);
        Assert.Single(_page.Sessions);
        Assert.Null(_page.Message);
    }

    [Fact]
    public async Task WaitingForTheDesktopAppGivesUpAndSaysWhereToLook()
    {
        _tools.Unavailable = true;
        await _page.RefreshSessionsAsync();

        await _page.WaitForDesktopAsync(TimeSpan.FromMilliseconds(30), TimeSpan.FromMilliseconds(5));

        Assert.True(_page.IsDesktopOffline);
        Assert.False(_page.IsWaitingForDesktop);
        Assert.Contains("仪表盘", _page.Message);
    }

    [Fact]
    public void TheSwitchIsOffUntilTurnedOn()
    {
        Assert.False(_page.IsEnabled);
        Assert.Equal("未开启", _page.StatusText);

        _page.IsEnabled = true;

        Assert.True(_agent.State.Enabled);
    }

    private sealed class Store : IDesktopSyncStateStore
    {
        private DesktopSyncState _state = DesktopSyncState.Empty;

        public DesktopSyncState Load() => _state;

        public void Save(DesktopSyncState state) => _state = state;
    }

    private sealed class Tools : IDesktopAppTools
    {
        public List<DesktopThread> Threads { get; } = [];

        public bool Unavailable { get; set; }

        /// <summary>Comes up after this many more list calls, like an app that is starting.</summary>
        public int? AvailableAfterCalls { get; set; }

        public AppToolsCapabilities Capabilities => new(true, true, true, true);

        public Task<AppToolsCapabilities> ConnectAsync(CancellationToken cancellationToken) => Task.FromResult(Capabilities);

        public Task<IReadOnlyList<DesktopThread>> ListThreadsAsync(int limit, CancellationToken cancellationToken)
        {
            if (AvailableAfterCalls is int left)
            {
                AvailableAfterCalls = --left <= 0 ? null : left;
                Unavailable = AvailableAfterCalls is not null;
            }

            return Unavailable
                ? throw new DesktopAppToolsException(DesktopAppToolsFailure.Unavailable, "not running")
                : Task.FromResult<IReadOnlyList<DesktopThread>>(Threads);
        }

        public Task<DesktopThreadStatus> GetThreadStatusAsync(string threadId, CancellationToken cancellationToken) =>
            Task.FromResult(new DesktopThreadStatus("idle", []));

        public Task SendMessageAsync(string threadId, string prompt, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task NavigateToAsync(string threadId, CancellationToken cancellationToken) => Task.CompletedTask;
    }
}
