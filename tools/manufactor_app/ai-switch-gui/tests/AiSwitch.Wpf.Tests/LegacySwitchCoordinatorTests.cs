using AiSwitchGui;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class LegacySwitchCoordinatorTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "LanAi.LegacySwitch.Tests", Guid.NewGuid().ToString("N"));

    [Fact]
    public void SelectSource_CloudAndFixedLocalPersistTheirLegacySelection()
    {
        string profilePath = Path.Combine(_root, "profiles.json");
        var paths = new AppDataPaths(
            userProfile: Path.GetDirectoryName(_root)!,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);

        var coordinator = new LegacySwitchCoordinator(paths);

        (ProfileStore cloudStore, TargetMode cloudMode) = coordinator.SelectSource("remote-a");
        (ProfileStore localStore, TargetMode localMode) = coordinator.SelectSource(ProfileSourceIds.LanDefault);

        Assert.Equal(TargetMode.Cloud, cloudMode);
        Assert.Equal("remote-a", cloudStore.SelectedCloudSourceId);
        Assert.Equal(TargetMode.Local, localMode);
        Assert.Equal(ProfileSourceIds.LanDefault, localStore.SelectedLocalSourceId);
    }

    [Fact]
    public void SelectSource_UnknownIdDoesNotCreateOrApplyANewProfile()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        var coordinator = new LegacySwitchCoordinator(paths);

        InvalidOperationException error = Assert.Throws<InvalidOperationException>(() =>
            coordinator.SelectSource("missing"));

        Assert.Contains("不存在", error.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void GetDashboardUrl_LanUsesTheExplicitPersistedDashboardAddress()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        var coordinator = new LegacySwitchCoordinator(paths);

        string? dashboardUrl = coordinator.GetDashboardUrl(ProfileSourceIds.LanDefault);

        Assert.Equal("http://192.168.10.8:3000/dashboard", dashboardUrl);
    }

    [Fact]
    public void SelectCodexCompatibleProfile_UsesClaudeCredentialsForClaudeTarget()
    {
        var claude = new ClientProfile
        {
            BaseUrl = "https://claude.example.test",
            Secret = "claude-group-key",
        };
        var profile = new ProfileDefinition
        {
            Codex = new ClientProfile(),
            Claude = claude,
            Grok = new ClientProfile
            {
                BaseUrl = "https://code-plan.site/v1",
                Secret = "grok-group-key",
            },
        };
        var mapping = new CodexClaudeModelMapping
        {
            TargetPlatform = "Claude",
            DefaultModel = "gpt-5.6-sol",
            ReviewModel = "gpt-5.4",
            ReasoningEffort = "high",
        };

        ClientProfile selected = LegacySwitchCoordinator.SelectCodexCompatibleProfile(profile, mapping);

        Assert.Same(claude, selected);
    }

    [Fact]
    public void SelectCodexCompatibleProfile_UsesGrokCredentialsForGrokModels()
    {
        var grok = new ClientProfile
        {
            BaseUrl = "https://code-plan.site/v1",
            Secret = "grok-group-key",
        };
        var profile = new ProfileDefinition
        {
            Codex = new ClientProfile
            {
                BaseUrl = "https://gpt.example.test/v1",
                Secret = "gpt-group-key",
            },
            Claude = new ClientProfile
            {
                BaseUrl = "https://claude.example.test",
                Secret = "claude-group-key",
            },
            Grok = grok,
        };
        var mapping = new CodexClaudeModelMapping
        {
            TargetPlatform = "Grok",
            DefaultModel = "grok-4.5",
            ReviewModel = "grok-4.5",
            ReasoningEffort = "high",
        };

        ClientProfile selected = LegacySwitchCoordinator.SelectCodexCompatibleProfile(profile, mapping);

        Assert.Same(grok, selected);
    }

    [Fact]
    public async Task RestoreApplicationSession_RestoresClientFilesToTheirLaunchState()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        string claudeSettings = Path.Combine(paths.UserProfile, ".claude", "settings.json");
        string codexConfig = Path.Combine(paths.UserProfile, ".codex", "config.toml");
        Directory.CreateDirectory(Path.GetDirectoryName(claudeSettings)!);
        Directory.CreateDirectory(Path.GetDirectoryName(codexConfig)!);
        byte[] originalClaude = "{\r\n  \"before\": true\r\n}\r\n"u8.ToArray();
        byte[] originalCodex = "model = \"before\"\r\n"u8.ToArray();
        await File.WriteAllBytesAsync(claudeSettings, originalClaude);
        await File.WriteAllBytesAsync(codexConfig, originalCodex);
        var coordinator = new LegacySwitchCoordinator(paths);

        await File.WriteAllTextAsync(claudeSettings, "{\"after\":true}");
        await File.WriteAllTextAsync(codexConfig, "model = \"after\"");
        OperationResult result = await coordinator.RestoreApplicationSessionAsync();

        Assert.True(result.Success, result.Summary);
        Assert.Equal(originalClaude, await File.ReadAllBytesAsync(claudeSettings));
        Assert.Equal(originalCodex, await File.ReadAllBytesAsync(codexConfig));
        Assert.True((await coordinator.RestoreApplicationSessionAsync()).Success);
    }

    [Fact]
    public async Task Constructor_RecoversClientFilesLeftByAnAbnormalPreviousExit()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        string claudeSettings = Path.Combine(paths.UserProfile, ".claude", "settings.json");
        Directory.CreateDirectory(Path.GetDirectoryName(claudeSettings)!);
        const string original = "{\"before\":true}";
        await File.WriteAllTextAsync(claudeSettings, original);
        _ = new LegacySwitchCoordinator(paths);

        await File.WriteAllTextAsync(claudeSettings, "{\"left_by_crash\":true}");
        _ = new LegacySwitchCoordinator(paths);

        Assert.Equal(original, await File.ReadAllTextAsync(claudeSettings));
    }

    [Fact]
    public async Task NormalExit_RestoresOriginalFiles_AndNextLaunchReappliesLastSource()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        string codexConfig = Path.Combine(paths.UserProfile, ".codex", "config.toml");
        Directory.CreateDirectory(Path.GetDirectoryName(codexConfig)!);
        const string original = "model = \"official\"\r\n";
        await File.WriteAllTextAsync(codexConfig, original);

        var firstLaunch = new LegacySwitchCoordinator(paths);
        OperationResult apply = await firstLaunch.ApplySourceAsync("remote-a");
        Assert.True(apply.Success, apply.Summary);
        Assert.Contains("https://example.test/v1", await File.ReadAllTextAsync(codexConfig), StringComparison.Ordinal);

        Assert.True((await firstLaunch.SaveApplicationStateAsync()).Success);
        Assert.True((await firstLaunch.RestoreApplicationSessionAsync()).Success);
        Assert.Equal(original, await File.ReadAllTextAsync(codexConfig));

        var secondLaunch = new LegacySwitchCoordinator(paths);
        OperationResult resume = await secondLaunch.ResumeLastApplicationStateAsync();

        Assert.True(resume.Success, resume.Summary);
        Assert.Contains("https://example.test/v1", await File.ReadAllTextAsync(codexConfig), StringComparison.Ordinal);
        Assert.True((await secondLaunch.RestoreApplicationSessionAsync()).Success);
        Assert.Equal(original, await File.ReadAllTextAsync(codexConfig));
    }

    [Fact]
    public async Task Resume_without_saved_state_reconciles_explicit_empty_backup_routing()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        string profilePath = CreateProfilesFile();
        string profiles = await File.ReadAllTextAsync(profilePath);
        profiles = profiles.Replace(
            "\"Mixed\": {}",
            "\"BackupSourceIds\": [], \"Mixed\": { \"CodexSourceId\": \"remote-a\" }",
            StringComparison.Ordinal);
        await File.WriteAllTextAsync(paths.LegacyProfilesPath, profiles);
        var localRouting = new RecordingLocalRoutingService();
        var coordinator = new LegacySwitchCoordinator(paths, localRouting);

        OperationResult result = await coordinator.ResumeLastApplicationStateAsync();

        Assert.True(result.Success, result.Summary);
        Assert.Equal(1, localRouting.ApplyRoutingCalls);
        Assert.Empty(localRouting.BackupSourceIds);
        Assert.Equal("remote-a", localRouting.LegacyCodexSourceId);
        Assert.Equal(0, localRouting.ApplySourceCalls);
    }

    [Fact]
    public void ResumeStateStore_IgnoresCorruptState_AndAtomicallyRoundTripsValidState()
    {
        string path = Path.Combine(_root, "state", "resume.json");
        var store = new ApplicationResumeStateStore(path);
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        File.WriteAllText(path, "{broken");
        Assert.Null(store.Load());

        store.Save(new ApplicationResumeState
        {
            BaseRoutingMode = ApplicationBaseRoutingMode.UnifiedSource,
            UnifiedSourceId = "remote-a",
            ClaudeGptEnabled = true,
            ClaudeGptSourceId = "remote-a",
            ClaudeGptTargetPlatform = "Grok",
            ClaudeGptMapping = new ClaudeGptModelMapping
            {
                OpusModel = "grok-4.5",
                SonnetModel = "grok-4.5",
                HaikuModel = "grok-4.5",
            },
        });

        ApplicationResumeState restored = Assert.IsType<ApplicationResumeState>(store.Load());
        Assert.Equal(ApplicationBaseRoutingMode.UnifiedSource, restored.BaseRoutingMode);
        Assert.Equal("remote-a", restored.UnifiedSourceId);
        Assert.True(restored.ClaudeGptEnabled);
        Assert.Equal("Grok", restored.ClaudeGptTargetPlatform);
        Assert.Equal("grok-4.5", restored.ClaudeGptMapping.OpusModel);
        Assert.Empty(Directory.GetFiles(Path.GetDirectoryName(path)!, "*.tmp"));
    }

    [Fact]
    public async Task SaveApplicationState_PreservesExplicitClaudeRoutingIntentWhenBridgeIsTemporarilyAbsent()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        File.Copy(CreateProfilesFile(), paths.LegacyProfilesPath, overwrite: true);
        var stateStore = new ApplicationResumeStateStore(paths.ApplicationResumeStatePath);
        stateStore.Save(new ApplicationResumeState
        {
            ClaudeGptEnabled = true,
            ClaudeGptSourceId = ProfileSourceIds.LocalMachine,
            ClaudeGptTargetPlatform = "GPT",
            ClaudeGptMapping = new ClaudeGptModelMapping
            {
                OpusModel = "gpt-5.6-sol",
                SonnetModel = "gpt-5.6-terra",
                HaikuModel = "gpt-5.4-mini",
            },
        });
        var coordinator = new LegacySwitchCoordinator(paths);

        OperationResult result = await coordinator.SaveApplicationStateAsync();

        Assert.True(result.Success, result.Summary);
        ApplicationResumeState restored = Assert.IsType<ApplicationResumeState>(stateStore.Load());
        Assert.True(restored.ClaudeGptEnabled);
        Assert.Equal("gpt-5.6-sol", restored.ClaudeGptMapping.OpusModel);
    }

    [Fact]
    public async Task ClaudeRouting_IsRecreatedWithAHealthyBridgeOnTheNextLaunch()
    {
        var paths = new AppDataPaths(
            userProfile: _root,
            localAppData: Path.Combine(_root, "local"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);
        string profiles = await File.ReadAllTextAsync(CreateProfilesFile());
        profiles = profiles.Replace(
            "{ \"Id\": \"local-machine\", \"Name\": \"本机中转\", \"Codex\": {}, \"Claude\": {}, \"Gemini\": {} }",
            "{ \"Id\": \"local-machine\", \"Name\": \"本机中转\", \"Codex\": { \"BaseUrl\": \"http://127.0.0.1:8080/v1\", \"Secret\": \"local-key\" }, \"Claude\": {}, \"Gemini\": {} }",
            StringComparison.Ordinal);
        await File.WriteAllTextAsync(paths.LegacyProfilesPath, profiles);
        var stateStore = new ApplicationResumeStateStore(paths.ApplicationResumeStatePath);
        stateStore.Save(new ApplicationResumeState
        {
            ClaudeGptEnabled = true,
            ClaudeGptSourceId = ProfileSourceIds.LocalMachine,
            ClaudeGptTargetPlatform = "GPT",
            ClaudeGptMapping = new ClaudeGptModelMapping
            {
                OpusModel = "gpt-5.6-sol",
                SonnetModel = "gpt-5.6-terra",
                HaikuModel = "gpt-5.4-mini",
            },
        });

        var firstLaunch = new LegacySwitchCoordinator(paths);
        OperationResult firstResume = await firstLaunch.ResumeLastApplicationStateAsync();
        Assert.True(firstResume.Success, firstResume.Summary);
        Assert.True(firstLaunch.ReadClaudeGptRoutingStatus().Enabled);
        Assert.True((await firstLaunch.SaveApplicationStateAsync()).Success);
        Assert.True((await firstLaunch.RestoreApplicationSessionAsync()).Success);

        var secondLaunch = new LegacySwitchCoordinator(paths);
        OperationResult secondResume = await secondLaunch.ResumeLastApplicationStateAsync();

        Assert.True(secondResume.Success, secondResume.Summary);
        Assert.True(secondLaunch.ReadClaudeGptRoutingStatus().Enabled);
        string settingsPath = Path.Combine(paths.UserProfile, ".claude", "settings.json");
        string settings = await File.ReadAllTextAsync(settingsPath);
        Assert.Contains("http://127.0.0.1:", settings, StringComparison.Ordinal);
        Assert.Contains("gpt-5.6-sol", settings, StringComparison.Ordinal);
        Assert.True((await secondLaunch.RestoreApplicationSessionAsync()).Success);
    }

    private string CreateProfilesFile()
    {
        string fixtureDirectory = Path.Combine(_root, "fixture");
        Directory.CreateDirectory(fixtureDirectory);
        string path = Path.Combine(fixtureDirectory, "profiles.json");
        File.WriteAllText(path, """
        {
          "CloudSources": [
            { "Id": "cloud-default", "Name": "远程来源", "Codex": {}, "Claude": {}, "Gemini": {} },
            { "Id": "remote-a", "Name": "备用来源", "Codex": { "BaseUrl": "https://example.test/v1" }, "Claude": {}, "Gemini": {} }
          ],
          "SelectedCloudSourceId": "cloud-default",
          "LocalSources": [
            { "Id": "local-machine", "Name": "本机中转", "Codex": {}, "Claude": {}, "Gemini": {} },
            { "Id": "lan-default", "Name": "局域网中转", "DashboardUrl": "http://192.168.10.8:3000/dashboard", "Codex": {}, "Claude": {}, "Gemini": {} }
          ],
          "SelectedLocalSourceId": "local-machine",
          "Mixed": {}
        }
        """);
        return path;
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private sealed class RecordingLocalRoutingService : ILocalSub2ApiRoutingService
    {
        public int ApplySourceCalls { get; private set; }

        public int ApplyRoutingCalls { get; private set; }

        public IReadOnlyList<string> BackupSourceIds { get; private set; } = [];

        public string LegacyCodexSourceId { get; private set; } = string.Empty;

        public Task<LocalSub2ApiRoutingResult> ApplySourceAsync(
            ProfileStore store,
            string profileId,
            CancellationToken cancellationToken)
        {
            ApplySourceCalls++;
            return Task.FromResult(new LocalSub2ApiRoutingResult(store, []));
        }

        public Task<LocalSub2ApiRoutingResult> ApplyRoutingAsync(
            ProfileStore store,
            CancellationToken cancellationToken)
        {
            ApplyRoutingCalls++;
            BackupSourceIds = store.BackupSourceIds.ToArray();
            LegacyCodexSourceId = store.Mixed.CodexSourceId;
            return Task.FromResult(new LocalSub2ApiRoutingResult(store, []));
        }

        public Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(
            ProfileStore store,
            CancellationToken cancellationToken)
            => Task.FromResult<IReadOnlySet<string>>(new HashSet<string>(StringComparer.OrdinalIgnoreCase));
    }
}
