using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ClientExitCoordinatorTests
{
    [Fact]
    public async Task SigningOutReleasesCodexBeforeRevokingTheLoginSession()
    {
        var order = new List<string>();
        var relay = new FakeRelayClient { OnLogout = () => order.Add("logout") };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup
        {
            OnRelease = () =>
            {
                order.Add("release");
                return Task.CompletedTask;
            },
        };
        var coordinator = new ClientExitCoordinator(codex, session);

        await coordinator.SignOutAsync();

        Assert.Equal(["release", "logout"], order);
        Assert.False(session.IsSignedIn);
    }

    [Fact]
    public async Task AReleaseFailureCannotLeaveTheUserSignedInAfterTheyChoseSignOut()
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup
        {
            OnRelease = () => throw new IOException("restore failed"),
        };
        var coordinator = new ClientExitCoordinator(codex, session);

        await coordinator.SignOutAsync();

        Assert.False(session.IsSignedIn);
    }

    [Fact]
    public async Task ARealProcessExitReleasesCodexButKeepsTheRememberedLogin()
    {
        var relay = new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        await session.SignInAsync("a@b.com", "pw");
        var codex = new FakeCodexStartup();
        var coordinator = new ClientExitCoordinator(codex, session);

        await coordinator.ReleaseForExitAsync();

        Assert.Equal(1, codex.ReleaseCallCount);
        Assert.True(session.IsSignedIn);
        Assert.Equal(0, relay.LogoutCallCount);
    }
}
