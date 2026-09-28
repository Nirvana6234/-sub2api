using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json.Nodes;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>An unsigned JWT with the given claims — enough for code that only reads them.</summary>
internal static class TestJwt
{
    public static string Make(JsonObject claims)
    {
        static string Part(string json) =>
            Convert.ToBase64String(Encoding.UTF8.GetBytes(json)).TrimEnd('=').Replace('+', '-').Replace('/', '_');
        return $"{Part("""{"alg":"none"}""")}.{Part(claims.ToJsonString())}.sig";
    }

    public static string Expiring(DateTimeOffset at) => Make(new JsonObject { ["exp"] = at.ToUnixTimeSeconds() });
}

internal sealed class FakeTokenRefresher : IOfficialTokenRefresher
{
    public List<string> CodexCalls { get; } = [];

    public List<string> ClaudeCalls { get; } = [];

    public Func<string, CodexRefreshedTokens> OnCodex { get; set; } =
        refresh => new CodexRefreshedTokens("new-access", refresh + "-next", null);

    public Func<string, ClaudeRefreshedTokens> OnClaude { get; set; } =
        refresh => new ClaudeRefreshedTokens("new-claude-access", refresh + "-next", 28800, null);

    /// <summary>Runs during a Claude refresh, after the request and before the answer — as Claude Code might.</summary>
    public Action? DuringClaudeRefresh { get; set; }

    public Task<CodexRefreshedTokens> RefreshCodexAsync(string refreshToken, CancellationToken cancellationToken)
    {
        CodexCalls.Add(refreshToken);
        return Task.FromResult(OnCodex(refreshToken));
    }

    public Task<ClaudeRefreshedTokens> RefreshClaudeAsync(string refreshToken, CancellationToken cancellationToken)
    {
        ClaudeCalls.Add(refreshToken);
        DuringClaudeRefresh?.Invoke();
        return Task.FromResult(OnClaude(refreshToken));
    }
}

/// <summary>Codex's own ChatGPT sign-in: used as it is, refreshed only when due, and never lost.</summary>
public sealed class LocalCodexAccountTests
{
    private static readonly DateTimeOffset Now = new(2026, 9, 28, 12, 0, 0, TimeSpan.Zero);

    private sealed class MemoryLoginStore : ICodexLoginStore
    {
        public CodexLogin? Login { get; set; }

        public int Updates { get; private set; }

        public Exception? FailUpdate { get; set; }

        public Func<bool>? OnUpdate { get; set; }

        public CodexLogin? ReadLogin() => Login;

        public bool UpdateLoginTokens(string previousRefreshToken, CodexRefreshedTokens refreshed, DateTimeOffset refreshedAt)
        {
            if (FailUpdate is { } failure)
            {
                throw failure;
            }
            if (OnUpdate is { } custom)
            {
                return custom();
            }
            if (Login is null || Login.RefreshToken != previousRefreshToken)
            {
                return false;
            }

            Updates++;
            Login = Login with
            {
                AccessToken = refreshed.AccessToken,
                RefreshToken = refreshed.RefreshToken ?? Login.RefreshToken,
                IdToken = refreshed.IdToken ?? Login.IdToken,
            };
            return true;
        }
    }

    private static readonly string IdToken = TestJwt.Make(new JsonObject
    {
        ["email"] = "me@example.com",
        ["https://api.openai.com/auth"] = new JsonObject
        {
            ["chatgpt_plan_type"] = "plus",
            ["chatgpt_account_id"] = "acct-from-claims",
            ["chatgpt_account_is_fedramp"] = true,
        },
    });

    private static CodexLogin Login(DateTimeOffset accessExpires, string accountId = "acct-1") =>
        new(TestJwt.Expiring(accessExpires), "refresh-1", IdToken, accountId);

    private static (LocalCodexAccount Account, MemoryLoginStore Store, FakeTokenRefresher Refresher, Func<DateTimeOffset> Clock) Rig(
        CodexLogin? login, DateTimeOffset? now = null)
    {
        var store = new MemoryLoginStore { Login = login };
        var refresher = new FakeTokenRefresher();
        var clock = new TestClockOf(now ?? Now);
        return (new LocalCodexAccount(store, refresher, clock.Read), store, refresher, clock.Read);
    }

    private sealed class TestClockOf(DateTimeOffset now)
    {
        public DateTimeOffset Now { get; set; } = now;

        public DateTimeOffset Read() => Now;
    }

    [Fact]
    public async Task WithoutASignInItSaysHowToSignIn()
    {
        var (account, _, _, _) = Rig(null);

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(
            () => account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None));
        Assert.Contains("codex login", error.UserMessage);
        Assert.Equal(LocalMachineAccountState.NotSignedIn, account.Probe().State);
    }

    [Fact]
    public async Task AFreshTokenIsUsedAsItIsWithTheAccountAndFedRampFromTheSignIn()
    {
        var (account, _, refresher, _) = Rig(Login(Now.AddHours(5)));

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);

        Assert.Empty(refresher.CodexCalls);
        Assert.Equal(LocalMachineAccounts.CodexId, credential.AccountId);
        Assert.Equal("openai", credential.Platform);
        Assert.Equal("acct-1", credential.ChatGptAccountId);
        Assert.True(credential.FedRamp);
        Assert.Equal("本机 ChatGPT 登录（me@example.com · Plus）", credential.Name);
    }

    [Fact]
    public async Task TheAccountIdFallsBackToTheIdTokenClaim()
    {
        var (account, _, _, _) = Rig(Login(Now.AddHours(5), accountId: ""));

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);

        Assert.Equal("acct-from-claims", credential.ChatGptAccountId);
    }

    [Fact]
    public async Task AnExpiringTokenIsRefreshedAndWrittenBackBeforeUse()
    {
        var (account, store, refresher, _) = Rig(Login(Now.AddMinutes(2)));

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);

        Assert.Equal(["refresh-1"], refresher.CodexCalls);
        Assert.Equal(1, store.Updates);
        Assert.Equal("refresh-1-next", store.Login!.RefreshToken);
        Assert.Equal("new-access", credential.AccessToken);
    }

    [Fact]
    public async Task ForcedRefreshesInABurstSpendOneRefreshToken()
    {
        var (account, _, refresher, _) = Rig(Login(Now.AddHours(5)));

        await account.GetAsync(LocalMachineAccounts.CodexId, forceRefresh: true, CancellationToken.None);
        await account.GetAsync(LocalMachineAccounts.CodexId, forceRefresh: true, CancellationToken.None);

        Assert.Single(refresher.CodexCalls);
    }

    [Fact]
    public async Task APairThatCouldNotBeSavedIsUsedAndSavedLater()
    {
        var (account, store, refresher, _) = Rig(Login(Now.AddMinutes(1)));
        store.FailUpdate = new IOException("disk full");

        LocalProxyCredential first = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);
        Assert.Equal("new-access", first.AccessToken);
        Assert.Equal("refresh-1", store.Login!.RefreshToken);

        store.FailUpdate = null;
        LocalProxyCredential second = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);

        Assert.Single(refresher.CodexCalls);
        Assert.Equal("refresh-1-next", store.Login!.RefreshToken);
        Assert.Equal("new-access", second.AccessToken);
    }

    [Fact]
    public async Task ASignInReplacedDuringTheRefreshWins()
    {
        var (account, store, _, _) = Rig(Login(Now.AddMinutes(1)));
        CodexLogin replacement = new(TestJwt.Expiring(Now.AddHours(8)), "refresh-other", IdToken, "acct-other");
        store.OnUpdate = () =>
        {
            store.Login = replacement;
            return false;
        };

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None);

        Assert.Equal(replacement.AccessToken, credential.AccessToken);
        Assert.Equal("acct-other", credential.ChatGptAccountId);
    }

    [Fact]
    public async Task ARefusedRefreshIsReportedInWords()
    {
        var (account, _, refresher, _) = Rig(Login(Now.AddMinutes(1)));
        refresher.OnCodex = _ => throw new LocalProxyCredentialException("本机 ChatGPT 登录已失效");

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(
            () => account.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None));
        Assert.Contains("已失效", error.UserMessage);
    }

    [Fact]
    public void TheProbeNamesTheSignIn()
    {
        var (account, _, _, _) = Rig(Login(Now.AddHours(5)));

        LocalMachineAccountStatus status = account.Probe();

        Assert.True(status.IsUsable);
        Assert.Equal("本机 ChatGPT 登录（me@example.com · Plus）", status.DisplayName);
    }
}

/// <summary>Claude Code's own sign-in: read where Claude Code keeps it, changed only in its token fields.</summary>
public sealed class LocalClaudeAccountTests
{
    private static readonly DateTimeOffset Now = new(2026, 9, 28, 12, 0, 0, TimeSpan.Zero);

    private sealed class MemoryClaudeStore : IClaudeCredentialStore
    {
        public string? Text { get; set; }

        public string? UnsupportedReason { get; set; }

        public bool ReadNeedsConsent { get; set; }

        public bool ReadMayBlock { get; set; }

        public int Reads { get; private set; }

        public int Writes { get; private set; }

        public Exception? FailWrite { get; set; }

        public bool Exists() => Text is not null;

        public string? Read()
        {
            Reads++;
            ReadNeedsConsent = false;
            return Text;
        }

        public string? ReadCached() => Text;

        public void Write(string json)
        {
            if (FailWrite is { } failure)
            {
                throw failure;
            }
            Writes++;
            Text = json;
        }

        public string ReadEmail() => "me@example.com";

        public JsonObject Oauth => (JsonObject)JsonNode.Parse(Text!)!["claudeAiOauth"]!;
    }

    private static string File(DateTimeOffset expires, string refresh = "claude-refresh-1") =>
        new JsonObject
        {
            ["claudeAiOauth"] = new JsonObject
            {
                ["accessToken"] = "claude-access-1",
                ["refreshToken"] = refresh,
                ["expiresAt"] = expires.ToUnixTimeMilliseconds(),
                ["scopes"] = new JsonArray("user:inference"),
                ["subscriptionType"] = "max",
            },
            ["mcpOAuth"] = new JsonObject { ["server"] = "kept" },
        }.ToJsonString();

    private static (LocalClaudeAccount Account, MemoryClaudeStore Store, FakeTokenRefresher Refresher) Rig(string? text)
    {
        var store = new MemoryClaudeStore { Text = text };
        var refresher = new FakeTokenRefresher();
        return (new LocalClaudeAccount(store, refresher, () => Now), store, refresher);
    }

    /// <summary>
    /// The macOS keychain (D10): opening the page only looks; the sign-in is read — and the
    /// system prompt comes — when the user switches it on, and off the UI thread.
    /// </summary>
    [Fact]
    public async Task ASignInBehindAPromptIsOnlyLookedAtUntilItIsSwitchedOn()
    {
        var (account, store, _) = Rig(File(Now.AddHours(8)));
        store.ReadNeedsConsent = true;
        store.ReadMayBlock = true;

        LocalMachineAccountStatus before = account.Probe();
        Assert.True(before.IsUsable);
        Assert.Equal(LocalClaudeAccount.ConsentDetail, before.Detail);
        Assert.Equal(0, store.Reads);

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.ClaudeId, forceRefresh: false, CancellationToken.None);
        Assert.Equal("claude-access-1", credential.AccessToken);
        Assert.Equal(1, store.Reads);

        LocalMachineAccountStatus after = account.Probe();
        Assert.Equal(string.Empty, after.Detail);
        Assert.Equal(1, store.Reads);
    }

    [Fact]
    public void NothingThereBehindAPromptIsNotSignedIn()
    {
        var (account, store, _) = Rig(null);
        store.ReadNeedsConsent = true;

        Assert.Equal(LocalMachineAccountState.NotSignedIn, account.Probe().State);
        Assert.Equal(0, store.Reads);
    }

    [Fact]
    public async Task AnUnsupportedPlatformSaysWhyAndGivesNothing()
    {
        var (account, store, _) = Rig(File(Now.AddHours(5)));
        store.UnsupportedReason = "macOS 钥匙串";

        Assert.Equal(LocalMachineAccountState.Unsupported, account.Probe().State);
        await Assert.ThrowsAsync<LocalProxyCredentialException>(
            () => account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None));
    }

    [Fact]
    public async Task WithoutASignInItSaysHowToSignIn()
    {
        var (account, _, _) = Rig(null);

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(
            () => account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None));
        Assert.Contains("/login", error.UserMessage);
        Assert.Equal(LocalMachineAccountState.NotSignedIn, account.Probe().State);
    }

    [Fact]
    public async Task AFreshTokenIsUsedAsItIs()
    {
        var (account, store, refresher) = Rig(File(Now.AddHours(5)));

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None);

        Assert.Empty(refresher.ClaudeCalls);
        Assert.Equal(0, store.Writes);
        Assert.Equal("claude-access-1", credential.AccessToken);
        Assert.Equal("anthropic", credential.Platform);
        Assert.Equal(LocalMachineAccounts.ClaudeId, credential.AccountId);
        Assert.Equal("本机 Claude 登录（me@example.com · Max）", credential.Name);
    }

    [Fact]
    public async Task AnExpiringTokenIsRefreshedAndOnlyItsFieldsAreWritten()
    {
        var (account, store, refresher) = Rig(File(Now.AddMinutes(1)));
        refresher.OnClaude = refresh => new ClaudeRefreshedTokens("claude-access-2", "claude-refresh-2", 3600, "user:inference user:profile");

        LocalProxyCredential credential = await account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None);

        Assert.Equal("claude-access-2", credential.AccessToken);
        JsonObject oauth = store.Oauth;
        Assert.Equal("claude-access-2", oauth["accessToken"]!.GetValue<string>());
        Assert.Equal("claude-refresh-2", oauth["refreshToken"]!.GetValue<string>());
        Assert.Equal(Now.AddSeconds(3600).ToUnixTimeMilliseconds(), oauth["expiresAt"]!.GetValue<long>());
        Assert.Equal(["user:inference", "user:profile"], oauth["scopes"]!.AsArray().Select(n => n!.GetValue<string>()));
        Assert.Equal("max", oauth["subscriptionType"]!.GetValue<string>());
        Assert.Equal("kept", JsonNode.Parse(store.Text!)!["mcpOAuth"]!["server"]!.GetValue<string>());
    }

    [Fact]
    public async Task APairClaudeCodeWroteDuringTheRefreshIsNeverOverwritten()
    {
        var (account, store, refresher) = Rig(File(Now.AddMinutes(1)));
        string theirs = File(Now.AddHours(8), refresh: "claude-code-refreshed");
        refresher.DuringClaudeRefresh = () => store.Text = theirs;

        await account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None);

        Assert.Equal(theirs, store.Text);
        Assert.Equal(0, store.Writes);
    }

    [Fact]
    public async Task APairThatCouldNotBeWrittenIsUsedAndWrittenLater()
    {
        var (account, store, refresher) = Rig(File(Now.AddMinutes(1)));
        store.FailWrite = new IOException("locked");

        LocalProxyCredential first = await account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None);
        Assert.Equal("new-claude-access", first.AccessToken);

        store.FailWrite = null;
        LocalProxyCredential second = await account.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None);

        Assert.Single(refresher.ClaudeCalls);
        Assert.Equal("new-claude-access", second.AccessToken);
        Assert.Equal("claude-refresh-1-next", store.Oauth["refreshToken"]!.GetValue<string>());
    }
}

/// <summary>The official token endpoints, as Codex and Claude Code call them.</summary>
public sealed class OfficialTokenRefresherTests
{
    private sealed class Handler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        public List<(HttpRequestMessage Request, string Body)> Seen { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string body = request.Content is null ? string.Empty : await request.Content.ReadAsStringAsync(cancellationToken);
            Seen.Add((request, body));
            return respond(request);
        }
    }

    private static HttpResponseMessage Json(HttpStatusCode status, string body) =>
        new(status) { Content = new StringContent(body, Encoding.UTF8, "application/json") };

    [Fact]
    public async Task CodexIsRefreshedWithItsOwnClientIdAsAForm()
    {
        var handler = new Handler(_ => Json(HttpStatusCode.OK, """{"access_token":"a2","refresh_token":"r2","id_token":"i2"}"""));
        var refresher = new OfficialTokenRefresher(handler: _ => handler);

        CodexRefreshedTokens tokens = await refresher.RefreshCodexAsync("r1", CancellationToken.None);

        Assert.Equal(new CodexRefreshedTokens("a2", "r2", "i2"), tokens);
        (HttpRequestMessage request, string body) = handler.Seen.Single();
        Assert.Equal("https://auth.openai.com/oauth/token", request.RequestUri!.ToString());
        Assert.Contains("grant_type=refresh_token", body);
        Assert.Contains("refresh_token=r1", body);
        Assert.Contains("client_id=app_EMoamEEZ73f0CkXaXp7hrann", body);
    }

    [Fact]
    public async Task ClaudeIsRefreshedWithItsOwnClientIdAsJson()
    {
        var handler = new Handler(_ => Json(HttpStatusCode.OK, """{"access_token":"a2","refresh_token":"r2","expires_in":28800,"scope":"user:inference"}"""));
        var refresher = new OfficialTokenRefresher(handler: _ => handler);

        ClaudeRefreshedTokens tokens = await refresher.RefreshClaudeAsync("r1", CancellationToken.None);

        Assert.Equal(new ClaudeRefreshedTokens("a2", "r2", 28800, "user:inference"), tokens);
        (HttpRequestMessage request, string body) = handler.Seen.Single();
        Assert.Equal("https://platform.claude.com/v1/oauth/token", request.RequestUri!.ToString());
        JsonObject sent = (JsonObject)JsonNode.Parse(body)!;
        Assert.Equal("9d1c250a-e61b-44d9-88ed-5944d1962f5e", sent["client_id"]!.GetValue<string>());
        Assert.Equal("r1", sent["refresh_token"]!.GetValue<string>());
    }

    [Fact]
    public async Task ASpentRefreshTokenAsksTheUserToSignInAgain()
    {
        var handler = new Handler(_ => Json(HttpStatusCode.BadRequest, """{"error":"invalid_grant","error_description":"refresh_token_reused"}"""));
        var refresher = new OfficialTokenRefresher(handler: _ => handler);

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => refresher.RefreshCodexAsync("r1", CancellationToken.None));
        Assert.Contains("重新登录", error.UserMessage);
        Assert.Contains("codex login", error.UserMessage);
    }

    [Fact]
    public async Task AServerErrorIsRetriedLaterRatherThanBlamingTheSignIn()
    {
        var handler = new Handler(_ => Json(HttpStatusCode.BadGateway, "{}"));
        var refresher = new OfficialTokenRefresher(handler: _ => handler);

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => refresher.RefreshClaudeAsync("r1", CancellationToken.None));
        Assert.Contains("稍后会自动重试", error.UserMessage);
    }

    [Fact]
    public async Task AnUnreachableHostPointsAtTheProxy()
    {
        var handler = new Handler(_ => throw new HttpRequestException("connection refused"));
        var refresher = new OfficialTokenRefresher(handler: _ => handler);

        var error = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => refresher.RefreshCodexAsync("r1", CancellationToken.None));
        Assert.Contains("代理/VPN", error.UserMessage);
    }
}

/// <summary>One credential source: relay accounts by id, this machine's sign-ins by their reserved ids.</summary>
public sealed class LocalProxyCredentialRouterTests
{
    private sealed class Source(string label, LocalProxyKind kind = LocalProxyKind.Codex) : ILocalMachineAccount
    {
        public List<long> Asked { get; } = [];

        public int Cleared { get; private set; }

        public LocalProxyKind Kind => kind;

        public LocalMachineAccountStatus Probe() => new(LocalMachineAccountState.SignedIn, label, string.Empty);

        public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken)
        {
            Asked.Add(accountId);
            return Task.FromResult(new LocalProxyCredential(accountId: accountId, accessToken: label));
        }

        public void Clear() => Cleared++;
    }

    [Fact]
    public async Task EachIdGoesToItsOwnSource()
    {
        var relay = new Source("relay");
        var codex = new Source("codex");
        var claude = new Source("claude", LocalProxyKind.ClaudeCode);
        var router = new LocalProxyCredentialRouter(relay, codex, claude);

        Assert.Equal("relay", (await router.GetAsync(7, false, CancellationToken.None)).AccessToken);
        Assert.Equal("codex", (await router.GetAsync(LocalMachineAccounts.CodexId, false, CancellationToken.None)).AccessToken);
        Assert.Equal("claude", (await router.GetAsync(LocalMachineAccounts.ClaudeId, false, CancellationToken.None)).AccessToken);
        Assert.Same(claude, router.LocalFor(LocalProxyKind.ClaudeCode));
    }

    [Fact]
    public void SigningOutOfTheRelayLeavesThisMachinesSignInsAlone()
    {
        var relay = new Source("relay");
        var codex = new Source("codex");
        var claude = new Source("claude", LocalProxyKind.ClaudeCode);

        new LocalProxyCredentialRouter(relay, codex, claude).Clear();

        Assert.Equal(1, relay.Cleared);
        Assert.Equal(0, codex.Cleared);
        Assert.Equal(0, claude.Cleared);
    }
}
