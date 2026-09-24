using System.Collections.Concurrent;
using System.Collections.Specialized;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;

namespace AiSwitch.Wpf.Tests;

public sealed class AccountCenterTests
{
    [Fact]
    public async Task Client_maps_owned_account_without_exposing_credentials()
    {
        const string secret = "secret-access-token-must-not-enter-ui";
        var handler = new StubHandler(request =>
        {
            Assert.Equal("Bearer", request.Headers.Authorization?.Scheme);
            Assert.Equal("session-token", request.Headers.Authorization?.Parameter);
            Assert.Equal("/api/v1/account-contributions", request.RequestUri?.AbsolutePath);
            return JsonResponse("""
                {"code":0,"data":{"items":[{
                  "id":715,"name":"owner@example.test","platform":"openai","type":"oauth",
                  "credentials":{"access_token":"__SECRET__"},"extra":{"share_mode":"pool","contribution_governance_state":"paused","contribution_governance_reason":"等待贡献者更新凭据"},
                  "proxy_id":9,"proxy":{"id":9,"name":"东京线路"},"concurrency":30,"load_factor":3,
                  "priority":20,"status":"active","error_message":"","schedulable":true,
                  "temp_unschedulable_reason":"upstream timeout","rate_limited_at":"2026-07-17T09:00:00Z","overload_until":"2026-07-17T09:30:00Z",
                  "created_at":"2026-07-17T08:00:00Z","last_used_at":"2026-07-17T10:00:00Z",
                  "group_ids":[8],"groups":[{"id":8,"name":"plus","platform":"openai"}]
                }],"total":1,"page":1,"limit":10,
                "wallet":{"balance":12.5,"earned_total":20,"spent_total":7.5},
                "income_rates":{"share_reward_rate_percent":99,"own_income_rate_percent":1}}}
                """.Replace("__SECRET__", secret, StringComparison.Ordinal));
        });
        using var httpClient = new HttpClient(handler);
        using var client = new Sub2ApiAccountCenterClient(httpClient);
        Sub2ApiSessionAccess access = CreateAccess();

        AccountCenterPage page = await client.ListAsync(access, 1, 10, CancellationToken.None);

        AccountCenterAccount account = Assert.Single(page.Items);
        Assert.Equal(715, account.Id);
        Assert.Equal("upstream timeout", account.TempUnschedulableReason);
        Assert.NotNull(account.RateLimitedAt);
        Assert.NotNull(account.OverloadUntil);
        Assert.Equal("plus", Assert.Single(account.GroupNames));
        Assert.Equal("东京线路", account.ProxyName);
        Assert.Null(typeof(AccountCenterPage).GetProperty("Wallet"));
        Assert.Null(typeof(AccountCenterPage).GetProperty("IncomeRates"));
        Assert.Null(typeof(AccountCenterAccount).GetProperty("ShareMode"));
        Assert.Null(typeof(AccountCenterAccount).GetProperty("GovernanceState"));
        Assert.DoesNotContain(
            typeof(AccountCenterAccount).GetProperties(),
            property => property.Name.Contains("credential", StringComparison.OrdinalIgnoreCase) ||
                        property.Name.Contains("token", StringComparison.OrdinalIgnoreCase));
        Assert.DoesNotContain(secret, account.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task Client_edit_options_only_request_personal_account_proxies()
    {
        var requestedPaths = new ConcurrentBag<string>();
        var handler = new StubHandler(request =>
        {
            string path = request.RequestUri?.AbsolutePath ?? string.Empty;
            requestedPaths.Add(path);
            return JsonResponse("""{"code":0,"data":[{"id":9,"name":"直连代理","protocol":"http","host":"127.0.0.1","port":7890,"status":"active"}]}""");
        });
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        AccountCenterEditOptions options = await client.GetEditOptionsAsync(CreateAccess(), CancellationToken.None);

        Assert.Empty(options.PrivateGroups);
        Assert.Single(options.Proxies);
        Assert.DoesNotContain(requestedPaths, path => path.Contains("groups", StringComparison.Ordinal));
        Assert.Equal("/api/v1/account-contributions/proxies", Assert.Single(requestedPaths));
    }

    [Fact]
    public async Task Client_creates_personal_proxy_without_returning_credentials()
    {
        string requestBody = string.Empty;
        var handler = new StubHandler(request =>
        {
            Assert.Equal(HttpMethod.Post, request.Method);
            Assert.Equal("/api/v1/account-contributions/proxies", request.RequestUri?.AbsolutePath);
            requestBody = request.Content!.ReadAsStringAsync().GetAwaiter().GetResult();
            return JsonResponse("""{"code":0,"data":{"id":12,"name":"本机代理","protocol":"http","host":"127.0.0.1","port":10808,"status":"active","has_password":true}}""");
        });
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        AccountCenterProxy proxy = await client.CreateProxyAsync(
            CreateAccess(),
            new AccountCenterProxyCreateRequest(" 本机代理 ", "HTTP", " 127.0.0.1 ", 10808, "alice", "secret"),
            CancellationToken.None);

        Assert.Equal(12, proxy.Id);
        Assert.Equal("本机代理", proxy.Name);
        using JsonDocument body = JsonDocument.Parse(requestBody);
        Assert.Equal("http", body.RootElement.GetProperty("protocol").GetString());
        Assert.Equal("127.0.0.1", body.RootElement.GetProperty("host").GetString());
        Assert.DoesNotContain("secret", proxy.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task Client_maps_rolling_usage_windows_and_local_rollup()
    {
        var handler = new StubHandler(_ => JsonResponse("""
            {"code":0,"data":{
              "upstream":{"source":"active",
                "five_hour":{"utilization":32,"resets_at":"2026-07-18T00:00:00Z","window_stats":{"requests":120,"tokens":26600000,"cost":12}},
                "seven_day":{"utilization":5,"resets_at":"2026-07-24T00:00:00Z","window_stats":{"requests":230,"tokens":36100000,"cost":29.07}}},
              "stats":{"summary":{"total_requests":350,"total_tokens":62700000,"total_cost":41.07}},"days":30}}
            """));
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        AccountCenterUsageSummary? summary = await client.GetUsageAsync(CreateAccess(), 715, false, CancellationToken.None);

        Assert.NotNull(summary);
        Assert.Equal(32, summary.FiveHour?.Utilization);
        Assert.Equal(120, summary.FiveHour?.Requests);
        Assert.Equal(5, summary.SevenDay?.Utilization);
        Assert.False(summary.IsLocalRollup);
    }

    [Fact]
    public async Task Client_posts_personal_api_key_creation_to_contribution_endpoint()
    {
        string requestBody = string.Empty;
        var handler = new StubHandler(request =>
        {
            Assert.Equal(HttpMethod.Post, request.Method);
            Assert.Equal("/api/v1/account-contributions", request.RequestUri?.AbsolutePath);
            requestBody = request.Content!.ReadAsStringAsync().GetAwaiter().GetResult();
            return JsonResponse("""
                {"code":0,"data":{"total":1,"created":1,"failed":0,"items":[{"index":0,"name":"my-claude","account_id":88,"status":"created"}]}}
                """);
        });
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        AccountCenterCreateResult result = await client.CreateAsync(
            CreateAccess(),
            new AccountCenterCreateRequest(
                "api_key", "my-claude", "anthropic", "sk-ant-test", "https://api.example.test", [],
                6, 2, 4, [7], 9, "claude-sonnet-4"),
            CancellationToken.None);

        using JsonDocument document = JsonDocument.Parse(requestBody);
        JsonElement root = document.RootElement;
        Assert.Equal("api_key", root.GetProperty("mode").GetString());
        Assert.Equal("anthropic", root.GetProperty("platform").GetString());
        Assert.Equal("sk-ant-test", root.GetProperty("api_key").GetString());
        Assert.Equal(7, root.GetProperty("group_ids")[0].GetInt64());
        Assert.Equal(9, root.GetProperty("proxy_id").GetInt64());
        Assert.Equal(1, result.Created);
        Assert.Equal(88, Assert.Single(result.Items).AccountId);
    }

    [Fact]
    public async Task Client_maps_duplicate_account_as_skipped()
    {
        var handler = new StubHandler(_ => JsonResponse("""
            {"code":0,"data":{"total":1,"created":0,"failed":0,"skipped":1,"items":[
              {"index":1,"name":"owner@example.test","status":"skipped","message":"同名账号已存在，已跳过"}
            ]}}
            """));
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        AccountCenterCreateResult result = await client.CreateAsync(
            CreateAccess(),
            new AccountCenterCreateRequest(
                "api_key", "owner@example.test", "openai", "sk-test", string.Empty, [],
                3, 0, 0, [], 0, string.Empty),
            CancellationToken.None);

        Assert.Equal(1, result.Skipped);
        Assert.Equal("skipped", Assert.Single(result.Items).Status);
    }

    [Fact]
    public async Task Client_reports_request_timeout_separately_from_gateway_unavailable()
    {
        var handler = new AsyncStubHandler(async (_, cancellationToken) =>
        {
            await Task.Delay(TimeSpan.FromSeconds(5), cancellationToken);
            return JsonResponse("""{"code":0,"data":{"items":[],"total":0,"page":1,"limit":10}}""");
        });
        using var httpClient = new HttpClient(handler) { Timeout = TimeSpan.FromMilliseconds(20) };
        using var client = new Sub2ApiAccountCenterClient(httpClient);

        AccountCenterClientException error = await Assert.ThrowsAsync<AccountCenterClientException>(
            () => client.ListAsync(CreateAccess(), 1, 10, CancellationToken.None));

        Assert.Equal(AccountCenterClientFailure.RequestTimedOut, error.Failure);
        Assert.Contains("20 毫秒", error.ServerMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Client_loads_contributed_account_models_and_streams_detailed_test_events()
    {
        string requestBody = string.Empty;
        var handler = new StubHandler(request =>
        {
            if (request.Method == HttpMethod.Get)
            {
                Assert.Equal("/api/v1/account-contributions/715/models", request.RequestUri?.AbsolutePath);
                return JsonResponse("""{"code":0,"data":[{"id":"gpt-5.4","display_name":"GPT 5.4"}]}""");
            }

            Assert.Equal("/api/v1/account-contributions/715/test-stream", request.RequestUri?.AbsolutePath);
            Assert.Equal("1", Assert.Single(request.Headers.GetValues("X-Admin-UI-Request")));
            requestBody = request.Content!.ReadAsStringAsync().GetAwaiter().GetResult();
            return new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StringContent(
                    "data: {\"type\":\"test_start\",\"model\":\"gpt-5.4\"}\n\n" +
                    "data: {\"type\":\"content\",\"text\":\"pong\"}\n\n" +
                    "data: {\"type\":\"test_complete\",\"success\":true}\n\n",
                    Encoding.UTF8,
                    "text/event-stream"),
            };
        });
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));
        var events = new List<AccountCenterTestEvent>();

        IReadOnlyList<AccountCenterTestModel> models = await client.GetAvailableModelsAsync(CreateAccess(), 715, CancellationToken.None);
        AccountCenterDetailedTestResult result = await client.RunDetailedTestAsync(
            CreateAccess(),
            715,
            new AccountCenterDetailedTestRequest("gpt-5.4", "hello", "compact"),
            new InlineProgress<AccountCenterTestEvent>(events.Add),
            CancellationToken.None);

        Assert.Equal("gpt-5.4", Assert.Single(models).Id);
        Assert.True(result.Success);
        Assert.Equal(["test_start", "content", "test_complete"], events.Select(item => item.Type).ToArray());
        using JsonDocument body = JsonDocument.Parse(requestBody);
        Assert.Equal("gpt-5.4", body.RootElement.GetProperty("model_id").GetString());
        Assert.Equal("hello", body.RootElement.GetProperty("prompt").GetString());
        Assert.Equal("compact", body.RootElement.GetProperty("mode").GetString());
    }

    [Fact]
    public async Task Client_routes_more_actions_to_local_admin_account_endpoints()
    {
        string path = string.Empty;
        var handler = new StubHandler(request =>
        {
            path = request.RequestUri?.AbsolutePath ?? string.Empty;
            return JsonResponse("""{"code":0,"data":{"models":["gpt-5.4"]}}""");
        });
        using var client = new Sub2ApiAccountCenterClient(new HttpClient(handler));

        await client.RunAdminActionAsync(
            CreateAccess(),
            715,
            AccountCenterAdminAction.SyncUpstreamModels,
            CancellationToken.None);

        Assert.Equal("/api/v1/admin/accounts/715/models/sync-upstream", path);
    }

    [Fact]
    public void Account_view_model_formats_window_percentage_for_the_visual_meter()
    {
        var account = new AccountCenterAccountViewModel(new AccountCenterAccount(
            715,
            "owner@example.test",
            "openai",
            "oauth",
            3,
            3,
            20,
            "active",
            string.Empty,
            null,
            DateTimeOffset.UtcNow,
            true,
            null,
            null,
            null,
            null,
            [],
            []));

        account.ApplyUsage(new AccountCenterUsageSummary(
            new AccountCenterUsageWindow(32.5, null, 12, 3456, 0.42),
            new AccountCenterUsageWindow(5, null, 20, 6789, 0.84),
            false, 32, 10245, 1.26));

        Assert.Equal("32.5%", account.FiveHourLabel);
        Assert.Equal("5%", account.SevenDayLabel);
        Assert.Contains("12 次", account.FiveHourDetail);
    }

    [Fact]
    public void Account_view_model_exposes_local_health_without_sharing_governance()
    {
        var account = new AccountCenterAccountViewModel(new AccountCenterAccount(
            715,
            "owner@example.test",
            "openai",
            "oauth",
            30,
            3,
            20,
            "active",
            "refresh token expired",
            DateTimeOffset.Parse("2026-07-17T10:00:00Z"),
            DateTimeOffset.Parse("2026-07-17T08:00:00Z"),
            false,
            null,
            null,
            null,
            null,
            [8],
            ["plus"],
            "upstream timeout"));

        Assert.Equal("需关注", account.HealthStatusLabel);
        Assert.Equal("refresh token expired", account.HealthMessageLabel);
        Assert.Equal("不参与调度", account.SchedulableLabel);
        Assert.True(account.NeedsAttention);
        Assert.Null(typeof(AccountCenterAccountViewModel).GetProperty("GovernanceStatusLabel"));
        Assert.Null(typeof(AccountCenterAccountViewModel).GetProperty("ScopeLabel"));
    }

    [Fact]
    public async Task View_model_reuses_page_and_usage_cache_during_ten_minutes()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new StubAccountCenterClient();
        using var viewModel = new AccountCenterViewModel(session, client, _ => true);
        var profile = new ConnectionProfile
        {
            Id = ConnectionProfileIds.LocalMachine,
            Name = "本机中转",
            Kind = ConnectionProfileKind.Local,
            BaseUrl = "http://127.0.0.1:8080/v1",
        };
        viewModel.ApplyConnections(
            [profile],
            new ConnectionProfileSelection(null, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine));

        await viewModel.ActivateAsync();
        await viewModel.ActivateAsync();

        Assert.Equal(1, client.ListCalls);
        Assert.Equal(2, client.UsageCalls);
        Assert.Equal(2, viewModel.Accounts.Count);
        Assert.Equal("本机后台", viewModel.SourceName);
        Assert.Contains("使用缓存", viewModel.UpdatedLabel);
    }

    [Fact]
    public async Task View_model_loads_every_backend_page_without_exposing_group_filters()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        AccountCenterAccount[] accounts = Enumerable.Range(1, 205)
            .Select(index => CreateAccount(
                index,
                $"account-{index:D3}",
                index % 2 == 0 ? "openai" : "anthropic",
                index == 205 ? [] : ["默认分组"]))
            .ToArray();
        var client = new BulkAccountCenterClient(accounts);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        Assert.Equal(3, client.ListCalls);
        Assert.Equal(205, viewModel.FilteredAccountCount);
        Assert.Null(typeof(AccountCenterViewModel).GetProperty("GroupFilterOptions"));
    }

    [Fact]
    public async Task Single_account_test_opens_model_dialog_and_displays_backend_stream()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient([CreateAccount(1, "owner@example.test")]);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        AccountCenterAccountViewModel account = Assert.Single(viewModel.Accounts);
        await viewModel.TestAccountCommand.ExecuteAsync(account);

        Assert.True(viewModel.IsTestDialogOpen);
        Assert.Equal("gpt-5.4", Assert.Single(viewModel.TestModels).Id);
        var pongObserved = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        NotifyCollectionChangedEventHandler onLogChanged = (_, args) =>
        {
            if (args.NewItems?.OfType<AccountCenterTestLogLine>()
                .Any(line => line.Text.Contains("pong", StringComparison.Ordinal)) == true)
            {
                pongObserved.TrySetResult();
            }
        };
        viewModel.TestLogLines.CollectionChanged += onLogChanged;
        try
        {
            await viewModel.StartDetailedAccountTestCommand.ExecuteAsync(null);
            await pongObserved.Task.WaitAsync(TimeSpan.FromSeconds(2));
        }
        finally
        {
            viewModel.TestLogLines.CollectionChanged -= onLogChanged;
        }

        AccountCenterTestLogLine[] logLines = viewModel.TestLogLines.ToArray();

        Assert.True(viewModel.TestSucceeded);
        Assert.Contains(logLines, line => line.Text.Contains("出口：直连（未配置代理）", StringComparison.Ordinal));
        Assert.Contains(logLines, line => line.Text.Contains("pong", StringComparison.Ordinal));
        Assert.Equal("gpt-5.4", Assert.Single(client.DetailedTestRequests).ModelId);
    }

