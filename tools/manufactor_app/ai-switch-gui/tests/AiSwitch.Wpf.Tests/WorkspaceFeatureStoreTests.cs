using System.Text.Json.Nodes;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Wpf.Tests;

public sealed class WorkspaceFeatureStoreTests
{
    [Fact]
    public async Task Store_RoundTripsStateAndCreatesRollingBackup()
    {
        using var fixture = new FeatureFixture();
        using var store = new WorkspaceFeatureStore(fixture.Paths);
        WorkspaceFeatureState first = StateWithMcp("one");
        await store.SaveAsync(first);
        await store.SaveAsync(StateWithMcp("two"));

        WorkspaceFeatureState loaded = await store.LoadAsync();

        Assert.Equal("two", Assert.Single(loaded.McpServers).Id);
        Assert.NotEmpty(Directory.EnumerateFiles(fixture.Paths.BackupsDirectory, "workspace-features-*.bak"));
    }

    [Fact]
    public async Task Synchronizer_PreservesUnmanagedConfigurationAndWritesAllThreeClients()
    {
        using var fixture = new FeatureFixture();
        fixture.Paths.EnsureWritableDirectories();
        Directory.CreateDirectory(fixture.Paths.CodexHome);
        Directory.CreateDirectory(fixture.Paths.ClaudeHome);
        Directory.CreateDirectory(fixture.Paths.GeminiHome);
        await File.WriteAllTextAsync(fixture.Paths.CodexConfigPath, "model = \"gpt-test\"\n[other]\nvalue = 1\n");
        await File.WriteAllTextAsync(fixture.Paths.ClaudeConfigPath, "{\"theme\":\"dark\"}");
        await File.WriteAllTextAsync(fixture.Paths.GeminiConfigPath, "{\"general\":{\"previewFeatures\":true}}");

        var state = new WorkspaceFeatureState
        {
            McpServers =
            [
                new McpServerDefinition
                {
                    Id = "fetch",
                    Name = "Fetch",
                    Command = "uvx",
                    Arguments = ["mcp-server-fetch"],
                    Targets = ManagedClientTargets.All,
                },
            ],
            PromptPresets =
            [
                new PromptPresetDefinition
                {
                    Id = "review",
                    Name = "Review",
                    Markdown = "# Review\nBe precise.",
                    Targets = ManagedClientTargets.All,
                },
            ],
        };
        var synchronizer = new OfficialClientExtensionSynchronizer(fixture.Paths);

        await synchronizer.SynchronizeAsync(new WorkspaceFeatureState(), state);

        string codex = await File.ReadAllTextAsync(fixture.Paths.CodexConfigPath);
        Assert.Contains("model = \"gpt-test\"", codex);
        Assert.Contains("[other]", codex);
        Assert.Contains("[mcp_servers.\"fetch\"]", codex);
        Assert.Contains("BEGIN LANAI WORKSPACE MCP", codex);
        JsonObject claude = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(fixture.Paths.ClaudeConfigPath)));
        Assert.Equal("dark", claude["theme"]!.GetValue<string>());
        Assert.NotNull(claude["mcpServers"]?["fetch"]);
        JsonObject gemini = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(fixture.Paths.GeminiConfigPath)));
        Assert.True(gemini["general"]?["previewFeatures"]!.GetValue<bool>());
        Assert.NotNull(gemini["mcpServers"]?["fetch"]);
        Assert.Equal("# Review\nBe precise.\n", NormalizeNewlines(await File.ReadAllTextAsync(fixture.Paths.CodexPromptPath)));
        Assert.Equal("# Review\nBe precise.\n", NormalizeNewlines(await File.ReadAllTextAsync(fixture.Paths.ClaudePromptPath)));
        Assert.Equal("# Review\nBe precise.\n", NormalizeNewlines(await File.ReadAllTextAsync(fixture.Paths.GeminiPromptPath)));
    }

    [Fact]
    public async Task Synchronizer_RejectsPlaintextCredentialAndRestoresOriginalFiles()
    {
        using var fixture = new FeatureFixture();
        Directory.CreateDirectory(fixture.Paths.ClaudeHome);
        await File.WriteAllTextAsync(fixture.Paths.ClaudeConfigPath, "{\"keep\":true}");
        var state = new WorkspaceFeatureState
        {
            McpServers =
            [
                new McpServerDefinition
                {
                    Id = "remote",
                    Name = "Remote",
                    Transport = McpTransportKind.Http,
                    Url = "https://example.com/mcp",
                    Headers = new Dictionary<string, string> { ["Authorization"] = "Bearer secret" },
                    Targets = ManagedClientTargets.Claude,
                },
            ],
        };
        var synchronizer = new OfficialClientExtensionSynchronizer(fixture.Paths);

        InvalidOperationException exception = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            synchronizer.SynchronizeAsync(new WorkspaceFeatureState(), state));

        Assert.Contains("env:环境变量名", exception.Message);
        Assert.Equal("{\"keep\":true}", await File.ReadAllTextAsync(fixture.Paths.ClaudeConfigPath));
    }

    [Fact]
    public async Task Synchronizer_CopiesOnlyManagedSkillsAndRemovesThemWhenDisabled()
    {
        using var fixture = new FeatureFixture();
        fixture.Paths.EnsureWritableDirectories();
        string source = Path.Combine(fixture.Paths.ManagedSkillsDirectory, "demo");
        Directory.CreateDirectory(source);
        await File.WriteAllTextAsync(Path.Combine(source, "SKILL.md"), "# Demo");
        var skill = new ManagedSkillDefinition
        {
            Id = "demo",
            Name = "demo",
            StorageDirectoryName = "demo",
            Targets = ManagedClientTargets.Codex,
        };
        var enabled = new WorkspaceFeatureState { Skills = [skill] };
        var synchronizer = new OfficialClientExtensionSynchronizer(fixture.Paths);

        await synchronizer.SynchronizeAsync(new WorkspaceFeatureState(), enabled);
        string installed = Path.Combine(fixture.Paths.CodexSkillsDirectory, "demo");
        Assert.True(File.Exists(Path.Combine(installed, "SKILL.md")));
        Assert.True(File.Exists(Path.Combine(installed, ".lanai-managed.json")));

        await synchronizer.SynchronizeAsync(enabled, new WorkspaceFeatureState());
        Assert.False(Directory.Exists(installed));
    }

    private static WorkspaceFeatureState StateWithMcp(string id) => new()
    {
        McpServers =
        [
            new McpServerDefinition
            {
                Id = id,
                Name = id,
                Command = "cmd",
            },
        ],
    };

    private static string NormalizeNewlines(string value) => value.Replace("\r\n", "\n", StringComparison.Ordinal);

    private sealed class FeatureFixture : IDisposable
    {
        public FeatureFixture()
        {
            Root = Path.Combine(Path.GetTempPath(), "LanAi.Workspace.Features.Tests", Guid.NewGuid().ToString("N"));
            string profile = Path.Combine(Root, "profile");
            string local = Path.Combine(Root, "local");
            Directory.CreateDirectory(profile);
            Directory.CreateDirectory(local);
            Paths = new AppDataPaths(profile, local);
        }

        public string Root { get; }
        public AppDataPaths Paths { get; }

        public void Dispose()
        {
            if (Directory.Exists(Root)) Directory.Delete(Root, recursive: true);
        }
    }
}
