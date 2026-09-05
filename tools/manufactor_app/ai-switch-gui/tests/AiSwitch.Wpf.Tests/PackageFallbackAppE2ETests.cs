using System.IO;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using AiSwitchGui;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using Xunit;

namespace AiSwitch.Wpf.Tests;

public sealed class PackageFallbackAppE2ETests
{
    [Fact]
    public async Task App_applies_fallback_routing_and_uses_it_after_personal_quota_exhaustion()
    {
        string? backendBaseUrl = Environment.GetEnvironmentVariable("LANAI_E2E_BACKEND_BASE_URL");
        string? primaryBaseUrl = Environment.GetEnvironmentVariable("LANAI_E2E_PRIMARY_BASE_URL");
        string? fallbackBaseUrl = Environment.GetEnvironmentVariable("LANAI_E2E_FALLBACK_BASE_URL");
        string? localControlToken = Environment.GetEnvironmentVariable("LANAI_E2E_LOCAL_CONTROL_TOKEN");
        if (string.IsNullOrWhiteSpace(backendBaseUrl) ||
            string.IsNullOrWhiteSpace(primaryBaseUrl) ||
            string.IsNullOrWhiteSpace(fallbackBaseUrl) ||
            string.IsNullOrWhiteSpace(localControlToken))
        {
            return;
        }

        Uri backendUri = new(backendBaseUrl.TrimEnd('/') + "/");
        string testRoot = Path.Combine(Path.GetTempPath(), "LanAi.PackageFallbackAppE2E", Guid.NewGuid().ToString("N"));
        var paths = new AppDataPaths(
            userProfile: Path.Combine(testRoot, "profile"),
            localAppData: Path.Combine(testRoot, "local-app-data"));
        Directory.CreateDirectory(Path.GetDirectoryName(paths.LegacyProfilesPath)!);

        ProfileDefinition local = ProfileDefinition.CreateLocalDefaults();
        local.Codex.BaseUrl = new Uri(backendUri, "v1").AbsoluteUri.TrimEnd('/');
        local.Codex.Secret = "bootstrap-placeholder";
        var fallback = new ProfileDefinition
        {
            Id = "package-app-e2e-fallback",
            Name = "Package app E2E fallback",
            Codex = new ClientProfile
            {
                BaseUrl = fallbackBaseUrl.TrimEnd('/') + "/v1",
                Secret = "sk-package-app-e2e-fallback",
            },
        };
        var store = new ProfileStore
        {
            Local = local,
            LocalSources = [local],
            Cloud = fallback,
            CloudSources = [fallback],
            SelectedCloudSourceId = fallback.Id,
            SelectedLocalSourceId = local.Id,
            BackupSourceIds = [fallback.Id],
            BackupUpstreamEnabled = true,
        };
        var configPaths = new ConfigPaths(
            Path.GetDirectoryName(paths.LegacyProfilesPath)!,
            paths.UserProfile,
            paths.LocalAppData);
        var profiles = new ProfileRepository(configPaths);
        profiles.SaveProfiles(store);

        try
        {
            using var session = new Sub2ApiSessionManager();
            Sub2ApiSessionAccess access = await session.LoginLocalControlAsync(
                backendUri,
                localControlToken,
                CancellationToken.None);
            using var adminClient = CreateAuthorizedClient(backendUri, access.AccessToken);
            using HttpResponseMessage personalResponse = await PostJsonAsync(
                adminClient,
                "api/v1/account-contributions",
                new
                {
                    mode = "api_key",
                    name = "package-app-e2e-personal",
                    platform = "openai",
                    api_key = "sk-package-app-e2e-primary",
                    base_url = primaryBaseUrl.TrimEnd('/') + "/v1",
                    concurrency = 30,
                    load_factor = 1,
                    priority = 0,
                    group_ids = Array.Empty<long>(),
                });
            Assert.True(personalResponse.IsSuccessStatusCode, await personalResponse.Content.ReadAsStringAsync());

            ISub2ApiSessionManager sessionManager = session;
            Func<string?> localControlTokenProvider = () => localControlToken;
            using var coordinator = new LegacySwitchCoordinator(
                paths,
                sessionManager,
                localControlTokenProvider);
            OperationResult apply = await coordinator.ApplyRoutingAsync();
            Assert.True(apply.Success, apply.Summary);

            string clientKey = await ReadManagedCodexKeyAsync(adminClient);
            using var gatewayClient = CreateAuthorizedClient(backendUri, clientKey);
            for (int request = 1; request <= 3; request++)
            {
                using HttpResponseMessage response = await PostJsonAsync(
                    gatewayClient,
                    "v1/responses",
                    new
                    {
                        model = "gpt-5.6-sol",
                        input = $"package app fallback request {request}",
                        stream = false,
                    });
                string body = await response.Content.ReadAsStringAsync();
                Assert.True(response.IsSuccessStatusCode, body);
                Assert.Contains("PACKAGE_FALLBACK_OK", body, StringComparison.Ordinal);
            }
        }
        finally
        {
            if (Directory.Exists(testRoot))
            {
                Directory.Delete(testRoot, recursive: true);
            }
        }
    }

    private static HttpClient CreateAuthorizedClient(Uri baseUri, string token)
    {
        var client = new HttpClient { BaseAddress = baseUri, Timeout = TimeSpan.FromSeconds(60) };
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", token);
        return client;
    }

    private static Task<HttpResponseMessage> PostJsonAsync(HttpClient client, string path, object body)
    {
        string json = JsonSerializer.Serialize(body);
        return client.PostAsync(path, new StringContent(json, Encoding.UTF8, "application/json"));
    }

    private static async Task<string> ReadManagedCodexKeyAsync(HttpClient client)
    {
        using HttpResponseMessage response = await client.GetAsync("api/v1/keys?page=1&page_size=1000");
        string body = await response.Content.ReadAsStringAsync();
        Assert.True(response.IsSuccessStatusCode, body);
        using JsonDocument document = JsonDocument.Parse(body);
        foreach (JsonElement item in document.RootElement.GetProperty("data").GetProperty("items").EnumerateArray())
        {
            if (string.Equals(
                    item.GetProperty("name").GetString(),
                    "共飞工作台-Codex-客户端",
                    StringComparison.OrdinalIgnoreCase))
            {
                string? key = item.GetProperty("key").GetString();
                Assert.False(string.IsNullOrWhiteSpace(key));
                return key!;
            }
        }
        throw new Xunit.Sdk.XunitException("The app did not create its managed Codex client key.");
    }
}
