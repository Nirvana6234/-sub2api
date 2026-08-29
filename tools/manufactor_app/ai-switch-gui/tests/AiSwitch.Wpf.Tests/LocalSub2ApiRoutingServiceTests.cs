using System.Net;
using System.Net.Http;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using AiSwitchGui;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class LocalSub2ApiRoutingServiceTests
{
    [Fact]
    public async Task Backup_routing_recovers_the_installed_local_administrator_session()
    {
        using var handler = new RoutingHandler(request => request.RequestUri!.PathAndQuery switch
        {
            "/api/v1/admin/groups/all?include_inactive=true" => Json("{\"code\":0,\"data\":[]}"),
            "/api/v1/account-contributions?page=1&limit=500" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
            "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
            "/api/v1/keys?page=1&page_size=1000" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
            "/api/v1/admin/groups" => Json("{\"code\":0,\"data\":{\"id\":73,\"name\":\"managed\",\"platform\":\"openai\",\"rate_multiplier\":0}}"),
            "/api/v1/admin/accounts" => Json("{\"code\":0,\"data\":{\"id\":82,\"name\":\"external\",\"platform\":\"openai\"}}"),
            "/api/v1/keys" => Json("{\"code\":0,\"data\":{\"id\":91,\"name\":\"client\",\"key\":\"local-client-key\"}}"),
            var path => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
        });
        using var session = new RecoveringRoutingSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(
            session,
            client,
            localControlTokenProvider: () => "installation-local-control-token");

        LocalSub2ApiRoutingResult result = await service.ApplySourceAsync(
            CreateStore(),
            "remote-one",
            CancellationToken.None);

        Assert.Equal(["Codex"], result.UpdatedPlatforms);
        Assert.Equal(1, session.LocalControlLoginCalls);
        Assert.Equal("installation-local-control-token", session.LastLocalControlToken);
    }

    [Fact]
    public async Task Remote_source_is_registered_in_local_sub2api_and_client_uses_local_key()
    {
        var requests = new List<(HttpMethod Method, string Path, string Body)>();
        long groupId = 73;
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null ? string.Empty : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            requests.Add((request.Method, path, body));
            if (path == "/api/v1/admin/groups")
            {
                return Json($"{{\"code\":0,\"data\":{{\"id\":{groupId},\"name\":\"managed\",\"platform\":\"openai\",\"rate_multiplier\":0}}}}");
            }
            return path switch
            {
                "/api/v1/admin/groups/all?include_inactive=true" => Json("{\"code\":0,\"data\":[]}"),
                "/api/v1/account-contributions?page=1&limit=500" => Json("{\"code\":0,\"data\":{\"items\":[{\"id\":44,\"name\":\"personal\",\"platform\":\"openai\",\"group_ids\":[9]}]}}"),
                "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/keys?page=1&page_size=1000" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/accounts" => Json("{\"code\":0,\"data\":{\"id\":82,\"name\":\"external\",\"platform\":\"openai\"}}"),
                "/api/v1/account-contributions/44" => Json("{\"code\":0,\"data\":{\"id\":44}}"),
                "/api/v1/keys" => Json("{\"code\":0,\"data\":{\"id\":91,\"name\":\"client\",\"key\":\"local-client-key\"}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();
        store.Local.Codex.BaseUrl = string.Empty;

        LocalSub2ApiRoutingResult result = await service.ApplySourceAsync(store, "remote-one", CancellationToken.None);

        Assert.Equal(["Codex"], result.UpdatedPlatforms);
        Assert.Equal("http://127.0.0.1:8080/v1", result.ClientStore.Local.Codex.BaseUrl);
        Assert.Equal("local-client-key", result.ClientStore.Local.Codex.Secret);
        Assert.Contains(requests, item =>
            item.Path == "/api/v1/admin/accounts" &&
            item.Body.Contains("https://upstream.example.test", StringComparison.Ordinal) &&
            !item.Body.Contains("https://upstream.example.test/v1", StringComparison.Ordinal) &&
            item.Body.Contains($"\"group_ids\":[{groupId}]", StringComparison.Ordinal) &&
            item.Body.Contains("\"workspace_source_id\":\"remote-one\"", StringComparison.Ordinal) &&
            item.Body.Contains("\"priority\":1000", StringComparison.Ordinal));
        Assert.Contains(requests, item =>
            item.Path == "/api/v1/admin/groups" &&
            item.Body.Contains("\"rate_multiplier\":0", StringComparison.Ordinal));
        Assert.DoesNotContain(requests, item =>
            item.Method == HttpMethod.Post && item.Path == "/api/v1/account-contributions");
        Assert.Contains(requests, item =>
            item.Path == "/api/v1/keys" && item.Body.Contains($"\"group_id\":{groupId}", StringComparison.Ordinal));
        Assert.Contains(requests, item =>
            item.Method == HttpMethod.Put && item.Path == "/api/v1/account-contributions/44" &&
            item.Body.Contains("\"group_ids\":[]", StringComparison.Ordinal) &&
            item.Body.Contains("\"priority\":0", StringComparison.Ordinal));
    }

    [Fact]
    public async Task Zero_balance_is_repaired_before_managed_routing()
    {
        var requests = new List<(HttpMethod Method, string Path, string Body)>();
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null
                ? string.Empty
                : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            requests.Add((request.Method, path, body));
            return path switch
            {
                "/api/v1/admin/users/1" => Json("{\"code\":0,\"data\":{\"id\":1,\"balance\":0}}"),
                "/api/v1/admin/users/1/balance" => Json("{\"code\":0,\"data\":{\"id\":1,\"balance\":1}}"),
                "/api/v1/admin/groups/all?include_inactive=true" => Json("{\"code\":0,\"data\":[]}"),
                "/api/v1/account-contributions?page=1&limit=500" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/keys?page=1&page_size=1000" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/groups" => Json("{\"code\":0,\"data\":{\"id\":73,\"name\":\"managed\",\"platform\":\"openai\",\"rate_multiplier\":0}}"),
                "/api/v1/admin/accounts" => Json("{\"code\":0,\"data\":{\"id\":82,\"name\":\"external\",\"platform\":\"openai\"}}"),
                "/api/v1/keys" => Json("{\"code\":0,\"data\":{\"id\":91,\"name\":\"client\",\"key\":\"local-client-key\"}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin", balance: 0m));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);

        LocalSub2ApiRoutingResult result = await service.ApplySourceAsync(
            CreateStore(),
            "remote-one",
            CancellationToken.None);

        Assert.Equal(["Codex"], result.UpdatedPlatforms);
        Assert.Contains(requests, item =>
            item.Method == HttpMethod.Post &&
            item.Path == "/api/v1/admin/users/1/balance" &&
            item.Body.Contains("\"balance\":1", StringComparison.Ordinal) &&
            item.Body.Contains("\"operation\":\"add\"", StringComparison.Ordinal));
    }
    [Fact]
    public async Task Routing_continues_after_one_source_account_creation_fails()
    {
        int nextGroupId = 70;
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null
                ? string.Empty
                : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            if (path == "/api/v1/admin/groups")
            {
                string platform = body.Contains("\"platform\":\"anthropic\"", StringComparison.Ordinal)
                    ? "anthropic"
                    : "openai";
                return Json($"{{\"code\":0,\"data\":{{\"id\":{++nextGroupId},\"name\":\"managed\",\"platform\":\"{platform}\"}}}}");
            }

            return path switch
            {
                "/api/v1/admin/groups/all?include_inactive=true" => Json("{\"code\":0,\"data\":[]}"),
                "/api/v1/account-contributions?page=1&limit=500" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/keys?page=1&page_size=1000" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/accounts" when body.Contains("\"platform\":\"openai\"", StringComparison.Ordinal) =>
                    Json("{\"code\":1,\"message\":\"account creation failed\",\"data\":null}"),
                "/api/v1/admin/accounts" => Json("{\"code\":0,\"data\":{\"id\":82,\"name\":\"external\",\"platform\":\"anthropic\"}}"),
                "/api/v1/keys" => Json("{\"code\":0,\"data\":{\"id\":91,\"name\":\"client\",\"key\":\"new-claude-key\"}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();
        store.Local.Claude.Secret = "existing-claude-key";
        var claudeSource = new ProfileDefinition
        {
            Id = "remote-two",
            Name = "Remote two",
            Claude = new ClientProfile
            {
                BaseUrl = "https://claude.example.test",
                Secret = "claude-upstream-secret",
            },
        };
        store.CloudSources.Add(claudeSource);
        store.BackupSourceIds = ["remote-one", "remote-two"];
        store.BackupUpstreamEnabled = true;

        LocalSub2ApiRoutingResult result = await service.ApplyRoutingAsync(store, CancellationToken.None);

        Assert.Equal(["Claude", "Gemini", "Grok"], result.UpdatedPlatforms);
        LocalSub2ApiRoutingIssue issue = Assert.Single(result.Issues);
        Assert.Equal("Codex", issue.Platform);
        Assert.Contains("account creation failed", issue.Summary, StringComparison.Ordinal);
        Assert.Equal("existing-local-key", result.ClientStore.Local.Codex.Secret);
        Assert.Equal("new-claude-key", result.ClientStore.Local.Claude.Secret);
    }

    [Fact]
    public async Task Existing_managed_group_is_migrated_to_zero_rate()
    {
        var requests = new List<(HttpMethod Method, string Path, string Body)>();
        string marker = Convert.ToHexString(
                SHA256.HashData(Encoding.UTF8.GetBytes("remote-one::openai")).AsSpan(0, 6))
            .ToLowerInvariant();
        string groupName = $"共飞工作台-openai-{marker}";
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null
                ? string.Empty
                : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            requests.Add((request.Method, path, body));
            return path switch
            {
                "/api/v1/admin/groups/all?include_inactive=true" =>
                    Json($"{{\"code\":0,\"data\":[{{\"id\":73,\"name\":\"{groupName}\",\"platform\":\"openai\",\"rate_multiplier\":1}}]}}"),
                "/api/v1/account-contributions?page=1&limit=500" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/keys?page=1&page_size=1000" => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                "/api/v1/admin/groups/73" => Json($"{{\"code\":0,\"data\":{{\"id\":73,\"name\":\"{groupName}\",\"platform\":\"openai\",\"rate_multiplier\":0}}}}"),
                "/api/v1/admin/accounts" => Json("{\"code\":0,\"data\":{\"id\":82,\"name\":\"external\",\"platform\":\"openai\"}}"),
                "/api/v1/keys" => Json("{\"code\":0,\"data\":{\"id\":91,\"name\":\"client\",\"key\":\"local-client-key\"}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);

        LocalSub2ApiRoutingResult result = await service.ApplySourceAsync(
            CreateStore(),
            "remote-one",
            CancellationToken.None);

        Assert.Equal(["Codex"], result.UpdatedPlatforms);
        Assert.Contains(requests, item =>
            item.Method == HttpMethod.Put &&
            item.Path == "/api/v1/admin/groups/73" &&
            item.Body.Contains("\"rate_multiplier\":0", StringComparison.Ordinal));
    }

    [Fact]
    public async Task Local_source_is_not_registered_as_an_upstream()
    {
        int requests = 0;
        using var handler = new RoutingHandler(_ =>
        {
            requests++;
            throw new InvalidOperationException("Local source must not call Sub2API management APIs.");
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler);
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();

        LocalSub2ApiRoutingResult result = await service.ApplySourceAsync(
            store,
            ProfileSourceIds.LocalMachine,
            CancellationToken.None);

        Assert.Empty(result.UpdatedPlatforms);
        Assert.Same(store, result.ClientStore);
        Assert.Equal(0, requests);
    }

    [Fact]
    public async Task Backup_sources_are_registered_in_user_defined_order()
    {
        var accountBodies = new List<string>();
        long nextId = 100;
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null
                ? string.Empty
                : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            return (request.Method.Method, path) switch
            {
                ("GET", "/api/v1/admin/groups/all?include_inactive=true") => Json("{\"code\":0,\"data\":[]}"),
                ("GET", "/api/v1/account-contributions?page=1&limit=500") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("GET", "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("GET", "/api/v1/keys?page=1&page_size=1000") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("POST", "/api/v1/admin/groups") => CreateGroupResponse(body, ref nextId),
                ("POST", "/api/v1/admin/accounts") => CreateAccountResponse(body, accountBodies, ref nextId),
                ("POST", "/api/v1/keys") => Json($"{{\"code\":0,\"data\":{{\"id\":{++nextId},\"name\":\"client\",\"key\":\"local-key-{nextId}\"}}}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();
        store.CloudSources.Add(new ProfileDefinition
        {
            Id = "remote-two",
            Name = "Remote two",
            Codex = new ClientProfile
            {
                BaseUrl = "https://second.example.test/v1",
                Secret = "second-secret",
            },
        });
        store.BackupSourceIds = ["lan-default", "remote-two", "remote-one"];
        store.BackupUpstreamEnabled = true;

        LocalSub2ApiRoutingResult result = await service.ApplyRoutingAsync(store, CancellationToken.None);

        Assert.Empty(result.Issues);
        Assert.Equal(2, accountBodies.Count);
        Assert.Contains("\"workspace_source_id\":\"remote-two\"", accountBodies[0], StringComparison.Ordinal);
        Assert.Contains("\"priority\":1000", accountBodies[0], StringComparison.Ordinal);
        Assert.Contains("\"workspace_source_id\":\"remote-one\"", accountBodies[1], StringComparison.Ordinal);
        Assert.Contains("\"priority\":1001", accountBodies[1], StringComparison.Ordinal);
    }

    [Fact]
    public async Task Legacy_mixed_selection_does_not_enable_backup_without_explicit_switch()
    {
        var accountBodies = new List<string>();
        long nextId = 200;
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            string body = request.Content is null
                ? string.Empty
                : request.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            return (request.Method.Method, path) switch
            {
                ("GET", "/api/v1/admin/groups/all?include_inactive=true") => Json("{\"code\":0,\"data\":[]}"),
                ("GET", "/api/v1/account-contributions?page=1&limit=500") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("GET", "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("GET", "/api/v1/keys?page=1&page_size=1000") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("POST", "/api/v1/admin/groups") => CreateGroupResponse(body, ref nextId),
                ("POST", "/api/v1/admin/accounts") => CreateAccountResponse(body, accountBodies, ref nextId),
                ("POST", "/api/v1/keys") => Json($"{{\"code\":0,\"data\":{{\"id\":{++nextId},\"name\":\"client\",\"key\":\"local-key-{nextId}\"}}}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();
        store.Mixed.CodexSourceId = "remote-one";
        store.BackupSourceIds = [];

        LocalSub2ApiRoutingResult result = await service.ApplyRoutingAsync(store, CancellationToken.None);

        Assert.Empty(result.Issues);
        Assert.Empty(accountBodies);
    }

    [Fact]
    public async Task Recent_managed_account_usage_maps_back_to_external_source()
    {
        string recent = DateTimeOffset.UtcNow.AddSeconds(-20).ToString("O");
        string stale = DateTimeOffset.UtcNow.AddMinutes(-5).ToString("O");
        string responseBody = JsonSerializer.Serialize(new
        {
            code = 0,
            data = new
            {
                items = new object[]
                {
                    new { id = 1, last_used_at = recent, extra = new { workspace_external_source = true, workspace_source_id = "remote-one" } },
                    new { id = 2, last_used_at = stale, extra = new { workspace_external_source = true, workspace_source_id = "remote-two" } },
                    new { id = 3, last_used_at = recent, extra = new { } },
                },
            },
        });
        using var handler = new RoutingHandler(request => request.RequestUri!.PathAndQuery switch
        {
            "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc" => Json(responseBody),
            _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {request.RequestUri!.PathAndQuery}"),
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);

        ProfileStore store = CreateStore();
        store.BackupUpstreamEnabled = true;
        IReadOnlySet<string> active = await service.GetActiveBackupSourceIdsAsync(store, CancellationToken.None);

        Assert.Equal(["remote-one"], active);
    }

    [Fact]
    public async Task Disabled_backup_switch_removes_managed_accounts_and_preserves_configured_order()
    {
        var requests = new List<(HttpMethod Method, string Path)>();
        using var handler = new RoutingHandler(request =>
        {
            string path = request.RequestUri!.PathAndQuery;
            requests.Add((request.Method, path));
            return (request.Method.Method, path) switch
            {
                ("GET", "/api/v1/admin/groups/all?include_inactive=true") => Json("{\"code\":0,\"data\":[{\"id\":11,\"name\":\"共飞工作台-openai-备用上游\",\"platform\":\"openai\",\"rate_multiplier\":0},{\"id\":12,\"name\":\"共飞工作台-anthropic-备用上游\",\"platform\":\"anthropic\",\"rate_multiplier\":0},{\"id\":13,\"name\":\"共飞工作台-gemini-备用上游\",\"platform\":\"gemini\",\"rate_multiplier\":0},{\"id\":14,\"name\":\"共飞工作台-grok-备用上游\",\"platform\":\"grok\",\"rate_multiplier\":0}]}"),
                ("GET", "/api/v1/account-contributions?page=1&limit=500") => Json("{\"code\":0,\"data\":{\"items\":[]}}"),
                ("GET", "/api/v1/admin/accounts?page=1&page_size=1000&sort_by=name&sort_order=asc") => Json("{\"code\":0,\"data\":{\"items\":[{\"id\":81,\"name\":\"共飞工作台-old-上游-key\",\"platform\":\"openai\"}]}}"),
                ("GET", "/api/v1/keys?page=1&page_size=1000") => Json("{\"code\":0,\"data\":{\"items\":[{\"id\":21,\"name\":\"共飞工作台-Codex-客户端\",\"key\":\"k1\"},{\"id\":22,\"name\":\"共飞工作台-Claude-客户端\",\"key\":\"k2\"},{\"id\":23,\"name\":\"共飞工作台-Gemini-客户端\",\"key\":\"k3\"},{\"id\":24,\"name\":\"共飞工作台-Grok-客户端\",\"key\":\"k4\"}]}}"),
                ("DELETE", "/api/v1/admin/accounts/81") => Json("{\"code\":0,\"data\":{}}"),
                ("PUT", _) when path.StartsWith("/api/v1/keys/", StringComparison.Ordinal) => Json("{\"code\":0,\"data\":{\"id\":1,\"name\":\"client\",\"key\":\"local-key\"}}"),
                _ => throw new InvalidOperationException($"Unexpected request: {request.Method} {path}"),
            };
        });
        using var session = new StubSessionManager(CreateAccess("admin"));
        using var client = new HttpClient(handler) { BaseAddress = new Uri("http://127.0.0.1:8080/") };
        using var service = new LocalSub2ApiRoutingService(session, client);
        ProfileStore store = CreateStore();
        store.BackupSourceIds = ["remote-one"];
        store.BackupUpstreamEnabled = false;

        await service.ApplyRoutingAsync(store, CancellationToken.None);

        Assert.Equal(["remote-one"], store.BackupSourceIds);
        Assert.Contains(requests, request => request.Method == HttpMethod.Delete && request.Path == "/api/v1/admin/accounts/81");
        Assert.DoesNotContain(requests, request => request.Method == HttpMethod.Post && request.Path == "/api/v1/admin/accounts");
    }

    private static HttpResponseMessage CreateGroupResponse(string body, ref long nextId)
    {
        using JsonDocument document = JsonDocument.Parse(body);
        string platform = document.RootElement.GetProperty("platform").GetString()!;
        return Json($"{{\"code\":0,\"data\":{{\"id\":{++nextId},\"name\":\"managed\",\"platform\":\"{platform}\",\"rate_multiplier\":0}}}}" );
    }

    private static HttpResponseMessage CreateAccountResponse(string body, List<string> accountBodies, ref long nextId)
    {
        accountBodies.Add(body);
        return Json($"{{\"code\":0,\"data\":{{\"id\":{++nextId},\"name\":\"external\"}}}}" );
    }

    private static ProfileStore CreateStore()
    {
        ProfileDefinition local = ProfileDefinition.CreateLocalDefaults();
        local.Codex.Secret = "existing-local-key";
        var remote = new ProfileDefinition
        {
            Id = "remote-one",
            Name = "Remote one",
            Codex = new ClientProfile
            {
                BaseUrl = "https://upstream.example.test/v1",
                Secret = "upstream-secret",
            },
        };
        return new ProfileStore
        {
            Local = local,
            LocalSources = [local, ProfileDefinition.CreateLanDefaults()],
            Cloud = remote,
            CloudSources = [remote],
            SelectedCloudSourceId = remote.Id,
            SelectedLocalSourceId = local.Id,
        };
    }

    private static Sub2ApiSessionAccess CreateAccess(string role, decimal balance = 1m) => new(
        new Uri("http://127.0.0.1:8080/"),
        "session-token",
        1,
        role,
        balance,
        0m,
        DateTimeOffset.UtcNow.AddHours(1));

    private static HttpResponseMessage Json(string body) => new(HttpStatusCode.OK)
    {
        Content = new StringContent(body, Encoding.UTF8, "application/json"),
    };

    private sealed class RoutingHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
            => Task.FromResult(respond(request));
    }

    private sealed class StubSessionManager(Sub2ApiSessionAccess access) : ISub2ApiSessionManager
    {
        public Sub2ApiSessionState Current { get; } = new(
            true,
            false,
            access.IsAdministrator,
            access.IsAdministrator ? "管理员" : "普通用户",
            access.Balance,
            access.FrozenBalance,
            access.ExpiresAtUtc,
            access.ApiBaseUri,
            "已登录");

        public event EventHandler? SessionChanged { add { } remove { } }
        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;
        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.FromResult(access);
        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, CancellationToken cancellationToken) => Task.FromResult(access);
        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, bool allowInsecurePublicHttp, CancellationToken cancellationToken) => Task.FromResult(access);
        public Task LogoutAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        public void Dispose() { }
    }

    private sealed class RecoveringRoutingSessionManager(Sub2ApiSessionAccess access) : ISub2ApiSessionManager
    {
        public int LocalControlLoginCalls { get; private set; }
        public string? LastLocalControlToken { get; private set; }
        public Sub2ApiSessionState Current { get; private set; } = new(
            true, false, true, "管理员", access.Balance, access.FrozenBalance,
            access.ExpiresAtUtc, access.ApiBaseUri, "已登录");
        public event EventHandler? SessionChanged;

        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken)
            => Current.IsAuthenticated
                ? Task.FromResult(access)
                : Task.FromException<Sub2ApiSessionAccess>(
                    new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable));

        public Task<Sub2ApiSessionAccess> LoginLocalControlAsync(
            Uri apiBaseUri,
            string localControlToken,
            CancellationToken cancellationToken)
        {
            LocalControlLoginCalls++;
            LastLocalControlToken = localControlToken;
            Current = new Sub2ApiSessionState(
                true, false, true, "管理员", access.Balance, access.FrozenBalance,
                access.ExpiresAtUtc, access.ApiBaseUri, "已登录");
            SessionChanged?.Invoke(this, EventArgs.Empty);
            return Task.FromResult(access);
        }

        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, CancellationToken cancellationToken)
            => Task.FromResult(access);
        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, bool allowInsecurePublicHttp, CancellationToken cancellationToken)
            => Task.FromResult(access);
        public Task LogoutAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        public void Dispose() { }
    }
}
