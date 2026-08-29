using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// These two files belong to the user. Most of what follows is about what must
/// survive the write, not about what it puts there.
/// </summary>
public sealed class CodexConfigWriterTests : IDisposable
{
    private readonly string _home = Path.Combine(Path.GetTempPath(), $"codex-home-{Guid.NewGuid():N}");
    private readonly string _snapshotRoot = Path.Combine(Path.GetTempPath(), $"codex-snapshot-{Guid.NewGuid():N}");
    private readonly string _legacySnapshotPath;
    private readonly CodexPaths _paths;
    private readonly FakeSnapshotProtector _protector = new();
    private readonly CodexConfigWriter _writer;

    public CodexConfigWriterTests()
    {
        _paths = new CodexPaths(_home);
        _legacySnapshotPath = Path.Combine(_snapshotRoot, "legacy-auth.json");
        _writer = new CodexConfigWriter(
            _paths,
            new CodexAuthSnapshot(_protector, _legacySnapshotPath),
            new CodexFileSnapshot(_paths, _snapshotRoot, _protector));
    }

    public void Dispose()
    {
        if (Directory.Exists(_home))
        {
            Directory.Delete(_home, recursive: true);
        }

        if (Directory.Exists(_snapshotRoot))
        {
            Directory.Delete(_snapshotRoot, recursive: true);
        }
    }

    private void GivenAuth(string json)
    {
        Directory.CreateDirectory(_home);
        File.WriteAllText(_paths.AuthPath, json);
    }

    private void GivenConfig(string toml)
    {
        Directory.CreateDirectory(_home);
        File.WriteAllText(_paths.ConfigPath, toml);
    }

    private JsonObject Auth() => (JsonObject)JsonNode.Parse(File.ReadAllText(_paths.AuthPath))!;

    private string Config() => File.ReadAllText(_paths.ConfigPath);

    private void GivenLegacyCompleteSnapshot(string auth, string config)
    {
        Directory.CreateDirectory(_snapshotRoot);
        File.WriteAllText(Path.Combine(_snapshotRoot, "auth.bin"), auth);
        File.WriteAllText(Path.Combine(_snapshotRoot, "config.bin"), config);
        File.WriteAllText(
            Path.Combine(_snapshotRoot, "manifest.json"),
            "{\"AuthExisted\":true,\"ConfigExisted\":true}");
    }

    [Fact]
    public void TheApiKeyIsWrittenWhereCodexLooksForIt()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Equal("sk-relay", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
    }

    [Fact]
    public void TheAccountSessionIsRemovedSoTheRelayKeyActuallyTakesEffect()
    {
        // Codex picks its credential by what the file holds: a tokens object means
        // "use the ChatGPT account" and wins over any key beside it. Leaving them
        // together produced a config that reported success and kept billing the
        // user's ChatGPT plan — the exact failure this client exists to prevent.
        GivenAuth("""{"OPENAI_API_KEY":null,"auth_mode":"chatgpt","tokens":{"access_token":"chatgpt-token"}}""");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        JsonObject auth = Auth();
        Assert.Equal("sk-relay", auth["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.False(auth.ContainsKey("tokens"));
        Assert.False(auth.ContainsKey("auth_mode"));
    }

    [Fact]
    public void TheAccountSessionIsKeptSafeSoItCanBeGivenBack()
    {
        // Removing it is only defensible because the client is holding it for them.
        GivenAuth("""{"auth_mode":"chatgpt","tokens":{"access_token":"chatgpt-token"},"last_refresh":"2026-07-01"}""");

        _writer.Apply("sk-relay", "https://relay.test/v1");
        Assert.True(_writer.RestoreOriginalAuth());

        JsonObject auth = Auth();
        Assert.Equal("chatgpt", auth["auth_mode"]!.GetValue<string>());
        Assert.Equal("chatgpt-token", auth["tokens"]!["access_token"]!.GetValue<string>());
        Assert.Equal("2026-07-01", auth["last_refresh"]!.GetValue<string>());
        Assert.False(auth.ContainsKey("OPENAI_API_KEY"));
    }

    [Fact]
    public void ASecondRunDoesNotOverwriteTheSavedSessionWithItsOwnKey()
    {
        // The second Apply sees a file this client already wrote. Capturing that as
        // "the user's original" would lose their ChatGPT login for good.
        GivenAuth("""{"auth_mode":"chatgpt","tokens":{"access_token":"chatgpt-token"}}""");

        _writer.Apply("sk-one", "https://relay.test/v1");
        _writer.Apply("sk-two", "https://relay.test/v1");
        Assert.True(_writer.RestoreOriginalAuth());

        Assert.Equal("chatgpt-token", Auth()["tokens"]!["access_token"]!.GetValue<string>());
    }

    [Fact]
    public void ApplyCapturesBothFilesByteForByteOnlyOnce()
    {
        byte[] originalAuth = [0x7B, 0x22, 0x78, 0x22, 0x3A, 0x31, 0x7D];
        byte[] originalConfig = [0xEF, 0xBB, 0xBF, 0x23, 0x20, 0x75, 0x73, 0x65, 0x72, 0x0D, 0x0A];
        Directory.CreateDirectory(_home);
        File.WriteAllBytes(_paths.AuthPath, originalAuth);
        File.WriteAllBytes(_paths.ConfigPath, originalConfig);

        _writer.Apply("sk-one", "https://relay.test/v1");
        _writer.Apply("sk-two", "https://relay.test/v2");

        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal(originalAuth, File.ReadAllBytes(_paths.AuthPath));
        Assert.Equal(originalConfig, File.ReadAllBytes(_paths.ConfigPath));
    }

    [Fact]
    public void RestoreRemovesFilesThatDidNotExistBeforeApply()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.RestoreOriginalFiles());
        Assert.False(File.Exists(_paths.AuthPath));
        Assert.False(File.Exists(_paths.ConfigPath));
    }