    [Fact]
    public async Task Selection_is_kept_across_pages_and_batch_test_uses_all_selected_accounts()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient(
            Enumerable.Range(1, 12).Select(index => CreateAccount(index, $"account-{index:D2}")).ToArray());
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        viewModel.Accounts[0].IsSelected = true;
        await viewModel.NextPageCommand.ExecuteAsync(null);
        viewModel.Accounts[0].IsSelected = true;

        Assert.Equal(2, viewModel.SelectedAccountCount);
        await viewModel.TestSelectedAccountsCommand.ExecuteAsync(null);

        Assert.Equal(2, client.TestedIds.Count);
        Assert.Contains(1, client.TestedIds);
        Assert.Contains(11, client.TestedIds);
    }

    [Fact]
    public async Task Drag_reorder_persists_personal_account_priority_and_updates_visible_order()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient(
            [CreateAccount(1, "alpha"), CreateAccount(2, "beta"), CreateAccount(3, "gamma")]);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        AccountCenterAccountViewModel source = viewModel.Accounts[2];
        AccountCenterAccountViewModel target = viewModel.Accounts[0];
        await viewModel.ReorderAccountAsync(source, target, insertAfter: false);

        Assert.Equal([3L, 1L, 2L], viewModel.Accounts.Select(account => account.Id).ToArray());
        Assert.Equal([1, 2, 3], viewModel.Accounts.Select(account => account.SchedulingOrder).ToArray());
        Assert.Equal(0, client.Updates[3].Priority);
        Assert.Equal(1, client.Updates[1].Priority);
        Assert.Equal(2, client.Updates[2].Priority);

        await viewModel.RefreshCommand.ExecuteAsync(null);
        Assert.Equal([3L, 1L, 2L], viewModel.Accounts.Select(account => account.Id).ToArray());
    }

    [Fact]
    public async Task Batch_edit_preserves_each_account_name()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient(
            [CreateAccount(1, "alpha"), CreateAccount(2, "beta")]);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        foreach (AccountCenterAccountViewModel account in viewModel.Accounts) account.IsSelected = true;
        await viewModel.BeginBatchEditCommand.ExecuteAsync(null);
        viewModel.EditConcurrency = 8;
        await viewModel.SaveEditCommand.ExecuteAsync(null);

        Assert.Equal("alpha", client.Updates[1].Name);
        Assert.Equal("beta", client.Updates[2].Name);
        Assert.All(client.Updates.Values, update => Assert.Equal(8, update.Concurrency));
    }

    [Fact]
    public async Task Personal_account_edit_removes_group_and_priority_controls()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        AccountCenterEditOptions options = new(
            [new AccountCenterGroup(1, "个人分组", "openai", 1)],
            []);
        var client = new BulkAccountCenterClient(
            [CreateAccount(1, "personal", groups: ["个人分组"])],
            options);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        AccountCenterAccountViewModel account = Assert.Single(viewModel.Accounts);
        await viewModel.BeginEditCommand.ExecuteAsync(account);

        viewModel.EditConcurrency = 12;
        viewModel.EditPriority = 15;
        await viewModel.SaveEditCommand.ExecuteAsync(null);

        AccountCenterUpdateRequest update = client.Updates[1];
        Assert.Empty(update.GroupIds);
        Assert.Null(update.Priority);
    }

    [Fact]
    public async Task Batch_delete_confirms_once_and_deletes_every_selected_account()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient(
            [CreateAccount(1, "alpha"), CreateAccount(2, "beta"), CreateAccount(3, "gamma")]);
        int confirmations = 0;
        using var viewModel = CreateViewModel(session, client, _ =>
        {
            confirmations++;
            return true;
        });

        await viewModel.ActivateAsync();
        foreach (AccountCenterAccountViewModel account in viewModel.Accounts.Take(2)) account.IsSelected = true;
        await viewModel.DeleteSelectedAccountsCommand.ExecuteAsync(null);

        Assert.Equal(1, confirmations);
        Assert.Equal([1L, 2L], client.DeletedIds.OrderBy(id => id).ToArray());
    }

    [Fact]
    public async Task Add_api_key_account_uses_local_personal_account_flow_and_refreshes_list()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        AccountCenterEditOptions options = new(
            [new AccountCenterGroup(7, "Claude 默认组", "anthropic", 1)],
            [new AccountCenterProxy(9, "东京线路", "http", "127.0.0.1", 7890, "active")]);
        var client = new BulkAccountCenterClient([], options);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.ActivateAsync();
        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectedAddPlatform = Assert.Single(viewModel.AddPlatformOptions, option => option.Id == "anthropic");
        Assert.Equal(9, viewModel.SelectedAddProxy?.Id);
        viewModel.AddName = "my-claude";
        viewModel.AddApiKey = "sk-ant-test";
        viewModel.AddConcurrency = 6;
        viewModel.AddLoadFactor = 2;

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.NotNull(client.CreatedRequest);
        Assert.Equal("api_key", client.CreatedRequest.Mode);
        Assert.Equal("anthropic", client.CreatedRequest.Platform);
        Assert.Equal("sk-ant-test", client.CreatedRequest.ApiKey);
        Assert.Empty(client.CreatedRequest.GroupIds);
        Assert.Equal(9, client.CreatedRequest.ProxyId);
        Assert.False(viewModel.IsAdding);
        Assert.Equal("my-claude", Assert.Single(viewModel.Accounts).Name);
    }

    [Fact]
    public async Task Add_dialog_creates_refreshes_and_selects_proxy()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient([]);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.ToggleAddProxyEditorCommand.Execute(null);
        viewModel.AddProxyName = "本机 v2rayN";
        viewModel.AddProxyHost = "127.0.0.1";
        viewModel.AddProxyPort = 10808;

        await viewModel.CreateAddProxyCommand.ExecuteAsync(null);

        Assert.NotNull(client.CreatedProxyRequest);
        Assert.Equal("127.0.0.1", client.CreatedProxyRequest.Host);
        Assert.Equal(10808, client.CreatedProxyRequest.Port);
        Assert.Equal(client.CreatedProxyId, viewModel.SelectedAddProxy?.Id);
        Assert.False(viewModel.IsAddProxyEditorOpen);
        Assert.Contains("已添加并选中", viewModel.AddValidationMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Add_duplicate_api_key_name_reports_skip_instead_of_failure()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        AccountCenterCreateResult skipped = new(
            1,
            0,
            0,
            [new AccountCenterCreateResultItem(1, "owner@example.test", null, "skipped", "同名账号已存在，已跳过")],
            1);
        var client = new BulkAccountCenterClient([CreateAccount(1, "owner@example.test")], createResult: skipped);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectedAddPlatform = Assert.Single(viewModel.AddPlatformOptions, option => option.Id == "openai");
        viewModel.SelectedAddMode = Assert.Single(viewModel.AddModeOptions, option => option.Id == "api_key");
        viewModel.AddName = " Owner@Example.test ";
        viewModel.AddApiKey = "sk-test";

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.True(viewModel.IsAdding);
        Assert.Contains("1 个同名账号已存在并跳过", viewModel.AddValidationMessage, StringComparison.Ordinal);
        Assert.DoesNotContain("添加失败", viewModel.AddValidationMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Sub2api_export_file_is_recognized_and_submitted_as_one_batch()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient([]);
        const string exportDocument = """
            {
              "exported_at": "2026-07-20T13:45:24+08:00",
              "proxies": [],
              "accounts": [
                { "name": "first@example.test", "platform": "openai", "type": "oauth", "credentials": { "access_token": "token-one" } },
                { "name": "second@example.test", "platform": "openai", "type": "oauth", "credentials": { "access_token": "token-two" } }
              ]
            }
            """;
        using var viewModel = new AccountCenterViewModel(
            session,
            client,
            confirmDelete: _ => true,
            selectOAuthDocuments: () => new AccountCenterOAuthDocuments([exportDocument], "sub2api-export.json"));
        ApplyLocalConnection(viewModel);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectAddOAuthFilesCommand.Execute(null);
        Assert.Contains("识别到 2 个账号", viewModel.AddOAuthFilesLabel, StringComparison.Ordinal);

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.NotNull(client.CreatedRequest);
        Assert.Equal("oauth", client.CreatedRequest.Mode);
        using JsonDocument submitted = JsonDocument.Parse(Assert.Single(client.CreatedRequest.Contents));
        Assert.Equal(2, submitted.RootElement.GetProperty("accounts").GetArrayLength());
    }

    [Fact]
    public async Task Sub2api_export_with_twenty_accounts_is_submitted_in_two_backend_safe_batches()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient(
            [],
            createResult: new AccountCenterCreateResult(10, 10, 0, []));
        string accounts = string.Join(",", Enumerable.Range(1, 20).Select(index => $$"""
            {
              "name": "account-{{index:D2}}@example.test",
              "platform": "openai",
              "type": "oauth",
              "credentials": { "access_token": "token-{{index:D2}}" }
            }
            """));
        string exportDocument = $$"""
            {
              "exported_at": "2026-07-21T15:26:49+08:00",
              "proxies": [],
              "accounts": [{{accounts}}]
            }
            """;
        using var viewModel = new AccountCenterViewModel(
            session,
            client,
            confirmDelete: _ => true,
            selectOAuthDocuments: () => new AccountCenterOAuthDocuments([exportDocument], "sub2api-export.json"));
        ApplyLocalConnection(viewModel);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectAddOAuthFilesCommand.Execute(null);
        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.Equal(2, client.CreatedRequests.Count);
        Assert.All(client.CreatedRequests, request => Assert.Single(request.Contents));
        Assert.All(client.CreatedRequests, request =>
        {
            using JsonDocument submitted = JsonDocument.Parse(Assert.Single(request.Contents));
            Assert.Equal(10, submitted.RootElement.GetProperty("accounts").GetArrayLength());
        });
        Assert.Contains("已添加 20 个个人账号", viewModel.StatusMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Sub2api_export_skips_existing_account_names_before_submission()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient([CreateAccount(1, "first@example.test")]);
        const string exportDocument = """
            { "accounts": [
              { "name": "first@example.test", "platform": "openai", "type": "oauth", "credentials": { "access_token": "token-one" } },
              { "name": "second@example.test", "platform": "openai", "type": "oauth", "credentials": { "access_token": "token-two" } }
            ] }
            """;
        using var viewModel = new AccountCenterViewModel(
            session,
            client,
            confirmDelete: _ => true,
            selectOAuthDocuments: () => new AccountCenterOAuthDocuments([exportDocument], "sub2api-export.json"));
        ApplyLocalConnection(viewModel);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectAddOAuthFilesCommand.Execute(null);
        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.NotNull(client.CreatedRequest);
        string filteredDocument = Assert.Single(client.CreatedRequest.Contents);
        using JsonDocument parsed = JsonDocument.Parse(filteredDocument);
        JsonElement importedAccount = Assert.Single(parsed.RootElement.GetProperty("accounts").EnumerateArray());
        Assert.Equal("second@example.test", importedAccount.GetProperty("name").GetString());
        Assert.Contains("已存在的 1 个同名账号未重复导入", viewModel.StatusMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Save_add_recovers_local_control_session_after_modal_was_opened()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new RecoveringSessionManager(access);
        var client = new BulkAccountCenterClient([]);
        using var viewModel = new AccountCenterViewModel(
            session,
            client,
            confirmDelete: _ => true,
            localControlTokenProvider: () => "local-control-token");
        ApplyLocalConnection(viewModel);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        session.SignOut();
        viewModel.SelectedAddPlatform = Assert.Single(viewModel.AddPlatformOptions, option => option.Id == "anthropic");
        viewModel.AddApiKey = "sk-ant-test";

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.Equal(3, session.LocalControlLoginCalls);
        Assert.NotNull(client.CreatedRequest);
        Assert.False(viewModel.IsAdding);
    }

    [Fact]
    public async Task Save_add_shows_session_error_instead_of_silently_returning()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new RecoveringSessionManager(access);
        var client = new BulkAccountCenterClient([]);
        using var viewModel = CreateViewModel(session, client);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        session.SignOut();
        viewModel.SelectedAddPlatform = Assert.Single(viewModel.AddPlatformOptions, option => option.Id == "anthropic");
        viewModel.AddApiKey = "sk-ant-test";

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.True(viewModel.IsAdding);
        Assert.Contains("本机管理会话尚未就绪", viewModel.AddValidationMessage, StringComparison.Ordinal);
        Assert.Null(client.CreatedRequest);
    }

    [Fact]
    public async Task Begin_add_shows_a_visible_error_when_local_administrator_session_is_unavailable()
    {
        using var session = new RecoveringSessionManager(CreateAccess());
        session.SignOut();
        using var viewModel = CreateViewModel(session, new BulkAccountCenterClient([]));

        await viewModel.BeginAddCommand.ExecuteAsync(null);

        Assert.False(viewModel.IsAdding);
        Assert.Contains("本机管理权限尚未就绪", viewModel.StatusMessage, StringComparison.Ordinal);
        Assert.Contains("本机管理会话尚未就绪", viewModel.AddValidationMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Save_add_explains_that_timeout_may_happen_after_account_was_written()
    {
        using var session = new StubSessionManager(CreateAccess());
        var client = new BulkAccountCenterClient(
            [],
            createException: new AccountCenterClientException(
                AccountCenterClientFailure.RequestTimedOut,
                "等待本机后台响应超过 60 秒。"));
        using var viewModel = CreateViewModel(session, client);

        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectedAddPlatform = Assert.Single(
            viewModel.AddPlatformOptions,
            option => option.Id == "anthropic");
        viewModel.AddApiKey = "sk-ant-test";

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.True(viewModel.IsAdding);
        Assert.Contains("账号可能已经写入", viewModel.AddValidationMessage, StringComparison.Ordinal);
        Assert.Contains("刷新账号列表确认", viewModel.AddValidationMessage, StringComparison.Ordinal);
        Assert.Contains("同名账号会自动跳过", viewModel.AddValidationMessage, StringComparison.Ordinal);
        Assert.Contains("60 秒", viewModel.AddValidationMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Openai_browser_authorization_opens_url_submits_callback_and_refreshes_list()
    {
        Sub2ApiSessionAccess access = CreateAccess();
        using var session = new StubSessionManager(access);
        var client = new BulkAccountCenterClient([]);
        string openedUrl = string.Empty;
        using var viewModel = new AccountCenterViewModel(
            session,
            client,
            _ => true,
            null,
            null,
            url => openedUrl = url);
        ApplyLocalConnection(viewModel);

        await viewModel.ActivateAsync();
        await viewModel.BeginAddCommand.ExecuteAsync(null);
        viewModel.SelectedOpenAiMethod = Assert.Single(viewModel.OpenAiMethodOptions, option => option.Id == "manual");
        await viewModel.GenerateOpenAiAuthorizationCommand.ExecuteAsync(null);
        viewModel.AddName = "my-codex";
        viewModel.AddOAuthText = "http://localhost:1455/auth/callback?code=code-123&state=state-456";

        await viewModel.SaveAddCommand.ExecuteAsync(null);

        Assert.Equal("https://auth.example.test/authorize", openedUrl);
        Assert.Equal(("session-42", "code-123", "state-456"), client.OpenAiCode);
        Assert.False(viewModel.IsAdding);
        Assert.Equal("my-codex", Assert.Single(viewModel.Accounts).Name);
    }

    [Fact]
    public void Transit_center_no_longer_exposes_service_operations()
    {
        Assert.Null(typeof(TransitCenterViewModel).GetProperty("ServiceOperations"));
        Assert.Equal(2, typeof(TransitCenterViewModel).GetConstructors().Single().GetParameters().Length);
    }

    [Fact]
    public void Account_center_view_contains_only_local_personal_account_controls()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "AccountCenterView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("Text=\"需处理账号\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("贡献额度", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("共享账号", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("共享号池", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("共享治理", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("累计获得", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("累计使用", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("使用范围", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"分组\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("GroupFilterOptions", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("GroupLabel", xaml, StringComparison.Ordinal);
        Assert.Contains("ToolTip=\"打开完整账号测试\"", xaml, StringComparison.Ordinal);
        Assert.Contains("ToolTip=\"更多账号管理功能\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Visibility=\"{Binding IsDetailsExpanded", xaml, StringComparison.Ordinal);
        Assert.Contains("ToolTip=\"拖动调整账号调度顺序\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"5 小时\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"周额度\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"{Binding FiveHourLabel}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"{Binding SevenDayLabel}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Style=\"{StaticResource AccountCompactUsageTrackStyle}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"后台测试输出\"", xaml, StringComparison.Ordinal);
        int addOverlayIndex = xaml.IndexOf("x:Name=\"AddAccountOverlay\"", StringComparison.Ordinal);
        int addScrollEndIndex = xaml.IndexOf("</ScrollViewer>", addOverlayIndex, StringComparison.Ordinal);
        int addValidationIndex = xaml.IndexOf("Text=\"{Binding AddValidationMessage}\"", addOverlayIndex, StringComparison.Ordinal);
        Assert.True(addValidationIndex > addScrollEndIndex, "添加账号错误提示应固定显示在滚动区域下方。");
    }

    [Fact]
    public void Gateway_view_does_not_expose_contribution_accounting()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "GatewayView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.DoesNotContain("贡献额度", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ContributionBalanceLabel", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ContributionAccountLabel", xaml, StringComparison.Ordinal);
    }

    private static AccountCenterViewModel CreateViewModel(
        ISub2ApiSessionManager session,
        ISub2ApiAccountCenterClient client,
        Func<IReadOnlyList<AccountCenterAccountViewModel>, bool>? confirmBatchDelete = null)
    {
        var viewModel = new AccountCenterViewModel(session, client, _ => true, confirmBatchDelete);
        ApplyLocalConnection(viewModel);
        return viewModel;
    }

    private static void ApplyLocalConnection(AccountCenterViewModel viewModel)
    {
        var profile = new ConnectionProfile
        {
            Id = ConnectionProfileIds.LocalMachine,
            Name = "本机中转",
            Kind = ConnectionProfileKind.Local,
            BaseUrl = "http://127.0.0.1:8080/v1",
        };
        viewModel.ApplyConnections(
            [profile],
            new ConnectionProfileSelection(null, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine),
            new ConnectionProfileRouting(ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine, ConnectionProfileIds.LocalMachine));
    }

    private static AccountCenterAccount CreateAccount(
        long id,
        string name,
        string platform = "openai",
        IReadOnlyList<string>? groups = null,
        string status = "active",
        int priority = 20)
        => new(
            id, name, platform, "oauth", 3, 3, priority, status, string.Empty,
            null, DateTimeOffset.UtcNow, string.Equals(status, "active", StringComparison.OrdinalIgnoreCase),
            null, null, null, null,
            groups is { Count: > 0 } ? Enumerable.Range(1, groups.Count).Select(value => (long)value).ToArray() : [],
            groups ?? []);

    private static Sub2ApiSessionAccess CreateAccess()
        => new(
            new Uri("http://127.0.0.1:8080/"),
            "session-token",
            42,
            "user",
            10m,
            0m,
            DateTimeOffset.UtcNow.AddHours(1));

    private static HttpResponseMessage JsonResponse(string body)
        => new(HttpStatusCode.OK)
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };

    private sealed class StubHandler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
            => Task.FromResult(respond(request));
    }

    private sealed class AsyncStubHandler(
        Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> respond) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
            => respond(request, cancellationToken);
    }

    private sealed class InlineProgress<T>(Action<T> report) : IProgress<T>
    {
        public void Report(T value) => report(value);
    }

    private sealed class StubSessionManager : ISub2ApiSessionManager
    {
        private readonly Sub2ApiSessionAccess _access;

        internal StubSessionManager(Sub2ApiSessionAccess access)
        {
            _access = access;
            Current = new Sub2ApiSessionState(
                true, false, false, "普通用户", 10m, 0m,
                access.ExpiresAtUtc, access.ApiBaseUri, "已登录");
        }

        public Sub2ApiSessionState Current { get; private set; }
        public event EventHandler? SessionChanged { add { } remove { } }
        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;
        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.FromResult(_access);
        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, CancellationToken cancellationToken) => Task.FromResult(_access);
        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, bool allowInsecurePublicHttp, CancellationToken cancellationToken) => Task.FromResult(_access);
        public Task LogoutAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        public void Dispose() { }
    }

    private sealed class RecoveringSessionManager : ISub2ApiSessionManager
    {
        private readonly Sub2ApiSessionAccess _access;

        internal RecoveringSessionManager(Sub2ApiSessionAccess access)
        {
            _access = access;
            Current = AuthenticatedState(access);
        }

        public int LocalControlLoginCalls { get; private set; }
        public Sub2ApiSessionState Current { get; private set; }
        public event EventHandler? SessionChanged;

        public void SignOut()
        {
            Current = Sub2ApiSessionState.SignedOut;
            SessionChanged?.Invoke(this, EventArgs.Empty);
        }

        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task<Sub2ApiSessionAccess> LoginLocalControlAsync(
            Uri apiBaseUri,
            string localControlToken,
            CancellationToken cancellationToken)
        {
            LocalControlLoginCalls++;
            Current = AuthenticatedState(_access);
            SessionChanged?.Invoke(this, EventArgs.Empty);
            return Task.FromResult(_access);
        }

        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken)
            => Current.IsAuthenticated
                ? Task.FromResult(_access)
                : Task.FromException<Sub2ApiSessionAccess>(
                    new Sub2ApiSessionException(Sub2ApiSessionFailure.AuthorizationUnavailable));

        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, CancellationToken cancellationToken)
            => Task.FromResult(_access);

        public Task<Sub2ApiSessionAccess> LoginAsync(Uri apiBaseUri, string email, string password, bool allowInsecurePublicHttp, CancellationToken cancellationToken)
            => Task.FromResult(_access);

        public Task LogoutAsync(CancellationToken cancellationToken) => Task.CompletedTask;
        public void Dispose() { }

        private static Sub2ApiSessionState AuthenticatedState(Sub2ApiSessionAccess access)
            => new(
                true, false, true, "管理员", 10m, 0m,
                access.ExpiresAtUtc, access.ApiBaseUri, "已登录");
    }

    private sealed class StubAccountCenterClient : ISub2ApiAccountCenterClient
    {
        public int ListCalls { get; private set; }
        public int UsageCalls { get; private set; }

        public Task<AccountCenterPage> ListAsync(Sub2ApiSessionAccess access, int page, int pageSize, CancellationToken cancellationToken)
        {
            ListCalls++;
            AccountCenterAccount[] accounts =
            [
                CreateAccount(1, "alpha@example.test"),
                CreateAccount(2, "beta@example.test"),
            ];
            return Task.FromResult(new AccountCenterPage(
                accounts, 2, 1, pageSize));
        }

        public Task<AccountCenterUsageSummary?> GetUsageAsync(Sub2ApiSessionAccess access, long accountId, bool force, CancellationToken cancellationToken)
        {
            UsageCalls++;
            AccountCenterUsageSummary summary = new(
                new AccountCenterUsageWindow(10, null, 1, 100, 0.01),
                new AccountCenterUsageWindow(20, null, 2, 200, 0.02),
                false, 3, 300, 0.03);
            return Task.FromResult<AccountCenterUsageSummary?>(summary);
        }

        public Task<AccountCenterEditOptions> GetEditOptionsAsync(Sub2ApiSessionAccess access, CancellationToken cancellationToken)
            => Task.FromResult(new AccountCenterEditOptions([], []));
        public Task<AccountCenterProxy> CreateProxyAsync(Sub2ApiSessionAccess access, AccountCenterProxyCreateRequest request, CancellationToken cancellationToken)
            => Task.FromResult(new AccountCenterProxy(1, request.Name, request.Protocol, request.Host, request.Port, "active"));
        public Task<AccountCenterCreateResult> CreateAsync(Sub2ApiSessionAccess access, AccountCenterCreateRequest request, CancellationToken cancellationToken)
            => Task.FromResult(CreateSuccess());
        public Task<AccountCenterOpenAiAuthSession> GenerateOpenAiAuthAsync(Sub2ApiSessionAccess access, long proxyId, CancellationToken cancellationToken)
            => Task.FromResult(new AccountCenterOpenAiAuthSession("https://auth.example.test/", "session-id"));
        public Task<AccountCenterCreateResult> CreateOpenAiFromCodeAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string sessionId, string code, string state, CancellationToken cancellationToken)
            => Task.FromResult(CreateSuccess());
        public Task<AccountCenterCreateResult> CreateOpenAiFromRefreshTokenAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string refreshToken, bool mobile, CancellationToken cancellationToken)
            => Task.FromResult(CreateSuccess());
        public Task<AccountCenterCreateResult> CreateOpenAiFromCodexPatAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string accessToken, CancellationToken cancellationToken)
            => Task.FromResult(CreateSuccess());
        public Task UpdateAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterUpdateRequest update, CancellationToken cancellationToken) => Task.CompletedTask;
        public Task<AccountCenterTestResult> TestAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken) => Task.FromResult(new AccountCenterTestResult("success", null, 10));
        public Task<IReadOnlyList<AccountCenterTestModel>> GetAvailableModelsAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
            => Task.FromResult<IReadOnlyList<AccountCenterTestModel>>([new("gpt-5.4", "GPT 5.4")]);
        public Task<AccountCenterDetailedTestResult> RunDetailedTestAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterDetailedTestRequest request, IProgress<AccountCenterTestEvent>? progress, CancellationToken cancellationToken)
        {
            progress?.Report(new AccountCenterTestEvent("content", "pong", string.Empty, string.Empty, false, string.Empty, string.Empty));
            progress?.Report(new AccountCenterTestEvent("test_complete", string.Empty, string.Empty, string.Empty, true, string.Empty, string.Empty));
            return Task.FromResult(new AccountCenterDetailedTestResult(true, null));
        }
        public Task RunAdminActionAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterAdminAction action, CancellationToken cancellationToken) => Task.CompletedTask;
        public Task DeleteAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken) => Task.CompletedTask;

        private static AccountCenterAccount CreateAccount(long id, string name)
            => new(
                id, name, "openai", "oauth", 3, 3, 20, "active", string.Empty,
                null, DateTimeOffset.UtcNow, true, null, null, null, null, [], []);

        private static AccountCenterCreateResult CreateSuccess()
            => new(1, 1, 0, [new AccountCenterCreateResultItem(0, "created", 99, "created", string.Empty)]);
    }

    private sealed class BulkAccountCenterClient(
        IEnumerable<AccountCenterAccount> accounts,
        AccountCenterEditOptions? editOptions = null,
        AccountCenterCreateResult? createResult = null,
        AccountCenterClientException? createException = null) : ISub2ApiAccountCenterClient
    {
        private readonly ConcurrentDictionary<long, AccountCenterAccount> _accounts =
            new(accounts.ToDictionary(account => account.Id));
        private AccountCenterEditOptions _editOptions = editOptions ?? new([], []);
        private readonly AccountCenterCreateResult? _createResult = createResult;

        public int ListCalls { get; private set; }
        public ConcurrentBag<long> TestedIds { get; } = [];
        public ConcurrentBag<long> DeletedIds { get; } = [];
        public ConcurrentBag<AccountCenterDetailedTestRequest> DetailedTestRequests { get; } = [];
        public ConcurrentBag<(long AccountId, AccountCenterAdminAction Action)> AdminActions { get; } = [];
        public ConcurrentDictionary<long, AccountCenterUpdateRequest> Updates { get; } = new();
        public AccountCenterCreateRequest? CreatedRequest { get; private set; }
        public List<AccountCenterCreateRequest> CreatedRequests { get; } = [];
        public AccountCenterProxyCreateRequest? CreatedProxyRequest { get; private set; }
        public long CreatedProxyId { get; private set; }
        public AccountCenterOpenAiCreateRequest? OpenAiRequest { get; private set; }
        public (string SessionId, string Code, string State)? OpenAiCode { get; private set; }

        public Task<AccountCenterPage> ListAsync(Sub2ApiSessionAccess access, int page, int pageSize, CancellationToken cancellationToken)
        {
            ListCalls++;
            AccountCenterAccount[] snapshot = _accounts.Values.OrderBy(account => account.Id).ToArray();
            AccountCenterAccount[] items = snapshot.Skip((page - 1) * pageSize).Take(pageSize).ToArray();
            return Task.FromResult(new AccountCenterPage(
                items, snapshot.Length, page, pageSize));
        }

        public Task<AccountCenterUsageSummary?> GetUsageAsync(Sub2ApiSessionAccess access, long accountId, bool force, CancellationToken cancellationToken)
            => Task.FromResult<AccountCenterUsageSummary?>(null);

        public Task<AccountCenterEditOptions> GetEditOptionsAsync(Sub2ApiSessionAccess access, CancellationToken cancellationToken)
            => Task.FromResult(_editOptions);

        public Task<AccountCenterProxy> CreateProxyAsync(Sub2ApiSessionAccess access, AccountCenterProxyCreateRequest request, CancellationToken cancellationToken)
        {
            CreatedProxyRequest = request;
            CreatedProxyId = _editOptions.Proxies.Select(proxy => proxy.Id).DefaultIfEmpty(0).Max() + 1;
            var created = new AccountCenterProxy(CreatedProxyId, request.Name.Trim(), request.Protocol, request.Host.Trim(), request.Port, "active");
            _editOptions = new AccountCenterEditOptions(_editOptions.PrivateGroups, [.. _editOptions.Proxies, created]);
            return Task.FromResult(created);
        }

        public Task<AccountCenterCreateResult> CreateAsync(Sub2ApiSessionAccess access, AccountCenterCreateRequest request, CancellationToken cancellationToken)
        {
            CreatedRequest = request;
            CreatedRequests.Add(request);
            if (createException is not null) throw createException;
            if (_createResult is not null) return Task.FromResult(_createResult);
            long id = _accounts.Keys.DefaultIfEmpty(0).Max() + 1;
            _accounts[id] = CreateAccount(id, string.IsNullOrWhiteSpace(request.Name) ? "new-account" : request.Name, request.Platform);
            return Task.FromResult(CreateSuccess(id));
        }

        public Task<AccountCenterOpenAiAuthSession> GenerateOpenAiAuthAsync(Sub2ApiSessionAccess access, long proxyId, CancellationToken cancellationToken)
            => Task.FromResult(new AccountCenterOpenAiAuthSession("https://auth.example.test/authorize", "session-42"));

        public Task<AccountCenterCreateResult> CreateOpenAiFromCodeAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string sessionId, string code, string state, CancellationToken cancellationToken)
        {
            OpenAiRequest = request;
            OpenAiCode = (sessionId, code, state);
            long id = _accounts.Keys.DefaultIfEmpty(0).Max() + 1;
            _accounts[id] = CreateAccount(id, string.IsNullOrWhiteSpace(request.Name) ? "openai-oauth" : request.Name, "openai");
            return Task.FromResult(CreateSuccess(id));
        }

        public Task<AccountCenterCreateResult> CreateOpenAiFromRefreshTokenAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string refreshToken, bool mobile, CancellationToken cancellationToken)
        {
            OpenAiRequest = request;
            return Task.FromResult(CreateSuccess(99));
        }

        public Task<AccountCenterCreateResult> CreateOpenAiFromCodexPatAsync(Sub2ApiSessionAccess access, AccountCenterOpenAiCreateRequest request, string accessToken, CancellationToken cancellationToken)
        {
            OpenAiRequest = request;
            return Task.FromResult(CreateSuccess(99));
        }

        public Task UpdateAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterUpdateRequest update, CancellationToken cancellationToken)
        {
            Updates[accountId] = update;
            if (_accounts.TryGetValue(accountId, out AccountCenterAccount? account))
            {
                _accounts[accountId] = account with
                {
                    Name = update.Name,
                    Concurrency = update.Concurrency,
                    LoadFactor = update.LoadFactor,
                    Priority = update.Priority ?? account.Priority,
                    Status = update.Status ?? account.Status,
                };
            }
            return Task.CompletedTask;
        }

        public Task<AccountCenterTestResult> TestAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
        {
            TestedIds.Add(accountId);
            return Task.FromResult(new AccountCenterTestResult("success", null, 10));
        }

        public Task<IReadOnlyList<AccountCenterTestModel>> GetAvailableModelsAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
            => Task.FromResult<IReadOnlyList<AccountCenterTestModel>>([new("gpt-5.4", "GPT 5.4")]);

        public Task<AccountCenterDetailedTestResult> RunDetailedTestAsync(
            Sub2ApiSessionAccess access,
            long accountId,
            AccountCenterDetailedTestRequest request,
            IProgress<AccountCenterTestEvent>? progress,
            CancellationToken cancellationToken)
        {
            DetailedTestRequests.Add(request);
            progress?.Report(new AccountCenterTestEvent("test_start", string.Empty, request.ModelId, string.Empty, false, string.Empty, string.Empty));
            progress?.Report(new AccountCenterTestEvent("content", "pong", string.Empty, string.Empty, false, string.Empty, string.Empty));
            progress?.Report(new AccountCenterTestEvent("test_complete", string.Empty, string.Empty, string.Empty, true, string.Empty, string.Empty));
            return Task.FromResult(new AccountCenterDetailedTestResult(true, null));
        }

        public Task RunAdminActionAsync(Sub2ApiSessionAccess access, long accountId, AccountCenterAdminAction action, CancellationToken cancellationToken)
        {
            AdminActions.Add((accountId, action));
            return Task.CompletedTask;
        }

        public Task DeleteAsync(Sub2ApiSessionAccess access, long accountId, CancellationToken cancellationToken)
        {
            _accounts.TryRemove(accountId, out _);
            DeletedIds.Add(accountId);
            return Task.CompletedTask;
        }

        private static AccountCenterCreateResult CreateSuccess(long id)
            => new(1, 1, 0, [new AccountCenterCreateResultItem(0, "created", id, "created", string.Empty)]);
    }
}
