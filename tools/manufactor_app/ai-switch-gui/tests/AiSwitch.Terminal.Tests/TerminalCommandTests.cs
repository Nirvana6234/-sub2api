using LanAi.Workspace.Terminal;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Terminal.Tests;

public sealed class TerminalCommandTests
{
    [Fact]
    public void BuildCommandLine_QuotesWhitespaceAndEmbeddedQuotes()
    {
        var command = new TerminalCommand(
            @"C:\Program Files\Tool\tool.exe",
            ["--name", "局域网 工作台", "say \"hello\""],
            Environment.CurrentDirectory);

        var actual = command.BuildCommandLine();

        Assert.Contains("\"C:\\Program Files\\Tool\\tool.exe\"", actual);
        Assert.Contains("\"局域网 工作台\"", actual);
        Assert.Contains("\"say \\\"hello\\\"\"", actual);
    }

    [Fact]
    public async Task Factory_UsesProcessEnvironmentForSecret()
    {
        var credentials = new StubCredentials("secret-value");
        var factory = new CliTerminalCommandFactory(credentials);
        var request = new LanAi.Workspace.Core.CliLaunchRequest
        {
            ProjectId = "p1",
            Cli = LanAi.Workspace.Core.CliKind.ClaudeCode,
            WorkingDirectory = Environment.CurrentDirectory,
            ConnectionProfileId = "lan"
        };
        var installation = new LanAi.Workspace.Core.CliInstallation
        {
            Kind = LanAi.Workspace.Core.CliKind.ClaudeCode,
            IsInstalled = true,
            ExecutablePath = @"C:\tools\claude.exe",
            DetectedAt = DateTimeOffset.UtcNow
        };
        var connection = new LanAi.Workspace.Core.ConnectionProfile
        {
            Id = "lan",
            Name = "局域网中转",
            BaseUrl = "http://192.168.1.2:8080"
        };

        var command = await factory.CreateAsync(request, installation, connection);

        Assert.Equal("secret-value", command.Environment!["ANTHROPIC_AUTH_TOKEN"]);
        Assert.DoesNotContain("secret-value", command.BuildCommandLine(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task Factory_KeyOnlyConnection_DoesNotOverrideClaudeEndpoint()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("secret-value"));
        var connection = CreateConnection(baseUrl: string.Empty);

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.ClaudeCode),
            CreateInstallation(CliKind.ClaudeCode),
            connection);

