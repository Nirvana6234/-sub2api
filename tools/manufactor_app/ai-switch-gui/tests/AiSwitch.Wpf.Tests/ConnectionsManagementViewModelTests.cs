using AiSwitchGui;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class ConnectionsManagementViewModelTests
{
    [Fact]
    public void ApplySnapshot_MarksOnlyTheActiveProfileAndProtectsOnlyFixedIds()
    {
        var editor = new RecordingEditor();
        var viewModel = new ConnectionsViewModel(() => Task.CompletedTask, editor);
        var local = CreateProfile(ConnectionProfileIds.LocalMachine, "任意旧名称", ConnectionProfileKind.Local);
        var remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        var legacyLocal = CreateProfile("legacy-local", "旧来源", ConnectionProfileKind.Local);

        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            new[] { local, remote, legacyLocal },
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection(
                "remote-a",
                ConnectionProfileIds.LocalMachine,
                ActiveProfileId: "remote-a")));

        ConnectionCardViewModel localCard = Assert.Single(viewModel.Connections, card => card.Record.Id == local.Id);
        ConnectionCardViewModel remoteCard = Assert.Single(viewModel.Connections, card => card.Record.Id == remote.Id);
        ConnectionCardViewModel legacyCard = Assert.Single(viewModel.Connections, card => card.Record.Id == legacyLocal.Id);

        Assert.True(localCard.IsFixed);
        Assert.False(localCard.CanDelete);
        Assert.False(localCard.IsSelected);
        Assert.True(remoteCard.IsSelected);
        Assert.False(legacyCard.IsFixed);
        Assert.False(legacyCard.IsSupported);
        Assert.False(legacyCard.CanDelete);
    }

    [Fact]
    public void ExternalSourceSelection_UsesOneEditorAndClearsPreviousDeleteState()
    {
        var viewModel = new ConnectionsViewModel(() => Task.CompletedTask, new RecordingEditor());
        ConnectionProfile firstProfile = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        ConnectionProfile secondProfile = CreateProfile("remote-b", "远程 B", ConnectionProfileKind.Cloud);

        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            new[] { firstProfile, secondProfile },
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection(
                "remote-a",
                "remote-a",
                ActiveProfileId: "remote-a")));

        ConnectionCardViewModel first = Assert.Single(viewModel.Connections, card => card.Record.Id == firstProfile.Id);
        ConnectionCardViewModel second = Assert.Single(viewModel.Connections, card => card.Record.Id == secondProfile.Id);
        Assert.Same(first, viewModel.SelectedLibrarySource);
        Assert.Equal(firstProfile.Id, viewModel.ConnectionEditor?.Original?.Id);
        Assert.All(viewModel.Connections, card => Assert.False(card.IsExpanded));

        viewModel.SelectedLibrarySource = second;

        Assert.Equal(secondProfile.Id, viewModel.ConnectionEditor?.Original?.Id);
        viewModel.RequestDeleteCommand.Execute(second);
        Assert.True(second.IsDeleteConfirmationVisible);
        Assert.NotNull(viewModel.ConnectionEditor);

        viewModel.SelectedLibrarySource = first;

        Assert.False(second.IsDeleteConfirmationVisible);
        Assert.Equal(firstProfile.Id, viewModel.ConnectionEditor?.Original?.Id);

        viewModel.RequestDeleteCommand.Execute(first);
        Assert.False(first.IsDeleteConfirmationVisible);
        Assert.Contains("仍被客户端路由使用", viewModel.MutationNotice, StringComparison.Ordinal);
    }

    [Fact]
    public void Editor_EmptyPasswordsKeepExistingAndExplicitClearWins()
    {
        ConnectionProfile profile = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionEditorViewModel editor = ConnectionEditorViewModel.FromExisting(profile, isFixed: true);
        editor.Name = "不应生效";
        editor.ClearClaudeSecret = true;
        editor.SetEnteredSecret(CliKind.GeminiCli, "new-gemini-key");

        ConnectionProfileDraft draft = editor.BuildDraft();

        Assert.Equal("本机中转", draft.Name);
        Assert.Equal(ConnectionSecretChangeKind.Keep, draft.Codex.SecretChange.Kind);
        Assert.Equal(ConnectionSecretChangeKind.Clear, draft.ClaudeCode.SecretChange.Kind);
        Assert.Equal(ConnectionSecretChangeKind.Replace, draft.GeminiCli.SecretChange.Kind);
        Assert.Equal("new-gemini-key", draft.GeminiCli.SecretChange.Replacement);
    }

    [Fact]
    public void Editor_FixedProfilesExposeAndPersistTheirOwnDashboardAddresses()
    {
        ConnectionProfile local = CreateProfile(
            ConnectionProfileIds.LocalMachine,
            "本机中转",
            ConnectionProfileKind.Local) with
        {
            DashboardUrl = "http://127.0.0.1:3300/dashboard",
        };
        ConnectionProfile lan = CreateProfile(
            ConnectionProfileIds.LanDefault,
            "局域网中转",
            ConnectionProfileKind.Lan) with
        {
            DashboardUrl = "http://192.168.31.247:3300/dashboard",
        };

        ConnectionEditorViewModel localEditor = ConnectionEditorViewModel.FromExisting(local, isFixed: true);
        ConnectionEditorViewModel lanEditor = ConnectionEditorViewModel.FromExisting(lan, isFixed: true);
        localEditor.DashboardUrl = "http://127.0.0.1:3400/control";

        ConnectionProfileDraft localDraft = localEditor.BuildDraft();
        ConnectionProfileDraft lanDraft = lanEditor.BuildDraft();

        Assert.True(localEditor.SupportsDashboardAddress);
        Assert.Equal("本机后台地址", localEditor.DashboardAddressLabel);
        Assert.Equal("http://127.0.0.1:3400/control", localDraft.DashboardUrl);
        Assert.True(lanEditor.SupportsDashboardAddress);
        Assert.Equal("局域网后台地址", lanEditor.DashboardAddressLabel);
        Assert.Equal("http://192.168.31.247:3300/dashboard", lanDraft.DashboardUrl);
    }

    [Fact]
    public void Editor_LocalSub2ApiPathIsAvailableOnlyForTheFixedLocalMachineSource()
    {
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile lan = CreateProfile(ConnectionProfileIds.LanDefault, "局域网中转", ConnectionProfileKind.Lan);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);

        ConnectionEditorViewModel localEditor = ConnectionEditorViewModel.FromExisting(local, isFixed: true, @"E:\workspace\sub2api");
        ConnectionEditorViewModel lanEditor = ConnectionEditorViewModel.FromExisting(lan, isFixed: true, @"E:\workspace\sub2api");
        ConnectionEditorViewModel remoteEditor = ConnectionEditorViewModel.FromExisting(remote, isFixed: false, @"E:\workspace\sub2api");

        Assert.True(localEditor.SupportsLocalSub2ApiPath);
        Assert.Equal(@"E:\workspace\sub2api", localEditor.LocalSub2ApiPath);
        Assert.False(localEditor.IsLocalSub2ApiPathChanged);
        Assert.False(localEditor.ShowsNotesEditor);
        Assert.False(localEditor.ShowsDashboardAddressEditor);
        Assert.False(lanEditor.SupportsLocalSub2ApiPath);
        Assert.True(lanEditor.ShowsNotesEditor);
        Assert.True(lanEditor.ShowsDashboardAddressEditor);
        Assert.False(remoteEditor.SupportsLocalSub2ApiPath);
    }

    [Fact]
    public async Task SaveLocalSource_ConfiguresSelectedSub2ApiDirectoryOutsideTheProfileDraft()
    {
        var editor = new RecordingEditor();
        var gateway = new RecordingLocalGatewayController(@"E:\old-workspace");
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            editor,
            profileTransfer: null,
            localGatewayController: gateway);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        var card = new ConnectionCardViewModel(
            local,
            "本机",
            local.BaseUrl,
            "Codex",
            "本机来源",
            false,
            true,
            true,
            "已配置",
            "LocalGateway");

        viewModel.EditConnectionCommand.Execute(card);

        Assert.NotNull(viewModel.ConnectionEditor);
        Assert.Equal(@"E:\old-workspace\sub2api", viewModel.ConnectionEditor!.LocalSub2ApiPath);
        viewModel.ConnectionEditor.LocalSub2ApiPath = @"D:\new-workspace\sub2api";

        await viewModel.SaveConnectionCommand.ExecuteAsync(null);

        Assert.Equal(@"D:\new-workspace\sub2api", gateway.ConfiguredPath);
        Assert.NotNull(editor.Updated);
        Assert.DoesNotContain(
            editor.Updated!.GetType().GetProperties(),
            property => property.Name.Contains("Sub2ApiPath", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task OpenLocalDashboard_StartsStoppedGatewayAndOpensItWithoutUsingTheActiveCloudSource()
    {
        var controller = new RecordingLocalGatewayController(@"E:\workspace")
        {
            Status = new LocalGatewayStatus
            {
                NativeMode = true,
                ControlAvailable = true,
                NativeRoot = @"E:\workspace",
                WebUrl = LocalGatewayService.NativeWebUrl,
                WebReachable = false,
            },
            StartMarksWebReachable = true,
        };
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            localGatewayController: controller);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        var card = new ConnectionCardViewModel(
            local,
            "本机",
            local.BaseUrl,
            "Codex",
            "固定来源",
            true,
            true,
            false,
            "已配置",
            "LocalGateway");

        await viewModel.OpenConnectionDashboardCommand.ExecuteAsync(card);

        Assert.Equal(1, controller.StartCalls);
        Assert.Equal(1, controller.WaitForWebCalls);
        Assert.Equal(LocalGatewayService.NativeWebUrl, Assert.Single(controller.OpenedDashboardUrls));
        Assert.Equal("打开后台", card.DashboardActionLabel);
        Assert.True(card.CanOpenDashboard);
    }

    [Fact]
    public async Task OpenLocalDashboard_DisablesActionWhenNoLocalControlIsConfigured()
    {
        var controller = new RecordingLocalGatewayController(@"E:\workspace")
        {
            Status = new LocalGatewayStatus
            {
                NativeMode = true,
                ControlAvailable = false,
                NativeRoot = string.Empty,
                WebUrl = LocalGatewayService.NativeWebUrl,
                WebReachable = false,
            },
        };
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            localGatewayController: controller);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        var card = new ConnectionCardViewModel(
            local,
            "本机",
            local.BaseUrl,
            "Codex",
            "固定来源",
            true,
            true,
            false,
            "已配置",
            "LocalGateway");

        await viewModel.OpenConnectionDashboardCommand.ExecuteAsync(card);

        Assert.Equal(0, controller.StartCalls);
        Assert.Empty(controller.OpenedDashboardUrls);
        Assert.False(card.CanOpenDashboard);
        Assert.Equal("本机后台未配置", card.DashboardActionLabel);
    }

    [Fact]
    public void Editor_ImportCurrentClientConfigKeepsSecretsOutOfTheReadModelButUsesThemOnSave()
    {
        ConnectionProfile profile = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        ConnectionEditorViewModel editor = ConnectionEditorViewModel.FromExisting(profile, isFixed: false);
        editor.ImportCurrentClientConfig(new ImportedLiveConfig
        {
            Codex = new ClientProfile { BaseUrl = "https://codex.example/v1", Secret = "imported-codex-secret" },
            Claude = new ClientProfile { BaseUrl = "https://claude.example", Secret = "imported-claude-secret" },
            Gemini = new ClientProfile { BaseUrl = "https://gemini.example", Secret = "imported-gemini-secret" },
        });

        ConnectionProfileDraft draft = editor.BuildDraft();

        Assert.Equal("https://codex.example/v1", draft.Codex.BaseUrl);
        Assert.Equal(ConnectionSecretChangeKind.Replace, draft.Codex.SecretChange.Kind);
        Assert.Equal("imported-codex-secret", draft.Codex.SecretChange.Replacement);
        Assert.DoesNotContain(
            typeof(ConnectionProfile).GetProperties(),
            property => property.Name.Contains("Secret", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public void ApplySnapshot_RoutingUsesExternalSourcesOnlyAndIgnoresLocalKinds()
    {
        var coordinator = new RecordingSwitchCoordinator();
        coordinator.ActiveBackupSourceIds.Add("remote-a");
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile lan = CreateProfile(ConnectionProfileIds.LanDefault, "局域网中转", ConnectionProfileKind.Lan);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        ConnectionProfile legacy = CreateProfile("legacy-local", "旧本地", ConnectionProfileKind.Local);

        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, lan, remote, legacy],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(
                "remote-a",
                ConnectionProfileIds.LanDefault,
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine,
                [ConnectionProfileIds.LanDefault, "remote-a"])));

        Assert.Equal("remote-a", viewModel.Routing.CodexSource?.Record.Id);
        Assert.Equal("remote-a", viewModel.Routing.ClaudeCodeSource?.Record.Id);
        Assert.Equal("remote-a", viewModel.Routing.GeminiCliSource?.Record.Id);
        Assert.Equal("remote-a", viewModel.Routing.GrokCliSource?.Record.Id);
        Assert.Collection(viewModel.Routing.AvailableSources, source => Assert.Equal(remote.Id, source.Record.Id));
        Assert.Collection(viewModel.ExternalSources, source => Assert.Equal(remote.Id, source.Record.Id));
        Assert.Collection(viewModel.BackupConnections, source => Assert.Equal(remote.Id, source.Record.Id));
        Assert.Contains("Codex → 远程 A", viewModel.ActiveClientStatus, StringComparison.Ordinal);
        Assert.Contains("Claude Code → 远程 A", viewModel.ActiveClientStatus, StringComparison.Ordinal);
        Assert.Equal("正在使用 · 备用第 1 顺位", Assert.Single(viewModel.Connections, card => card.Record.Id == remote.Id).Badge);
        Assert.Equal("固定入口", Assert.Single(viewModel.Connections, card => card.Record.Id == lan.Id).Badge);
    }

    [Fact]
    public async Task SaveAndSelect_UseEditorAndRefreshWithoutLeakingSecrets()
    {
        var editor = new RecordingEditor();
        var refreshes = 0;
        var viewModel = new ConnectionsViewModel(() =>
        {
            refreshes++;
            return Task.CompletedTask;
        }, editor);

        viewModel.AddConnectionCommand.Execute(null);
        Assert.NotNull(viewModel.ConnectionEditor);
        viewModel.ConnectionEditor!.Name = "我的远程来源";
        viewModel.ConnectionEditor.CodexBaseUrl = "https://example.test/v1";
        viewModel.SetEnteredSecret(CliKind.Codex, "not-returned-to-ui");

        await viewModel.SaveConnectionCommand.ExecuteAsync(null);

        Assert.NotNull(editor.Added);
        Assert.Equal("我的远程来源", editor.Added!.Name);
        Assert.Equal(ConnectionSecretChangeKind.Replace, editor.Added.Codex.SecretChange.Kind);
        Assert.Equal(1, refreshes);
        Assert.DoesNotContain("not-returned-to-ui", viewModel.MutationNotice, StringComparison.Ordinal);

        var card = new ConnectionCardViewModel(
            CreateProfile("remote-b", "来源 B", ConnectionProfileKind.Cloud),
            "远程", "https://example.test", "Codex", "远程来源", false, true, false, "未检测到凭据", "CloudGateway");
        await viewModel.SelectConnectionCommand.ExecuteAsync(card);

        Assert.Equal((ConnectionProfileSelectionGroup.Cloud, "remote-b"), editor.Selection);
        Assert.Equal(2, refreshes);
    }

    [Fact]
    public async Task ApplyConnection_SynchronizesAllClientRoutingAfterSuccessfulSwitch()
    {
        var editor = new RecordingEditor();
        var coordinator = new RecordingSwitchCoordinator();
        var refreshes = 0;
        var viewModel = new ConnectionsViewModel(
            () =>
            {
                refreshes++;
                return Task.CompletedTask;
            },
            editor,
            coordinator);
        var card = new ConnectionCardViewModel(
            CreateProfile("cloud-source", "云端来源", ConnectionProfileKind.Cloud),
            "远程",
            "https://example.test",
            "Codex · Claude Code · Gemini CLI",
            "远程来源",
            false,
            true,
            false,
            "已检测到凭据引用",
            "CloudGateway");

        await viewModel.ApplyConnectionCommand.ExecuteAsync(card);

        Assert.Null(coordinator.AppliedProfileId);
        Assert.Null(editor.Selection);
        Assert.NotNull(editor.Routing);
        Assert.Equal(ConnectionProfileIds.LocalMachine, editor.Routing.CodexProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, editor.Routing.ClaudeCodeProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, editor.Routing.GeminiCliProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, editor.Routing.GrokCliProfileId);
        Assert.Equal(["cloud-source"], editor.Routing.BackupProfileIds);
        Assert.Equal(1, refreshes);
        Assert.Contains("加入备用上游末位", viewModel.MutationNotice, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Drag_reorder_moves_backup_source_to_the_requested_position()
    {
        var editor = new RecordingEditor();
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(() => Task.CompletedTask, editor, coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile first = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        ConnectionProfile second = CreateProfile("remote-b", "远程 B", ConnectionProfileKind.Cloud);
        ConnectionProfile third = CreateProfile("remote-c", "远程 C", ConnectionProfileKind.Cloud);
        var routing = new ConnectionProfileRouting(
            ConnectionProfileIds.LocalMachine,
            ConnectionProfileIds.LocalMachine,
            ConnectionProfileIds.LocalMachine,
            ConnectionProfileIds.LocalMachine,
            ["remote-a", "remote-b", "remote-c"]);
        await editor.SetRoutingAsync(routing);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, first, second, third],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection(null, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            routing));

        ConnectionCardViewModel source = Assert.Single(viewModel.BackupConnections, card => card.Record.Id == "remote-c");
        ConnectionCardViewModel target = Assert.Single(viewModel.BackupConnections, card => card.Record.Id == "remote-a");
        await viewModel.ReorderBackupAsync(source, target, insertAfter: false);

        Assert.Equal(["remote-c", "remote-a", "remote-b"], editor.Routing?.BackupProfileIds);
        Assert.Contains("已调整", viewModel.MutationNotice, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ApplyRouting_UnifiedSourceAlsoUpdatesTheGlobalCurrentSource()
    {
        var editor = new RecordingEditor();
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(() => Task.CompletedTask, editor, coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, remote],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine)));

        ConnectionCardViewModel remoteCard = Assert.Single(viewModel.Routing.AvailableSources, source => source.Record.Id == "remote-a");
        viewModel.Routing.CodexSource = remoteCard;
        viewModel.Routing.ClaudeCodeSource = remoteCard;
        viewModel.Routing.GeminiCliSource = remoteCard;
        viewModel.Routing.GrokCliSource = remoteCard;

        await viewModel.ApplyRoutingCommand.ExecuteAsync(null);

        Assert.Equal(new ConnectionProfileRouting("remote-a", "remote-a", "remote-a", "remote-a"), editor.Routing);
        Assert.Equal((ConnectionProfileSelectionGroup.Cloud, "remote-a"), editor.Selection);
    }

    [Fact]
    public async Task ClaudeGptConfiguration_SubmitsIndependentRoleMappings()
    {
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, remote],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", null, "remote-a"),
            new ConnectionProfileRouting("remote-a", "remote-a", "remote-a")));

        await viewModel.ConfigureClaudeGptCommand.ExecuteAsync(null);
        viewModel.ClaudeGptOpusModel = "gpt-5.5";
        viewModel.ClaudeGptSonnetModel = "gpt-5.4";
        viewModel.ClaudeGptHaikuModel = "gpt-5.4-mini";
        await viewModel.EnableClaudeGptCommand.ExecuteAsync(null);

        Assert.Equal(ConnectionProfileIds.LocalMachine, coordinator.EnabledClaudeGptProfileId);
        Assert.NotNull(coordinator.EnabledClaudeGptMapping);
        Assert.Equal("gpt-5.5", coordinator.EnabledClaudeGptMapping!.OpusModel);
        Assert.Equal("gpt-5.4", coordinator.EnabledClaudeGptMapping.SonnetModel);
        Assert.Equal("gpt-5.4-mini", coordinator.EnabledClaudeGptMapping.HaikuModel);
    }

    [Fact]
    public async Task ClaudeGptConfiguration_RestoresTheMappingPreviouslySavedForThatSource()
    {
        var coordinator = new RecordingSwitchCoordinator();
        coordinator.ClaudeGptPresets["local-machine::GPT"] = new ClaudeGptModelMapping
        {
            OpusModel = "saved-opus",
            SonnetModel = "saved-sonnet",
            HaikuModel = "saved-haiku",
        };
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, remote],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", null, "remote-a"),
            new ConnectionProfileRouting("remote-a", "remote-a", "remote-a")));

        await viewModel.ConfigureClaudeGptCommand.ExecuteAsync(null);

        Assert.Equal("saved-opus", viewModel.ClaudeGptOpusModel);
        Assert.Equal("saved-sonnet", viewModel.ClaudeGptSonnetModel);
        Assert.Equal("saved-haiku", viewModel.ClaudeGptHaikuModel);
        Assert.Equal("已恢复此来源的上次设置", viewModel.ClaudeGptMappingStatus);
    }

    [Fact]
    public async Task ModelRouting_AutomaticallySelectsLatestDefaultsAndEnablesCodexClaude()
    {
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, remote],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", null, "remote-a"),
            new ConnectionProfileRouting("remote-a", "remote-a", "remote-a")));

        await viewModel.ConfigureClaudeGptCommand.ExecuteAsync(null);

        Assert.Equal("gpt-5.6-sol", viewModel.ClaudeGptOpusModel);
        Assert.Equal("gpt-5.5", viewModel.ClaudeGptSonnetModel);
        Assert.Equal("gpt-5.4-mini", viewModel.ClaudeGptHaikuModel);

        await viewModel.ConfigureCodexClaudeCommand.ExecuteAsync(null);

        Assert.False(viewModel.IsClaudeGptEditorOpen);
        Assert.True(viewModel.IsCodexClaudeEditorOpen);
        Assert.Equal("Claude", viewModel.SelectedCodexClaudeTargetPlatform);
        Assert.Equal("claude-opus-4-8", viewModel.CodexClaudeDefaultModel);
        Assert.Equal("claude-sonnet-4-6", viewModel.CodexClaudeReviewModel);
        Assert.Equal("高", viewModel.CodexClaudeReasoningEffort);

        await viewModel.EnableCodexClaudeCommand.ExecuteAsync(null);

        Assert.Equal(ConnectionProfileIds.LocalMachine, coordinator.EnabledCodexClaudeProfileId);
        Assert.NotNull(coordinator.EnabledCodexClaudeMapping);
        Assert.Equal("Claude", coordinator.EnabledCodexClaudeMapping!.TargetPlatform);
        Assert.Equal("claude-opus-4-8", coordinator.EnabledCodexClaudeMapping.DefaultModel);
        Assert.Equal("claude-sonnet-4-6", coordinator.EnabledCodexClaudeMapping.ReviewModel);
        Assert.Equal("high", coordinator.EnabledCodexClaudeMapping.ReasoningEffort);
    }

    [Fact]
    public async Task CodexClaudeConfiguration_SwitchesModelsAndRoutingTargetToGrok()
    {
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection(ConnectionProfileIds.LocalMachine, null, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine)));

        viewModel.SelectCodexClaudeTargetPlatformCommand.Execute("Grok");
        await viewModel.ConfigureCodexClaudeCommand.ExecuteAsync(null);

        Assert.Equal("Grok", viewModel.SelectedCodexClaudeTargetPlatform);
        Assert.Equal("grok-latest", viewModel.CodexClaudeDefaultModel);
        Assert.Equal("grok-latest", viewModel.CodexClaudeReviewModel);

        await viewModel.EnableCodexClaudeCommand.ExecuteAsync(null);

        Assert.NotNull(coordinator.EnabledCodexClaudeMapping);
        Assert.Equal("Grok", coordinator.EnabledCodexClaudeMapping!.TargetPlatform);
        Assert.Equal("grok-latest", coordinator.EnabledCodexClaudeMapping.DefaultModel);
    }

    [Fact]
    public async Task ClaudeGptConfiguration_DefaultsToGrokWhenTheSourceHasGrok()
    {
        var coordinator = new RecordingSwitchCoordinator();
        var viewModel = new ConnectionsViewModel(
            () => Task.CompletedTask,
            new RecordingEditor(),
            coordinator);
        ConnectionProfile local = CreateProfile(ConnectionProfileIds.LocalMachine, "本机中转", ConnectionProfileKind.Local);
        ConnectionProfile remote = CreateProfile("remote-a", "远程 A", ConnectionProfileKind.Cloud) with
        {
            ClientBaseUrls = new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = "https://gpt.example/v1",
                [CliKind.GrokCli] = "https://grok.example/v1",
            },
            EnabledClients = new[] { CliKind.Codex, CliKind.GrokCli },
        };
        viewModel.ApplySnapshot(new WorkspaceDataSnapshot(
            Array.Empty<ProjectRecord>(),
            Array.Empty<ConversationRecord>(),
            Array.Empty<CliInstallation>(),
            [local, remote],
            Array.Empty<WorkspaceLoadError>(),
            0,
            DateTimeOffset.UtcNow,
            new ConnectionProfileSelection("remote-a", null, "remote-a"),
            new ConnectionProfileRouting("remote-a", "remote-a", "remote-a")));

        viewModel.SelectClaudeGptTargetPlatformCommand.Execute("Grok");
        await viewModel.ConfigureClaudeGptCommand.ExecuteAsync(null);

        Assert.Equal("Grok", viewModel.SelectedClaudeGptTargetPlatform);
        Assert.Equal("grok-latest", viewModel.ClaudeGptOpusModel);
        Assert.Equal("grok-latest", viewModel.ClaudeGptSonnetModel);
        Assert.Equal("grok-latest", viewModel.ClaudeGptHaikuModel);

        await viewModel.EnableClaudeGptCommand.ExecuteAsync(null);

        Assert.Equal(ConnectionProfileIds.LocalMachine, coordinator.EnabledClaudeGptProfileId);
        Assert.NotNull(coordinator.EnabledClaudeGptMapping);
        Assert.Equal("grok-latest", coordinator.EnabledClaudeGptMapping!.OpusModel);
    }

    [Fact]
    public void CodexClaudeRouting_PrefersTheSourcesOriginalClaudeCredentials()
    {
        var profile = new ProfileDefinition
        {
            Codex = new ClientProfile
            {
                BaseUrl = "https://gpt.example/v1",
                Secret = "gpt-key",
            },
            Claude = new ClientProfile
            {
                BaseUrl = "https://claude.example",
                Secret = "claude-key",
            },
        };

        ClientProfile selected = LegacySwitchCoordinator.SelectCodexClaudeProfile(profile);

        Assert.Equal("https://claude.example", selected.BaseUrl);
        Assert.Equal("claude-key", selected.Secret);

        profile.Claude = new ClientProfile();
        selected = LegacySwitchCoordinator.SelectCodexClaudeProfile(profile);

        Assert.Equal("https://gpt.example/v1", selected.BaseUrl);
        Assert.Equal("gpt-key", selected.Secret);
    }

    private static ConnectionProfile CreateProfile(string id, string name, ConnectionProfileKind kind) => new()
    {
        Id = id,
        Name = name,
        Kind = kind,
        BaseUrl = "https://example.test",
        ClientBaseUrls = new Dictionary<CliKind, string> { [CliKind.Codex] = "https://example.test/v1" },
        EnabledClients = new[] { CliKind.Codex },
    };

    private sealed class RecordingEditor : IConnectionProfileEditor
    {
        public ConnectionProfileDraft? Added { get; private set; }

        public ConnectionProfileDraft? Updated { get; private set; }

        public (ConnectionProfileSelectionGroup Group, string Id)? Selection { get; private set; }

        public ConnectionProfileRouting? Routing { get; private set; }

        public Task<ConnectionProfile> AddAsync(ConnectionProfileDraft draft, CancellationToken cancellationToken = default)
        {
            Added = draft;
            return Task.FromResult(CreateProfile("created", draft.Name, draft.Kind));
        }

        public Task<ConnectionProfile> UpdateAsync(string id, ConnectionProfileDraft draft, CancellationToken cancellationToken = default)
        {
            Updated = draft;
            return Task.FromResult(CreateProfile(id, draft.Name, draft.Kind));
        }

        public Task DeleteAsync(string id, CancellationToken cancellationToken = default) => Task.CompletedTask;

        public Task<ConnectionProfileSelection> GetSelectionAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new ConnectionProfileSelection(null, null));

        public Task SetSelectedAsync(ConnectionProfileSelectionGroup group, string id, CancellationToken cancellationToken = default)
        {
            Selection = (group, id);
            return Task.CompletedTask;
        }

        public Task<ConnectionProfileRouting> GetRoutingAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(Routing ?? new ConnectionProfileRouting(
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine,
                ConnectionProfileIds.LocalMachine));

        public Task SetRoutingAsync(ConnectionProfileRouting routing, CancellationToken cancellationToken = default)
        {
            Routing = routing;
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingLocalGatewayController(string nativeRoot) : ILocalGatewayController
    {
        public string? ConfiguredPath { get; private set; }

        public LocalGatewayStatus Status { get; set; } = new()
        {
            NativeMode = true,
            ControlAvailable = true,
            NativeRoot = nativeRoot,
            WebUrl = LocalGatewayService.NativeWebUrl,
        };

        public bool StartMarksWebReachable { get; set; }

        public int StartCalls { get; private set; }

        public int WaitForWebCalls { get; private set; }

        public List<string> OpenedDashboardUrls { get; } = [];

        public LocalGatewayStatus GetStartupStatus() => Status;

        public Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken) =>
            Task.FromResult(Status);

        public Task<CommandResult> StartAsync(CancellationToken cancellationToken)
        {
            StartCalls++;
            if (StartMarksWebReachable)
            {
                Status.WebReachable = true;
            }
            return Task.FromResult(new CommandResult { ExitCode = 0 });
        }

        public Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken)
        {
            ConfiguredPath = selectedPath;
            return Task.FromResult(new CommandResult { ExitCode = 0 });
        }

        public Task<CommandResult> StopAsync(CancellationToken cancellationToken) =>
            Task.FromResult(new CommandResult { ExitCode = 0 });

        public Task<CommandResult> RestartAsync(CancellationToken cancellationToken) =>
            Task.FromResult(new CommandResult { ExitCode = 0 });

        public Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken)
        {
            WaitForWebCalls++;
            return Task.FromResult(Status.WebReachable);
        }

        public Task OpenDashboardAsync(string url, CancellationToken cancellationToken)
        {
            OpenedDashboardUrls.Add(url);
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingSwitchCoordinator : ILegacySwitchCoordinator
    {
        public string? AppliedProfileId { get; private set; }

        public string? EnabledClaudeGptProfileId { get; private set; }

        public ClaudeGptModelMapping? EnabledClaudeGptMapping { get; private set; }

        public string? EnabledCodexClaudeProfileId { get; private set; }

        public CodexClaudeModelMapping? EnabledCodexClaudeMapping { get; private set; }

        public Dictionary<string, ClaudeGptModelMapping> ClaudeGptPresets { get; } = new(StringComparer.OrdinalIgnoreCase);

        public Dictionary<string, CodexClaudeModelMapping> CodexClaudePresets { get; } = new(StringComparer.OrdinalIgnoreCase);

        public HashSet<string> ActiveBackupSourceIds { get; } = new(StringComparer.OrdinalIgnoreCase);

        public LiveStatus ReadLiveStatus() => new();

        public ImportedLiveConfig ReadCurrentClientConfig() => new();

        public Task<OperationResult> ApplySourceAsync(string profileId, CancellationToken cancellationToken = default)
        {
            AppliedProfileId = profileId;
            return Task.FromResult(new OperationResult { Success = true, Summary = "切换完成。" });
        }

        public Task<OperationResult> ValidateSourceAsync(string profileId, CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "验证完成。" });

        public Task<OperationResult> ApplyRoutingAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "分流完成。" });

        public Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult<IReadOnlySet<string>>(ActiveBackupSourceIds);

        public Task<OperationResult> ValidateRoutingAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "验证完成。" });

        public Task<OperationResult> RestoreLatestBackupAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "恢复完成。" });

        public ClaudeGptRoutingStatus ReadClaudeGptRoutingStatus() => new();

        public ClaudeGptModelMapping? ReadClaudeGptPreset(string profileId, string targetPlatform) =>
            ClaudeGptPresets.GetValueOrDefault($"{profileId}::{targetPlatform}");

        public Task<IReadOnlyList<string>> GetClaudeGptModelsAsync(
            string profileId,
            string targetPlatform,
            CancellationToken cancellationToken = default) =>
            Task.FromResult<IReadOnlyList<string>>(
                string.Equals(targetPlatform, "Grok", StringComparison.OrdinalIgnoreCase)
                    ? ["grok-latest", "grok-4.5"]
                    : ["gpt-5.6-sol", "gpt-5.5", "gpt-5.4-mini"]);

        public Task<OperationResult> EnableClaudeGptRoutingAsync(
            string profileId,
            string targetPlatform,
            ClaudeGptModelMapping mapping,
            CancellationToken cancellationToken = default)
        {
            EnabledClaudeGptProfileId = profileId;
            EnabledClaudeGptMapping = mapping;
            ClaudeGptPresets[$"{profileId}::{targetPlatform}"] = mapping;
            return Task.FromResult(new OperationResult { Success = true, Summary = "GPT 路由已启用。" });
        }

        public Task<OperationResult> DisableClaudeGptRoutingAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "GPT 路由已停用。" });

        public CodexClaudeRoutingStatus ReadCodexClaudeRoutingStatus() => new();

        public CodexClaudeModelMapping? ReadCodexClaudePreset(string profileId, string targetPlatform) =>
            CodexClaudePresets.GetValueOrDefault($"{profileId}::{targetPlatform}");

        public Task<IReadOnlyList<string>> GetCodexClaudeModelsAsync(
            string profileId,
            string targetPlatform,
            CancellationToken cancellationToken = default) =>
            Task.FromResult<IReadOnlyList<string>>(
                string.Equals(targetPlatform, "Grok", StringComparison.OrdinalIgnoreCase)
                    ? ["grok-latest", "grok-4.5"]
                    : ["claude-opus-4-8", "claude-sonnet-4-6"]);

        public Task<OperationResult> EnableCodexClaudeRoutingAsync(
            string profileId,
            CodexClaudeModelMapping mapping,
            CancellationToken cancellationToken = default)
        {
            EnabledCodexClaudeProfileId = profileId;
            EnabledCodexClaudeMapping = mapping;
            CodexClaudePresets[$"{profileId}::{mapping.TargetPlatform}"] = mapping;
            return Task.FromResult(new OperationResult { Success = true, Summary = "Claude 路由已启用。" });
        }

        public Task<OperationResult> DisableCodexClaudeRoutingAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "Claude 路由已停用。" });

        public Task<OperationResult> ResumeLastApplicationStateAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "上次状态已恢复。" });

        public Task<OperationResult> SaveApplicationStateAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "当前状态已保存。" });

        public Task<OperationResult> RestoreApplicationSessionAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new OperationResult { Success = true, Summary = "启动前配置已恢复。" });

        public string? GetDashboardUrl(string profileId) => null;
    }
}


