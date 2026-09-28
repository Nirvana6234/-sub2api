using System.Text.Json.Nodes;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The client's own official sign-ins as local-proxy credentials (任务计划 §3.5, A4): refreshed
/// once at a time, written before use, never lost, and marked when gone.
/// </summary>
public sealed class OfficialAccountSourceTests : IDisposable
{
    private static readonly DateTimeOffset Now = new(2026, 9, 28, 12, 0, 0, TimeSpan.Zero);

    private readonly string _root = Path.Combine(Path.GetTempPath(), "official-source-" + Guid.NewGuid().ToString("N"));
    private DateTimeOffset _now = Now;

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private (OfficialAccountSource Source, OfficialAccountStore Store, FakeTokenRefresher Refresher) Create()
    {
        var store = new OfficialAccountStore(new TestSnapshotProtector(), _root, () => _now);
        store.SetScope("user-a");
        var refresher = new FakeTokenRefresher();
        return (new OfficialAccountSource(store, refresher, () => _now), store, refresher);
    }

    private static OfficialAccount ChatGpt(DateTimeOffset expiresAt, string refresh = "rt-1") =>
        OfficialAccountStoreTests.ChatGpt(refresh: refresh) with { ExpiresAt = expiresAt, FedRamp = true };

    private string AccountsFile() => Directory.GetFiles(_root, "accounts.bin", SearchOption.AllDirectories).Single();