        Assert.Equal("secret-value", command.Environment!["ANTHROPIC_AUTH_TOKEN"]);
        Assert.Equal("secret-value", command.Environment["ANTHROPIC_API_KEY"]);
        Assert.DoesNotContain("ANTHROPIC_BASE_URL", command.Environment.Keys);
        Assert.DoesNotContain("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", command.Environment.Keys);
    }

    [Fact]
    public async Task Factory_MissingClientUrl_DoesNotFallBackToAnotherClientsAddress()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("claude-secret"));
        var connection = CreateConnection(
            baseUrl: "https://codex.example.test/v1",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = "https://codex.example.test/v1",
            });

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.ClaudeCode),
            CreateInstallation(CliKind.ClaudeCode),
            connection);

        Assert.Equal("claude-secret", command.Environment!["ANTHROPIC_AUTH_TOKEN"]);
        Assert.DoesNotContain("ANTHROPIC_BASE_URL", command.Environment.Keys);
        Assert.DoesNotContain("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", command.Environment.Keys);
    }

    [Fact]
    public async Task Factory_ExplicitBlankClientUrl_DoesNotFallBackToSharedAddress()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("gemini-secret"));
        var connection = CreateConnection(
            baseUrl: "https://shared.example.test",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.GeminiCli] = "   ",
            });

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.GeminiCli),
            CreateInstallation(CliKind.GeminiCli),
            connection);

        Assert.Equal("gemini-secret", command.Environment!["GEMINI_API_KEY"]);
        Assert.Equal("gemini-secret", command.Environment["GOOGLE_API_KEY"]);
        Assert.DoesNotContain("GOOGLE_GEMINI_BASE_URL", command.Environment.Keys);
        Assert.DoesNotContain("GEMINI_CLI_HOME", command.Environment.Keys);
    }

    [Fact]
    public async Task Factory_GeminiGateway_UsesIsolatedModelHomeWithGatewayWhitelist()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("gemini-secret"));
        var connection = CreateConnection(
            baseUrl: "https://shared.example.test",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.GeminiCli] = "https://gateway.example.test",
            });

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.GeminiCli),
            CreateInstallation(CliKind.GeminiCli),
            connection);

        Assert.Equal("https://gateway.example.test", command.Environment!["GOOGLE_GEMINI_BASE_URL"]);
        Assert.True(command.Environment.TryGetValue("GEMINI_CLI_HOME", out string? geminiHome));
        Assert.False(string.IsNullOrWhiteSpace(geminiHome));
        Assert.DoesNotContain("gemini-secret", geminiHome, StringComparison.Ordinal);

        string settingsPath = Path.Combine(geminiHome!, "settings.json");
        Assert.True(File.Exists(settingsPath));
        string settings = await File.ReadAllTextAsync(settingsPath);
        Assert.Contains("\"name\": \"gemini-3-flash\"", settings, StringComparison.Ordinal);
        Assert.Contains("\"gemini-3-flash-agent\"", settings, StringComparison.Ordinal);
        Assert.Contains("\"gemini-pro-agent\"", settings, StringComparison.Ordinal);
        Assert.Contains("\"dynamicModelConfiguration\": true", settings, StringComparison.Ordinal);
        Assert.Contains("\"selectedType\": \"gemini-api-key\"", settings, StringComparison.Ordinal);
        Assert.Contains("\"gemini-2.5-pro\"", settings, StringComparison.Ordinal);
        Assert.Contains("\"isVisible\": false", settings, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Factory_CodexKeyOnlyConnection_DoesNotConfigureCustomProvider()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("codex-secret"));

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.Codex),
            CreateInstallation(CliKind.Codex),
            CreateConnection(baseUrl: string.Empty));

        Assert.Equal("codex-secret", command.Environment!["OPENAI_API_KEY"]);
        Assert.DoesNotContain(command.Arguments, argument =>
            argument.Contains("model_provider", StringComparison.Ordinal));
    }

    [Fact]
    public async Task Factory_CodexMissingClientUrl_DoesNotUseAnotherClientsAddress()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("codex-secret"));
        var connection = CreateConnection(
            baseUrl: "https://claude.example.test",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.ClaudeCode] = "https://claude.example.test",
            });

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.Codex),
            CreateInstallation(CliKind.Codex),
            connection);

        Assert.Equal("codex-secret", command.Environment!["OPENAI_API_KEY"]);
        Assert.DoesNotContain(command.Arguments, argument =>
            argument.Contains("model_provider", StringComparison.Ordinal));
        Assert.DoesNotContain(command.Arguments, argument =>
            argument.Contains("claude.example.test", StringComparison.Ordinal));
    }

    [Fact]
    public async Task Factory_CodexValidClientUrl_ConfiguresCustomProvider()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("codex-secret"));
        var connection = CreateConnection(
            baseUrl: "https://claude.example.test",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = " https://codex.example.test/v1 ",
                [CliKind.ClaudeCode] = "https://claude.example.test",
            });

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.Codex),
            CreateInstallation(CliKind.Codex),
            connection);

        Assert.Contains("model_provider=\"lan_ai_workspace\"", command.Arguments);
        Assert.Contains(
            "model_providers.lan_ai_workspace.base_url=\"https://codex.example.test/v1\"",
            command.Arguments);
    }

    [Fact]
    public async Task Factory_NormalizesVersionSuffixBeforeLaunchingEachClient()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("secret-value"));
        var connection = CreateConnection(
            baseUrl: "https://unused.example.test",
            clientBaseUrls: new Dictionary<CliKind, string>
            {
                [CliKind.Codex] = "https://relay.example.test",
                [CliKind.ClaudeCode] = "https://relay.example.test/v1",
                [CliKind.GeminiCli] = "https://relay.example.test/v1beta/models",
                [CliKind.GrokCli] = "https://relay.example.test/models",
            });

        TerminalCommand codex = await factory.CreateAsync(
            CreateRequest(CliKind.Codex),
            CreateInstallation(CliKind.Codex),
            connection);
        TerminalCommand claude = await factory.CreateAsync(
            CreateRequest(CliKind.ClaudeCode),
            CreateInstallation(CliKind.ClaudeCode),
            connection);
        TerminalCommand gemini = await factory.CreateAsync(
            CreateRequest(CliKind.GeminiCli),
            CreateInstallation(CliKind.GeminiCli),
            connection);
        TerminalCommand grok = await factory.CreateAsync(
            CreateRequest(CliKind.GrokCli),
            CreateInstallation(CliKind.GrokCli),
            connection);

        Assert.Contains(
            "model_providers.lan_ai_workspace.base_url=\"https://relay.example.test/v1\"",
            codex.Arguments);
        Assert.Equal("https://relay.example.test", claude.Environment!["ANTHROPIC_BASE_URL"]);
        Assert.Equal("https://relay.example.test", gemini.Environment!["GOOGLE_GEMINI_BASE_URL"]);
        Assert.Equal("https://relay.example.test/v1", grok.Environment!["OPENAI_BASE_URL"]);
    }

    [Theory]
    [InlineData(CliKind.Codex, "codex.local/v1")]
    [InlineData(CliKind.ClaudeCode, "ftp://claude.example.test")]
    [InlineData(CliKind.GeminiCli, "/relative/gemini")]
    public async Task Factory_RejectsInvalidExplicitClientUrl(CliKind cli, string invalidUrl)
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("secret-value"));
        var connection = CreateConnection(
            baseUrl: "https://unrelated.example.test",
            clientBaseUrls: new Dictionary<CliKind, string> { [cli] = invalidUrl });

        InvalidOperationException exception = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            factory.CreateAsync(CreateRequest(cli), CreateInstallation(cli), connection));

        Assert.Contains(GetCliDisplayName(cli), exception.Message, StringComparison.Ordinal);
        Assert.Contains(invalidUrl, exception.Message, StringComparison.Ordinal);
        Assert.Contains("http://", exception.Message, StringComparison.Ordinal);
        Assert.Contains("https://", exception.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("http://127.0.0.1:8080")]
    [InlineData("https://gateway.example.test/api")]
    public async Task Factory_AcceptsAbsoluteHttpSharedUrlWhenNoClientUrlsExist(string baseUrl)
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("secret-value"));

        TerminalCommand command = await factory.CreateAsync(
            CreateRequest(CliKind.ClaudeCode),
            CreateInstallation(CliKind.ClaudeCode),
            CreateConnection(baseUrl));

        Assert.Equal(baseUrl, command.Environment!["ANTHROPIC_BASE_URL"]);
    }

    [Fact]
    public async Task Factory_RejectsInvalidSharedUrlWhenNoClientUrlsExist()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials("secret-value"));

        InvalidOperationException exception = await Assert.ThrowsAsync<InvalidOperationException>(() =>
            factory.CreateAsync(
                CreateRequest(CliKind.ClaudeCode),
                CreateInstallation(CliKind.ClaudeCode),
                CreateConnection("claude.example.test")));

        Assert.Contains("Claude Code", exception.Message, StringComparison.Ordinal);
        Assert.Contains("claude.example.test", exception.Message, StringComparison.Ordinal);
        Assert.Contains("绝对 URL", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LegacyReader_GrokOnlyProfile_ConfiguresOnlyGrokCli()
    {
        string testDirectory = Path.Combine(
            Path.GetTempPath(),
            $"lan-ai-legacy-grok-{Guid.NewGuid():N}");
        string profilesPath = Path.Combine(testDirectory, "profiles.json");
        Directory.CreateDirectory(testDirectory);

        try
        {
            await File.WriteAllTextAsync(
                profilesPath,
                """
                {
                  "CloudSources": [
                    {
                      "Id": "grok-only",
                      "Name": "仅 Grok 来源",
                      "Grok": {
                        "BaseUrl": "https://grok.example.test/v1",
                        "Secret": "grok-secret"
                      }
                    }
                  ]
                }
                """);

            var reader = new LegacyProfileReader(profilesPath);
            ConnectionProfile profile = Assert.Single(await reader.GetAllAsync());

            Assert.Equal("https://grok.example.test/v1", profile.BaseUrl);
            Assert.Equal("https://grok.example.test/v1", profile.ClientBaseUrls[CliKind.GrokCli]);
            Assert.Equal([CliKind.GrokCli], profile.EnabledClients);
            Assert.True(reader.TryGetLegacyBaseUrl("grok-only", "Grok", out string grokUrl));
            Assert.Equal("https://grok.example.test/v1", grokUrl);

            var factory = new CliTerminalCommandFactory(reader);
            foreach (CliKind cli in Enum.GetValues<CliKind>())
            {
                TerminalCommand command = await factory.CreateAsync(
                    CreateRequest(cli),
                    CreateInstallation(cli),
                    profile);

                if (cli == CliKind.GrokCli)
                {
                    Assert.Equal("grok-secret", command.Environment!["XAI_API_KEY"]);
                    Assert.Equal("https://grok.example.test/v1", command.Environment!["GROK_MODELS_BASE_URL"]);
                }
                else
                {
                    Assert.Empty(command.Environment!);
                    Assert.DoesNotContain(command.Arguments, argument =>
                        argument.Contains("grok.example.test", StringComparison.Ordinal));
                    Assert.DoesNotContain(command.Arguments, argument =>
                        argument.Contains("model_provider", StringComparison.Ordinal));
                }
            }
        }
        finally
        {
            Directory.Delete(testDirectory, recursive: true);
        }
    }

    [Fact]
    public async Task LegacyReader_DisposeClearsCachedCredentials()
    {
        string testDirectory = Path.Combine(
            Path.GetTempPath(),
            $"lan-ai-legacy-dispose-{Guid.NewGuid():N}");
        string profilesPath = Path.Combine(testDirectory, "profiles.json");
        Directory.CreateDirectory(testDirectory);

        try
        {
            await File.WriteAllTextAsync(
                profilesPath,
                """
                {
                  "CloudSources": [
                    {
                      "Id": "dispose-test",
                      "Name": "释放测试",
                      "Codex": {
                        "BaseUrl": "https://codex.example.test/v1",
                        "Secret": "temporary-secret"
                      }
                    }
                  ]
                }
                """);

            var reader = new LegacyProfileReader(profilesPath);
            await reader.GetAllAsync();
            Assert.Equal(
                "temporary-secret",
                await reader.GetSecretAsync("dispose-test", CliKind.Codex));

            reader.Dispose();

            Assert.Null(await reader.GetSecretAsync("dispose-test", CliKind.Codex));
            Assert.False(reader.TryGetLegacySecret("legacy:dispose-test:Codex", CliKind.Codex, out _));
            await Assert.ThrowsAsync<ObjectDisposedException>(() => reader.GetAllAsync());
        }
        finally
        {
            Directory.Delete(testDirectory, recursive: true);
        }
    }

    [Fact]
    public async Task Factory_UsesOfficialNativeSessionResumeArguments()
    {
        var factory = new CliTerminalCommandFactory(new StubCredentials(string.Empty));
        var cases = new[]
        {
            new
            {
                Cli = LanAi.Workspace.Core.CliKind.Codex,
                Mode = LanAi.Workspace.Core.CliLaunchMode.Resume,
                Expected = new[] { "resume", "native-session-123" },
            },
            new
            {
                Cli = LanAi.Workspace.Core.CliKind.ClaudeCode,
                Mode = LanAi.Workspace.Core.CliLaunchMode.Fork,
                Expected = new[] { "--resume", "native-session-123", "--fork-session" },
            },
            new
            {
                Cli = LanAi.Workspace.Core.CliKind.GeminiCli,
                Mode = LanAi.Workspace.Core.CliLaunchMode.Resume,
                Expected = new[] { "--resume", "native-session-123" },
            },
        };

        foreach (var testCase in cases)
        {
            var request = new LanAi.Workspace.Core.CliLaunchRequest
            {
                ProjectId = "project-1",
                Cli = testCase.Cli,
                WorkingDirectory = Environment.CurrentDirectory,
                Mode = testCase.Mode,
                NativeSessionId = "native-session-123",
            };
            var installation = new LanAi.Workspace.Core.CliInstallation
            {
                Kind = testCase.Cli,
                IsInstalled = true,
                ExecutablePath = @"C:\tools\official-cli.exe",
                DetectedAt = DateTimeOffset.UtcNow,
            };

            TerminalCommand command = await factory.CreateAsync(request, installation, connection: null);

            Assert.Equal(testCase.Expected, command.Arguments);
        }
    }

    [Fact]
    public async Task ConPty_CapturesCmdOutput()
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 17763))
        {
            return;
        }

        var shell = Environment.GetEnvironmentVariable("COMSPEC") ?? @"C:\Windows\System32\cmd.exe";
        await using var session = new TerminalSession(80, 20);
        var captured = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        session.FrameChanged += (_, _) =>
        {
            var current = session.CaptureFrame();
            if (current.Lines.Any(line => line.Contains("conpty-ok", StringComparison.OrdinalIgnoreCase)))
            {
                captured.TrySetResult();
            }
        };

        await session.StartAsync(new TerminalCommand(
            shell,
            ["/d", "/s", "/c", "echo conpty-ok"],
            Environment.CurrentDirectory,
            DisplayName: "ConPTY smoke test"));

        await captured.Task.WaitAsync(TimeSpan.FromSeconds(10));
        var frame = session.CaptureFrame();

        Assert.Contains(frame.Lines, line => line.Contains("conpty-ok", StringComparison.OrdinalIgnoreCase));
    }

    private sealed class StubCredentials(string secret) : LanAi.Workspace.Core.IConnectionCredentialProvider
    {
        public ValueTask<string?> GetSecretAsync(
            string connectionProfileId,
            LanAi.Workspace.Core.CliKind client,
            CancellationToken cancellationToken = default) => ValueTask.FromResult<string?>(secret);
    }

    private static CliLaunchRequest CreateRequest(CliKind cli) => new()
    {
        ProjectId = "project-1",
        Cli = cli,
        WorkingDirectory = Environment.CurrentDirectory,
        ConnectionProfileId = "connection-1",
    };

    private static CliInstallation CreateInstallation(CliKind cli) => new()
    {
        Kind = cli,
        IsInstalled = true,
        ExecutablePath = @"C:\tools\official-cli.exe",
        DetectedAt = DateTimeOffset.UtcNow,
    };

    private static ConnectionProfile CreateConnection(
        string baseUrl,
        IReadOnlyDictionary<CliKind, string>? clientBaseUrls = null) => new()
    {
        Id = "connection-1",
        Name = "测试连接",
        BaseUrl = baseUrl,
        ClientBaseUrls = clientBaseUrls ?? new Dictionary<CliKind, string>(),
    };

    private static string GetCliDisplayName(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude Code",
        CliKind.GeminiCli => "Gemini CLI",
        _ => cli.ToString(),
    };
}



