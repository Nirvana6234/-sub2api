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

        await dashboard.ClaudePreference.LoadAsync();

        Assert.Equal(ClaudePreferenceViewModel.ClaudeThinkingLevels[2], dashboard.ClaudePreference.SelectedClaudeThinkingLevel);
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

        await dashboard.ClaudePreference.LoadAsync();

        Assert.Equal(ClaudePreferenceViewModel.ClaudeThinkingLevels[0], dashboard.ClaudePreference.SelectedClaudeThinkingLevel);
    }

    [Fact]
    public async Task AFailedUsageCardLeavesTheAccountCardIntact()
    {
        // The heart of F4.2: cards fail alone. A shared try/catch or a combined
        // await would take all of them down together.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.Account.AccountReady);
        Assert.False(dashboard.Usage.UsageReady);
        Assert.True(dashboard.Usage.UsageUnavailable);
        Assert.True(dashboard.GroupsReady);
    }

    [Fact]
    public async Task ACardReturningUnauthorizedDoesNotSignTheUserOutByItself()
    {
        // F4.2 forbids a card failure from directly logging anyone out. The card
        // does report the rejected token (so a genuinely revoked session is still
        // found — see the next test), but that goes through a forced renewal, and
        // here the renewal succeeds (the fake's default), so the session survives.
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");

        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.Unauthenticated, "过期");

        await dashboard.RefreshAsync();

        Assert.True(session.IsSignedIn);
        Assert.False(dashboard.Usage.UsageReady);
        Assert.Equal(1, relay.RefreshCallCount);
    }

    [Fact]
    public async Task ACardReturningUnauthorizedEndsTheSessionWhenTheTokenWasGenuinelyRevoked()
    {
        // The gap this closes: session-binding (or an admin kick, or a password
        // change) can revoke a token before the client's own clock thinks it is due
        // for renewal — GetAccessTokenAsync would then keep handing out a token the
        // server already rejects, and every card would grey out forever with the
        // client still sitting on the signed-in screen. A card's 401 has to be able
        // to prompt the renewal check that finds this out.
        (DashboardViewModel dashboard, FakeRelayClient relay, _, RelaySessionManager session, _) = Build();
        await session.SignInAsync("a@b.com", "pw");

        relay.OnDashboardStats = () => throw new RelayApiException(RelayFailure.Unauthenticated, "过期");
        relay.OnRefresh = () => throw new RelayApiException(RelayFailure.Unauthenticated, "会话已失效");

        await dashboard.RefreshAsync();

        Assert.False(session.IsSignedIn);
        Assert.Equal(SignOutReason.SessionExpired, session.LastSignOutReason);
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
        Assert.Equal("1 Token 计价相当于官方标准计价的 1.5 倍", Assert.Single(dashboard.Groups).RateDescription);
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
        Assert.Equal("1 Token 计价相当于官方标准计价的 0.8 倍", item.RateDescription);
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
    public async Task UnderTheLocalTransportTheSwitchIsPushedToTheRelayImmediately()
    {
        // Under the loopback relay there is no server-side key to re-point, so this
        // used to fall into the "no managed key" branch above and promise the user a
        // switch that nothing ever applied — traffic kept being billed to the group
        // that was selected when Codex launched.
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var preferences = new FakeGroupPreferenceStore();
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), codex);
        await session.SignInAsync("a@b.com", "pw");

        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () => [];
        relay.OnUpdateKeyGroup = _ => throw new InvalidOperationException("the local transport has no key to update");

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.Equal(12, codex.ActiveGroups.LastOrDefault());
        Assert.Equal(12, preferences.Saved);
        Assert.Equal("乙", dashboard.CurrentGroupName);
        Assert.Equal("已切换到 乙。", dashboard.GroupMessage);
    }

    [Fact]
    public async Task ALeftoverManagedKeyDoesNotDragTheGroupBackOnTheNextRefresh()
    {
        // An account can still have a managed key on the server — left over from
        // before this installation moved to the loopback relay, or created for some
        // other reason — even while running under the local transport. The switch
        // does record the new group onto that key (so another installation can
        // bootstrap from it — see the test below), but once this installation has
        // its own local preference, LoadGroupCardAsync must not read the key back
        // as anything more than that: it used to prefer the server value outright,
        // so the ~60s poll that runs after every switch would silently drag the
        // active group back to whatever the key said.
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var preferences = new FakeGroupPreferenceStore();
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay, session, preferences, new ManagedKeyNaming(new FixedInstallId("testinst")), codex);
        await session.SignInAsync("a@b.com", "pw");

        relay.OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")];
        relay.OnListKeys = () =>
        [
            new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "abc", GroupId = 11 },
        ];

        await dashboard.RefreshAsync();
        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));
        Assert.Equal(12, dashboard.SelectedGroup!.Id);

        // The poll that runs on the same cadence as MonitorCodexAsync after the switch.
        await dashboard.RefreshAsync();

        Assert.Equal(12, dashboard.SelectedGroup!.Id);
        Assert.Equal("乙", dashboard.CurrentGroupName);
        Assert.Equal(12, codex.ActiveGroups.LastOrDefault());
    }

    [Fact]
    public async Task ASwitchRecordsTheGroupSoAnotherInstallationBootstrapsFromIt()
    {
        // The server-side record exists purely for a client with no opinion of its
        // own yet — a reinstall on this machine (ManagedKeyNaming adopts an earlier
        // install's key as an orphan), or the very first launch. It must never be
        // read back once the reading installation already has a local preference
        // (that is the test above); here it is the only thing a fresh one has to
        // go on.
        long serverGroupId = 11;
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "甲"), Group(12, "乙")],
            OnListKeys = () =>
            [
                new RelayApiKey { Id = 5, Name = ManagedKeyNaming.MachinePrefix() + "old-install", GroupId = serverGroupId },
            ],
            OnUpdateKeyGroup = groupId =>
            {
                serverGroupId = groupId;
                return new RelayApiKey { Id = 5, GroupId = groupId };
            },
        };

        var sessionA = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var dashboardA = new DashboardViewModel(
            relay,
            sessionA,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("old-install")),
            new FakeCodexStartup { UsesLocalTransport = true });
        await sessionA.SignInAsync("a@b.com", "pw");
        await dashboardA.RefreshAsync();
        await dashboardA.SwitchGroupAsync(dashboardA.Groups.Single(g => g.Id == 12));

        Assert.Equal(12, serverGroupId);

        // A second installation on the same machine — a reinstall — with no local
        // preference of its own.
        var sessionB = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var dashboardB = new DashboardViewModel(
            relay,
            sessionB,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("new-install")),
            new FakeCodexStartup { UsesLocalTransport = true });
        await sessionB.SignInAsync("a@b.com", "pw");
        await dashboardB.RefreshAsync();

        Assert.Equal(12, dashboardB.SelectedGroup!.Id);
        Assert.Equal("乙", dashboardB.CurrentGroupName);
    }

    [Fact]
    public async Task ChoosingAutomaticGroupOpensPolicyAndSavesBeforeChangingTheRelay()
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex);
        await session.SignInAsync("a@b.com", "pw");
        relay.OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙"), Group(13, "Claude", platform: "anthropic")];
        relay.OnListKeys = () => [];
        dashboard.ConfigureAutoGroup = (settings, candidates) =>
        {
            Assert.False(settings.AutoGroup);
            Assert.Equal([11L, 12L], candidates.Select(candidate => candidate.Id));
            Assert.DoesNotContain(candidates, candidate => candidate.Id == 13);
            Assert.DoesNotContain(null, codex.ActiveGroups);
            return Task.FromResult<PawAutoGroupSettings?>(new PawAutoGroupSettings(true, [12], "speed"));
        };

        await dashboard.RefreshAsync();
        GroupItemViewModel automatic = Assert.Single(dashboard.Groups, group => group.IsAutomatic);
        await dashboard.SwitchGroupAsync(automatic);

        Assert.True(relay.LastSavedPawAutoGroup?.AutoGroup);
        Assert.Equal([12L], relay.LastSavedPawAutoGroup?.AutoGroupIds);
        Assert.Equal("speed", relay.LastSavedPawAutoGroup?.AutoGroupStrategy);
        Assert.Same(automatic, dashboard.SelectedGroup);
        Assert.Null(codex.ActiveGroups.Last());
        Assert.Equal("已启用自动分组。", dashboard.GroupMessage);
        Assert.Equal(2, relay.PawAutoGroupCallCount);
    }

    /// <summary>
    /// Leaving automatic routing must not write auto_group=false to the server.
    /// </summary>
    /// <remarks>
    /// That write used to look harmless and broke two things silently: the server
    /// stops returning the candidate list once the flag is off (hydrateAutoGroupIDs
    /// short-circuits), so the dialog reopened empty and the user had to re-tick
    /// every group; and the web Playground drops this key, because it requires
    /// auto_group or a group_id and this key never has a group_id of its own.
    /// </remarks>
    [Fact]
    public async Task LeavingAutomaticRoutingIsLocalAndNeverDisablesTheServerFlag()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var preferences = new FakeGroupPreferenceStore();
        var dashboard = new DashboardViewModel(
            relay,
            session,
            preferences,
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (_, _) => Task.FromResult<PawAutoGroupSettings?>(
                new PawAutoGroupSettings(true, [11, 12], "price")),
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(group => group.IsAutomatic));
        Assert.True(preferences.SavedAutomatic);
        int savesWhileEnabling = relay.PawAutoGroupSaveCallCount;

        GroupItemViewModel fixedGroup = dashboard.Groups.First(group => !group.IsAutomatic);
        await dashboard.SwitchGroupAsync(fixedGroup);

        // The mode moved, but only locally.
        Assert.Same(fixedGroup, dashboard.SelectedGroup);
        Assert.False(preferences.SavedAutomatic);
        Assert.Equal(savesWhileEnabling, relay.PawAutoGroupSaveCallCount);
        Assert.True(relay.LastSavedPawAutoGroup?.AutoGroup);
        Assert.Equal([11L, 12L], relay.LastSavedPawAutoGroup?.AutoGroupIds);
    }

    /// <summary>The mode survives a restart, because the server flag can no longer carry it.</summary>
    [Fact]
    public async Task AutomaticRoutingIsRestoredFromLocalPreferencesOnTheNextRefresh()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [11, 12], "price"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var preferences = new FakeGroupPreferenceStore { SavedAutomatic = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            preferences,
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex);
        await session.SignInAsync("a@b.com", "pw");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.SelectedGroup?.IsAutomatic);
        Assert.Null(codex.ActiveGroups.Last());
        Assert.True(dashboard.CanStartCodex);
    }

    /// <summary>
    /// Choosing 自动分组 asks only while nothing is configured.
    /// </summary>
    /// <remarks>
    /// The dialog used to open on every switch, which turned a routine mode change
    /// into a form. With candidates already saved it must just turn routing on —
    /// using exactly what is saved, not silently replacing it.
    /// </remarks>
    [Fact]
    public async Task ChoosingAutomaticGroupSkipsTheDialogWhenCandidatesAreAlreadyConfigured()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [12], "speed"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        int dialogOpened = 0;
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (_, _) =>
            {
                dialogOpened++;
                return Task.FromResult<PawAutoGroupSettings?>(null);
            },
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(group => group.IsAutomatic));

        Assert.Equal(0, dialogOpened);
        Assert.True(dashboard.SelectedGroup?.IsAutomatic);
        Assert.Null(codex.ActiveGroups.Last());
        Assert.Equal([12L], relay.LastSavedPawAutoGroup?.AutoGroupIds);
        Assert.Equal("speed", relay.LastSavedPawAutoGroup?.AutoGroupStrategy);
    }

    /// <summary>
    /// Candidates the account can no longer use do not count as "configured".
    /// </summary>
    /// <remarks>
    /// A saved list that points only at groups since removed would otherwise skip the
    /// dialog and then be pruned to nothing, leaving the user with no way to pick.
    /// </remarks>
    [Fact]
    public async Task StaleCandidatesStillCountAsNothingConfigured()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲")],
            OnListKeys = () => [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [999], "price"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        int dialogOpened = 0;
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (_, _) =>
            {
                dialogOpened++;
                return Task.FromResult<PawAutoGroupSettings?>(new PawAutoGroupSettings(true, [11], "price"));
            },
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(group => group.IsAutomatic));

        Assert.Equal(1, dialogOpened);
        Assert.Equal([11L], relay.LastSavedPawAutoGroup?.AutoGroupIds);
    }

    /// <summary>The 配置 button edits the saved settings without moving the client between modes.</summary>
    [Fact]
    public async Task TheConfigureButtonSavesSettingsWithoutChangingTheMode()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [11], "price"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var preferences = new FakeGroupPreferenceStore();
        var dashboard = new DashboardViewModel(
            relay,
            session,
            preferences,
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (settings, candidates) =>
            {
                Assert.Equal([11L], settings.AutoGroupIds);
                Assert.Equal([11L, 12L], candidates.Select(candidate => candidate.Id));
                return Task.FromResult<PawAutoGroupSettings?>(new PawAutoGroupSettings(true, [11, 12], "balanced"));
            },
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        GroupItemViewModel fixedGroup = dashboard.SelectedGroup!;
        Assert.False(fixedGroup.IsAutomatic);
        Assert.True(dashboard.CanConfigureAutoGroup);

        await dashboard.ConfigureAutoGroupAsync();

        Assert.Equal([11L, 12L], relay.LastSavedPawAutoGroup?.AutoGroupIds);
        Assert.Equal("balanced", relay.LastSavedPawAutoGroup?.AutoGroupStrategy);
        Assert.Same(fixedGroup, dashboard.SelectedGroup);
        Assert.False(preferences.SavedAutomatic);
        Assert.Contains("选择「自动分组」", dashboard.GroupMessage);
    }

    /// <summary>
    /// The button only edits the automatic candidate set, which a fixed group has none
    /// of — it must disappear the moment the selection leaves 自动分组, even though the
    /// feature itself (<see cref="DashboardViewModel.CanConfigureAutoGroup"/>) stays
    /// available so a fixed-group caller (a tray menu, this suite) can still reach it.
    /// </summary>
    [Fact]
    public async Task TheConfigureButtonIsShownOnlyWhileAutomaticRoutingIsSelected()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
            OnPawAutoGroup = () => new PawAutoGroupSettings(true, [11], "price"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (settings, candidates) =>
                Task.FromResult<PawAutoGroupSettings?>(new PawAutoGroupSettings(true, [11], "price")),
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();

        Assert.True(dashboard.CanConfigureAutoGroup);
        Assert.False(dashboard.SelectedGroup!.IsAutomatic);
        Assert.False(dashboard.ShowConfigureAutoGroupButton);

        GroupItemViewModel automatic = Assert.Single(dashboard.Groups, g => g.IsAutomatic);
        await dashboard.SwitchGroupAsync(automatic);

        Assert.True(dashboard.ShowConfigureAutoGroupButton);

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(g => g.Id == 12));

        Assert.False(dashboard.ShowConfigureAutoGroupButton);
    }

    [Fact]
    public async Task TheConfigureButtonIsHiddenWhenThereIsNothingToRouteBetween()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(13, "Claude", platform: "anthropic")],
            OnListKeys = () => [],
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            new FakeCodexStartup { UsesLocalTransport = true });
        await session.SignInAsync("a@b.com", "pw");

        await dashboard.RefreshAsync();

        Assert.False(dashboard.CanConfigureAutoGroup);
        Assert.DoesNotContain(dashboard.Groups, group => group.IsAutomatic);
    }

    [Fact]
    public async Task AutomaticGroupSaveFailureKeepsThePreviousFixedGroup()
    {
        var relay = new FakeRelayClient
        {
            OnAvailableGroups = () => [Group(11, "OpenAI 甲"), Group(12, "OpenAI 乙")],
            OnListKeys = () => [],
            OnSavePawAutoGroup = _ => throw new RelayApiException(RelayFailure.ServerError, "保存失败"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex)
        {
            ConfigureAutoGroup = (_, _) => Task.FromResult<PawAutoGroupSettings?>(
                new PawAutoGroupSettings(true, [11], "balanced")),
        };
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        GroupItemViewModel previous = dashboard.SelectedGroup!;

        await dashboard.SwitchGroupAsync(dashboard.Groups.Single(group => group.IsAutomatic));

        Assert.Same(previous, dashboard.SelectedGroup);
        Assert.True(previous.IsCurrent);
        Assert.DoesNotContain(null, codex.ActiveGroups);
        Assert.Equal("服务器暂时出了点问题，请稍后重试。", dashboard.GroupMessage);
    }

    [Fact]
    public async Task TheStartButtonWaitsForTheGroupListInsteadOfBlamingTheUser()
    {
        // Replays a real session: the button was live the moment the dashboard
        // appeared, so 启动 pressed a few seconds in reached the relay with no group
        // and was refused with "请先选择一个分组" — for a group the client picks
        // itself and which landed a second later. The account had one all along.
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            contextFilterPreferences: new FakeContextFilterPreferenceStore());
        await session.SignInAsync("a@b.com", "pw");

        // Before the group list has loaded.
        Assert.False(dashboard.CanStartCodex);
        Assert.Equal("正在加载分组…", dashboard.StartCodexLabel);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));
        Assert.Equal(0, codex.RunCount);
        Assert.Contains("稍候", dashboard.CodexMessage, StringComparison.Ordinal);

        // Once it lands, the group is selected for the user and the button opens up.
        relay.OnAvailableGroups = () => [Group(2, "老号分组")];
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();

        Assert.True(dashboard.CanStartCodex);
        Assert.Equal("启动 ChatGPT", dashboard.StartCodexLabel);

        await dashboard.StartCodexAsync(_ => Task.FromResult(false));
        Assert.Equal(1, codex.RunCount);
    }

    [Fact]
    public async Task AnAccountWithNoCodexGroupSaysSoRatherThanLookingLikeItIsLoading()
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            contextFilterPreferences: new FakeContextFilterPreferenceStore());
        await session.SignInAsync("a@b.com", "pw");

        // Loaded successfully, and there is genuinely nothing Codex can bill to.
        relay.OnAvailableGroups = () => [];
        relay.OnListKeys = () => [];
        await dashboard.RefreshAsync();

        Assert.True(dashboard.GroupsReady);
        Assert.False(dashboard.CanStartCodex);
        Assert.Equal("没有可用分组", dashboard.StartCodexLabel);
    }

    // ---- 启用上下文压缩 -------------------------------------------------------

    private static DashboardViewModel BuildForContextFilter(
        FakeCodexStartup codex,
        FakeContextFilterPreferenceStore preferences,
        SafeAsyncRunner? safeAsync = null,
        IContextFilterUsageStore? usage = null)
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        return new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("testinst")),
            codex,
            safeAsync: safeAsync,
            contextFilterPreferences: preferences,
            contextFilterUsage: usage);
    }

    [Fact]
    public void TheCompressionSwitchDefaultsToOnAndIsRestoredFromDisk()
    {
        Assert.True(BuildForContextFilter(new FakeCodexStartup(), new FakeContextFilterPreferenceStore()).ContextFilterEnabled);
        Assert.False(BuildForContextFilter(new FakeCodexStartup(), new FakeContextFilterPreferenceStore(false)).ContextFilterEnabled);
        Assert.True(BuildForContextFilter(new FakeCodexStartup(), new FakeContextFilterPreferenceStore(true)).ContextFilterEnabled);
    }

    [Fact]
    public void RestoringTheSavedValueIsNotTreatedAsTheUserChangingIt()
    {
        // Otherwise every launch writes the file straight back and asks the startup to
        // restart a filter process that is not even running yet.
        var codex = new FakeCodexStartup();
        var preferences = new FakeContextFilterPreferenceStore(false);

        DashboardViewModel dashboard = BuildForContextFilter(codex, preferences);

        Assert.False(dashboard.ContextFilterEnabled);
        Assert.Equal(0, preferences.SaveCount);
        Assert.Empty(codex.ContextFilterStates);
    }

    [Fact]
    public void TogglingCompressionPersistsItAndAppliesItWithoutWaitingForTheNextLaunch()
    {
        var codex = new FakeCodexStartup();
        var preferences = new FakeContextFilterPreferenceStore(true);
        DashboardViewModel dashboard = BuildForContextFilter(codex, preferences);

        dashboard.ContextFilterEnabled = false;

        Assert.Equal(false, preferences.Saved);
        Assert.Equal([false], codex.ContextFilterStates);

        dashboard.ContextFilterEnabled = true;

        Assert.Equal(true, preferences.Saved);
        Assert.Equal([false, true], codex.ContextFilterStates);
    }

    [Fact]
    public async Task AFailedSwitchPutsTheCheckboxBackWhereTheChainActuallyIs()
    {
        // A box left showing a setting that did not take is worse than the failure
        // itself: the user has no way to tell the two apart.
        var reported = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var safeAsync = new SafeAsyncRunner(report: _ =>
        {
            reported.TrySetResult();
            return Task.CompletedTask;
        });
        var codex = new FakeCodexStartup { OnSetContextFilter = new InvalidOperationException("filter will not restart") };
        DashboardViewModel dashboard = BuildForContextFilter(codex, new FakeContextFilterPreferenceStore(true), safeAsync);

        dashboard.ContextFilterEnabled = false;
        await reported.Task.WaitAsync(TimeSpan.FromSeconds(5));

        Assert.True(dashboard.ContextFilterEnabled);
    }

    [Fact]
    public void TheUsageLineStartsAsNoDataWhenNothingHasBeenRecorded()
    {
        DashboardViewModel dashboard = BuildForContextFilter(
            new FakeCodexStartup(), new FakeContextFilterPreferenceStore(), usage: new FakeContextFilterUsageStore());

        Assert.Equal("尚无压缩数据", dashboard.ContextFilterUsageText);
    }

    [Fact]
    public void TheUsageLineReflectsWhatWasAlreadyOnDiskAtConstruction()
    {
        // Loaded synchronously in the constructor (mirrors how ContextFilterEnabled
        // itself is restored) so the number is not blank on first paint while the
        // dashboard waits for its first poll.
        var usage = new FakeContextFilterUsageStore(new ContextFilterUsage(40_000, 10_000));

        DashboardViewModel dashboard = BuildForContextFilter(
            new FakeCodexStartup(), new FakeContextFilterPreferenceStore(), usage: usage);

        Assert.Contains("25.0%", dashboard.ContextFilterUsageText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task TheUsageLineAdvancesOnTheNextCodexPoll()
    {
        // There is no live event out of the transport layer (see
        // LocalPawRelay._onCompressionMeasured) — the dashboard has to notice new
        // usage the same way it notices everything else about Codex, on the next
        // MonitorCodexAsync tick, not the instant a request completes.
        var usage = new FakeContextFilterUsageStore();
        DashboardViewModel dashboard = BuildForContextFilter(
            new FakeCodexStartup(), new FakeContextFilterPreferenceStore(), usage: usage);
        Assert.Equal("尚无压缩数据", dashboard.ContextFilterUsageText);

        usage.Add(4000, 1000);
        await dashboard.MonitorCodexAsync();

        Assert.Contains("25.0%", dashboard.ContextFilterUsageText, StringComparison.Ordinal);
    }

    [Fact]
    public void WithNoBundledFilterTheSwitchIsGreyedOut()
    {
        Assert.False(BuildForContextFilter(
            new FakeCodexStartup { HasContextFilter = false },
            new FakeContextFilterPreferenceStore()).CanToggleContextFilter);

        Assert.True(BuildForContextFilter(
            new FakeCodexStartup { HasContextFilter = true },
            new FakeContextFilterPreferenceStore()).CanToggleContextFilter);
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

        Assert.True(dashboard.Account.BalanceIsLow);
        Assert.True(dashboard.Account.CanRecharge);
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

        Assert.False(dashboard.Account.BalanceIsLow);
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

        Assert.False(dashboard.Account.BalanceIsLow);
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

        Assert.True(dashboard.Account.AccountReady);
        Assert.False(dashboard.Usage.UsageReady);
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
        Assert.Equal("￥42", dashboard.Account.BalanceText);

        dashboard.Reset();

        Assert.Equal("—", dashboard.Account.BalanceText);
        Assert.Equal("—", dashboard.Usage.TodayRequestsText);
        Assert.Empty(dashboard.Account.UserDisplayName);
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

        Assert.Equal("￥7", dashboard.Account.BalanceText);
        Assert.True(dashboard.Account.AccountReady);
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

        // The standalone repair button stays live even here — it exists precisely
        // for states MonitorCodexAsync's own detection has not (yet) flagged, so
        // gating it on the same "already running" condition would defeat its point.
        Assert.True(dashboard.CanRepairCodexStartup);
    }

    [Fact]
    public void TheRepairButtonIsDisabledOnlyWhileNotInstalledOrMidOperation()
    {
        var codex = new FakeCodexStartup { IsInstalled = false };
        DashboardViewModel dashboard = BuildWith(codex);

        Assert.True(dashboard.CanRepairCodexStartup);

        dashboard.CodexNotInstalled = true;
        Assert.False(dashboard.CanRepairCodexStartup);

        dashboard.CodexNotInstalled = false;
        dashboard.IsInstallingCodex = true;
        Assert.False(dashboard.CanRepairCodexStartup);

        dashboard.IsInstallingCodex = false;
        dashboard.IsStartingCodex = true;
        Assert.False(dashboard.CanRepairCodexStartup);

        dashboard.IsStartingCodex = false;
        Assert.True(dashboard.CanRepairCodexStartup);
    }

    // ---- Starting for the phone: a start, never a restart or an install ------------------

    [Fact]
    public async Task ThePhoneLeavesARunningChatGptAlone()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        DashboardViewModel dashboard = BuildWith(codex);

        LanAi.RelayClient.DesktopSync.DesktopStartResult result = await dashboard.StartCodexForPhoneAsync();

        Assert.Equal(LanAi.RelayClient.DesktopSync.DesktopStartOutcome.AlreadyRunning, result.Outcome);
        Assert.Equal(0, codex.RunCount);
    }

    [Fact]
    public async Task ThePhoneNeverInstallsChatGpt()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(false, false, null) };
        DashboardViewModel dashboard = BuildWith(codex);

        LanAi.RelayClient.DesktopSync.DesktopStartResult result = await dashboard.StartCodexForPhoneAsync();

        Assert.Equal(LanAi.RelayClient.DesktopSync.DesktopStartOutcome.Refused, result.Outcome);
        Assert.Contains("安装", result.Message);
        Assert.Equal(0, codex.RunCount);
        Assert.False(dashboard.IsInstallingCodex);
    }

    [Fact]
    public async Task ThePhoneStartsAChatGptThatIsNotRunning()
    {
        var codex = new FakeCodexStartup();
        DashboardViewModel dashboard = BuildWith(codex);

        LanAi.RelayClient.DesktopSync.DesktopStartResult result = await dashboard.StartCodexForPhoneAsync();

        Assert.Equal(LanAi.RelayClient.DesktopSync.DesktopStartOutcome.Starting, result.Outcome);
        Assert.Equal(1, codex.RunCount);
        Assert.False(codex.LastAllowRestart);
    }

    // ---- 远程修复 from the phone: the repair button, without the dialog -------------------

    /// <summary>Restarts, and keeps the key: replacing it is the computer's button's call.</summary>
    [Fact]
    public async Task ARemoteRepairRestartsAndKeepsTheKey()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(true, true, null) };
        DashboardViewModel dashboard = BuildWith(codex);

        string? failure = await dashboard.RepairCodexForPhoneAsync();

        Assert.Null(failure);
        Assert.Equal(1, codex.RunCount);
        Assert.True(codex.LastAllowRestart);
        Assert.False(codex.LastForceNewKey);
    }

    [Fact]
    public async Task ARemoteRepairNeverInstalls()
    {
        var codex = new FakeCodexStartup { OnCheck = () => new CodexHealth(false, false, null) };
        DashboardViewModel dashboard = BuildWith(codex);

        string? failure = await dashboard.RepairCodexForPhoneAsync();

        Assert.Contains("安装", failure);
        Assert.Equal(0, codex.RunCount);
    }

    /// <summary>A forced restart would get through an install in progress; the repair must not.</summary>
    [Fact]
    public async Task ARemoteRepairWaitsForAnInstallOrStart()
    {
        var codex = new FakeCodexStartup();
        DashboardViewModel dashboard = BuildWith(codex);

        dashboard.IsInstallingCodex = true;
        string? whileInstalling = await dashboard.RepairCodexForPhoneAsync();
        dashboard.IsInstallingCodex = false;
        dashboard.IsStartingCodex = true;
        string? whileStarting = await dashboard.RepairCodexForPhoneAsync();

        Assert.NotNull(whileInstalling);
        Assert.NotNull(whileStarting);
        Assert.Equal(0, codex.RunCount);
    }

    /// <summary>Launched but slow to answer: still starting, so the messages waiting for it are kept.</summary>
    [Fact]
    public async Task AChatGptThatIsSlowToAnswerIsStillStarting()
    {
        int checks = 0;
        var codex = new FakeCodexStartup
        {
            OnCheck = () => new CodexHealth(true, checks++ > 0, null),
            OnRun = (_, _) => new CodexStartupResult(CodexStartupStatus.CodexUnresponsive, "ChatGPT 没有响应"),
        };
        DashboardViewModel dashboard = BuildWith(codex);

        LanAi.RelayClient.DesktopSync.DesktopStartResult result = await dashboard.StartCodexForPhoneAsync();

        Assert.Equal(LanAi.RelayClient.DesktopSync.DesktopStartOutcome.Starting, result.Outcome);
    }

    /// <summary>A start that turns out to need a restart is declined, and the phone told why.</summary>
    [Fact]
    public async Task ThePhoneNeverAgreesToARestart()
    {
        var codex = new FakeCodexStartup
        {
            OnRun = (_, allowRestart) => allowRestart
                ? new CodexStartupResult(CodexStartupStatus.Ready, "好了")
                : new CodexStartupResult(CodexStartupStatus.NeedsRestartConfirmation, "需要重启"),
        };
        DashboardViewModel dashboard = BuildWith(codex);

        LanAi.RelayClient.DesktopSync.DesktopStartResult result = await dashboard.StartCodexForPhoneAsync();

        Assert.Equal(LanAi.RelayClient.DesktopSync.DesktopStartOutcome.Refused, result.Outcome);
        Assert.Equal("需要重启", result.Message);
        Assert.Equal(1, codex.RunCount);
        Assert.False(codex.LastAllowRestart);
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
        dashboard.ClaudePreference.SelectedClaudeModel = "claude-opus-5";

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

        Assert.True(dashboard.Usage.TrendReady);
        Assert.Equal(2, dashboard.Usage.CostTrend.Count);
        Assert.Equal(2.25, dashboard.Usage.CostTrend[1].Value);

        // Labelled by day only: seven full dates do not fit under a chart this
        // narrow, and the year is never in question.
        Assert.Equal("31", dashboard.Usage.CostTrend[1].Label);
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

        Assert.Equal(["big", "mid", "small"], dashboard.Usage.TopModelUsage.Select(m => m.Model));
        Assert.Equal("$9", dashboard.Usage.TopModelUsage[0].CostText);
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

        Assert.Equal(5, dashboard.Usage.TopModelUsage.Count);
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

        Assert.True(dashboard.Usage.TrendReady);
        Assert.Single(dashboard.Usage.CostTrend);
        Assert.Empty(dashboard.Usage.TopModelUsage);
    }

    [Fact]
    public async Task AnAccountWithNoTrafficIsToldSoRatherThanShownABlankCard()
    {
        // An empty card and a broken one look identical to a novice; the group
        // dropdown taught us that once already.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => [];

        await dashboard.RefreshAsync();

        Assert.True(dashboard.Usage.TrendReady);
        Assert.False(dashboard.Usage.HasTrend);
        Assert.True(dashboard.Usage.HasNoUsageYet);
    }

    [Fact]
    public async Task AFailedTrendSaysUnavailableRatherThanEmpty()
    {
        // The two states must not be confused: "nothing yet" invites patience,
        // "cannot fetch" invites a retry.
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.True(dashboard.Usage.TrendUnavailable);
        Assert.False(dashboard.Usage.HasNoUsageYet);
    }

    [Fact]
    public async Task AFailedTrendLeavesTheOtherCardsAlone()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnUsageTrend = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.False(dashboard.Usage.TrendReady);
        Assert.True(dashboard.Usage.TrendUnavailable);
        Assert.True(dashboard.Account.AccountReady);
        Assert.True(dashboard.Usage.UsageReady);
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

        Assert.True(dashboard.Account.SubscriptionReady);
        Assert.True(dashboard.Account.HasSubscription);
        Assert.Equal("专业订阅", dashboard.Account.SubscriptionName);
        Assert.Contains("$5 / $10", dashboard.Account.SubscriptionProgressText, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnAccountWithoutSubscriptionsHidesTheSubscriptionCard()
    {
        (DashboardViewModel dashboard, _, _) = await SignedInAsync();

        await dashboard.RefreshAsync();

        Assert.True(dashboard.Account.SubscriptionReady);
        Assert.False(dashboard.Account.HasSubscription);
    }

    [Fact]
    public async Task ASubscriptionFailureDoesNotTakeDownTheOtherCards()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnSubscriptionSummary = () => throw new RelayApiException(RelayFailure.ServerError, "boom");

        await dashboard.RefreshAsync();

        Assert.False(dashboard.Account.SubscriptionReady);
        Assert.False(dashboard.Account.HasSubscription);
        Assert.True(dashboard.Account.AccountReady);
        Assert.True(dashboard.Usage.UsageReady);
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
        Assert.True(dashboard.Account.AccountUnavailable);
        Assert.True(dashboard.Usage.UsageUnavailable);
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
        Assert.True(dashboard.RefreshState.IsRateLimited);
        Assert.Contains("请求频繁", dashboard.RefreshState.RefreshMessage, StringComparison.Ordinal);

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
        Assert.True(dashboard.RefreshState.IsRateLimited);
        Assert.Contains("请求频繁", dashboard.RefreshState.RefreshMessage, StringComparison.Ordinal);
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

    [Fact]
    public async Task TheCatalogKeepsEveryGroupWhileEachToolSeesOnlyItsOwn()
    {
        (DashboardViewModel dashboard, FakeRelayClient relay, _) = await SignedInAsync();
        relay.OnAvailableGroups = () =>
        [
            Group(11, "GPT", platform: "openai"),
            Group(21, "Claude", platform: "anthropic"),
            Group(31, "Kimi", platform: "kimi"),
        ];

        await dashboard.RefreshAsync();

        // A later Kimi page reads the catalog; a list derived from Codex's would never
        // have the Kimi group in it.
        Assert.Equal([11L, 21L, 31L], dashboard.Catalog.Groups.Select(g => g.Id));
        Assert.DoesNotContain(dashboard.Groups, g => g.Id == 31);
        Assert.Equal([21L], dashboard.ClaudeCode.ClaudePluginGroups.Select(g => g.Id));

        dashboard.Reset();

        Assert.Empty(dashboard.Catalog.Groups);
    }
}

/// <summary>A preference store the test can read back.</summary>
internal sealed class FakeGroupPreferenceStore : IGroupPreferenceStore
{
    public long? Saved { get; private set; }

    public bool SavedAutomatic { get; set; }

    public long? SavedClaudeGroup { get; private set; }

    public long? Load() => Saved;

    public void Save(long groupId) => Saved = groupId;

    public bool LoadAutomatic() => SavedAutomatic;

    public void SaveAutomatic(bool automatic) => SavedAutomatic = automatic;

    public long? LoadClaudeGroup() => SavedClaudeGroup;

    public void SaveClaudeGroup(long groupId) => SavedClaudeGroup = groupId;
}
