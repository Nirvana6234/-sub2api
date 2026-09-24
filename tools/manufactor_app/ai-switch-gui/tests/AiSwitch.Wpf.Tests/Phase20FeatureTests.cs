using System.Text.Json.Nodes;
using System.Net.Http;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class Phase20FeatureTests
{
    [Fact]
    public async Task DesktopSettingsStore_RoundTripsAndBacksUp()
    {
        using var fixture = new Fixture();
        using var store = new DesktopSettingsStore(fixture.Paths);
        await store.SaveAsync(new WorkspaceDesktopSettings { StartWithWindows = true });
        await store.SaveAsync(new WorkspaceDesktopSettings
        {
            MinimizeToTray = false,
            NetworkProbeIntervalMinutes = 5,
        });

        WorkspaceDesktopSettings loaded = await store.LoadAsync();

        Assert.False(loaded.MinimizeToTray);
        Assert.Equal(5, loaded.NetworkProbeIntervalMinutes);
        Assert.NotEmpty(Directory.EnumerateFiles(fixture.Paths.BackupsDirectory, "desktop-settings-*.bak"));
    }

    [Fact]
    public async Task Settings_RepairsStaleStartupRegistrationWhenPreferenceIsEnabled()
    {
        using var fixture = new Fixture();
        var store = new InMemoryDesktopSettingsStore(new WorkspaceDesktopSettings
        {
            StartWithWindows = true,
        });
        var startup = new RecordingStartupRegistrationService(isEnabled: false);
        using var updates = new ApplicationUpdateService(fixture.Paths.UpdatesDirectory);
        var viewModel = new SettingsViewModel(store, startup, updates, fixture.Paths);

        await viewModel.InitializeAsync();

        Assert.True(viewModel.StartWithWindows);
        Assert.Equal([true], startup.SetEnabledCalls);
    }

    [Fact]
    public async Task McpImporter_MergesTargetsAndDoesNotPersistPlaintextCredential()
    {
        using var fixture = new Fixture();
        Directory.CreateDirectory(fixture.Paths.ClaudeHome);
        Directory.CreateDirectory(fixture.Paths.GeminiHome);
        await File.WriteAllTextAsync(fixture.Paths.ClaudeConfigPath,
            """{"mcpServers":{"shared":{"command":"node","args":["server.js"]},"remote":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer secret"}}}}""");
        await File.WriteAllTextAsync(fixture.Paths.GeminiConfigPath,
            """{"mcpServers":{"shared":{"command":"node","args":["server.js"]}}}""");

        McpImportResult result = await new OfficialMcpImportService(fixture.Paths)
            .ImportAllAsync(new WorkspaceFeatureState());

        McpServerDefinition shared = Assert.Single(result.State.McpServers, item => item.Id == "shared");
        Assert.Equal(ManagedClientTargets.Claude | ManagedClientTargets.Gemini, shared.Targets);
        McpServerDefinition remote = Assert.Single(result.State.McpServers, item => item.Id == "remote");
        Assert.Empty(remote.Headers);
        Assert.Contains(result.Warnings, warning => warning.Contains("明文凭据", StringComparison.Ordinal));
    }

    [Fact]
    public async Task ConnectionTransfer_ExportsWithoutSecretsAndRestoresBackup()
    {
        using var fixture = new Fixture();
        Directory.CreateDirectory(Path.GetDirectoryName(fixture.Paths.LegacyProfilesPath)!);
        await File.WriteAllTextAsync(fixture.Paths.LegacyProfilesPath,
            """{"CloudSources":[{"Id":"cloud-a","Name":"A","Codex":{"BaseUrl":"https://a.example/v1","Secret":"sk-test"}}],"LocalSources":[]}""");
        var service = new ConnectionProfileTransferService(fixture.Paths);
        string exported = Path.Combine(fixture.Root, "export.json");
        await service.ExportSafeAsync(exported);

        string exportText = await File.ReadAllTextAsync(exported);
        Assert.DoesNotContain("sk-test", exportText, StringComparison.Ordinal);
        Assert.DoesNotContain("Secret", exportText, StringComparison.OrdinalIgnoreCase);

        await File.WriteAllTextAsync(Path.Combine(fixture.Root, "import.json"),
            """{"product":"LanAi.Workspace","cloud_sources":[{"Id":"cloud-b","Name":"B","Claude":{"BaseUrl":"https://b.example","Token":"hidden"}}]}""");
        await service.ImportSafeAsync(Path.Combine(fixture.Root, "import.json"));
        Assert.Contains("cloud-b", await File.ReadAllTextAsync(fixture.Paths.LegacyProfilesPath));
        Assert.True(await service.RestoreLatestAsync());
        Assert.DoesNotContain("cloud-b", await File.ReadAllTextAsync(fixture.Paths.LegacyProfilesPath));
    }

    [Fact]
    public async Task ConnectionTransfer_RetainsOnlyFiveSourceBackups()
    {
        using var fixture = new Fixture();
        Directory.CreateDirectory(Path.GetDirectoryName(fixture.Paths.LegacyProfilesPath)!);
        await File.WriteAllTextAsync(fixture.Paths.LegacyProfilesPath, "{\"CloudSources\":[]}");
        var service = new ConnectionProfileTransferService(fixture.Paths);
        string importPath = Path.Combine(fixture.Root, "import.json");

        for (int index = 0; index < 7; index++)
        {
            await File.WriteAllTextAsync(importPath,
                $$"""{"cloud_sources":[{"Id":"cloud-{{index}}","Name":"Source {{index}}"}]}""");
            await service.ImportSafeAsync(importPath);
        }

        Assert.Equal(5, Directory.EnumerateFiles(
            fixture.Paths.BackupsDirectory,
            "connection-profiles-*.bak").Count());
    }

    [Fact]
    public async Task ProjectProfile_AutoSavesPreviousProjectAndAppliesTargetProfile()
    {
        using var fixture = new Fixture();
        using var store = new WorkspaceFeatureStore(fixture.Paths);
        var sync = new RecordingSynchronizer();
        var connections = new FakeConnectionEditor(new ConnectionProfileRouting("a", "a", "a"));
        var service = new ProjectWorkspaceProfileService(store, sync, connections);
        await store.SaveAsync(new WorkspaceFeatureState
        {
            McpServers = [new McpServerDefinition { Id = "mcp", Name = "MCP", Command = "cmd", Targets = ManagedClientTargets.Codex }],
            PromptPresets = [new PromptPresetDefinition { Id = "prompt", Name = "Prompt", Markdown = "one", Targets = ManagedClientTargets.Codex }],
        });
        await service.CaptureAsync("project-a");

        connections.Routing = new ConnectionProfileRouting("b", "b", "b");
        WorkspaceFeatureState changed = await store.LoadAsync();
        await store.SaveAsync(changed with
        {
            McpServers = changed.McpServers.Select(item => item with { Targets = ManagedClientTargets.Claude }).ToArray(),
            PromptPresets = changed.PromptPresets.Select(item => item with { Targets = ManagedClientTargets.Claude }).ToArray(),
        });
        await service.CaptureAsync("project-b");

        ProjectProfileOperationResult result = await service.ApplyAsync("project-a");
        WorkspaceFeatureState applied = await store.LoadAsync();

        Assert.Empty(result.Warnings);
        Assert.Equal("a", connections.Routing.CodexProfileId);
        Assert.Equal(ManagedClientTargets.Codex, Assert.Single(applied.McpServers).Targets);
        Assert.Equal(ManagedClientTargets.Codex, Assert.Single(applied.PromptPresets).Targets);
        Assert.Equal("project-a", applied.CurrentProjectProfileId);
        Assert.True(sync.CallCount > 0);
    }

    [Fact]
    public async Task UpdateService_DownloadsOnlyHttpsPackageWithMatchingSha256()
    {
        using var fixture = new Fixture();
        byte[] package = "verified-package"u8.ToArray();
        string sha = Convert.ToHexString(System.Security.Cryptography.SHA256.HashData(package));
        string manifest = $$"""{"product":"LanAi.Workspace","version":"99.0.0.0","package_url":"https://updates.example/app.zip","sha256":"{{sha}}"}""";
        using var service = new ApplicationUpdateService(
            fixture.Paths.UpdatesDirectory,
            new StaticHttpHandler(request => request.RequestUri!.AbsolutePath.EndsWith("manifest.json", StringComparison.Ordinal)
                ? new HttpResponseMessage(System.Net.HttpStatusCode.OK) { Content = new StringContent(manifest) }
                : new HttpResponseMessage(System.Net.HttpStatusCode.OK) { Content = new ByteArrayContent(package) }));

        AppUpdateCheckResult check = await service.CheckAsync("https://updates.example/manifest.json");
        string downloaded = await service.DownloadVerifiedAsync(check.Manifest!);

        Assert.True(check.HasUpdate);
        Assert.Equal(package, await File.ReadAllBytesAsync(downloaded));
        await Assert.ThrowsAsync<InvalidOperationException>(() => service.CheckAsync("http://updates.example/manifest.json"));
    }

    private sealed class RecordingSynchronizer : IOfficialClientExtensionSynchronizer
    {
        public int CallCount { get; private set; }
        public Task SynchronizeAsync(WorkspaceFeatureState previous, WorkspaceFeatureState current, CancellationToken cancellationToken = default)
        {
            CallCount++;
            return Task.CompletedTask;
        }
    }

    private sealed class FakeConnectionEditor(ConnectionProfileRouting routing) : IConnectionProfileEditor
    {
        public ConnectionProfileRouting Routing { get; set; } = routing;
        public Task<ConnectionProfile> AddAsync(ConnectionProfileDraft draft, CancellationToken cancellationToken = default) => throw new NotSupportedException();
        public Task<ConnectionProfile> UpdateAsync(string id, ConnectionProfileDraft draft, CancellationToken cancellationToken = default) => throw new NotSupportedException();
        public Task DeleteAsync(string id, CancellationToken cancellationToken = default) => throw new NotSupportedException();
        public Task<ConnectionProfileSelection> GetSelectionAsync(CancellationToken cancellationToken = default) => Task.FromResult(new ConnectionProfileSelection(null, null, null));
        public Task SetSelectedAsync(ConnectionProfileSelectionGroup group, string id, CancellationToken cancellationToken = default) => Task.CompletedTask;
        public Task<ConnectionProfileRouting> GetRoutingAsync(CancellationToken cancellationToken = default) => Task.FromResult(Routing);
        public Task SetRoutingAsync(ConnectionProfileRouting routing, CancellationToken cancellationToken = default)
        {
            Routing = routing;
            return Task.CompletedTask;
        }
    }

    private sealed class StaticHttpHandler(Func<HttpRequestMessage, HttpResponseMessage> responseFactory) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(responseFactory(request));
    }

    private sealed class InMemoryDesktopSettingsStore(WorkspaceDesktopSettings settings) : IDesktopSettingsStore
    {
        private WorkspaceDesktopSettings _settings = settings;

        public Task<WorkspaceDesktopSettings> LoadAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(_settings);

        public Task SaveAsync(WorkspaceDesktopSettings settings, CancellationToken cancellationToken = default)
        {
            _settings = settings;
            return Task.CompletedTask;
        }
    }

    private sealed class RecordingStartupRegistrationService(bool isEnabled) : IWindowsStartupRegistrationService
    {
        public List<bool> SetEnabledCalls { get; } = [];

        public bool IsEnabled() => isEnabled;

        public void SetEnabled(bool enabled) => SetEnabledCalls.Add(enabled);
    }

    private sealed class Fixture : IDisposable
    {
        public Fixture()
        {
            Root = Path.Combine(Path.GetTempPath(), "LanAi.Phase20.Tests", Guid.NewGuid().ToString("N"));
            string profile = Path.Combine(Root, "profile");
            string local = Path.Combine(Root, "local");
            Directory.CreateDirectory(profile);
            Directory.CreateDirectory(local);
            Paths = new AppDataPaths(profile, local);
            Paths.EnsureWritableDirectories();
        }

        public string Root { get; }
        public AppDataPaths Paths { get; }
        public void Dispose()
        {
            if (Directory.Exists(Root)) Directory.Delete(Root, recursive: true);
        }
    }
}
