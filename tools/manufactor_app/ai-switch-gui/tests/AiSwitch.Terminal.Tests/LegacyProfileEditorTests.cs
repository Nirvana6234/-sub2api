using System.Text.Json.Nodes;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Terminal.Tests;

public sealed class LegacyProfileEditorTests
{
    [Fact]
    public async Task AddCloud_PreservesUnknownFieldsCreatesBackupAndRejectsLocalKinds()
    {
        using var fixture = new ProfileFixture();
        string original = fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        ConnectionProfileDraft draft = CreateDraft(
            "新增远程",
            ConnectionProfileKind.Cloud,
            codexSecret: ConnectionSecretChange.Replace("new-cloud-key"));

        ConnectionProfile added = await editor.AddAsync(draft);

        Assert.Equal(ConnectionProfileKind.Cloud, added.Kind);
        Assert.Equal("新增远程", added.Name);
        Assert.NotNull(added.ApiKeyCredentialId);
        JsonObject root = fixture.Read();
        Assert.True(root["FutureTop"]?["Keep"]?.GetValue<bool>());
        JsonObject addedNode = FindProfile(root, "CloudSources", added.Id);
        Assert.Equal("new-cloud-key", addedNode["Codex"]?["Secret"]?.GetValue<string>());
        Assert.Equal(original, File.ReadAllText(fixture.ProfilesPath + ".bak"));
        Assert.Empty(Directory.GetFiles(fixture.Root, "*.tmp"));

        await Assert.ThrowsAsync<InvalidOperationException>(() => editor.AddAsync(
            CreateDraft("不允许新增本机", ConnectionProfileKind.Local)));
        await Assert.ThrowsAsync<InvalidOperationException>(() => editor.AddAsync(
            CreateDraft("不允许新增局域网", ConnectionProfileKind.Lan)));
    }

    [Fact]
    public async Task UpdateFixed_EnforcesNameAndAppliesKeepReplaceClearWithoutReturningSecrets()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        var draft = new ConnectionProfileDraft(
            "试图改名",
            ConnectionProfileKind.Local,
            "更新后的备注",
            new ConnectionClientDraft("http://127.0.0.1:9000/v1", ConnectionSecretChange.Keep),
            new ConnectionClientDraft("http://127.0.0.1:9000", ConnectionSecretChange.Replace("new-claude-key")),
            new ConnectionClientDraft("http://127.0.0.1:9000", ConnectionSecretChange.Clear));

        ConnectionProfile updated = await editor.UpdateAsync(ConnectionProfileIds.LocalMachine, draft);

        Assert.Equal("本机中转", updated.Name);
        Assert.Equal(ConnectionProfileKind.Local, updated.Kind);
        Assert.DoesNotContain(
            typeof(ConnectionProfile).GetProperties(),
            property => property.Name.Contains("Secret", StringComparison.OrdinalIgnoreCase));
        JsonObject root = fixture.Read();
        JsonObject local = FindProfile(root, "LocalSources", ConnectionProfileIds.LocalMachine);
        Assert.Equal("本机中转", local["Name"]?.GetValue<string>());
        Assert.Equal("keep-codex-key", local["Codex"]?["Secret"]?.GetValue<string>());
        Assert.Equal("new-claude-key", local["Claude"]?["Secret"]?.GetValue<string>());
        Assert.Null(local["Gemini"]?["Secret"]);
        Assert.Equal("preserved", local["FutureProfile"]?.GetValue<string>());
        Assert.Equal(7, local["Codex"]?["FutureClient"]?.GetValue<int>());
        Assert.Equal("本机中转", root["Local"]?["Name"]?.GetValue<string>());