    [Fact]
    public async Task AFreshTokenIsUsedAsItIsWithTheAccountsIdentity()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddHours(1)));

        LocalProxyCredential credential = await source.GetAsync(account.Id, forceRefresh: false, CancellationToken.None);

        Assert.Empty(refresher.CodexCalls);
        Assert.Equal("at-rt-1", credential.AccessToken);
        Assert.Equal("acc-1", credential.ChatGptAccountId);
        Assert.True(credential.FedRamp);
        Assert.Equal(account.Id, credential.AccountId);
    }

    [Fact]
    public async Task AnExpiringTokenIsRefreshedAndWrittenBeforeItIsUsed()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(3)));

        LocalProxyCredential credential = await source.GetAsync(account.Id, forceRefresh: false, CancellationToken.None);

        Assert.Equal(["rt-1"], refresher.CodexCalls);
        Assert.Equal("new-access", credential.AccessToken);
        OfficialAccount stored = store.Get(account.Id)!;
        Assert.Equal("rt-1-next", stored.RefreshToken);
        Assert.Equal(Now, stored.LastRefreshAt);
    }

    [Fact]
    public async Task ARefusedTokenForcesOneRefreshAMinuteAtMost()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddHours(1)));

        await source.GetAsync(account.Id, forceRefresh: true, CancellationToken.None);
        await source.GetAsync(account.Id, forceRefresh: true, CancellationToken.None);
        _now = Now.AddSeconds(61);
        await source.GetAsync(account.Id, forceRefresh: true, CancellationToken.None);

        Assert.Equal(["rt-1", "rt-1-next"], refresher.CodexCalls);
    }

    [Fact]
    public async Task ManyTurnsAtOnceSpendTheRefreshTokenOnlyOnce()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(1)));

        LocalProxyCredential[] results = await Task.WhenAll(Enumerable.Range(0, 6)
            .Select(_ => Task.Run(() => source.GetAsync(account.Id, forceRefresh: false, CancellationToken.None))));

        Assert.Single(refresher.CodexCalls);
        Assert.All(results, r => Assert.Equal("new-access", r.AccessToken));
    }

    [Fact]
    public async Task AGoneSignInIsMarkedAndNotRefreshedAgain()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(1)));
        refresher.OnCodex = _ => throw new LocalProxyCredentialException("本机 ChatGPT 登录已失效") { SignInGone = true };

        var first = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => source.GetAsync(account.Id, false, CancellationToken.None));
        var second = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => source.GetAsync(account.Id, false, CancellationToken.None));

        Assert.Contains("重新登录", first.UserMessage, StringComparison.Ordinal);
        Assert.DoesNotContain("本机", first.UserMessage, StringComparison.Ordinal);
        Assert.Equal(first.UserMessage, second.UserMessage);
        Assert.Single(refresher.CodexCalls);
        Assert.False(store.Get(account.Id)!.IsValid);

        // Signing in again makes it usable again.
        store.Add(ChatGpt(Now.AddHours(1), refresh: "rt-again"));
        Assert.Equal("at-rt-again", (await source.GetAsync(account.Id, false, CancellationToken.None)).AccessToken);
    }

    [Fact]
    public async Task OtherRefreshFailuresAreNotAboutThisMachine()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(1)));
        refresher.OnCodex = _ => throw new LocalProxyCredentialException("刷新本机 ChatGPT 登录失败（HTTP 500），稍后会自动重试。");

        var failure = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => source.GetAsync(account.Id, false, CancellationToken.None));

        Assert.Equal("刷新 ChatGPT 登录失败（HTTP 500），稍后会自动重试。", failure.UserMessage);
        Assert.True(store.Get(account.Id)!.IsValid);
    }

    [Fact]
    public async Task APairThatCouldNotBeWrittenIsUsedAndWrittenLater()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(1)));
        string blocker = AccountsFile() + ".tmp";
        Directory.CreateDirectory(blocker);

        LocalProxyCredential first = await source.GetAsync(account.Id, false, CancellationToken.None);
        Assert.Equal("new-access", first.AccessToken);
        Assert.Equal("rt-1", store.Get(account.Id)!.RefreshToken);

        Directory.Delete(blocker);
        LocalProxyCredential second = await source.GetAsync(account.Id, false, CancellationToken.None);

        Assert.Equal("new-access", second.AccessToken);
        Assert.Equal("rt-1-next", store.Get(account.Id)!.RefreshToken);
        Assert.Single(refresher.CodexCalls);
    }

    [Fact]
    public async Task ASignInMadeAgainDuringARefreshWins()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(OfficialAccountStoreTests.Claude(refresh: "crt-1") with { ExpiresAt = Now.AddMinutes(1) });
        refresher.DuringClaudeRefresh = () =>
            store.Add(OfficialAccountStoreTests.Claude(refresh: "crt-again") with { ExpiresAt = Now.AddHours(8) });

        LocalProxyCredential credential = await source.GetAsync(account.Id, false, CancellationToken.None);

        Assert.Equal("cat-crt-again", credential.AccessToken);
        Assert.Equal("crt-again", store.Get(account.Id)!.RefreshToken);
    }

    [Fact]
    public async Task AClaudeRefreshSetsItsExpiryFromTheAnswer()
    {
        var (source, store, _) = Create();
        OfficialAccount account = store.Add(OfficialAccountStoreTests.Claude() with { ExpiresAt = Now.AddMinutes(1) });

        LocalProxyCredential credential = await source.GetAsync(account.Id, false, CancellationToken.None);

        Assert.Equal("new-claude-access", credential.AccessToken);
        Assert.Equal(Now.AddSeconds(28800), store.Get(account.Id)!.ExpiresAt);
    }

    [Fact]
    public async Task ARemovedAccountSaysSo()
    {
        var (source, store, _) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddHours(1)));
        store.Remove(account.Id);

        var failure = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => source.GetAsync(account.Id, false, CancellationToken.None));
        Assert.Contains("已被删除", failure.UserMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task TheRouterSendsTheClientsOwnIdsHere()
    {
        var (source, store, _) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddHours(1)));
        var router = new LocalProxyCredentialRouter(
            new LocalProxyViewModelTests.FakeLocalAccount(LocalProxyKind.Codex),
            new LocalProxyViewModelTests.FakeLocalAccount(LocalProxyKind.ClaudeCode),
            source);

        Assert.Equal("at-rt-1", (await router.GetAsync(account.Id, false, CancellationToken.None)).AccessToken);

        // A relay-server account id, saved by an older client, is refused in words.
        var retired = await Assert.ThrowsAsync<LocalProxyCredentialException>(() => router.GetAsync(7, false, CancellationToken.None));
        Assert.Contains("不再使用中转站上的账号", retired.UserMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ARefreshedIdTokenUpdatesTheLabel()
    {
        var (source, store, refresher) = Create();
        OfficialAccount account = store.Add(ChatGpt(Now.AddMinutes(1)));
        string idToken = TestJwt.Make(new JsonObject
        {
            ["email"] = "new@example.com",
            ["https://api.openai.com/auth"] = new JsonObject { ["chatgpt_plan_type"] = "pro", ["chatgpt_account_id"] = "acc-1" },
        });
        refresher.OnCodex = refresh => new CodexRefreshedTokens("new-access", refresh + "-next", idToken);

        LocalProxyCredential credential = await source.GetAsync(account.Id, false, CancellationToken.None);

        Assert.Equal("new@example.com · Pro", credential.Name);
        Assert.False(store.Get(account.Id)!.FedRamp);
    }
}
