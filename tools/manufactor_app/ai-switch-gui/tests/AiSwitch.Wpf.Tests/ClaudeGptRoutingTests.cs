using System.Net;
using System.Net.Http;
using System.Collections.Concurrent;
using System.Text;
using System.Text.Json.Nodes;
using AiSwitchGui;

namespace AiSwitch.Wpf.Tests;

public sealed class ClaudeGptRoutingTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "LanAi.ClaudeGptRouting.Tests",
        Guid.NewGuid().ToString("N"));

    [Theory]
    [InlineData("https://relay.example/v1", "https://relay.example")]
    [InlineData("https://relay.example/api/v1/", "https://relay.example/api")]
    [InlineData("https://relay.example/api/v1/messages", "https://relay.example/api")]
    [InlineData("https://relay.example/v1beta/models", "https://relay.example")]
    [InlineData("http://127.0.0.1:8080", "http://127.0.0.1:8080")]
    public void NormalizeGatewayRoot_StripsOnlyTheTrailingV1(string input, string expected)
    {
        Assert.Equal(expected, SwitchService.NormalizeGatewayRoot(input));
    }

    [Theory]
    [InlineData("https://relay.example", "https://relay.example/v1")]
    [InlineData("https://relay.example/v1", "https://relay.example/v1")]
    [InlineData("https://relay.example\\v1", "https://relay.example/v1")]
    [InlineData("https://relay.example/api/v1/", "https://relay.example/api/v1")]
    [InlineData("https://relay.example/api/v1/responses", "https://relay.example/api/v1")]
    [InlineData("https://relay.example/models", "https://relay.example/v1")]
    [InlineData("http://127.0.0.1:8080", "http://127.0.0.1:8080/v1")]
    public void NormalizeOpenAiApiBaseUrl_AddsTheCodexApiSuffix(string input, string expected)
    {
        Assert.Equal(expected, SwitchService.NormalizeOpenAiApiBaseUrl(input));
    }

    [Fact]
    public async Task ApplySource_PreservesExistingCodexModelSettings()
    {
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.CodexConfigPath)!);
        await File.WriteAllTextAsync(paths.CodexConfigPath, """
            model_provider = "old-provider"
            model = "grok-4.5"
            review_model = "grok-4.5"
            model_reasoning_effort = "xhigh"
            model_context_window = 131072
            model_auto_compact_token_limit = 120000

            [features]
            js_repl = false
            """);
        await File.WriteAllTextAsync(paths.CodexAuthPath, "{\"OPENAI_API_KEY\":\"old-key\"}");
        SwitchService service = CreateService(paths, new RecordingHandler(_ =>
            throw new InvalidOperationException("Applying a source must not make network requests.")));
        var store = new ProfileStore
        {
            Cloud = new ProfileDefinition
            {
                Name = "Grok source",
                Codex = new ClientProfile
                {
                    BaseUrl = "https://code-plan.site/v1",
                    Secret = "new-key",
                },
            },
        };

        OperationResult result = await service.SwitchAsync(store, TargetMode.Cloud, CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        string config = await File.ReadAllTextAsync(paths.CodexConfigPath);
        Assert.Contains("model = \"grok-4.5\"", config, StringComparison.Ordinal);
        Assert.Contains("review_model = \"grok-4.5\"", config, StringComparison.Ordinal);
        Assert.Contains("model_reasoning_effort = \"xhigh\"", config, StringComparison.Ordinal);
        Assert.Contains("model_context_window = 131072", config, StringComparison.Ordinal);
        Assert.Contains("model_auto_compact_token_limit = 120000", config, StringComparison.Ordinal);
        Assert.Contains("base_url = \"https://code-plan.site/v1\"", config, StringComparison.Ordinal);
        Assert.DoesNotContain("gpt-5.6-sol", config, StringComparison.Ordinal);
        Assert.Contains("[features]", config, StringComparison.Ordinal);
        Assert.Contains("\"OPENAI_API_KEY\": \"new-key\"", await File.ReadAllTextAsync(paths.CodexAuthPath), StringComparison.Ordinal);
    }

    [Fact]
    public async Task ApplySource_SelectsLatestGrokModelReturnedForTheKey()
    {
        ConfigPaths paths = CreatePaths();
        SwitchService service = CreateService(paths, new RecordingHandler(request =>
            request.RequestUri?.AbsolutePath.EndsWith("/models", StringComparison.Ordinal) == true
                ? Models("grok-4.20-reasoning", "grok-4.5", "grok-4.5-latest", "grok-latest", "grok-imagine-video")
                : throw new InvalidOperationException("Unexpected request.")));
        var store = new ProfileStore
        {
            Cloud = new ProfileDefinition
            {
                Name = "Grok source",
                Grok = new ClientProfile
                {
                    BaseUrl = "http://127.0.0.1:8080/v1",
                    Secret = "managed-key",
                },
            },
        };

        OperationResult result = await service.SwitchAsync(store, TargetMode.Cloud, CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        string config = await File.ReadAllTextAsync(paths.GrokConfigPath);
        Assert.Contains("default = \"grok-latest\"", config, StringComparison.Ordinal);
        Assert.Contains("[model.grok-latest]", config, StringComparison.Ordinal);
        Assert.Contains("model = \"grok-latest\"", config, StringComparison.Ordinal);
        Assert.DoesNotContain("model = \"grok-4\"", config, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData(new[] { "grok-4.5", "grok-4.20", "grok-imagine-video" }, "grok-4.20")]
    [InlineData(new[] { "grok-4.20", "grok-4.5-latest" }, "grok-4.5-latest")]
    [InlineData(new[] { "grok-build-latest", "grok-4.5" }, "grok-4.5")]
    public void SelectLatestGrokModel_PrefersAliasesThenNumericVersions(string[] models, string expected)
    {
        Assert.Equal(expected, SwitchService.SelectLatestGrokModel(models));
    }

    [Fact]
    public async Task ApplySource_FallsBackWhenPrimaryBackupDirectoryIsUnavailable()
    {
        ConfigPaths paths = CreatePaths();
        await File.WriteAllTextAsync(paths.BackupRoot, "blocks-directory-creation");
        SwitchService service = CreateService(paths, new RecordingHandler(_ =>
            throw new InvalidOperationException("Applying a source must not make network requests.")));
        var store = new ProfileStore
        {
            Cloud = new ProfileDefinition
            {
                Name = "Local gateway",
                Codex = new ClientProfile
                {
                    BaseUrl = "http://127.0.0.1:8080/v1",
                    Secret = "managed-key",
                },
            },
        };

        OperationResult result = await service.SwitchAsync(store, TargetMode.Cloud, CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        Assert.Contains(paths.FallbackBackupRoot, result.Summary, StringComparison.OrdinalIgnoreCase);
        Assert.True(Directory.Exists(paths.FallbackBackupRoot));
        Assert.Contains(
            "\"OPENAI_API_KEY\": \"managed-key\"",
            await File.ReadAllTextAsync(paths.CodexAuthPath),
            StringComparison.Ordinal);
    }

    [Fact]
    public async Task Enable_PreflightsBridgeAndDisableRestoresExactOriginalSettings()
    {
        byte[] originalSettings = Encoding.UTF8.GetBytes("{\r\n  \"theme\": \"dark\",\r\n  \"keep\": true\r\n}\r\n");
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.ClaudeSettingsPath)!);
        await File.WriteAllBytesAsync(paths.ClaudeSettingsPath, originalSettings);
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("gpt-5.5", "gpt-5.4", "gpt-5.4-mini")
            : Json(HttpStatusCode.OK, "{\"input_tokens\":1}"));
        SwitchService service = CreateService(paths, handler);

        OperationResult enabled = await service.EnableClaudeGptRoutingAsync(
            "remote-a",
            "远程 A",
            new ClientProfile { BaseUrl = "https://relay.example/v1", Secret = "secret-value" },
            Mapping("gpt-5.5", "gpt-5.4", "gpt-5.4-mini"),
            CancellationToken.None);

        Assert.True(enabled.Success, enabled.Summary);
        Assert.Equal("https://relay.example/v1/responses", handler.LastUri?.AbsoluteUri);
        Assert.Equal("Bearer", handler.AuthorizationScheme);
        Assert.Equal("secret-value", handler.AuthorizationParameter);
        Assert.Equal("secret-value", handler.ApiKeyHeader);
        Assert.Equal(3, handler.RequestBodies.Count(body => !string.IsNullOrWhiteSpace(body)));
        Assert.Contains(handler.RequestBodies, body => body.Contains("\"model\":\"gpt-5.5\"", StringComparison.Ordinal));
        Assert.Contains(handler.RequestBodies, body => body.Contains("\"model\":\"gpt-5.4\"", StringComparison.Ordinal));
        Assert.Contains(handler.RequestBodies, body => body.Contains("\"model\":\"gpt-5.4-mini\"", StringComparison.Ordinal));

        JsonObject settings = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(paths.ClaudeSettingsPath)));
        JsonObject env = Assert.IsType<JsonObject>(settings["env"]);
        string bridgeUrl = env["ANTHROPIC_BASE_URL"]?.GetValue<string>() ?? string.Empty;
        string bridgeToken = env["ANTHROPIC_AUTH_TOKEN"]?.GetValue<string>() ?? string.Empty;
        Assert.StartsWith("http://127.0.0.1:", bridgeUrl, StringComparison.Ordinal);
        Assert.StartsWith("lanai-bridge-", bridgeToken, StringComparison.Ordinal);
        Assert.Null(env["ANTHROPIC_MODEL"]);
        Assert.Null(env["ANTHROPIC_API_KEY"]);
        Assert.Equal("gpt-5.5", env["ANTHROPIC_DEFAULT_OPUS_MODEL"]?.GetValue<string>());
        Assert.Equal("gpt-5.4", env["ANTHROPIC_DEFAULT_SONNET_MODEL"]?.GetValue<string>());
        Assert.Equal("gpt-5.4-mini", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"]?.GetValue<string>());
        Assert.Equal("gpt-5.4-mini", env["ANTHROPIC_SMALL_FAST_MODEL"]?.GetValue<string>());
        JsonObject metadata = Assert.IsType<JsonObject>(settings["_gongfei_claude_gpt"]);
        Assert.DoesNotContain("secret-value", metadata.ToJsonString(), StringComparison.Ordinal);
        Assert.Equal("anthropic-messages-to-openai-responses", metadata["bridge"]?["mode"]?.GetValue<string>());

        using var bridgeClient = new HttpClient();
        using var bridgeRequest = new HttpRequestMessage(HttpMethod.Post, $"{bridgeUrl}/v1/messages");
        bridgeRequest.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", bridgeToken);
        bridgeRequest.Content = new StringContent(
            "{\"model\":\"gpt-5.5\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
            Encoding.UTF8,
            "application/json");
        using HttpResponseMessage bridgeResponse = await bridgeClient.SendAsync(bridgeRequest);
        string bridgeBody = await bridgeResponse.Content.ReadAsStringAsync();
        Assert.True(bridgeResponse.IsSuccessStatusCode, bridgeBody);
        Assert.Contains("\"type\":\"message\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"content\"", bridgeBody, StringComparison.Ordinal);

        ClaudeGptRoutingStatus restartedStatus = CreateService(paths, handler).ReadClaudeGptRoutingStatus();
        Assert.False(restartedStatus.Enabled);
        Assert.Equal("remote-a", restartedStatus.SourceId);
        Assert.Equal("gpt-5.5", restartedStatus.Mapping.OpusModel);
        Assert.Equal("gpt-5.4", restartedStatus.Mapping.SonnetModel);
        Assert.Equal("gpt-5.4-mini", restartedStatus.Mapping.HaikuModel);

        OperationResult disabled = service.DisableClaudeGptRouting();

        Assert.True(disabled.Success, disabled.Summary);
        Assert.Equal(originalSettings, await File.ReadAllBytesAsync(paths.ClaudeSettingsPath));
        Assert.False(service.ReadClaudeGptRoutingStatus().Enabled);
    }

    [Fact]
    public async Task Bridge_PreservesClaudeStreamingModeWhenCallingResponsesUpstream()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("grok-4.5")
            : Sse("""
                  event: response.output_text.delta
                  data: {"type":"response.output_text.delta","delta":"GROK"}

                  event: response.output_text.delta
                  data: {"type":"response.output_text.delta","delta":"_STREAM_OK"}

                  event: response.completed
                  data: {"type":"response.completed","response":{"id":"resp_test","output_text":"GROK_STREAM_OK","usage":{"input_tokens":3,"output_tokens":2}}}

                  data: [DONE]

                  """));
        SwitchService service = CreateService(paths, handler);

        OperationResult enabled = await service.EnableClaudeGptRoutingAsync(
            "remote-a",
            "远程 A",
            "Grok",
            new ClientProfile { BaseUrl = "https://relay.example/v1", Secret = "secret-value" },
            Mapping("grok-4.5", "grok-4.5", "grok-4.5"),
            CancellationToken.None);

        Assert.True(enabled.Success, enabled.Summary);
        JsonObject settings = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(paths.ClaudeSettingsPath)));
        JsonObject env = Assert.IsType<JsonObject>(settings["env"]);
        string bridgeUrl = env["ANTHROPIC_BASE_URL"]?.GetValue<string>() ?? string.Empty;
        string bridgeToken = env["ANTHROPIC_AUTH_TOKEN"]?.GetValue<string>() ?? string.Empty;

        using var bridgeClient = new HttpClient();
        using var bridgeRequest = new HttpRequestMessage(HttpMethod.Post, $"{bridgeUrl}/v1/messages");
        bridgeRequest.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", bridgeToken);
        bridgeRequest.Content = new StringContent(
            "{\"model\":\"grok-4.5\",\"stream\":true,\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
            Encoding.UTF8,
            "application/json");
        using HttpResponseMessage bridgeResponse = await bridgeClient.SendAsync(bridgeRequest);
        string bridgeBody = await bridgeResponse.Content.ReadAsStringAsync();

        Assert.True(bridgeResponse.IsSuccessStatusCode, bridgeBody);
        Assert.Equal("text/event-stream", bridgeResponse.Content.Headers.ContentType?.MediaType);
        Assert.Contains("event: content_block_delta", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"text\":\"GROK\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"text\":\"_STREAM_OK\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains(handler.RequestBodies, body =>
            body.Contains("\"model\":\"grok-4.5\"", StringComparison.Ordinal) &&
            body.Contains("\"stream\":true", StringComparison.Ordinal));
        Assert.True(service.DisableClaudeGptRouting().Success);
    }

    [Fact]
    public async Task Bridge_ConvertsClaudeToolUseAndToolResultInBothDirections()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("gpt-5.6-sol")
            : Sse("""
                  event: response.output_item.done
                  data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"toolu_shell_1","name":"shell","arguments":"{\"command\":\"pwd\"}"}}

                  event: response.completed
                  data: {"type":"response.completed","response":{"id":"resp_tool","output":[],"usage":{"input_tokens":12,"output_tokens":8}}}

                  data: [DONE]

                  """));
        SwitchService service = CreateService(paths, handler);
        OperationResult enabled = await service.EnableClaudeGptRoutingAsync(
            "local-machine",
            "本机中转",
            "GPT",
            Profile("http://127.0.0.1:8080/v1"),
            Mapping("gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol"),
            CancellationToken.None,
            validateUpstream: false);
        Assert.True(enabled.Success, enabled.Summary);

        JsonObject settings = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(paths.ClaudeSettingsPath)));
        JsonObject env = Assert.IsType<JsonObject>(settings["env"]);
        string bridgeUrl = env["ANTHROPIC_BASE_URL"]?.GetValue<string>() ?? string.Empty;
        string bridgeToken = env["ANTHROPIC_AUTH_TOKEN"]?.GetValue<string>() ?? string.Empty;
        using var bridgeClient = new HttpClient();
        using var bridgeRequest = new HttpRequestMessage(HttpMethod.Post, $"{bridgeUrl}/v1/messages");
        bridgeRequest.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", bridgeToken);
        bridgeRequest.Content = new StringContent(
            """
            {
              "model":"claude-opus-4-1",
              "stream":true,
              "max_tokens":128,
              "tools":[{"name":"shell","description":"Run a command","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],
              "messages":[
                {"role":"assistant","content":[{"type":"tool_use","id":"toolu_previous","name":"shell","input":{"command":"whoami"}}]},
                {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_previous","content":"Administrator"},{"type":"text","text":"continue"}]}
              ]
            }
            """,
            Encoding.UTF8,
            "application/json");

        using HttpResponseMessage bridgeResponse = await bridgeClient.SendAsync(bridgeRequest);
        string bridgeBody = await bridgeResponse.Content.ReadAsStringAsync();

        Assert.True(bridgeResponse.IsSuccessStatusCode, bridgeBody);
        Assert.Contains("\"type\":\"tool_use\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"id\":\"toolu_shell_1\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"type\":\"input_json_delta\"", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("\"stop_reason\":\"tool_use\"", bridgeBody, StringComparison.Ordinal);
        string upstreamBody = handler.RequestBodies.Last(body => body.Contains("\"stream\":true", StringComparison.Ordinal));
        Assert.Contains("\"type\":\"function_call\"", upstreamBody, StringComparison.Ordinal);
        Assert.Contains("\"call_id\":\"toolu_previous\"", upstreamBody, StringComparison.Ordinal);
        Assert.Contains("\"type\":\"function_call_output\"", upstreamBody, StringComparison.Ordinal);
        Assert.Contains("Administrator", upstreamBody, StringComparison.Ordinal);
        Assert.True(service.DisableClaudeGptRouting().Success);
    }

    [Fact]
    public async Task Bridge_StopsImmediatelyAndShowsResponsesStreamErrors()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("gpt-5.6-sol")
            : Sse("""
                  event: response.failed
                  data: {"type":"response.failed","response":{"error":{"message":"no available accounts"}}}

                  """));
        SwitchService service = CreateService(paths, handler);
        Assert.True((await service.EnableClaudeGptRoutingAsync(
            "local-machine",
            "本机中转",
            "GPT",
            Profile("http://127.0.0.1:8080/v1"),
            Mapping("gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol"),
            CancellationToken.None,
            validateUpstream: false)).Success);

        JsonObject settings = Assert.IsType<JsonObject>(JsonNode.Parse(await File.ReadAllTextAsync(paths.ClaudeSettingsPath)));
        JsonObject env = Assert.IsType<JsonObject>(settings["env"]);
        using var bridgeClient = new HttpClient();
        using var bridgeRequest = new HttpRequestMessage(HttpMethod.Post, $"{env["ANTHROPIC_BASE_URL"]}/v1/messages");
        bridgeRequest.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue(
            "Bearer",
            env["ANTHROPIC_AUTH_TOKEN"]?.GetValue<string>());
        bridgeRequest.Content = new StringContent(
            "{\"model\":\"claude-opus-4-1\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
            Encoding.UTF8,
            "application/json");

        using HttpResponseMessage bridgeResponse = await bridgeClient.SendAsync(bridgeRequest);
        string bridgeBody = await bridgeResponse.Content.ReadAsStringAsync();

        Assert.Contains("no available accounts", bridgeBody, StringComparison.Ordinal);
        Assert.Contains("event: message_stop", bridgeBody, StringComparison.Ordinal);
        Assert.True(service.DisableClaudeGptRouting().Success);
    }

    [Fact]
    public async Task Enable_SecondSelectionKeepsTheOriginalBackup()
    {
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.ClaudeSettingsPath)!);
        const string original = "{\"original\":true}";
        await File.WriteAllTextAsync(paths.ClaudeSettingsPath, original);
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("gpt-5.5", "gpt-5.4", "gpt-5.4-mini")
            : Json(HttpStatusCode.OK, "{\"input_tokens\":1}"));
        SwitchService service = CreateService(paths, handler);

        Assert.True((await service.EnableClaudeGptRoutingAsync(
            "source-a", "来源 A", Profile("https://a.example/v1"), Mapping("gpt-5.4", "gpt-5.4", "gpt-5.4"), CancellationToken.None)).Success);
        Assert.True((await service.EnableClaudeGptRoutingAsync(
            "source-b", "来源 B", Profile("https://b.example/v1"), Mapping("gpt-5.5", "gpt-5.4", "gpt-5.4-mini"), CancellationToken.None)).Success);

        ClaudeGptRoutingStatus status = service.ReadClaudeGptRoutingStatus();
        Assert.Equal("source-b", status.SourceId);
        Assert.Equal("gpt-5.5", status.Mapping.OpusModel);
        Assert.Equal("gpt-5.4", status.Mapping.SonnetModel);
        Assert.Equal("gpt-5.4-mini", status.Mapping.HaikuModel);
        Assert.True(service.DisableClaudeGptRouting().Success);
        Assert.Equal(original, await File.ReadAllTextAsync(paths.ClaudeSettingsPath));
    }

    [Fact]
    public async Task Enable_ForbiddenPreflightDoesNotMutateSettingsOrCreateState()
    {
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.ClaudeSettingsPath)!);
        const string original = "{\"keep\":true}";
        await File.WriteAllTextAsync(paths.ClaudeSettingsPath, original);
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("gpt-5.4")
            : Json(
                HttpStatusCode.Forbidden,
                "{\"error\":{\"message\":\"This group does not allow /v1/messages dispatch\"}}"));
        SwitchService service = CreateService(paths, handler);

        OperationResult result = await service.EnableClaudeGptRoutingAsync(
            "source-a",
            "来源 A",
            Profile("https://relay.example"),
            Mapping("gpt-5.4", "gpt-5.4", "gpt-5.4"),
            CancellationToken.None);

        Assert.False(result.Success);
        Assert.Contains("does not allow /v1/messages dispatch", result.Summary, StringComparison.Ordinal);
        Assert.Equal(original, await File.ReadAllTextAsync(paths.ClaudeSettingsPath));
        Assert.False(File.Exists(paths.ClaudeGptRoutingStatePath));
    }

    [Fact]
    public async Task Enable_AllowsVerifiedModelWhenResponsesPreflightTimesOut()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request =>
        {
            if (request.Method == HttpMethod.Get)
            {
                return Models("gpt-5.6-sol");
            }

            throw new TaskCanceledException("The request was canceled due to timeout.");
        });
        SwitchService service = CreateService(paths, handler);

        OperationResult result = await service.EnableClaudeGptRoutingAsync(
            "source-a",
            "来源 A",
            Profile("https://relay.example/v1"),
            Mapping("gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol"),
            CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        Assert.Contains("Responses 上游预检成功", result.Summary, StringComparison.Ordinal);
        Assert.True(service.DisableClaudeGptRouting().Success);
    }

    [Fact]
    public async Task Enable_DoesNotRequireUpstreamAnthropicMessagesCompatibility()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request =>
        {
            if (request.Method == HttpMethod.Get)
            {
                return Models("grok-4.5");
            }

            return request.RequestUri?.AbsolutePath.EndsWith("/v1/responses", StringComparison.OrdinalIgnoreCase) == true
                ? Json(HttpStatusCode.OK, "{\"id\":\"resp_test\",\"output_text\":\"OK\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
                : Json(HttpStatusCode.InternalServerError, "{\"error\":{\"message\":\"not available\"}}");
        });
        SwitchService service = CreateService(paths, handler);

        OperationResult result = await service.EnableClaudeGptRoutingAsync(
            "source-a",
            "来源 A",
            Profile("https://code-plan.site/v1"),
            Mapping("grok-4.5", "grok-4.5", "grok-4.5"),
            CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        Assert.DoesNotContain(handler.RequestBodies, body => body.Contains("\"max_tokens\":16", StringComparison.Ordinal));
        Assert.DoesNotContain(handler.RequestBodies, body => body.Contains("/v1/messages", StringComparison.Ordinal));
        Assert.True(service.DisableClaudeGptRouting().Success);
    }

    [Fact]
    public async Task Enable_FailsWhenResponsesUpstreamIsUnavailable()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(request =>
        {
            if (request.Method == HttpMethod.Get)
            {
                return Models("grok-4.5");
            }

            return Json(HttpStatusCode.InternalServerError, "{\"error\":{\"message\":\"not available\"}}");
        });
        SwitchService service = CreateService(paths, handler);

        OperationResult result = await service.EnableClaudeGptRoutingAsync(
            "source-a",
            "来源 A",
            Profile("https://code-plan.site/v1"),
            Mapping("grok-4.5", "grok-4.5", "grok-4.5"),
            CancellationToken.None);

        Assert.False(result.Success);
        Assert.Contains("Responses 上游预检失败", result.Summary, StringComparison.Ordinal);
        Assert.Contains("not available", result.Summary, StringComparison.Ordinal);
    }

    [Fact]
    public async Task GetModels_ReadsOnlyOpenAiCompatibleModelNames()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(_ => Json(
            HttpStatusCode.OK,
            "{\"data\":[{\"id\":\"gpt-5.5\"},{\"id\":\"claude-sonnet-4\"},{\"id\":\"o3\"},{\"id\":\"gpt-5.4\"},{\"id\":\"gpt-image-2\"},{\"id\":\"gpt-4o-audio-preview\"},{\"id\":\"codex-auto-review\"}]}"));
        SwitchService service = CreateService(paths, handler);

        IReadOnlyList<string> models = await service.GetClaudeGptModelsAsync(
            Profile("https://relay.example/v1"),
            CancellationToken.None);

        Assert.Equal("https://relay.example/v1/models", handler.LastUri?.AbsoluteUri);
        Assert.Contains("gpt-5.5", models);
        Assert.Contains("gpt-5.4", models);
        Assert.Contains("o3", models);
        Assert.DoesNotContain("claude-sonnet-4", models);
        Assert.DoesNotContain("gpt-image-2", models);
        Assert.DoesNotContain("gpt-4o-audio-preview", models);
        Assert.DoesNotContain("codex-auto-review", models);
    }

    [Fact]
    public async Task GetModels_NormalizesBackslashBaseUrlAndIncludesGrokModels()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(_ => Json(
            HttpStatusCode.OK,
            "{\"data\":[{\"id\":\"grok-4\"},{\"id\":\"grok-4.5\"},{\"id\":\"gpt-5.5\"}]}"));
        SwitchService service = CreateService(paths, handler);

        IReadOnlyList<string> models = await service.GetClaudeGptModelsAsync(
            Profile("https://code-plan.site\\v1"),
            CancellationToken.None);

        Assert.Equal("https://code-plan.site/v1/models", handler.LastUri?.AbsoluteUri);
        Assert.Contains("grok-4", models);
        Assert.Contains("grok-4.5", models);
        Assert.Contains("gpt-5.5", models);
    }

    [Fact]
    public async Task CodexClaude_EnablePreflightsResponsesAndDisableRestoresExactOriginalFiles()
    {
        byte[] originalConfig = Encoding.UTF8.GetBytes("model = \"gpt-original\"\r\n");
        byte[] originalAuth = Encoding.UTF8.GetBytes("{\"OPENAI_API_KEY\":\"original-key\"}\r\n");
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.CodexConfigPath)!);
        await File.WriteAllBytesAsync(paths.CodexConfigPath, originalConfig);
        await File.WriteAllBytesAsync(paths.CodexAuthPath, originalAuth);
        var handler = new RecordingHandler(request => request.Method == HttpMethod.Get
            ? Models("claude-opus-4-8", "claude-sonnet-4-6", "gpt-5.6-sol")
            : Json(HttpStatusCode.OK, "{\"id\":\"resp_test\",\"status\":\"completed\"}"));
        SwitchService service = CreateService(paths, handler);

        OperationResult enabled = await service.EnableCodexClaudeRoutingAsync(
            "remote-a",
            "远程 A",
            Profile("https://relay.example/v1"),
            new CodexClaudeModelMapping
            {
                TargetPlatform = "Claude",
                DefaultModel = "claude-opus-4-8",
                ReviewModel = "claude-sonnet-4-6",
                ReasoningEffort = "xhigh",
            },
            CancellationToken.None);

        Assert.True(enabled.Success, enabled.Summary);
        Assert.Equal("https://relay.example/v1/responses", handler.LastUri?.AbsoluteUri);
        Assert.Contains(handler.RequestBodies, body => body.Contains("\"model\":\"claude-opus-4-8\"", StringComparison.Ordinal));
        Assert.Contains(handler.RequestBodies, body => body.Contains("\"model\":\"claude-sonnet-4-6\"", StringComparison.Ordinal));
        Assert.All(handler.RequestBodies.Where(body => body.Contains("\"model\":\"claude-", StringComparison.Ordinal)),
            body => Assert.Contains("\"effort\":\"xhigh\"", body, StringComparison.Ordinal));
        string config = await File.ReadAllTextAsync(paths.CodexConfigPath);
        Assert.Contains("model = \"claude-opus-4-8\"", config, StringComparison.Ordinal);
        Assert.Contains("review_model = \"claude-sonnet-4-6\"", config, StringComparison.Ordinal);
        Assert.Contains("base_url = \"https://relay.example/v1\"", config, StringComparison.Ordinal);
        Assert.Contains("wire_api = \"responses\"", config, StringComparison.Ordinal);
        Assert.Contains("model_reasoning_effort = \"xhigh\"", config, StringComparison.Ordinal);
        Assert.True(File.Exists(paths.CodexClaudeRoutingStatePath));

        CodexClaudeRoutingStatus status = service.ReadCodexClaudeRoutingStatus();
        Assert.True(status.Enabled);
        Assert.Equal("remote-a", status.SourceId);
        Assert.Equal("Claude", status.Mapping.TargetPlatform);
        Assert.Equal("claude-opus-4-8", status.Mapping.DefaultModel);
        Assert.Equal("xhigh", status.Mapping.ReasoningEffort);

        OperationResult disabled = service.DisableCodexClaudeRouting();

        Assert.True(disabled.Success, disabled.Summary);
        Assert.Equal(originalConfig, await File.ReadAllBytesAsync(paths.CodexConfigPath));
        Assert.Equal(originalAuth, await File.ReadAllBytesAsync(paths.CodexAuthPath));
        Assert.False(service.ReadCodexClaudeRoutingStatus().Enabled);
    }

    [Fact]
    public async Task CodexClaude_GetModelsReadsOnlyClaudeModels()
    {
        ConfigPaths paths = CreatePaths();
        var handler = new RecordingHandler(_ => Models(
            "claude-opus-4-8",
            "claude-sonnet-4-6",
            "gpt-5.6-sol"));
        SwitchService service = CreateService(paths, handler);

        IReadOnlyList<string> models = await service.GetCodexClaudeModelsAsync(
            Profile("https://relay.example/v1"),
            CancellationToken.None);

        Assert.Contains("claude-opus-4-8", models);
        Assert.Contains("claude-sonnet-4-6", models);
        Assert.DoesNotContain("gpt-5.6-sol", models);
    }

    [Fact]
    public async Task CodexClaude_AllowsGrokRoutingWhenModelListIsVerifiedButResponsesPreflightTimesOut()
    {
        ConfigPaths paths = CreatePaths();
        Directory.CreateDirectory(Path.GetDirectoryName(paths.CodexConfigPath)!);
        await File.WriteAllTextAsync(paths.CodexConfigPath, "model = \"gpt-original\"\r\n");
        await File.WriteAllTextAsync(paths.CodexAuthPath, "{\"OPENAI_API_KEY\":\"original-key\"}\r\n");
        var handler = new RecordingHandler(request =>
        {
            if (request.Method == HttpMethod.Get)
            {
                return Models("grok-4.5", "claude-sonnet-4-6");
            }

            throw new TaskCanceledException("The request was canceled due to timeout.");
        });
        SwitchService service = CreateService(paths, handler);

        OperationResult result = await service.EnableCodexClaudeRoutingAsync(
            "grok-source",
            "Grok 来源",
            Profile("https://code-plan.site/v1"),
            new CodexClaudeModelMapping
            {
                TargetPlatform = "Grok",
                DefaultModel = "grok-4.5",
                ReviewModel = "grok-4.5",
                ReasoningEffort = "high",
            },
            CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        Assert.Contains("短预检超时", result.Summary, StringComparison.Ordinal);
        string config = await File.ReadAllTextAsync(paths.CodexConfigPath);
        Assert.Contains("model = \"grok-4.5\"", config, StringComparison.Ordinal);
        Assert.Contains("base_url = \"https://code-plan.site/v1\"", config, StringComparison.Ordinal);
    }

    private ConfigPaths CreatePaths()
    {
        string userProfile = Path.Combine(_root, "user");
        string localAppData = Path.Combine(_root, "local");
        string profilesRoot = Path.Combine(userProfile, "ai-switch-gui");
        Directory.CreateDirectory(profilesRoot);
        return new ConfigPaths(profilesRoot, userProfile, localAppData);
    }

    private static SwitchService CreateService(ConfigPaths paths, HttpMessageHandler handler) =>
        new(paths, new ProfileRepository(paths), new HttpClient(handler), writeUserEnvironment: false);

    private static ClientProfile Profile(string baseUrl) => new()
    {
        BaseUrl = baseUrl,
        Secret = "secret-value",
    };

    private static ClaudeGptModelMapping Mapping(string opus, string sonnet, string haiku) => new()
    {
        OpusModel = opus,
        SonnetModel = sonnet,
        HaikuModel = haiku,
    };

    private static HttpResponseMessage Json(HttpStatusCode status, string body) => new(status)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private static HttpResponseMessage Models(params string[] models) => Json(
        HttpStatusCode.OK,
        "{\"data\":[" + string.Join(",", models.Select(model => $"{{\"id\":\"{model}\"}}")) + "]}");

    private static HttpResponseMessage Sse(string body) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(body, Encoding.UTF8, "text/event-stream"),
    };

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private sealed class RecordingHandler(Func<HttpRequestMessage, HttpResponseMessage> responseFactory) : HttpMessageHandler
    {
        public Uri? LastUri { get; private set; }
        public string? AuthorizationScheme { get; private set; }
        public string? AuthorizationParameter { get; private set; }
        public string? ApiKeyHeader { get; private set; }
        public ConcurrentBag<string> RequestBodies { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            LastUri = request.RequestUri;
            AuthorizationScheme = request.Headers.Authorization?.Scheme;
            AuthorizationParameter = request.Headers.Authorization?.Parameter;
            ApiKeyHeader = request.Headers.TryGetValues("x-api-key", out IEnumerable<string>? values)
                ? values.Single()
                : null;
            string requestBody = request.Content is null
                ? string.Empty
                : await request.Content.ReadAsStringAsync(cancellationToken);
            RequestBodies.Add(requestBody);
            return responseFactory(request);
        }
    }
}
