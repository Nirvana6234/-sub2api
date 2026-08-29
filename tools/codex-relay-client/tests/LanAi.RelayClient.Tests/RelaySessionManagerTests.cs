using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class RelaySessionManagerTests
{
    private const string Server = "https://relay.test/";

    private static (RelaySessionManager Manager, FakeRelayClient Client, FakeSessionStore Store, TestClock Clock) Build()
    {
        var client = new FakeRelayClient();
        var store = new FakeSessionStore();
        var clock = new TestClock();
        return (new RelaySessionManager(client, store, Server, clock.Read), client, store, clock);
    }

    [Fact]
    public async Task SigningInAdoptsAndPersistsTheSession()
    {
        (RelaySessionManager manager, _, FakeSessionStore store, _) = Build();

        await manager.SignInAsync("a@b.com", "pw");

        Assert.True(manager.IsSignedIn);
        Assert.Equal("ann", manager.UserDisplayName);
        Assert.NotNull(store.Current);
        Assert.Equal(Server, store.Current!.ServerAddress);
    }

    [Fact]
    public async Task ATwoFactorDemandDoesNotSignTheUserIn()
    {
        (RelaySessionManager manager, FakeRelayClient client, FakeSessionStore store, _) = Build();
        client.OnLogin = () => LoginOutcome.TwoFactorRequired("tmp", "a***@b.com");

        LoginOutcome outcome = await manager.SignInAsync("a@b.com", "pw");

        Assert.True(outcome.RequiresTwoFactor);
        Assert.False(manager.IsSignedIn);
        Assert.Null(store.Current);
    }

    [Fact]
    public async Task AFreshTokenIsReturnedWithoutContactingTheServer()
    {
        (RelaySessionManager manager, FakeRelayClient client, _, _) = Build();
        await manager.SignInAsync("a@b.com", "pw");

        string token = await manager.GetAccessTokenAsync();

        Assert.Equal("at", token);
        Assert.Equal(0, client.RefreshCallCount);
    }

    [Fact]
    public async Task TheTokenIsRenewedBeforeItActuallyExpires()
    {
        // Renewing only at the moment of expiry would let a call go out with a
        // token that dies in flight, which the user would see as a random logout.
        (RelaySessionManager manager, FakeRelayClient client, _, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");

        clock.Advance(TimeSpan.FromSeconds(3600 - 60));
        string token = await manager.GetAccessTokenAsync();

        Assert.Equal("at-renewed", token);
        Assert.Equal(1, client.RefreshCallCount);
    }

    [Fact]
    public async Task ARejectedRefreshSignsTheUserOutWithAnExplanation()
    {
        (RelaySessionManager manager, FakeRelayClient client, FakeSessionStore store, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        client.OnRefresh = () => throw new RelayApiException(RelayFailure.Unauthenticated, "expired");

        clock.Advance(TimeSpan.FromHours(2));

        await Assert.ThrowsAsync<RelayApiException>(() => manager.GetAccessTokenAsync());
        Assert.False(manager.IsSignedIn);
        Assert.Equal(SignOutReason.SessionExpired, manager.LastSignOutReason);
        Assert.Null(store.Current);
    }

    [Fact]
    public async Task BeingOfflineDoesNotThrowTheSessionAway()
    {
        // A network outage says nothing about whether the token is still valid.
        // Discarding the session here would make every commute a forced re-login.
        (RelaySessionManager manager, FakeRelayClient client, FakeSessionStore store, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        client.OnRefresh = () => throw new RelayApiException(RelayFailure.NetworkUnreachable, "offline");

        clock.Advance(TimeSpan.FromHours(2));

        await Assert.ThrowsAsync<RelayApiException>(() => manager.GetAccessTokenAsync());
        Assert.True(manager.IsSignedIn);
        Assert.NotNull(store.Current);
    }

    [Fact]
    public async Task ARefreshThatOmitsANewRefreshTokenKeepsTheExistingOne()
    {
        // Otherwise the session would silently become unrenewable and the user
        // would be thrown out at the next expiry for no visible reason.
        (RelaySessionManager manager, FakeRelayClient client, FakeSessionStore store, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        client.OnRefresh = () => FakeRelayClient.Tokens("at-2", refreshToken: string.Empty);

        clock.Advance(TimeSpan.FromHours(2));
        await manager.GetAccessTokenAsync();

        Assert.Equal("rt", store.Current!.RefreshToken);
        Assert.True(store.Current.CanRenew);
    }

    [Fact]
    public async Task ARefreshThatOmitsTheUserKeepsTheNameAlreadyOnScreen()
    {
        (RelaySessionManager manager, FakeRelayClient client, _, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        client.OnRefresh = () => FakeRelayClient.Tokens("at-2", email: null);

        clock.Advance(TimeSpan.FromHours(2));
        await manager.GetAccessTokenAsync();

        Assert.Equal("ann", manager.UserDisplayName);
        Assert.Equal("a@b.com", manager.UserEmail);
    }

    [Fact]
    public async Task AnAccessOnlySessionExpiresCleanlyInsteadOfFailingToRenew()
    {
        // The server may issue an access token with no refresh token. That session
        // simply ends; it must not look like a malfunction.
        (RelaySessionManager manager, FakeRelayClient client, _, TestClock clock) = Build();
        client.OnLogin = () => LoginOutcome.Authenticated(
            FakeRelayClient.Tokens("at", refreshToken: string.Empty));
        await manager.SignInAsync("a@b.com", "pw");

        clock.Advance(TimeSpan.FromHours(2));

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(() => manager.GetAccessTokenAsync());

        Assert.Equal(RelayFailure.Unauthenticated, error.Failure);
        Assert.Equal(SignOutReason.SessionExpired, manager.LastSignOutReason);
        Assert.Equal(0, client.RefreshCallCount);
    }

    [Fact]
    public async Task AMissingExpiryIsTreatedAsShortLivedRatherThanEternal()
    {
        // Assuming a long life would let calls fail unexplained; assuming zero
        // would sign the user out immediately. A short assumed life verifies early.
        (RelaySessionManager manager, FakeRelayClient client, _, TestClock clock) = Build();
        client.OnLogin = () => LoginOutcome.Authenticated(FakeRelayClient.Tokens("at", expiresIn: 0));
        await manager.SignInAsync("a@b.com", "pw");

        clock.Advance(TimeSpan.FromMinutes(4));
        await manager.GetAccessTokenAsync();

        Assert.Equal(1, client.RefreshCallCount);
    }

    [Fact]
    public async Task ConcurrentCallersSpendOnlyOneRefresh()
    {
        (RelaySessionManager manager, FakeRelayClient client, _, TestClock clock) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        clock.Advance(TimeSpan.FromHours(2));

        await Task.WhenAll(Enumerable.Range(0, 8).Select(_ => manager.GetAccessTokenAsync()));

        Assert.Equal(1, client.RefreshCallCount);
    }

    [Fact]
    public async Task ASessionFromAnotherServerIsDiscardedOnRestore()
    {
        // A stored session proves nothing about which relay is behind an address.
        var client = new FakeRelayClient();
        var store = new FakeSessionStore
        {
            Current = new StoredSession
            {
                ServerAddress = "https://other.test/",
                AccessToken = "at",
                RefreshToken = "rt",
                AccessExpiresAt = DateTimeOffset.MaxValue,
            },
        };
        var manager = new RelaySessionManager(client, store, Server, new TestClock().Read);

        bool restored = await manager.RestoreAsync();

        Assert.False(restored);
        Assert.False(manager.IsSignedIn);
        Assert.Null(store.Current);
    }

    [Fact]
    public async Task SigningOutCompletesEvenWhenRevocationFails()
    {
        // A user who chose to sign out must end up signed out locally regardless
        // of whether the server could be told.
        (RelaySessionManager manager, FakeRelayClient client, FakeSessionStore store, _) = Build();
        await manager.SignInAsync("a@b.com", "pw");
        client.OnLogout = () => throw new RelayApiException(RelayFailure.NetworkUnreachable, "offline");

        await manager.SignOutAsync();

        Assert.False(manager.IsSignedIn);
        Assert.Null(store.Current);
        Assert.Equal(SignOutReason.UserRequested, manager.LastSignOutReason);
    }

    [Fact]
    public async Task StateChangesAreAnnouncedOnSignInAndSignOut()
    {
        (RelaySessionManager manager, _, _, _) = Build();
        int changes = 0;
        manager.StateChanged += (_, _) => changes++;

        await manager.SignInAsync("a@b.com", "pw");
        await manager.SignOutAsync();

        Assert.Equal(2, changes);
    }
}
