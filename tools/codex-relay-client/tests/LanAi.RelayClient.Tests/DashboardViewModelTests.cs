using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Locks F4.2's isolation rules and F5.4/F5.5's switching behaviour.
/// </summary>
public sealed class DashboardViewModelTests
{
    private static (DashboardViewModel Dashboard, FakeRelayClient Relay, FakeSessionStore Store, RelaySessionManager Session, FakeGroupPreferenceStore Preferences) Build()
    {
        var relay = new FakeRelayClient();
        var store = new FakeSessionStore();
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, store, "https://relay.test/", clock.Read);
        var preferences = new FakeGroupPreferenceStore();

        var naming = new ManagedKeyNaming(new FixedInstallId("testinst"));
        var codex = new FakeCodexStartup();

        return (new DashboardViewModel(relay, session, preferences, naming, codex), relay, store, session, preferences);
    }

    private static async Task<(DashboardViewModel Dashboard, FakeRelayClient Relay, FakeGroupPreferenceStore Preferences)> SignedInAsync()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, FakeGroupPreferenceStore preferences) = Build();
        await session.SignInAsync("a@b.com", "pw");
        return (dashboard, relay, preferences);
    }

    private static RelayGroup Group(
        long id,
        string name,
        double rate = 1.0,
        string type = "standard",
        string platform = "openai") =>
        new() { Id = id, Name = name, RateMultiplier = rate, SubscriptionType = type, Platform = platform };

    [Fact]
    public async Task MissingClaudeThinkingPreferenceDefaultsToMedium()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");
        relay.ClaudePreference = new ClaudePreferenceDto
        {
            Model = "claude-sonnet-5",
            ThinkingLevel = string.Empty,
        };

        await dashboard.LoadClaudePreferenceAsync();

        Assert.Equal(DashboardViewModel.ClaudeThinkingLevels[2], dashboard.SelectedClaudeThinkingLevel);
    }

    [Fact]
    public async Task ExplicitlyDisabledClaudeThinkingPreferenceRemainsOff()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");
        relay.ClaudePreference = new ClaudePreferenceDto
        {
            Model = "claude-sonnet-5",
            ThinkingLevel = "off",
        };

        await dashboard.LoadClaudePreferenceAsync();

        Assert.Equal(DashboardViewModel.ClaudeThinkingLevels[0], dashboard.SelectedClaudeThinkingLevel);
    }

    [Fact]
    public async Task AFailedUsageCardLeavesTheAccountCardIntact()
    {
        // The heart of F4.2: cards fail alone. A shared try/catch or a combined
        // await would take all of them down together.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.AccountReady);
        Assert.False(dashboard.UsageReady);
        Assert.True(dashboard.UsageUnavailable);
        Assert.True(dashboard.GroupsReady);
    }

    [Fact]
    public async Task ACardReturningUnauthorizedDoesNotSignTheUserOut()
    {
        // F4.2 forbids a card failure from logging anyone out. A 401 from a panel
        // endpoint is still an explicit rejection, so without deliberate handling
        // the session rules from M1 would end the session here.
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");

        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.Unauthenticated, "过期");

        await dashboard.RefreshAsync();

        Assert.True(session.IsSignedIn);
        Assert.False(dashboard.UsageReady);
    }

    [Fact]
    public async Task ANewSessionAutomaticallySelectsAndRemembersTheFirstAvailableGroup()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, FakeGroupPreferenceStore preferences) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];

        await dashboard.RefreshAsync();

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.Equal("甲", dashboard.CurrentGroupName);
        Assert.Equal(11, preferences.Saved);
        Assert.True(dashboard.Groups.Single(group => group.Id == 11).IsCurrent);
    }

    [Fact]
    public async Task LosingTheRatesCallStillShowsGroupsAtTheirDefaultMultiplier()
    {
        // Without personal rates every group's own multiplier is still correct for
        // anyone who has no special deal; dropping the list instead would also cost
        // the user the ability to switch.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "标准组", rate: 1.5)];
        relay.OnGroupRates = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.GroupsReady);
        Assert.Equal("1.500x", Assert.Single(dashboard.Groups).RateLabel);
        Assert.Equal("每 $1 Token 额度扣除 ￥1.500 账户余额", Assert.Single(dashboard.Groups).RateDescription);
    }

    [Fact]
    public async Task ASubscriptionGroupShowsTheWordNotANumber()
    {
        // Matches GroupBadge, which prints t('groups.subscription') for these.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "订阅组", rate: 2.0, type: "subscription")];

        await dashboard.RefreshAsync();

        Assert.Equal("订阅", Assert.Single(dashboard.Groups).RateLabel);
    }

    [Fact]
    public async Task APersonalRateIsShownAgainstTheStruckThroughDefault()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "标准组", rate: 2.0)];
        relay.OnGroupRates = () => new Dictionary<long, double> { [11] = 0.8 };

        await dashboard.RefreshAsync();

        GroupItemViewModel item = Assert.Single(dashboard.Groups);
        Assert.Equal("0.800x", item.RateLabel);
        Assert.Equal("2.000x", item.StruckThroughRateLabel);
        Assert.Equal("每 $1 Token 额度扣除 ￥0.800 账户余额", item.RateDescription);
        Assert.True(item.HasStruckThroughRate);
    }

    [Fact]
    public async Task WithNoManagedKeyTheChoiceIsRecordedLocally()
    {
        // The managed key is issued in M3, and it is created with its group already
        // set — so the selection has to exist before the key does. This is a real
        // branch, not a placeholder.
        (DashboardViewModel dashboard, FakeRelayClient relay, FakeGroupPreferenceStore preferences) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () => [];

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(12, preferences.Saved);
        Assert.Equal("乙", dashboard.CurrentGroupName);
    }

    [Fact]
    public async Task WithAManagedKeyTheSwitchIsWrittenToTheServer()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 11 },
        ];

        long? written = null;
        relay.OnUpdateKeyGroup = groupId =>
        {
            written = groupId;
            return new RelayApiKey { Id = 5, GroupId = groupId };
        };

        await dashboard.RefreshAsync();
        Assert.Equal("甲", dashboard.CurrentGroupName);

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(12, written);
        Assert.Equal("乙", dashboard.CurrentGroupName);
    }

    [Fact]
    public async Task ClaudeGroupsRemainAvailableForTheClaudeBridge()
    {
        // Claude groups are supported by the relay's Claude-over-Codex bridge;
        // unrelated platforms remain hidden from this client.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () =>
        [
            Group(1, "openai 组", platform: "openai"),
            Group(2, "anthropic 组", platform: "anthropic"),
            Group(3, "gemini 组", platform: "gemini"),
            Group(4, "grok 组", platform: "grok"),
            Group(5, "复合组", platform: "composite"),
        ];
        relay.OnListKeys = () => [];

        await dashboard.RefreshAsync();

        // Composite is excluded because its actual route is not present in the
        // user-facing payload.
        Assert.Equal([1L, 2L], dashboard.Groups.Select(g => g.Id));
    }

    [Fact]
    public async Task TheGroupInForceStaysVisibleEvenOnAnotherPlatform()
    {
        // An account may already be bound to a non-Codex group. Filtering it out
        // would leave the user unable to see what they are actually billed on,
        // which is worse than showing a row they should probably move off.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () =>
        [
            Group(1, "openai 组", platform: "openai"),
            Group(2, "default", platform: "anthropic"),
        ];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 2 },
        ];

        await dashboard.RefreshAsync();

        Assert.Equal([1L, 2L], dashboard.Groups.Select(g => g.Id));
        Assert.Equal("default", dashboard.CurrentGroupName);
        Assert.Equal(2, dashboard.SelectedGroup!.Id);
    }

    [Fact]
    public async Task LoadingTheDropdownDoesNotCountAsTheUserSwitching()
    {
        // The classic dropdown trap: populating the list and preselecting the
        // current group looks identical to a user picking one. Without a guard the
        // client would PUT the same group back on every 60-second refresh.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 11 },
        ];
        relay.OnUpdateKeyGroup = _ => throw new InvalidOperationException("refresh must not write anything");

        await dashboard.RefreshAsync();
        await dashboard.RefreshAsync();

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.Equal("甲", dashboard.CurrentGroupName);
    }

    [Fact]
    public async Task TheDropdownOpensOnTheGroupActuallyInForce()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙", rate: 2.0)];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 12 },
        ];

        await dashboard.RefreshAsync();

        Assert.Equal(12, dashboard.SelectedGroup!.Id);
        Assert.Equal("乙", dashboard.CurrentGroupName);
        Assert.Equal("2.000x", dashboard.CurrentGroupRate);
        Assert.Equal("当前使用中", dashboard.Groups.Single(g => g.Id == 12).CurrentMarker);
        Assert.Equal(string.Empty, dashboard.Groups.Single(g => g.Id == 11).CurrentMarker);
    }

    [Fact]
    public async Task ARejectedSwitchRollsTheDropdownBackToo()
    {
        // The selection is what the user reads, so rolling back the flag without
        // rolling back the dropdown would leave the two disagreeing.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 11 },
        ];
        relay.OnUpdateKeyGroup = _ => throw new RelayApiException(RelayFailure.Rejected, "分组已下线");

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(11, dashboard.SelectedGroup!.Id);
        Assert.Equal("甲", dashboard.CurrentGroupName);
    }

    [Fact]
    public async Task ARejectedSwitchRollsTheSelectionBack()
    {
        // F5.5. Leaving the tick on the new row would tell the user their traffic is
        // billed somewhere it is not.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 11 },
        ];
        relay.OnUpdateKeyGroup = _ => throw new RelayApiException(RelayFailure.Rejected, "分组已下线");

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal("甲", dashboard.CurrentGroupName);
        Assert.True(dashboard.Groups.Single(g => g.Id == 11).IsCurrent);
        Assert.False(dashboard.Groups.Single(g => g.Id == 12).IsCurrent);
        Assert.Equal("分组已下线", dashboard.GroupMessage);
    }

    [Fact]
    public async Task TheLowBalanceWarningUsesTheServersThresholdNotAHardCodedOne()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        dashboard.ApplySettings(new PublicSettings
        {
            BalanceLowNotifyEnabled = true,
            BalanceLowNotifyThreshold = 5.0,
            BalanceLowNotifyRechargeUrl = "https://relay.test/recharge",
        });
        relay.OnCurrentUser = () => new RelayUser { Username = "ann", Balance = 3.0 };

        await dashboard.RefreshAsync();

        Assert.True(dashboard.BalanceIsLow);
        Assert.True(dashboard.CanRecharge);
    }

    [Fact]
    public async Task ABalanceAboveTheThresholdRaisesNoWarning()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        dashboard.ApplySettings(new PublicSettings
        {
            BalanceLowNotifyEnabled = true,
            BalanceLowNotifyThreshold = 5.0,
        });
        relay.OnCurrentUser = () => new RelayUser { Username = "ann", Balance = 12.0 };

        await dashboard.RefreshAsync();

        Assert.False(dashboard.BalanceIsLow);
    }

    [Fact]
    public async Task TheWarningStaysOffWhenTheOperatorDisabledIt()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        dashboard.ApplySettings(new PublicSettings
        {
            BalanceLowNotifyEnabled = false,
            BalanceLowNotifyThreshold = 5.0,
        });
        relay.OnCurrentUser = () => new RelayUser { Username = "ann", Balance = 0.5 };

        await dashboard.RefreshAsync();

        Assert.False(dashboard.BalanceIsLow);
    }

    [Fact]
    public async Task OnlyKeysNamedForThisMachineAreTreatedAsManaged()
    {
        // 认名不认值 (F3.2.1). A key the user made by hand must never be rebound by
        // this client — switching groups would silently change how their own key bills.
        (DashboardViewModel dashboard, FakeRelayClient relay, FakeGroupPreferenceStore preferences) = await SignedInAsync();
        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () => [new RelayApiKey { Id = 9, Name = "我自己建的 key", GroupId = 11 }];
        relay.OnUpdateKeyGroup = _ => throw new InvalidOperationException("must not touch a key we do not manage");

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(12, preferences.Saved);
    }

    [Fact]
    public async Task ACardFailingWithAnUnexpectedExceptionStillSparesTheOthers()
    {
        // F4.2 has to hold for more than RelayApiException. A narrower catch would
        // let a mapper bug or a cancellation abandon the cards queued behind it,
        // producing exactly the "one card takes down the page" outcome.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnDashboardStats = () => throw new InvalidOperationException("unexpected");
        relay.OnAvailableGroups = () => [Group(11, "甲")];

        await dashboard.RefreshAsync();

        Assert.True(dashboard.AccountReady);
        Assert.False(dashboard.UsageReady);
        Assert.True(dashboard.GroupsReady);
    }

    [Fact]
    public async Task SigningOutClearsThePreviousAccountsFigures()
    {
        // Otherwise the next user sees the last user's balance and usage until the
        // 60-second tick — a disclosure, not merely a stale view.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnCurrentUser = () => new RelayUser { Username = "ann", Balance = 42 };
        relay.OnAvailableGroups = () => [Group(11, "甲")];

        await dashboard.RefreshAsync();
        Assert.Equal("￥42", dashboard.BalanceText);

        dashboard.Reset();

        Assert.Equal("—", dashboard.BalanceText);
        Assert.Equal("—", dashboard.TodayRequestsText);
        Assert.Empty(dashboard.UserDisplayName);
        Assert.Empty(dashboard.Groups);
        Assert.Null(dashboard.SelectedGroup);
        Assert.Equal("未选择", dashboard.CurrentGroupName);
    }

    [Fact]
    public async Task ResetReleasesTheRefreshGuardSoTheNextUserCanLoad()
    {
        // The guard is what makes this subtle: if Reset left IsRefreshing set, the
        // next sign-in's refresh would return immediately and the new user would
        // stare at empty cards until the next poll.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();

        dashboard.Reset();
        relay.OnCurrentUser = () => new RelayUser { Username = "bob", Balance = 7 };

        await dashboard.RefreshAsync();

        Assert.Equal("￥7", dashboard.BalanceText);
        Assert.True(dashboard.AccountReady);
    }

    [Fact]
    public async Task StartingCodexReportsWhatHappened()
    {
        (DashboardViewModel dashboard, _, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal("ChatGPT 已就绪，可以开始对话了。", dashboard.CodexMessage);
        Assert.False(dashboard.IsStartingCodex);

        // Disabled afterwards, not re-armed: Codex is now up, and a live button
        // would invite a second launch that only rewrites the config.
        Assert.False(dashboard.CanStartCodex);
    }

    [Fact]
    public async Task ARunningCodexIsNotRestartedWithoutTheUsersConsent()
    {
        // Restarting throws away whatever turn they have in flight, so a silent
        // retry with AllowTerminateExisting would destroy work they never agreed
        // to lose.
        var codex = new FakeCodexStartup
        {
            OnRun = (_, allowRestart) => allowRestart
                ? new CodexStartupResult(CodexStartupStatus.Ready, "好了")
                : new CodexStartupResult(CodexStartupStatus.NeedsRestartConfirmation, "需要重启"),
        };
        DashboardViewModel dashboard = BuildWith(codex);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal(1, codex.RunCount);
        Assert.False(codex.LastAllowRestart);
        Assert.Equal("需要重启", dashboard.CodexMessage);
    }

    [Fact]
    public async Task AgreeingToTheRestartRetriesWithPermission()
    {
        var codex = new FakeCodexStartup
        {
            OnRun = (_, allowRestart) => allowRestart
                ? new CodexStartupResult(CodexStartupStatus.Ready, "好了")
                : new CodexStartupResult(CodexStartupStatus.NeedsRestartConfirmation, "需要重启"),
        };
        DashboardViewModel dashboard = BuildWith(codex);

        await dashboard.StartCodexAsync(_ => Task.FromResult(true));

        Assert.Equal(2, codex.RunCount);
        Assert.True(codex.LastAllowRestart);
        Assert.Equal("好了", dashboard.CodexMessage);
    }

    [Fact]
    public async Task TheSelectedGroupIsWhatTheLeaseBillsAgainst()
    {
        // The key is created with its group already set, so picking a group and
        // then pressing the button must carry that choice through.
        var codex = new FakeCodexStartup();
        long? seen = null;
        codex.OnRun = (groupId, _) =>
        {
            seen = groupId;
            return new CodexStartupResult(CodexStartupStatus.Ready, "好了");
        };

        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")],
            OnListKeys = () => [],
        };
        DashboardViewModel dashboard = BuildWith(codex, relay, out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();

        dashboard.SelectedGroup = dashboard.Groups.Single(g => g.Id == 12);
        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal(12, seen);
    }

    [Fact]
    public async Task StartingAClaudeGroupPassesTheSelectedModelToCodex()
    {
        var codex = new FakeCodexStartup();
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(12, "Claude", platform: "anthropic")],
            OnListKeys = () => [],
        };
        DashboardViewModel dashboard = BuildWith(codex, relay, out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        dashboard.SelectedClaudeModel = "claude-opus-5";

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal("claude-opus-5", codex.LastPreferredModel);
    }

    [Fact]
    public async Task AnUnexpectedFailureWhileStartingDoesNotEndTheSession()
    {
        var codex = new FakeCodexStartup { OnRun = (_, _) => throw new InvalidOperationException("boom") };
        DashboardViewModel dashboard = BuildWith(codex, new FakeRelayClient(), out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.True(session.IsSignedIn);
        Assert.False(dashboard.IsStartingCodex);
        Assert.Contains("出错", dashboard.CodexMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task TheButtonGoesQuietOnceCodexIsUp()
    {
        // A button that stays live after a successful launch reads to a novice as
        // "it did not work, press again" — and pressing again just rewrites the
        // config for no benefit.
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        DashboardViewModel dashboard = BuildWith(codex);

        await dashboard.MonitorCodexAsync();

        Assert.True(dashboard.IsCodexRunning);
        Assert.False(dashboard.CanStartCodex);
        Assert.Equal("ChatGPT 已启动", dashboard.StartCodexLabel);
    }

    [Fact]
    public async Task TheButtonComesBackWhenCodexIsClosed()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        DashboardViewModel dashboard = BuildWith(codex);
        await dashboard.MonitorCodexAsync();

        codex.OnCheck = () => new CodexHealth(true, false, null);
        await dashboard.MonitorCodexAsync();

        Assert.True(dashboard.CanStartCodex);
        Assert.Equal("启动 ChatGPT", dashboard.StartCodexLabel);
    }

    [Fact]
    public async Task ARunningCodexForAnotherAccountShowsManualActivationWithoutRestarting()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        var account = new FakeCodexAccountStore { Email = "old@example.com" };
        DashboardViewModel dashboard = BuildWith(codex, account, "new@example.com", out RelaySessionManager session);
        await session.SignInAsync("new@example.com", "pw");

        await dashboard.MonitorCodexAsync();

        Assert.True(dashboard.RequiresCodexAccountRestart);
        Assert.Equal("重启 ChatGPT 激活账户", dashboard.StartCodexLabel);
        Assert.Equal(0, codex.RunCount);
    }

    [Fact]
    public async Task ARunningCodexForTheSameAccountDoesNotNeedActivation()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        var account = new FakeCodexAccountStore { Email = "old@example.com" };
        DashboardViewModel dashboard = BuildWith(codex, account, "OLD@EXAMPLE.COM", out RelaySessionManager session);
        await session.SignInAsync("OLD@EXAMPLE.COM", "pw");

        await dashboard.MonitorCodexAsync();

        Assert.False(dashboard.RequiresCodexAccountRestart);
    }

    [Fact]
    public async Task ARunningCodexWithoutAnAccountRecordDoesNotNeedActivation()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        DashboardViewModel dashboard = BuildWith(codex, new FakeCodexAccountStore(), "new@example.com", out RelaySessionManager session);
        await session.SignInAsync("new@example.com", "pw");

        await dashboard.MonitorCodexAsync();

        Assert.False(dashboard.RequiresCodexAccountRestart);
    }

    [Fact]
    public async Task ConfirmingAccountActivationRestartsCodexAndRecordsTheCurrentAccount()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        var account = new FakeCodexAccountStore { Email = "old@example.com" };
        DashboardViewModel dashboard = BuildWith(codex, account, "new@example.com", out RelaySessionManager session);
        await session.SignInAsync("new@example.com", "pw");
        await dashboard.MonitorCodexAsync();

        await dashboard.StartCodexAsync(_ => Task.FromResult(true));

        Assert.Equal(1, codex.RunCount);
        Assert.True(codex.LastAllowRestart);
        Assert.Equal("new@example.com", account.Email);
        Assert.Equal(1, account.SaveCallCount);
        Assert.False(dashboard.RequiresCodexAccountRestart);
    }

    [Fact]
    public async Task CancellingAccountActivationLeavesCodexAndTheAccountRecordUntouched()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        var account = new FakeCodexAccountStore { Email = "old@example.com" };
        DashboardViewModel dashboard = BuildWith(codex, account, "new@example.com", out RelaySessionManager session);
        await session.SignInAsync("new@example.com", "pw");
        await dashboard.MonitorCodexAsync();

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.Equal(0, codex.RunCount);
        Assert.Equal("old@example.com", account.Email);
        Assert.Equal(0, account.SaveCallCount);
        Assert.True(dashboard.RequiresCodexAccountRestart);
    }

    [Fact]
    public async Task AFailedAccountActivationDoesNotReplaceTheAccountRecord()
    {
        var codex = new FakeCodexStartup
        {
            OnCheck = () => new CodexHealth(true, true, null),
            OnRun = (_, _) => new CodexStartupResult(CodexStartupStatus.LocalFailure, "failed"),
        };
        var account = new FakeCodexAccountStore { Email = "old@example.com" };
        DashboardViewModel dashboard = BuildWith(codex, account, "new@example.com", out RelaySessionManager session);
        await session.SignInAsync("new@example.com", "pw");
        await dashboard.MonitorCodexAsync();

        await dashboard.StartCodexAsync(_ => Task.FromResult(true));

        Assert.Equal(1, codex.RunCount);
        Assert.True(codex.LastAllowRestart);
        Assert.Equal("old@example.com", account.Email);
        Assert.True(dashboard.RequiresCodexAccountRestart);
    }

    [Fact]
    public async Task MissingCodexSwitchesTheButtonToInstallWithoutIssuingAKey()
    {
        var codex = new FakeCodexStartup
        {
            OnRun = (_, _) => new CodexStartupResult(CodexStartupStatus.NotInstalled, "未安装"),
        };
        var installer = new FakeCodexInstaller();
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            codexInstaller: installer);
        await session.SignInAsync("a@b.com", "pw");

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.True(dashboard.CodexNotInstalled);
        Assert.Equal("安装 ChatGPT", dashboard.StartCodexLabel);
        await dashboard.InstallCodexAsync();
        Assert.Equal(1, installer.EnsureAndLaunchCallCount);
    }

    [Fact]
    public async Task DownloadingCodexShowsProgressAndDisablesRepeatClicks()
    {
        var enteredDownload = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var allowInstall = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var installer = new FakeCodexInstaller
        {
            OnEnsureAndLaunch = async (progress, cancellationToken) =>
            {
                progress!.Report(new CodexDownloadProgress(50, 100));
                enteredDownload.SetResult();
                await allowInstall.Task.WaitAsync(cancellationToken);
                return new CodexInstallerResult(false, "downloaded");
            },
        };
        DashboardViewModel dashboard = BuildWith(new FakeCodexStartup(), installer, out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");
        dashboard.CodexNotInstalled = true;

        Task installation = dashboard.InstallCodexAsync();
        await enteredDownload.Task.WaitAsync(TimeSpan.FromSeconds(3));

        Assert.True(dashboard.IsInstallingCodex);
        Assert.Equal("正在下载 ChatGPT… 50%", dashboard.StartCodexLabel);
        Assert.False(dashboard.CanStartCodex);

        await dashboard.InstallCodexAsync();
        Assert.Equal(1, installer.EnsureAndLaunchCallCount);

        allowInstall.SetResult();
        await installation;
    }

    [Fact]
    public async Task InstallingCodexDisablesRepeatClicksAndActivatesAfterInstallation()
    {
        int checks = 0;
        var codex = new FakeCodexStartup
        {
            IsInstalled = false,
            OnInstalled = () => ++checks >= 2,
            OnRun = (_, allowRestart) =>
                allowRestart
                    ? new CodexStartupResult(CodexStartupStatus.Ready, "ready")
                    : new CodexStartupResult(CodexStartupStatus.Ready, "unexpected"),
        };
        var installer = new FakeCodexInstaller
        {
            LaunchResult = new CodexInstallerResult(true, "installing"),
        };
        DashboardViewModel dashboard = BuildWith(codex, installer, out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");
        dashboard.CodexNotInstalled = true;

        Task installation = dashboard.InstallCodexAsync();
        Assert.True(dashboard.IsInstallingCodex);
        Assert.False(dashboard.CanStartCodex);

        await dashboard.InstallCodexAsync();
        Assert.Equal(1, installer.EnsureAndLaunchCallCount);

        await installation;

        Assert.False(dashboard.IsInstallingCodex);
        Assert.False(dashboard.CodexNotInstalled);
        Assert.True(dashboard.IsCodexRunning);
        Assert.True(codex.LastAllowRestart);
    }

    [Fact]
    public async Task CancellingCodexInstallationRestoresTheInstallButton()
    {
        var codex = new FakeCodexStartup
        {
            IsInstalled = false,
            OnInstalled = () => false,
        };
        var installer = new FakeCodexInstaller
        {
            LaunchResult = new CodexInstallerResult(true, "installing"),
        };
        DashboardViewModel dashboard = BuildWith(codex, installer, out RelaySessionManager session);
        await session.SignInAsync("a@b.com", "pw");
        dashboard.CodexNotInstalled = true;
        using var cancellation = new CancellationTokenSource();

        Task installation = dashboard.InstallCodexAsync(cancellation.Token);
        Assert.True(dashboard.IsInstallingCodex);
        cancellation.Cancel();
        await installation;

        Assert.False(dashboard.IsInstallingCodex);
        Assert.True(dashboard.CodexNotInstalled);
        Assert.True(dashboard.CanStartCodex);
    }

    [Fact]
    public async Task ASuccessfulLaunchUpdatesTheButtonWithoutWaitingForThePoll()
    {
        DashboardViewModel dashboard = BuildWith(new FakeCodexStartup());

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));

        Assert.True(dashboard.IsCodexRunning);
    }

    [Fact]
    public async Task MonitoringRollsTheLeaseForwardAndSaysSo()
    {
        // The renewal is why the client stays resident at all; a tray icon that sat
        // there without renewing would keep the process alive and still let Codex
        // stop working overnight.
        DateTimeOffset renewed = DateTimeOffset.UtcNow.AddDays(1);
        var codex = new FakeCodexStartup
        {
            OnCheck = () => new CodexHealth(true, true, DateTimeOffset.UtcNow.AddHours(2)),
            OnRenew = () => renewed,
        };
        DashboardViewModel dashboard = BuildWith(codex);

        await dashboard.MonitorCodexAsync();

        Assert.Equal(1, codex.RenewCallCount);
        Assert.Contains("授权有效至", dashboard.LeaseStatus, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AFailingMonitorNeverInterruptsTheUser()
    {
        var codex = new FakeCodexStartup { OnCheck = () => throw new InvalidOperationException("boom") };
        DashboardViewModel dashboard = BuildWith(codex);

        await dashboard.MonitorCodexAsync();

        Assert.False(dashboard.IsCodexRunning);
    }

    [Fact]
    public async Task TheTrendChartPlotsWhatTheUserWasActuallyCharged()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () =>
        [
            new UsageTrendPoint { Date = "2026-07-30", ActualCost = 1.5, Requests = 10 },
            new UsageTrendPoint { Date = "2026-07-31", ActualCost = 2.25, Requests = 20 },
        ];

        await dashboard.RefreshAsync();

        Assert.True(dashboard.TrendReady);
        Assert.Equal(2, dashboard.CostTrend.Count);
        Assert.Equal(2.25, dashboard.CostTrend[1].Value);

        // Labelled by day only: seven full dates do not fit under a chart this
        // narrow, and the year is never in question.
        Assert.Equal("31", dashboard.CostTrend[1].Label);
    }

    [Fact]
    public async Task TheModelBreakdownShowsTheBiggestSpendersFirst()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnModelUsage = () =>
        [
            new ModelUsage { Model = "small", ActualCost = 0.1, Requests = 5 },
            new ModelUsage { Model = "big", ActualCost = 9.0, Requests = 2 },
            new ModelUsage { Model = "mid", ActualCost = 1.0, Requests = 3 },
        ];

        await dashboard.RefreshAsync();

        Assert.Equal(["big", "mid", "small"], dashboard.TopModelUsage.Select(m => m.Model));
        Assert.Equal("$9", dashboard.TopModelUsage[0].CostText);
    }

    [Fact]
    public async Task OnlyFiveModelsAreListed()
    {
        // The card answers "where is my money going"; a list long enough to scroll
        // stops answering that at a glance.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnModelUsage = () => Enumerable.Range(1, 12)
            .Select(i => new ModelUsage { Model = $"m{i}", ActualCost = i })
            .ToArray();

        await dashboard.RefreshAsync();

        Assert.Equal(5, dashboard.TopModelUsage.Count);
    }

    [Fact]
    public async Task LosingTheModelBreakdownStillLeavesTheChart()
    {
        // Nested isolation: the chart is worth showing even when the per-model
        // split is unavailable.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => [new UsageTrendPoint { Date = "2026-07-31", ActualCost = 1 }];
        relay.OnModelUsage = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.TrendReady);
        Assert.Single(dashboard.CostTrend);
        Assert.Empty(dashboard.TopModelUsage);
    }

    [Fact]
    public async Task AnAccountWithNoTrafficIsToldSoRatherThanShownABlankCard()
    {
        // An empty card and a broken one look identical to a novice; the group
        // dropdown taught us that once already.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => [];

        await dashboard.RefreshAsync();

        Assert.True(dashboard.TrendReady);
        Assert.False(dashboard.HasTrend);
        Assert.True(dashboard.HasNoUsageYet);
    }

    [Fact]
    public async Task AFailedTrendSaysUnavailableRatherThanEmpty()
    {
        // The two states must not be confused: "nothing yet" invites patience,
        // "cannot fetch" invites a retry.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.TrendUnavailable);
        Assert.False(dashboard.HasNoUsageYet);
    }

    [Fact]
    public async Task AFailedTrendLeavesTheOtherCardsAlone()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.False(dashboard.TrendReady);
        Assert.True(dashboard.TrendUnavailable);
        Assert.True(dashboard.AccountReady);
        Assert.True(dashboard.UsageReady);
    }

    [Fact]
    public async Task ASubscriptionShowsItsNameAndMonthlyRemainingProgress()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnSubscriptionSummary = () =>
        [
            new SubscriptionSummaryItem
            {
                GroupName = "专业订阅",
                MonthlyUsedUsd = 5,
                MonthlyLimitUsd = 10,
                WeeklyUsedUsd = 8,
                WeeklyLimitUsd = 9,
            },
        ];

        await dashboard.RefreshAsync();

        Assert.True(dashboard.SubscriptionReady);
        Assert.True(dashboard.HasSubscription);
        Assert.Equal("专业订阅", dashboard.SubscriptionName);
        Assert.Contains("$5 / $10", dashboard.SubscriptionProgressText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnAccountWithoutSubscriptionsHidesTheSubscriptionCard()
    {
        (DashboardViewModel dashboard, _, _) = await SignedInAsync();

        await dashboard.RefreshAsync();

        Assert.True(dashboard.SubscriptionReady);
        Assert.False(dashboard.HasSubscription);
    }

    [Fact]
    public async Task ASubscriptionFailureDoesNotTakeDownTheOtherCards()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnSubscriptionSummary = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.False(dashboard.SubscriptionReady);
        Assert.False(dashboard.HasSubscription);
        Assert.True(dashboard.AccountReady);
        Assert.True(dashboard.UsageReady);
    }

    private static DashboardViewModel BuildWith(FakeCodexStartup codex, FakeCodexAccountStore? account = null) =>
        BuildWith(codex, new FakeRelayClient(), out _, account);

    private static DashboardViewModel BuildWith(
        FakeCodexStartup codex,
        FakeCodexInstaller installer,
        out RelaySessionManager session)
    {
        var relay = new FakeRelayClient();
        var clock = new TestClock();
        session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);

        return new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            codexInstaller: installer,
            codexAccountStore: new FakeCodexAccountStore());
    }

    private static DashboardViewModel BuildWith(
        FakeCodexStartup codex,
        FakeCodexAccountStore account,
        string email,
        out RelaySessionManager session) =>
        BuildWith(
            codex,
            new FakeRelayClient
            {
                OnLogin = () => LoginOutcome.Authenticated(FakeRelayClient.Tokens("at", email: email)),
            },
            out session,
            account);

    private static DashboardViewModel BuildWith(
        FakeCodexStartup codex,
        FakeRelayClient relay,
        out RelaySessionManager session,
        FakeCodexAccountStore? account = null)
    {
        var clock = new TestClock();
        session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);

        return new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            codexAccountStore: account ?? new FakeCodexAccountStore());
    }

    [Fact]
    public async Task AnUnreachableServerGreysEveryCardWithoutSigningOut()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");

        relay.OnCurrentUser = () => throw new RelayApiException(RelayFailure.NetworkUnreachable, "断网");
        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.NetworkUnreachable, "断网");
        relay.OnAvailableGroups = () => throw new RelayApiException(RelayFailure.NetworkUnreachable, "断网");

        await dashboard.RefreshAsync();

        Assert.True(session.IsSignedIn);
        Assert.True(dashboard.AccountUnavailable);
        Assert.True(dashboard.UsageUnavailable);
        Assert.True(dashboard.GroupsUnavailable);
    }

    [Fact]
    public async Task ARateLimitSuppressesRefreshesUntilTheBackoffDeadline()
    {
        var relay = new FakeRelayClient
        {
            OnCurrentUser = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);
        await session.SignInAsync("a@b.com", "pw");
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            new FakeCodexStartup(),
            new PollingBackoff(clock.Read));

        await dashboard.RefreshAsync();
        await dashboard.RefreshAsync();

        Assert.Equal(1, relay.CurrentUserCallCount);
        Assert.True(dashboard.IsRateLimited);
        Assert.Contains("请求频繁", dashboard.RefreshMessage, StringComparison.Ordinal);

        clock.Advance(TimeSpan.FromMinutes(1));
        await dashboard.RefreshAsync();

        Assert.Equal(2, relay.CurrentUserCallCount);
    }

    [Fact]
    public async Task ARateLimitStopsTheCurrentRefreshBeforeLaterEndpoints()
    {
        var relay = new FakeRelayClient
        {
            OnCurrentUser = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            new FakeCodexStartup());

        await dashboard.RefreshAsync();

        Assert.Equal(1, relay.CurrentUserCallCount);
        Assert.Equal(0, relay.DashboardStatsCallCount);
        Assert.Equal(0, relay.SubscriptionSummaryCallCount);
        Assert.Equal(0, relay.AvailableGroupsCallCount);
        Assert.Equal(0, relay.GroupRatesCallCount);
        Assert.Equal(0, relay.ListKeysCallCount);
        Assert.Equal(0, relay.UsageTrendCallCount);
        Assert.Equal(0, relay.ModelUsageCallCount);
    }

    [Fact]
    public async Task AUsageRateLimitStopsBeforeSubscriptionAndGroups()
    {
        var relay = new FakeRelayClient
        {
            OnDashboardStats = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            new FakeCodexStartup());

        await dashboard.RefreshAsync();

        Assert.Equal(1, relay.DashboardStatsCallCount);
        Assert.Equal(0, relay.SubscriptionSummaryCallCount);
        Assert.Equal(0, relay.AvailableGroupsCallCount);
        Assert.Equal(0, relay.UsageTrendCallCount);
    }

    [Fact]
    public async Task AGroupRateLimitStopsBeforeManagedKeyAndTrendCalls()
    {
        var relay = new FakeRelayClient
        {
            OnGroupRates = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            new FakeCodexStartup());

        await dashboard.RefreshAsync();

        Assert.Equal(1, relay.GroupRatesCallCount);
        Assert.Equal(0, relay.ListKeysCallCount);
        Assert.Equal(0, relay.UsageTrendCallCount);
        Assert.Equal(0, relay.ModelUsageCallCount);
    }

    [Fact]
    public async Task RateLimitBackoffAlsoSuppressesCodexMonitoring()
    {
        var relay = new FakeRelayClient
        {
            OnCurrentUser = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup();
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            new PollingBackoff(clock.Read));

        await dashboard.RefreshAndMonitorAsync();
        await dashboard.RefreshAndMonitorAsync();

        Assert.Equal(0, codex.CheckCallCount);

        clock.Advance(TimeSpan.FromMinutes(1));
        relay.OnCurrentUser = () => new RelayUser { Email = "a@b.com", Username = "ann" };
        await dashboard.RefreshAndMonitorAsync();

        Assert.Equal(1, codex.CheckCallCount);
    }

    [Fact]
    public async Task CodexMonitorRateLimitStopsRenewalAndBacksOffTheNextPoll()
    {
        var relay = new FakeRelayClient();
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", clock.Read);
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup
        {
            OnCheck = () => throw new RelayApiException(RelayFailure.RateLimited, "slow down"),
        };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            new PollingBackoff(clock.Read));

        await dashboard.RefreshAndMonitorAsync();
        await dashboard.RefreshAndMonitorAsync();

        Assert.Equal(1, codex.CheckCallCount);
        Assert.Equal(0, codex.RenewCallCount);
        Assert.True(dashboard.IsRateLimited);
        Assert.Contains("请求频繁", dashboard.RefreshMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnOverlappingPollDoesNotRunCodexMonitoringOnItsOwn()
    {
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var allowResponse = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var relay = new FakeRelayClient
        {
            OnCurrentUserAsync = async cancellationToken =>
            {
                entered.SetResult();
                await allowResponse.Task.WaitAsync(cancellationToken);
                return new RelayUser { Email = "a@b.com", Username = "ann" };
            },
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup();
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex);

        Task firstPoll = dashboard.RefreshAndMonitorAsync();
        await entered.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Task overlappingPoll = dashboard.RefreshAndMonitorAsync();
        try
        {
            await overlappingPoll.WaitAsync(TimeSpan.FromSeconds(5));
            Assert.Equal(0, codex.CheckCallCount);
        }
        finally
        {
            allowResponse.TrySetResult();
            await firstPoll;
        }

        Assert.Equal(1, codex.CheckCallCount);
    }
}

/// <summary>A preference store the test can read back.</summary>
internal sealed class FakeGroupPreferenceStore : IGroupPreferenceStore
{
    public long? Saved { get; private set; }

    public long? Load() => Saved;

    public void Save(long groupId) => Saved = groupId;
}
