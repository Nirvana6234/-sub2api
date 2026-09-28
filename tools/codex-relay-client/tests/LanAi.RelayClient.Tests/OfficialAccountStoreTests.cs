using System.Text;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The official accounts signed in within the client (任务计划 §3.1–§3.2, A1): per 共飞 user,
/// encrypted, written whole, ids from −1000 down and never reused.
/// </summary>
public sealed class OfficialAccountStoreTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "official-accounts-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private OfficialAccountStore Open(string? scope = "user-a")
    {
        var store = new OfficialAccountStore(new TestSnapshotProtector(), _root);
        store.SetScope(scope);
        return store;
    }

    internal static OfficialAccount ChatGpt(string accountId = "acc-1", string refresh = "rt-1", string email = "me@example.com") => new()
    {
        Platform = OfficialAccount.OpenAi,
        Email = email,
        PlanType = "plus",
        AccessToken = "at-" + refresh,
        RefreshToken = refresh,
        ChatGptAccountId = accountId,
    };

    internal static OfficialAccount Claude(string uuid = "uuid-1", string refresh = "crt-1") => new()
    {
        Platform = OfficialAccount.Anthropic,
        Email = "me@example.com",
        AccessToken = "cat-" + refresh,
        RefreshToken = refresh,
        AccountUuid = uuid,
    };

    [Fact]
    public void IdsStartAtMinusOneThousandAndAreNeverReused()
    {
        OfficialAccountStore store = Open();

        OfficialAccount first = store.Add(ChatGpt("a"));
        OfficialAccount second = store.Add(Claude("b"));
        store.Remove(second.Id);
        OfficialAccount third = store.Add(ChatGpt("c"));

        Assert.Equal(-1000, first.Id);
        Assert.Equal(-1001, second.Id);
        Assert.Equal(-1002, third.Id);
        Assert.True(OfficialAccountIds.IsOfficial(third.Id));
        Assert.False(OfficialAccountIds.IsOfficial(LocalMachineAccounts.ClaudeId));
    }

    [Fact]
    public void TheSameAccountSignedInAgainKeepsItsIdAndBecomesValidAgain()
    {
        OfficialAccountStore store = Open();
        OfficialAccount first = store.Add(ChatGpt("a", refresh: "rt-old"));
        store.Update(first.Id, "rt-old", a => a with { InvalidReason = "登录已失效" });

        OfficialAccount again = store.Add(ChatGpt("a", refresh: "rt-new"));

        Assert.Equal(first.Id, again.Id);
        Assert.Equal(first.AddedAt, again.AddedAt);
        OfficialAccount stored = Assert.Single(store.List());
        Assert.Equal("rt-new", stored.RefreshToken);
        Assert.True(stored.IsValid);
    }

    [Fact]
    public void ItSurvivesAReopenAndTheFileIsNotPlaintext()
    {
        Open().Add(ChatGpt(refresh: "secret-refresh-token"));

        OfficialAccount reread = Assert.Single(Open().List());
        Assert.Equal("secret-refresh-token", reread.RefreshToken);
        Assert.Equal(LocalProxyKind.Codex, reread.Kind);

        byte[] raw = File.ReadAllBytes(Directory.GetFiles(_root, "accounts.bin", SearchOption.AllDirectories).Single());
        Assert.DoesNotContain("secret-refresh-token", Encoding.UTF8.GetString(raw), StringComparison.Ordinal);
    }

    [Fact]
    public void TwoClientUsersDoNotSeeEachOthersAccounts()
    {
        OfficialAccountStore store = Open("user-a");
        store.Add(ChatGpt());

        store.SetScope("user-b");
        Assert.Empty(store.List());

        store.SetScope(null);
        Assert.Empty(store.List());
        Assert.Throws<InvalidOperationException>(() => store.Add(ChatGpt()));

        store.SetScope("user-a");
        Assert.Single(store.List());
    }

    [Fact]
    public void AnUpdateIsRefusedOnceTheRefreshTokenChangedUnderIt()
    {
        OfficialAccountStore store = Open();
        OfficialAccount account = store.Add(ChatGpt(refresh: "rt-1"));
        store.Add(ChatGpt(refresh: "rt-2")); // signed in again meanwhile

        bool written = store.Update(account.Id, "rt-1", a => a with { RefreshToken = "rt-from-refresh" });

        Assert.False(written);
        Assert.Equal("rt-2", store.Get(account.Id)!.RefreshToken);
        Assert.False(store.Update(-4242, "rt-1", a => a));
    }

    [Fact]
    public void ConcurrentUpdatesOfDifferentAccountsAreAllKept()
    {
        OfficialAccountStore store = Open();
        long[] ids = Enumerable.Range(0, 8).Select(i => store.Add(ChatGpt($"acc-{i}", refresh: $"rt-{i}")).Id).ToArray();

        Parallel.ForEach(Enumerable.Range(0, 8), i =>
            Assert.True(store.Update(ids[i], $"rt-{i}", a => a with { RefreshToken = $"new-{i}" })));

        Dictionary<long, string> reread = Open().List().ToDictionary(a => a.Id, a => a.RefreshToken);
        Assert.All(Enumerable.Range(0, 8), i => Assert.Equal($"new-{i}", reread[ids[i]]));
    }

    [Fact]
    public void AnUnreadableFileIsKeptAsideNotOverwritten()
    {
        string directory = Path.Combine(_root, "user-a");
        Directory.CreateDirectory(directory);
        File.WriteAllBytes(Path.Combine(directory, "accounts.bin"), [1, 2, 3]);

        OfficialAccountStore store = Open();
        Assert.Empty(store.List());
        store.Add(ChatGpt());

        Assert.Single(Directory.GetFiles(directory, "accounts.bin.unreadable-*"));
        Assert.Single(Open().List());
    }

    [Fact]
    public void AFailedWriteChangesNothing()
    {
        OfficialAccountStore store = Open();
        OfficialAccount account = store.Add(ChatGpt(refresh: "rt-1"));
        string file = Directory.GetFiles(_root, "accounts.bin", SearchOption.AllDirectories).Single();

        // A directory where the temporary file must go makes the write fail.
        Directory.CreateDirectory(file + ".tmp");
        Exception failure = Assert.ThrowsAny<Exception>(() => store.Update(account.Id, "rt-1", a => a with { RefreshToken = "rt-2" }));
        Assert.True(failure is IOException or UnauthorizedAccessException, failure.GetType().Name);
        Directory.Delete(file + ".tmp");

        Assert.Equal("rt-1", store.Get(account.Id)!.RefreshToken);
        Assert.Equal("rt-1", Open().Get(account.Id)!.RefreshToken);
    }

    [Fact]
    public void NothingSecretReachesTheLogOrToString()
    {
        var log = new StringBuilder();
        using (ClientLog.Capture(log))
        {
            OfficialAccountStore store = Open();
            OfficialAccount account = store.Add(ChatGpt(refresh: "secret-refresh-token"));
            store.Remove(account.Id);
            Assert.DoesNotContain("secret", account.ToString(), StringComparison.Ordinal);
        }

        Assert.DoesNotContain("secret", log.ToString(), StringComparison.Ordinal);
        Assert.DoesNotContain("me@example.com", log.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void ChatGptPlansAndNamesAreShownReadably()
    {
        Assert.Equal("me@example.com · Plus", ChatGpt().DisplayName);
        Assert.Equal("Claude 账号", (Claude() with { Email = string.Empty }).DisplayName);
    }
}
