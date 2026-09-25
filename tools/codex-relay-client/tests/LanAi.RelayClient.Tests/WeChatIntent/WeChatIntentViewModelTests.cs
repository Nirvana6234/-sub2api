using System.Text.Json;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.ViewModels;
using LanAi.RelayClient.WeChatIntent;
using Xunit;
using static LanAi.RelayClient.Tests.WeChatIntent.ChatScreenParserTests;

namespace LanAi.RelayClient.Tests.WeChatIntent;

public sealed class WeChatIntentViewModelTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "wechat-intent-vm-" + Guid.NewGuid().ToString("N"));
    private DateTimeOffset _now = new(2026, 9, 25, 20, 0, 0, TimeSpan.FromHours(8));
    private bool _weChatRunning = true;
    private FakeUiTimer? _timer;
    private string _lastDir = string.Empty;

    public void Dispose()
    {
        if (Directory.Exists(_dir))
        {
            Directory.Delete(_dir, recursive: true);
        }
    }

    private sealed class FakeReader : IWeChatReader
    {
        public event Action<ReaderEvent>? EventReceived;

        public event Action<string>? Failed;

        public bool IsRunning { get; private set; }

        public int Snapshots { get; private set; }

        public void Start() => IsRunning = true;

        public void Stop() => IsRunning = false;

        public void Snapshot() => Snapshots++;

        public void Dispose() => Stop();

        public void Raise(ReaderEvent e) => EventReceived?.Invoke(e);

        public void Fail(string message) => Failed?.Invoke(message);
    }

    private sealed class FakeJev : IJevClient
    {
        public List<JevState> Seen { get; } = [];

        public Func<JevOutcome> Next { get; set; } =
            () => new JevOutcome(JsonSerializer.Deserialize(JevClientTests.OkBody, JevJsonContext.Default.JevResponse), JevFailure.None, 200);

        /// <summary>When set, answers wait for it — to see the 「分析中」 placeholder.</summary>
        public TaskCompletionSource? Gate { get; set; }

        public async Task<JevOutcome> EvaluateAsync(JevState state, CancellationToken cancellationToken)
        {
            Seen.Add(state);
            if (Gate is not null)
            {
                await Gate.Task;
            }

            return Next();
        }
    }

    private sealed class PlainProtector : ISnapshotProtector
    {
        public byte[] Protect(byte[] plaintext) => plaintext;

        public byte[] Unprotect(byte[] protectedData) => protectedData;
    }

    private FakeJev? _relay;

    /// <param name="ownKeyRoute">A test build (the own-key route exists). False is a production build.</param>
    /// <param name="startOnOwnKey">Most tests exercise the pipeline through the own-key client; the 共飞 group is the default otherwise.</param>
    private (WeChatIntentViewModel Vm, FakeReader Reader, FakeJev Jev) Create(bool withReader = true, bool withKey = true, JevKeyCheck check = JevKeyCheck.Valid, bool ownKeyRoute = true, bool startOnOwnKey = true)
    {
        var reader = new FakeReader();
        var jev = new FakeJev();
        string dir = Path.Combine(_dir, Guid.NewGuid().ToString("N"));
        _lastDir = dir;
        var keys = new JevApiKeyStore(new PlainProtector(), dir);
        string scope = JevApiKeyStore.ScopeFor("https://relay.test", "ann@example.com");
        if (withKey)
        {
            keys.Save(scope, "apikey-1234567890");
        }

        var preferences = new WeChatIntentPreferenceStore(Path.Combine(dir, "prefs.json"));
        if (startOnOwnKey && ownKeyRoute)
        {
            preferences.Save(new WeChatIntentPreferenceStore.Preferences { UseRelayGroup = false });
        }

        var vm = new WeChatIntentViewModel(
            withReader ? reader : null,
            ownKeyRoute ? jev : null,
            (_, _) => Task.FromResult(check),
            keys,
            preferences,
            new WeChatIntentUsageStore(Path.Combine(dir, "usage.json"), () => _now.Date),
            new WeChatIntentFeedbackStore(Path.Combine(dir, "feedback.jsonl")),
            () => _weChatRunning,
            action => action(),
            (interval, onTick) => _timer = new FakeUiTimer(interval, onTick),
            () => _now,
            relayJev: _relay = new FakeJev())
        {
            Confirm = _ => Task.FromResult(true),
        };
        vm.SetSignedIn(true, scope);
        return (vm, reader, jev);
    }

    private static ReaderEvent InFront() => new()
    {
        Type = ReaderEvent.Window,
        State = ReaderEvent.StateForeground,
        Rect = new ReaderRect { X = 346, Y = 111, W = 1164, H = 963 },
        Scale = 1.0,
    };

    private void Advance(double seconds)
    {
        _now = _now.AddSeconds(seconds);
        _timer!.Tick();
    }

    [Fact]
    public void TheSwitchNeedsWeChatAKeyAndTheReader()
    {
        _weChatRunning = false;
        Assert.Equal("需要先打开电脑版微信", Create().Vm.DisabledReason);

        _weChatRunning = true;
        Assert.Equal("先填写并验证 TypeSafe API Key", Create(withKey: false).Vm.DisabledReason);
        Assert.StartsWith("未安装微信读取组件", Create(withReader: false).Vm.DisabledReason, StringComparison.Ordinal);
        Assert.True(Create().Vm.CanTurnOn);
    }

    [Fact]
    public async Task TurningOnAsksForConsentOnceAndStartsTheReader()
    {
        var (vm, reader, _) = Create();
        int asked = 0;
        vm.Confirm = _ => { asked++; return Task.FromResult(false); };

        await vm.SetEnabledAsync(true);
        Assert.False(vm.IsEnabled);
        Assert.False(reader.IsRunning);

        vm.Confirm = _ => { asked++; return Task.FromResult(true); };
        await vm.SetEnabledAsync(true);
        await vm.SetEnabledAsync(false);
        await vm.SetEnabledAsync(true);

        Assert.Equal(2, asked);
        Assert.Equal(WeChatIntentRunState.Running, vm.RunState);
        Assert.True(reader.IsRunning);
    }

    [Fact]
    public async Task WeChatQuittingWaitsInsteadOfSwitchingOff()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);

        _weChatRunning = false;
        Advance(3);

        Assert.True(vm.IsEnabled);
        Assert.Equal(WeChatIntentRunState.WaitingForWeChat, vm.RunState);
        Assert.False(reader.IsRunning);

        _weChatRunning = true;
        Advance(3);
        Assert.Equal(WeChatIntentRunState.Running, vm.RunState);
    }

    [Fact]
    public async Task EveryMessageFromThemGetsItsOwnJudgementWithOnlyWhatCameBefore()
    {
        var (vm, reader, jev) = Create();
        await vm.SetEnabledAsync(true);
        reader.Raise(InFront());
        Assert.True(vm.Overlay.IsVisible);

        reader.Raise(Frame("小明", Them(187, "你今天是不是又忘了"), Me(250, "记得"), Them(320, "那你说"), Me(390, "等一下")));
        Assert.Empty(jev.Seen);
        Advance(1);

        Assert.Equal(2, jev.Seen.Count);
        JevState first = jev.Seen.Single(s => s.LatestFromThem[0] == "你今天是不是又忘了");
        JevState second = jev.Seen.Single(s => s.LatestFromThem[0] == "那你说");
        Assert.Equal(["你今天是不是又忘了"], first.Conversation.Select(t => t.Text));
        Assert.Equal(["你今天是不是又忘了", "记得", "那你说"], second.Conversation.Select(t => t.Text));

        Assert.Equal([111 + 187 - 8, 111 + 320 - 8], vm.Overlay.InlineCards.Select(c => c.ScreenY));
        Assert.All(vm.Overlay.InlineCards, c => Assert.StartsWith("⏱ 在考验你 93%", c.Headline, StringComparison.Ordinal));
        Assert.StartsWith("今日 2 次", vm.TodayText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task CardsFollowScrollingAndAMessageIsJudgedOnlyOnce()
    {
        var (vm, reader, jev) = Create();
        await vm.SetEnabledAsync(true);
        reader.Raise(InFront());

        reader.Raise(Frame("小明", Me(187, "在"), Them(250, "那你说")));
        Advance(1);
        InlineCardViewModel card = Assert.Single(vm.Overlay.InlineCards);
        Assert.Equal(111 + 250 - 8, card.ScreenY);            // WeChat's top + the bubble's top
        Assert.True(card.ScreenX > 346 + 583);                 // right of the bubble

        // Scrolling: cleared at once, back at the new position on the settled screen, not judged again.
        reader.Raise(new ReaderEvent { Type = ReaderEvent.Scrolling });
        Assert.Empty(vm.Overlay.InlineCards);
        reader.Raise(Frame("小明", Me(120, "在"), Them(183, "那你说"), Me(300, "我想说完整一点")));
        Advance(2);
        Assert.Equal(111 + 183 - 8, Assert.Single(vm.Overlay.InlineCards).ScreenY);
        Assert.Single(jev.Seen);

        // WeChat moved: the card moves with it.
        reader.Raise(InFront() with { Rect = new ReaderRect { X = 500, Y = 200, W = 1164, H = 963 } });
        Assert.Equal(200 + 183 - 8, Assert.Single(vm.Overlay.InlineCards).ScreenY);
    }

    [Fact]
    public async Task OnARetinaMacCardsArePlacedInPointsNotImagePixels()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);

        // macOS: the window in points, the captured image twice as large.
        reader.Raise(new ReaderEvent
        {
            Type = ReaderEvent.Window,
            State = ReaderEvent.StateForeground,
            Rect = new ReaderRect { X = 100, Y = 50, W = 582, H = 481 },
            Scale = 1.0,
            ImageScale = 2.0,
        });
        reader.Raise(Frame("小明", Them(500, "那你说")));
        Advance(1);

        // The bubble's top is 500 image pixels = 250 points; the card sits 8 design pixels above it.
        Assert.Equal(50 + 250 - 8, Assert.Single(vm.Overlay.InlineCards).ScreenY);
    }

    [Fact]
    public async Task ClickingACardKeepsTheCardsUpWhenTheReaderSaysTheClientIsInFront()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);

        reader.Raise(InFront() with { State = ReaderEvent.StateBackground, FrontPid = Environment.ProcessId });

        Assert.True(vm.Overlay.IsVisible);
    }

    [Fact]
    public async Task AMessageBeingJudgedShowsAPlaceholder()
    {
        var (vm, reader, jev) = Create();
        var gate = new TaskCompletionSource();
        jev.Gate = gate;
        await vm.SetEnabledAsync(true);
        reader.Raise(InFront());

        reader.Raise(Frame("小明", Them(250, "那你说")));
        Advance(1);
        InlineCardViewModel pending = Assert.Single(vm.Overlay.InlineCards);
        Assert.True(pending.IsPending);
        Assert.Equal("分析中…", pending.Headline);

        gate.SetResult();
        await Task.Yield();
        Assert.False(Assert.Single(vm.Overlay.InlineCards).IsPending);
    }

    [Fact]
    public async Task FeedbackIsRecordedOncePerCard()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);
        reader.Raise(InFront());
        reader.Raise(Frame("小明", Them(250, "那你说")));
        Advance(1);

        vm.GiveFeedback(vm.Overlay.InlineCards[0], correct: false);
        vm.GiveFeedback(vm.Overlay.InlineCards[0], correct: true);

        Assert.True(vm.Overlay.InlineCards[0].FeedbackGiven);
        string line = Assert.Single(File.ReadAllLines(Path.Combine(_lastDir, "feedback.jsonl")));
        Assert.Contains("\"correct\":false", line, StringComparison.Ordinal);
        Assert.DoesNotContain("那你说", line, StringComparison.Ordinal);
    }

    [Fact]
    public void TheRelayRouteIsOfferedOnlyOnceTheServerHasAJevGroup()
    {
        var (vm, _, _) = Create(withKey: false);
        Assert.False(vm.HasRelayGroups);

        vm.SetRelayGroups([]);
        Assert.False(vm.HasRelayGroups);

        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);
        Assert.True(vm.HasRelayGroups);
        Assert.Equal(7, vm.SelectedRelayGroup!.Id);
    }

    [Fact]
    public async Task SwitchingToTheRelayAsksItsOwnConsentAndThenJudgesThroughIt()
    {
        var (vm, reader, ownKey) = Create(withKey: false);
        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);
        var asked = new List<string>();
        vm.Confirm = text => { asked.Add(text); return Task.FromResult(true); };

        await vm.SetUseRelayGroupAsync(true);
        Assert.True(vm.UseRelayGroup);
        Assert.True(vm.CanTurnOn);                      // no key needed on this route
        Assert.Equal(7, vm.CurrentRelayGroupId());

        await vm.SetEnabledAsync(true);
        reader.Raise(InFront());
        reader.Raise(Frame("小明", Them(250, "那你说")));
        Advance(1);

        Assert.Single(_relay!.Seen);
        Assert.Empty(ownKey.Seen);
        // Once: the relay's consent covers the whole route, so turning on does not ask again.
        Assert.Contains("共飞服务器", Assert.Single(asked), StringComparison.Ordinal);
    }

    [Fact]
    public void AProductionBuildOnlyHasTheRelayRouteAndNeverReadsASavedKey()
    {
        // A key left behind by an earlier test build, and a preference for it.
        var (vm, _, _) = Create(ownKeyRoute: false);

        Assert.False(vm.OwnKeyAvailable);
        Assert.True(vm.UseRelayGroup);
        Assert.Equal(JevKeyState.Missing, vm.KeyState);
        Assert.Null(vm.CurrentKey());
        Assert.Equal("暂无可用的共飞 Jev 分组", vm.DisabledReason);
    }

    [Fact]
    public async Task AProductionBuildCannotBeSwitchedToTheOwnKey()
    {
        var (vm, _, _) = Create(ownKeyRoute: false);
        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);

        await vm.SetUseRelayGroupAsync(false);

        Assert.True(vm.UseRelayGroup);
        Assert.True(vm.CanTurnOn);
    }

    [Fact]
    public void ATestBuildStartsOnTheRelayGroupByDefault()
    {
        var (vm, _, _) = Create(startOnOwnKey: false);

        Assert.True(vm.OwnKeyAvailable);
        Assert.True(vm.UseRelayGroup);
    }

    [Fact]
    public async Task DecliningTheRelayConsentKeepsTheOwnKeyRoute()
    {
        var (vm, _, _) = Create();
        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);
        vm.Confirm = _ => Task.FromResult(false);

        await vm.SetUseRelayGroupAsync(true);

        Assert.False(vm.UseRelayGroup);
    }

    [Fact]
    public async Task TheRelayGroupDisappearingStopsTheRelayRoute()
    {
        var (vm, reader, _) = Create(withKey: false);
        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);
        await vm.SetUseRelayGroupAsync(true);
        await vm.SetEnabledAsync(true);
        Assert.True(reader.IsRunning);

        vm.SetRelayGroups([]);

        Assert.Equal(WeChatIntentRunState.Stopped, vm.RunState);
        Assert.False(reader.IsRunning);
    }

    [Fact]
    public async Task ARelayBalanceProblemStopsWithTheRelaysWording()
    {
        var (vm, reader, _) = Create(withKey: false);
        vm.SetRelayGroups([new JevGroupOption(7, "Jev 意图判断", "1.0x")]);
        await vm.SetUseRelayGroupAsync(true);
        await vm.SetEnabledAsync(true);
        _relay!.Next = () => new JevOutcome(null, JevFailure.Quota, 402);

        reader.Raise(Frame("小明", Them(250, "那你说")));
        Advance(1);

        Assert.Equal(WeChatIntentRunState.Stopped, vm.RunState);
        Assert.Contains("共飞余额不足", vm.StatusText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AGroupChatIsNotAnalysed()
    {
        var (vm, reader, jev) = Create();
        await vm.SetEnabledAsync(true);

        reader.Raise(Frame("家人群(12)", Them(187, "吃饭了吗")));
        Advance(5);

        Assert.Empty(jev.Seen);
        Assert.Equal("群聊暂不分析", vm.Overlay.Status);
    }

    [Fact]
    public async Task AnInvalidKeyStopsTheFeatureAndFlagsThePage()
    {
        var (vm, reader, jev) = Create();
        jev.Next = () => new JevOutcome(null, JevFailure.InvalidKey, 401);
        await vm.SetEnabledAsync(true);

        reader.Raise(Frame("小明", Them(187, "在吗")));
        Advance(3);

        Assert.Equal(WeChatIntentRunState.Stopped, vm.RunState);
        Assert.True(vm.NeedsAttention);
        Assert.Equal(JevKeyState.Invalid, vm.KeyState);
        Assert.False(reader.IsRunning);
    }

    [Fact]
    public async Task SigningOutStopsAndForgetsEverythingRead()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);
        reader.Raise(Frame("小明", Them(187, "在吗")));
        Advance(3);
        Assert.NotEmpty(vm.Overlay.InlineCards);

        vm.SetSignedIn(false, null);

        Assert.False(reader.IsRunning);
        Assert.Empty(vm.Overlay.InlineCards);
        Assert.Equal(JevKeyState.Missing, vm.KeyState);
    }

    [Fact]
    public async Task ARejectedKeyIsNotSaved()
    {
        var (vm, _, _) = Create(withKey: false, check: JevKeyCheck.Invalid);
        vm.KeyInput = "wrong";

        await vm.SaveKeyAsync();

        Assert.Equal(JevKeyState.Missing, vm.KeyState);
        Assert.Equal("TypeSafe 不接受这个 key，请检查后重试", vm.KeyMessage);
        Assert.Empty(Directory.Exists(_lastDir) ? Directory.GetFiles(_lastDir, "typesafe-key-*") : []);
    }

    [Fact]
    public async Task ReaderGivingUpStopsTheFeatureWithItsReason()
    {
        var (vm, reader, _) = Create();
        await vm.SetEnabledAsync(true);

        reader.Fail("微信读取组件异常，已停止。");

        Assert.Equal(WeChatIntentRunState.Stopped, vm.RunState);
        Assert.Contains("微信读取组件异常", vm.StatusText, StringComparison.Ordinal);
    }
}