        await Assert.ThrowsAsync<ArgumentException>(() => Task.FromResult(
            ConnectionSecretChange.Replace("   ")));
    }

    [Fact]
    public async Task UpdateCloud_PreservesSecretWhenUiLeavesPasswordBlank()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        ConnectionProfileDraft draft = CreateDraft(
            "远程 A 已编辑",
            ConnectionProfileKind.Cloud,
            codexSecret: ConnectionSecretChange.Keep,
            claudeSecret: ConnectionSecretChange.Keep,
            geminiSecret: ConnectionSecretChange.Keep);

        await editor.UpdateAsync("cloud-a", draft);

        JsonObject cloud = FindProfile(fixture.Read(), "CloudSources", "cloud-a");
        Assert.Equal("cloud-a-codex-key", cloud["Codex"]?["Secret"]?.GetValue<string>());
        Assert.Equal("cloud-a-claude-key", cloud["Claude"]?["Secret"]?.GetValue<string>());
        Assert.Equal("cloud-a-gemini-key", cloud["Gemini"]?["Secret"]?.GetValue<string>());
        Assert.Equal("keep-profile-field", cloud["FutureProfile"]?.GetValue<string>());
    }

    [Fact]
    public async Task UpdateCloud_NormalizesVersionSuffixForEveryClient()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        var draft = new ConnectionProfileDraft(
            "远程 A",
            ConnectionProfileKind.Cloud,
            null,
            new ConnectionClientDraft("https://relay.example", ConnectionSecretChange.Keep),
            new ConnectionClientDraft("https://relay.example/v1/messages", ConnectionSecretChange.Keep),
            new ConnectionClientDraft("https://relay.example/v1beta/models", ConnectionSecretChange.Keep),
            GrokCli: new ConnectionClientDraft("https://relay.example/models", ConnectionSecretChange.Keep));

        ConnectionProfile updated = await editor.UpdateAsync("cloud-a", draft);

        Assert.Equal("https://relay.example/v1", updated.ClientBaseUrls[CliKind.Codex]);
        Assert.Equal("https://relay.example", updated.ClientBaseUrls[CliKind.ClaudeCode]);
        Assert.Equal("https://relay.example", updated.ClientBaseUrls[CliKind.GeminiCli]);
        Assert.Equal("https://relay.example/v1", updated.ClientBaseUrls[CliKind.GrokCli]);
    }

    [Fact]
    public async Task LanDashboard_UsesNativeMigrationUntilAnExplicitAddressIsSaved()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using (var reader = new LegacyProfileReader(fixture.ProfilesPath))
        {
            ConnectionProfile migrated = Assert.Single(
                await reader.GetAllAsync(),
                profile => profile.Id == ConnectionProfileIds.LanDefault);
            Assert.Equal("http://192.168.1.8:8080/dashboard", migrated.DashboardUrl);
        }

        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        ConnectionProfileDraft draft = CreateDraft("局域网中转", ConnectionProfileKind.Lan) with
        {
            DashboardUrl = "http://192.168.1.8:3100/admin",
        };

        ConnectionProfile updated = await editor.UpdateAsync(ConnectionProfileIds.LanDefault, draft);

        Assert.Equal("http://192.168.1.8:3100/admin", updated.DashboardUrl);
        JsonObject root = fixture.Read();
        Assert.Equal(
            "http://192.168.1.8:3100/admin",
            FindProfile(root, "LocalSources", ConnectionProfileIds.LanDefault)["DashboardUrl"]?.GetValue<string>());
        Assert.Equal(
            "http://192.168.1.8:3100/admin",
            root["Lan"]?["DashboardUrl"]?.GetValue<string>());
    }

    [Fact]
    public async Task LocalDashboard_PersistsAnExplicitCustomFrontendAddress()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);
        ConnectionProfileDraft draft = CreateDraft("本机中转", ConnectionProfileKind.Local) with
        {
            DashboardUrl = "http://127.0.0.1:3300/control",
        };

        ConnectionProfile updated = await editor.UpdateAsync(ConnectionProfileIds.LocalMachine, draft);

        Assert.Equal("http://127.0.0.1:3300/control", updated.DashboardUrl);
        JsonObject root = fixture.Read();
        Assert.Equal(
            "http://127.0.0.1:3300/control",
            FindProfile(root, "LocalSources", ConnectionProfileIds.LocalMachine)["DashboardUrl"]?.GetValue<string>());
        Assert.Equal(
            "http://127.0.0.1:3300/control",
            root["Local"]?["DashboardUrl"]?.GetValue<string>());
    }

    [Fact]
    public async Task Delete_RejectsFixedRemovesRemoteAndRepairsCloudSelection()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            editor.DeleteAsync(ConnectionProfileIds.LocalMachine));
        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            editor.DeleteAsync(ConnectionProfileIds.LanDefault));

        await editor.DeleteAsync("cloud-a");

        JsonObject root = fixture.Read();
        Assert.Null(TryFindProfile(root, "CloudSources", "cloud-a"));
        Assert.Equal("cloud-b", root["SelectedCloudSourceId"]?.GetValue<string>());
        Assert.Equal("cloud-b", root["Cloud"]?["Id"]?.GetValue<string>());
        Assert.True(root["Cloud"]?["AliasUnknown"]?.GetValue<bool>());
        Assert.Equal("cloud-b", root["Mixed"]?["CodexSourceId"]?.GetValue<string>());

        await editor.DeleteAsync("cloud-b");
        root = fixture.Read();
        Assert.Empty(root["CloudSources"]!.AsArray());
        Assert.Equal(string.Empty, root["SelectedCloudSourceId"]?.GetValue<string>());
        Assert.Null(root["Cloud"]);
        Assert.Equal(ConnectionProfileIds.LocalMachine, root["Mixed"]?["CodexSourceId"]?.GetValue<string>());
    }

    [Fact]
    public async Task Selection_PersistsCloudAndOnlyAllowsTwoFixedLocalSources()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);

        await editor.SetSelectedAsync(ConnectionProfileSelectionGroup.Cloud, "cloud-b");
        await editor.SetSelectedAsync(ConnectionProfileSelectionGroup.Local, ConnectionProfileIds.LanDefault);

        ConnectionProfileSelection selection = await editor.GetSelectionAsync();
        Assert.Equal("cloud-b", selection.CloudProfileId);
        Assert.Equal(ConnectionProfileIds.LanDefault, selection.LocalProfileId);
        Assert.Equal(ConnectionProfileIds.LanDefault, selection.ActiveProfileId);
        JsonObject root = fixture.Read();
        Assert.Equal("cloud-b", root["Cloud"]?["Id"]?.GetValue<string>());
        Assert.Equal(ConnectionProfileIds.LanDefault, root["Local"]?["Id"]?.GetValue<string>());
        Assert.Equal(ConnectionProfileIds.LanDefault, root["Lan"]?["Id"]?.GetValue<string>());
        Assert.Equal(ConnectionProfileIds.LanDefault, root["ActiveConnectionProfileId"]?.GetValue<string>());

        await Assert.ThrowsAsync<InvalidOperationException>(() =>
            editor.SetSelectedAsync(ConnectionProfileSelectionGroup.Local, "custom-local"));
        await Assert.ThrowsAsync<KeyNotFoundException>(() =>
            editor.SetSelectedAsync(ConnectionProfileSelectionGroup.Cloud, "missing-cloud"));
    }

    [Fact]
    public async Task Routing_PersistsPerClientSourcesAndRetainsUnknownMixedFields()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);

        ConnectionProfileRouting defaults = await editor.GetRoutingAsync();
        Assert.Equal("cloud-a", defaults.CodexProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, defaults.ClaudeCodeProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, defaults.GeminiCliProfileId);
        Assert.Equal(ConnectionProfileIds.LocalMachine, defaults.GrokCliProfileId);
        Assert.False(defaults.BackupUpstreamEnabled);

        var desired = new ConnectionProfileRouting(
            "cloud-b",
            ConnectionProfileIds.LanDefault,
            "cloud-a",
            "cloud-a",
            ["cloud-b", "cloud-a"],
            BackupUpstreamEnabled: true);
        await editor.SetRoutingAsync(desired);

        ConnectionProfileRouting reread = await editor.GetRoutingAsync();
        Assert.Equal(desired.CodexProfileId, reread.CodexProfileId);
        Assert.Equal(desired.ClaudeCodeProfileId, reread.ClaudeCodeProfileId);
        Assert.Equal(desired.GeminiCliProfileId, reread.GeminiCliProfileId);
        Assert.Equal(desired.GrokCliProfileId, reread.GrokCliProfileId);
        Assert.Equal(["cloud-b", "cloud-a"], reread.BackupProfileIds);
        Assert.True(reread.BackupUpstreamEnabled);
        Assert.True(fixture.Read()["BackupUpstreamEnabled"]?.GetValue<bool>());
        JsonObject mixed = fixture.Read()["Mixed"]!.AsObject();
        Assert.True(mixed["FutureMixed"]?.GetValue<bool>());
        Assert.Equal(0, mixed["CodexSource"]?.GetValue<int>());
        Assert.Equal(1, mixed["ClaudeSource"]?.GetValue<int>());

        await Assert.ThrowsAsync<InvalidOperationException>(() => editor.SetRoutingAsync(
            desired with { GeminiCliProfileId = "missing" }));
    }

    [Fact]
    public async Task Routing_MigratesExistingBackupListToEnabledWithoutLosingOrder()
    {
        using var fixture = new ProfileFixture();
        JsonObject document = JsonNode.Parse(DefaultDocument())!.AsObject();
        document["BackupSourceIds"] = new JsonArray("cloud-b", "cloud-a");
        fixture.Write(document.ToJsonString());
        using var editor = new LegacyProfileEditor(fixture.ProfilesPath);

        ConnectionProfileRouting routing = await editor.GetRoutingAsync();

        Assert.True(routing.BackupUpstreamEnabled);
        Assert.Equal(["cloud-b", "cloud-a"], routing.BackupProfileIds);
    }

    [Fact]
    public void Reader_ExposesOnlyTheNonSecretProfilesPath()
    {
        using var fixture = new ProfileFixture();
        using var reader = new LegacyProfileReader(fixture.ProfilesPath);

        Assert.Equal(Path.GetFullPath(fixture.ProfilesPath), reader.ProfilesPath);
    }

    [Fact]
    public async Task Reader_ProvidesMaskedCredentialHintsWithoutReturningTheSecret()
    {
        using var fixture = new ProfileFixture();
        fixture.Write(DefaultDocument());
        using var reader = new LegacyProfileReader(fixture.ProfilesPath);

        ConnectionProfile profile = Assert.Single(
            await reader.GetAllAsync(),
            candidate => candidate.Id == "cloud-a");
        ConnectionCredentialHint hint = profile.ClientCredentialHints[CliKind.Codex];

        Assert.StartsWith("clo", hint.MaskedPreview, StringComparison.Ordinal);
        Assert.DoesNotContain("cloud-a-codex-key", hint.MaskedPreview, StringComparison.Ordinal);
        Assert.Equal(12, hint.Fingerprint.Length);
        Assert.DoesNotContain(
            typeof(ConnectionCredentialHint).GetProperties(),
            property => property.Name.Contains("Secret", StringComparison.OrdinalIgnoreCase));
    }

    private static ConnectionProfileDraft CreateDraft(
        string name,
        ConnectionProfileKind kind,
        ConnectionSecretChange? codexSecret = null,
        ConnectionSecretChange? claudeSecret = null,
        ConnectionSecretChange? geminiSecret = null) => new(
        name,
        kind,
        "测试备注",
        new ConnectionClientDraft("https://example.test/v1", codexSecret ?? ConnectionSecretChange.Keep),
        new ConnectionClientDraft("https://example.test", claudeSecret ?? ConnectionSecretChange.Keep),
        new ConnectionClientDraft("https://example.test", geminiSecret ?? ConnectionSecretChange.Keep));

    private static JsonObject FindProfile(JsonObject root, string collectionName, string id) =>
        TryFindProfile(root, collectionName, id)
        ?? throw new Xunit.Sdk.XunitException($"Missing profile {id} in {collectionName}.");

    private static JsonObject? TryFindProfile(JsonObject root, string collectionName, string id) =>
        root[collectionName]!.AsArray()
            .OfType<JsonObject>()
            .FirstOrDefault(profile => string.Equals(
                profile["Id"]?.GetValue<string>(),
                id,
                StringComparison.OrdinalIgnoreCase));

    private static string DefaultDocument() =>
        """
        {
          "FutureTop": { "Keep": true },
          "Cloud": {
            "Id": "cloud-a",
            "Name": "远程 A",
            "AliasUnknown": true,
            "Codex": { "BaseUrl": "https://a.example/v1", "Secret": "cloud-a-codex-key" },
            "Claude": { "BaseUrl": "https://a.example", "Secret": "cloud-a-claude-key" },
            "Gemini": { "BaseUrl": "https://a.example", "Secret": "cloud-a-gemini-key" }
          },
          "CloudSources": [
            {
              "Id": "cloud-a",
              "Name": "远程 A",
              "Notes": "A",
              "FutureProfile": "keep-profile-field",
              "Codex": { "BaseUrl": "https://a.example/v1", "Secret": "cloud-a-codex-key" },
              "Claude": { "BaseUrl": "https://a.example", "Secret": "cloud-a-claude-key" },
              "Gemini": { "BaseUrl": "https://a.example", "Secret": "cloud-a-gemini-key" }
            },
            {
              "Id": "cloud-b",
              "Name": "远程 B",
              "Notes": "B",
              "Codex": { "BaseUrl": "https://b.example/v1" },
              "Claude": { "BaseUrl": "https://b.example" },
              "Gemini": { "BaseUrl": "https://b.example" }
            }
          ],
          "SelectedCloudSourceId": "cloud-a",
          "Local": {
            "Id": "local-machine",
            "Name": "本机中转",
            "AliasLocalUnknown": 1,
            "Codex": { "BaseUrl": "http://127.0.0.1:8080/v1", "Secret": "keep-codex-key" },
            "Claude": { "BaseUrl": "http://127.0.0.1:8080", "Secret": "old-claude-key" },
            "Gemini": { "BaseUrl": "http://127.0.0.1:8080", "Secret": "old-gemini-key" }
          },
          "Lan": {
            "Id": "lan-default",
            "Name": "局域网中转",
            "Codex": { "BaseUrl": "http://192.168.1.8:8080/v1" },
            "Claude": { "BaseUrl": "http://192.168.1.8:8080" },
            "Gemini": { "BaseUrl": "http://192.168.1.8:8080" }
          },
          "LocalSources": [
            {
              "Id": "local-machine",
              "Name": "本机中转",
              "Notes": "local",
              "FutureProfile": "preserved",
              "Codex": {
                "BaseUrl": "http://127.0.0.1:8080/v1",
                "Secret": "keep-codex-key",
                "FutureClient": 7
              },
              "Claude": { "BaseUrl": "http://127.0.0.1:8080", "Secret": "old-claude-key" },
              "Gemini": { "BaseUrl": "http://127.0.0.1:8080", "Secret": "old-gemini-key" }
            },
            {
              "Id": "lan-default",
              "Name": "局域网中转",
              "Notes": "lan",
              "Codex": { "BaseUrl": "http://192.168.1.8:8080/v1" },
              "Claude": { "BaseUrl": "http://192.168.1.8:8080" },
              "Gemini": { "BaseUrl": "http://192.168.1.8:8080" }
            },
            {
              "Id": "custom-local",
              "Name": "旧自定义本地",
              "Codex": { "BaseUrl": "http://192.168.1.9:8080/v1" }
            }
          ],
          "SelectedLocalSourceId": "local-machine",
          "Mixed": { "FutureMixed": true }
        }
        """;

    private sealed class ProfileFixture : IDisposable
    {
        public ProfileFixture()
        {
            Root = Path.Combine(
                Path.GetTempPath(),
                "LanAi.LegacyProfileEditor.Tests",
                Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
            ProfilesPath = Path.Combine(Root, "profiles.json");
        }

        public string Root { get; }

        public string ProfilesPath { get; }

        public string Write(string json)
        {
            File.WriteAllText(ProfilesPath, json);
            return json;
        }

        public JsonObject Read() =>
            JsonNode.Parse(File.ReadAllText(ProfilesPath))!.AsObject();

        public void Dispose()
        {
            string fullRoot = Path.GetFullPath(Root);
            string safeParent = Path.GetFullPath(
                Path.Combine(Path.GetTempPath(), "LanAi.LegacyProfileEditor.Tests"));
            if (fullRoot.StartsWith(safeParent, StringComparison.OrdinalIgnoreCase) && Directory.Exists(fullRoot))
            {
                Directory.Delete(fullRoot, recursive: true);
            }
        }
    }
}