    [Fact]
    public void ASecondWriterCannotOverwriteTheOriginalSnapshot()
    {
        GivenAuth("original-auth");
        GivenConfig("original-config");

        _writer.Apply("sk-one", "https://relay.test/v1");
        File.WriteAllText(_paths.AuthPath, "changed-auth");
        File.WriteAllText(_paths.ConfigPath, "changed-config");

        var secondWriter = new CodexConfigWriter(
            _paths,
            new CodexAuthSnapshot(
                new FakeSnapshotProtector(),
                Path.Combine(_snapshotRoot, "second-legacy-auth.json")),
            new CodexFileSnapshot(_paths, _snapshotRoot, new FakeSnapshotProtector()));
        secondWriter.Apply("sk-two", "https://relay.test/v2");

        Assert.True(secondWriter.RestoreOriginalFiles());
        Assert.Equal("original-auth", File.ReadAllText(_paths.AuthPath));
        Assert.Equal("original-config", File.ReadAllText(_paths.ConfigPath));
    }

    [Fact]
    public void RestoringCompleteFilesTwiceIsANoOpTheSecondTime()
    {
        GivenAuth("original-auth");
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.RestoreOriginalFiles());
        Assert.False(_writer.RestoreOriginalFiles());
        Assert.Equal("original-auth", File.ReadAllText(_paths.AuthPath));
    }

    [Fact]
    public void RestoringCompleteFilesClearsTheLegacyCredentialSnapshot()
    {
        GivenAuth("{\"tokens\":{\"access_token\":\"original\"}}");
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(File.Exists(_legacySnapshotPath));
        Assert.True(_writer.RestoreOriginalFiles());
        Assert.False(File.Exists(_legacySnapshotPath));
    }

    [Fact]
    public void CompleteSnapshotEncryptsBothFilesAndRestoresExactBytes()
    {
        byte[] originalAuth = Encoding.UTF8.GetBytes(
            "{\"tokens\":{\"refresh_token\":\"oauth-secret\"}}");
        byte[] originalConfig = Encoding.UTF8.GetBytes(
            "[mcp_servers.demo]\nenv = { TOKEN = \"mcp-secret\" }");
        Directory.CreateDirectory(_home);
        File.WriteAllBytes(_paths.AuthPath, originalAuth);
        File.WriteAllBytes(_paths.ConfigPath, originalConfig);

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.DoesNotContain(
            "oauth-secret",
            Encoding.UTF8.GetString(
                File.ReadAllBytes(Path.Combine(_snapshotRoot, "auth.bin"))),
            StringComparison.Ordinal);
        Assert.DoesNotContain(
            "mcp-secret",
            Encoding.UTF8.GetString(
                File.ReadAllBytes(Path.Combine(_snapshotRoot, "config.bin"))),
            StringComparison.Ordinal);
        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal(originalAuth, File.ReadAllBytes(_paths.AuthPath));
        Assert.Equal(originalConfig, File.ReadAllBytes(_paths.ConfigPath));
    }

    [Fact]
    public void ExistingPlaintextCompleteSnapshotIsMigratedBeforeApplyContinues()
    {
        GivenLegacyCompleteSnapshot("old-auth", "old-config");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.DoesNotContain(
            "old-auth",
            Encoding.UTF8.GetString(
                File.ReadAllBytes(Path.Combine(_snapshotRoot, "auth.bin"))),
            StringComparison.Ordinal);
        Assert.DoesNotContain(
            "old-config",
            Encoding.UTF8.GetString(
                File.ReadAllBytes(Path.Combine(_snapshotRoot, "config.bin"))),
            StringComparison.Ordinal);
        using JsonDocument manifest = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(_snapshotRoot, "manifest.json")));
        Assert.Equal(1, manifest.RootElement.GetProperty("ProtectionVersion").GetInt32());
    }

    [Fact]
    public void PartiallyMigratedSnapshotFinishesWithoutDoubleProtecting()
    {
        GivenLegacyCompleteSnapshot("old-auth", "old-config");
        byte[] authPlain = File.ReadAllBytes(Path.Combine(_snapshotRoot, "auth.bin"));
        File.WriteAllBytes(
            Path.Combine(_snapshotRoot, "auth.bin"),
            SnapshotBlobFormat.Protect(authPlain, _protector));
        int callsBefore = _protector.ProtectCallCount;

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Equal(callsBefore + 1, _protector.ProtectCallCount);
        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal("old-auth", File.ReadAllText(_paths.AuthPath));
        Assert.Equal("old-config", File.ReadAllText(_paths.ConfigPath));
    }

    [Fact]
    public void LegacyPlaintextSnapshotCanRestoreWithoutASecondApply()
    {
        GivenLegacyCompleteSnapshot("old-auth", "old-config");

        Assert.True(_writer.RestoreOriginalFiles());

        Assert.Equal("old-auth", File.ReadAllText(_paths.AuthPath));
        Assert.Equal("old-config", File.ReadAllText(_paths.ConfigPath));
    }

    [Fact]
    public void ProtectedManifestWithPlaintextDataNeverOverwritesLiveFiles()
    {
        GivenAuth("{\"OPENAI_API_KEY\":\"live\"}");
        GivenConfig("model = \"live\"");
        GivenLegacyCompleteSnapshot("snapshot-auth", "snapshot-config");
        File.WriteAllText(
            Path.Combine(_snapshotRoot, "manifest.json"),
            "{\"AuthExisted\":true,\"ConfigExisted\":true,\"ProtectionVersion\":1}");

        Assert.Throws<InvalidDataException>(() => _writer.RestoreOriginalFiles());
        Assert.Equal("live", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"live\"", Config());
    }

    [Fact]
    public void UnknownManifestProtectionVersionIsRejected()
    {
        GivenLegacyCompleteSnapshot("snapshot-auth", "snapshot-config");
        File.WriteAllText(
            Path.Combine(_snapshotRoot, "manifest.json"),
            "{\"AuthExisted\":true,\"ConfigExisted\":true,\"ProtectionVersion\":99}");

        Assert.Throws<InvalidDataException>(() => _writer.RestoreOriginalFiles());
    }

    [Fact]
    public void ProtectFailureDoesNotCommitManifestOrChangeLiveFiles()
    {
        GivenAuth("{\"OPENAI_API_KEY\":\"live\"}");
        GivenConfig("model = \"live\"");
        _protector.FailProtect = true;

        Assert.Throws<CryptographicException>(() =>
            _writer.Apply("sk-relay", "https://relay.test/v1"));

        Assert.False(File.Exists(Path.Combine(_snapshotRoot, "manifest.json")));
        Assert.Equal("live", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"live\"", Config());
    }

    [Fact]
    public void DamagedCiphertextCannotPartiallyRestoreLiveFiles()
    {
        GivenAuth("{\"tokens\":{\"refresh_token\":\"original\"}}");
        GivenConfig("model = \"original\"");
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth("{\"OPENAI_API_KEY\":\"current-live\"}");
        GivenConfig("model = \"current-live\"");
        string authSnapshotPath = Path.Combine(_snapshotRoot, "auth.bin");
        byte[] damaged = File.ReadAllBytes(authSnapshotPath);
        damaged[^1] ^= 0x01;
        File.WriteAllBytes(authSnapshotPath, damaged);

        Assert.Throws<InvalidDataException>(() => _writer.RestoreOriginalFiles());
        Assert.Equal("current-live", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"current-live\"", Config());
    }

    [Fact]
    public void ADamagedSnapshotManifestPreventsOverwritingLiveFiles()
    {
        GivenAuth("{\"OPENAI_API_KEY\":\"sk-user\"}");
        GivenConfig("model = \"gpt-user\"");
        Directory.CreateDirectory(_snapshotRoot);
        File.WriteAllText(Path.Combine(_snapshotRoot, "manifest.json"), "{ damaged");

        Assert.Throws<InvalidDataException>(() =>
            _writer.Apply("sk-relay", "https://relay.test/v1"));

        Assert.Equal("sk-user", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"gpt-user\"", Config());
    }

    [Fact]
    public void CaptureReplacesInterruptedTemporarySnapshotFiles()
    {
        GivenAuth("{\"OPENAI_API_KEY\":\"sk-user\"}");
        GivenConfig("model = \"gpt-user\"");
        Directory.CreateDirectory(_snapshotRoot);
        File.WriteAllText(Path.Combine(_snapshotRoot, "auth.bin.tmp"), "stale");
        File.WriteAllText(Path.Combine(_snapshotRoot, "config.bin.tmp"), "stale");
        File.WriteAllText(Path.Combine(_snapshotRoot, "manifest.json.tmp"), "stale");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Empty(Directory.GetFiles(_snapshotRoot, "*.tmp"));
        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal("sk-user", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"gpt-user\"", Config());
    }

    [Fact]
    public void RestoreReplacesInterruptedTemporaryLiveFiles()
    {
        GivenAuth("{\"OPENAI_API_KEY\":\"sk-user\"}");
        GivenConfig("model = \"gpt-user\"");
        _writer.Apply("sk-relay", "https://relay.test/v1");
        File.WriteAllText(_paths.AuthPath + ".tmp", "stale");
        File.WriteAllText(_paths.ConfigPath + ".tmp", "stale");

        Assert.True(_writer.RestoreOriginalFiles());

        Assert.False(File.Exists(_paths.AuthPath + ".tmp"));
        Assert.False(File.Exists(_paths.ConfigPath + ".tmp"));
        Assert.Equal("sk-user", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
        Assert.Equal("model = \"gpt-user\"", Config());
    }

    [Fact]
    public void RestoringWithNothingSavedLeavesTheFileAlone()
    {
        // Better to leave a working configuration in place than to overwrite it
        // with something invented.
        GivenAuth("""{"OPENAI_API_KEY":"sk-user-own"}""");

        Assert.False(_writer.RestoreOriginalAuth());
        Assert.Equal("sk-user-own", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
    }

    [Fact]
    public void LegacyCredentialSnapshotIsEncryptedAndRestored()
    {
        GivenAuth("{\"tokens\":{\"refresh_token\":\"oauth-secret\"}}");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        byte[] stored = File.ReadAllBytes(_legacySnapshotPath);
        Assert.DoesNotContain(
            "oauth-secret",
            Encoding.UTF8.GetString(stored),
            StringComparison.Ordinal);
        Assert.True(_writer.RestoreOriginalAuth());
        Assert.Equal("oauth-secret", Auth()["tokens"]!["refresh_token"]!.GetValue<string>());
    }

    [Fact]
    public void PlaintextLegacyCredentialSnapshotIsMigratedAfterAValidRead()
    {
        Directory.CreateDirectory(_snapshotRoot);
        File.WriteAllText(
            _legacySnapshotPath,
            "{\"tokens\":{\"refresh_token\":\"legacy-secret\"}}");
        var snapshot = new CodexAuthSnapshot(_protector, _legacySnapshotPath);

        JsonObject? restored = snapshot.Read();

        Assert.Equal("legacy-secret", restored!["tokens"]!["refresh_token"]!.GetValue<string>());
        Assert.DoesNotContain(
            "legacy-secret",
            File.ReadAllText(_legacySnapshotPath),
            StringComparison.Ordinal);
        Assert.Equal(1, _protector.ProtectCallCount);
    }

    [Fact]
    public void DamagedProtectedCredentialSnapshotIsNotReturnedOrRewritten()
    {
        var snapshot = new CodexAuthSnapshot(_protector, _legacySnapshotPath);
        snapshot.CaptureOnce(
            (JsonObject)JsonNode.Parse("{\"tokens\":{\"refresh_token\":\"secret\"}}")!);
        byte[] damaged = File.ReadAllBytes(_legacySnapshotPath);
        damaged[^1] ^= 0x01;
        File.WriteAllBytes(_legacySnapshotPath, damaged);

        Assert.Null(snapshot.Read());
        Assert.Equal(damaged, File.ReadAllBytes(_legacySnapshotPath));
    }

    [Fact]
    public void ProtectedConvenienceConstructorUsesTheInjectedProtector()
    {
        var protector = new FakeSnapshotProtector();
        var writer = new CodexConfigWriter(
            _paths,
            protector,
            snapshotRoot: _snapshotRoot,
            legacySnapshotPath: _legacySnapshotPath);
        GivenAuth("{\"tokens\":{\"refresh_token\":\"constructor-secret\"}}");

        writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(protector.ProtectCallCount >= 2);
        Assert.DoesNotContain(
            "constructor-secret",
            Encoding.UTF8.GetString(
                File.ReadAllBytes(Path.Combine(_snapshotRoot, "auth.bin"))),
            StringComparison.Ordinal);
    }

    [Fact]
    public void AMissingAuthFileIsCreatedRatherThanFailing()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(File.Exists(_paths.AuthPath));
    }

    [Fact]
    public void ADamagedAuthFileDoesNotBlockSetup()
    {
        // Nothing can be merged into an unparseable file. Losing its contents is
        // bad; leaving the user unable to use the client at all is worse.
        GivenAuth("{ this is not json");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Equal("sk-relay", Auth()["OPENAI_API_KEY"]!.GetValue<string>());
    }

    [Fact]
    public void TheProviderIsSelectedAndDefined()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        string config = Config();
        Assert.Contains("model_provider = \"gongfei\"", config, StringComparison.Ordinal);
        Assert.Contains("[model_providers.gongfei]", config, StringComparison.Ordinal);
        Assert.Contains("base_url = \"https://relay.test/v1\"", config, StringComparison.Ordinal);
        Assert.Contains("wire_api = \"responses\"", config, StringComparison.Ordinal);
        Assert.Contains("requires_openai_auth = true", config, StringComparison.Ordinal);
    }

    [Fact]
    public void ANewConfigDefaultsReasoningEffortToMedium()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Contains("model_reasoning_effort = \"medium\"", Config(), StringComparison.Ordinal);
    }

    [Fact]
    public void TheUsersOwnTopLevelSettingsSurvive()
    {
        // The client routes traffic; it has no business overriding which model the
        // user chose or how hard they asked it to think.
        GivenConfig("""
            model = "gpt-5.6-sol"
            model_reasoning_effort = "high"
            model_provider = "something-else"
            """);

        _writer.Apply("sk-relay", "https://relay.test/v1");

        string config = Config();
        Assert.Contains("model = \"gpt-5.6-sol\"", config, StringComparison.Ordinal);
        Assert.Contains("model_reasoning_effort = \"high\"", config, StringComparison.Ordinal);
        Assert.Contains("model_provider = \"gongfei\"", config, StringComparison.Ordinal);
        Assert.DoesNotContain("something-else", config, StringComparison.Ordinal);
    }

    [Fact]
    public void APreferredClaudeModelReplacesOnlyTheTopLevelModel()
    {
        GivenConfig("""
            model = "gpt-5.6-sol"
            review_model = "gpt-5.4"
            model_reasoning_effort = "high"
            model_context_window = 1000000
            model_provider = "something-else"
            """);

        _writer.Apply("sk-relay", "https://relay.test/v1", "claude-sonnet-5");

        string config = Config();
        Assert.Contains("model = \"claude-sonnet-5\"", config, StringComparison.Ordinal);
        Assert.DoesNotContain("model = \"gpt-5.6-sol\"", config, StringComparison.Ordinal);
        Assert.Contains("review_model = \"gpt-5.4\"", config, StringComparison.Ordinal);
        Assert.Contains("model_reasoning_effort = \"high\"", config, StringComparison.Ordinal);
        Assert.Contains("model_context_window = 1000000", config, StringComparison.Ordinal);
    }

    [Fact]
    public void OtherSectionsSurviveWithTheirComments()
    {
        // MCP servers and profiles are the user's work. Losing them to a routing
        // change would be a far bigger loss than the feature is worth.
        GivenConfig("""
            model = "gpt-5.6-sol"

            [mcp_servers.blender]
            command = "uvx"
            # 用户自己加的备注
            args = ["blender-mcp"]

            [profiles.work]
            approval_policy = "on-request"
            """);

        _writer.Apply("sk-relay", "https://relay.test/v1");

        string config = Config();
        Assert.Contains("[mcp_servers.blender]", config, StringComparison.Ordinal);
        Assert.Contains("command = \"uvx\"", config, StringComparison.Ordinal);
        Assert.Contains("# 用户自己加的备注", config, StringComparison.Ordinal);
        Assert.Contains("[profiles.work]", config, StringComparison.Ordinal);
        Assert.Contains("approval_policy = \"on-request\"", config, StringComparison.Ordinal);
    }

    [Fact]
    public void ReapplyingDoesNotAccumulateDuplicateSections()
    {
        // The client rewrites this on every launch. A duplicate table name is a
        // TOML parse error, which would break Codex outright.
        _writer.Apply("sk-one", "https://relay.test/v1");
        _writer.Apply("sk-two", "https://relay.test/v2");

        string config = Config();
        Assert.Equal(1, CountOccurrences(config, "[model_providers.gongfei]"));
        Assert.Equal(1, CountOccurrences(config, "model_provider = "));
        Assert.Contains("https://relay.test/v2", config, StringComparison.Ordinal);
        Assert.DoesNotContain("https://relay.test/v1", config, StringComparison.Ordinal);
    }

    [Fact]
    public void TheLiveRouteMatchesOnlyWhenProviderAndBaseUrlBothMatch()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.IsRelayRoute("https://relay.test/v1"));
        Assert.False(_writer.IsRelayRoute("https://other.test/v1"));
    }

    [Fact]
    public void ARewrittenAuthFileMakesTheManagedRouteStale()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth("{\"OPENAI_API_KEY\":\"sk-other\"}");

        Assert.False(_writer.IsRelayRoute("https://relay.test/v1", "sk-relay"));
    }

    [Fact]
    public void AuthWithOAuthMaterialIsNotConsideredTheManagedRoute()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth("{\"OPENAI_API_KEY\":\"sk-relay\",\"tokens\":{\"access_token\":\"oauth\"}}");

        Assert.False(_writer.IsRelayRoute("https://relay.test/v1", "sk-relay"));
    }

    [Fact]
    public void AnOfficialProviderRewriteIsDetected()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenConfig(Config().Replace(
            "model_provider = \"gongfei\"",
            "model_provider = \"openai\"",
            StringComparison.Ordinal));

        Assert.False(_writer.IsRelayRoute("https://relay.test/v1"));
    }

    [Fact]
    public void ARelaySectionRewriteIsDetected()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenConfig(Config().Replace(
            "base_url = \"https://relay.test/v1\"",
            "base_url = \"https://changed.test/v1\"",
            StringComparison.Ordinal));

        Assert.False(_writer.IsRelayRoute("https://relay.test/v1"));
    }

    [Fact]
    public void AProviderKeyInsideAnotherSectionIsNotTheTopLevelSelector()
    {
        GivenConfig("""
            [profile.work]
            model_provider = "gongfei"

            [model_providers.gongfei]
            base_url = "https://relay.test/v1"
            """);

        Assert.False(_writer.IsRelayRoute("https://relay.test/v1"));
    }

    [Fact]
    public void AKeyThatMerelyStartsTheSameWayIsNotRemoved()
    {
        GivenConfig("""
            model_provider_extra = "keep me"
            """);

        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Contains("model_provider_extra = \"keep me\"", Config(), StringComparison.Ordinal);
    }

    [Fact]
    public void QuotesInTheBaseUrlCannotBreakOutOfTheValue()
    {
        _writer.Apply("sk-relay", """https://relay.test/"evil""");

        Assert.Contains(""""base_url = "https://relay.test/\"evil"""", Config(), StringComparison.Ordinal);
    }

    [Fact]
    public void TheWrittenAuthFileIsValidJson()
    {
        GivenAuth("""{"tokens":{"access_token":"t"}}""");

        _writer.Apply("sk-relay", "https://relay.test/v1");

        JsonDocument.Parse(File.ReadAllText(_paths.AuthPath));
    }

    [Fact]
    public void NoTemporaryFilesAreLeftBehind()
    {
        // Codex reads these files; a stray .tmp beside them is at best confusing.
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Empty(Directory.GetFiles(_home, "*.tmp"));
    }

    private static int CountOccurrences(string haystack, string needle)
    {
        int count = 0;
        int index = 0;
        while ((index = haystack.IndexOf(needle, index, StringComparison.Ordinal)) >= 0)
        {
            count++;
            index += needle.Length;
        }

        return count;
    }
}
