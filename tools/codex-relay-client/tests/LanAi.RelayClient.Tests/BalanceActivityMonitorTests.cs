using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class BalanceActivityMonitorTests
{
    [Fact]
    public async Task FirstObservationOnlyEstablishesTheRequestBaseline()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 11);

        BalanceActivityObservation observation = await monitor.CheckAsync();

        Assert.False(observation.IsActive);
        Assert.False(observation.IsLowBalance);
        Assert.False(observation.ShouldNotify);
        Assert.Equal(0, relay.CurrentUserCallCount);
    }

    [Fact]
    public async Task NewRequestWithBalanceBelowThresholdRequestsTrayReminder()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 11);
        relay.OnCurrentUser = () => new RelayUser { Balance = 0.19 };
        await monitor.CheckAsync();

        BalanceActivityObservation observation = await monitor.CheckAsync();

        Assert.True(observation.IsActive);
        Assert.True(observation.IsLowBalance);
        Assert.True(observation.ShouldNotify);
        Assert.Equal(0.19, observation.Balance);
        Assert.Equal(1, relay.CurrentUserCallCount);
    }

    [Fact]
    public async Task NoNewRequestDoesNotReadTheBalance()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 10);
        await monitor.CheckAsync();

        BalanceActivityObservation observation = await monitor.CheckAsync();

        Assert.False(observation.IsActive);
        Assert.Equal(0, relay.CurrentUserCallCount);
    }

    [Fact]
    public async Task BalanceAtThresholdDoesNotRequestReminder()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 11);
        relay.OnCurrentUser = () => new RelayUser { Balance = 0.2 };
        await monitor.CheckAsync();

        BalanceActivityObservation observation = await monitor.CheckAsync();

        Assert.True(observation.IsActive);
        Assert.False(observation.IsLowBalance);
        Assert.False(observation.ShouldNotify);
    }

    [Fact]
    public async Task LowBalanceReminderWaitsForTheCooldownBeforeRepeating()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, TestClock clock) = await BuildAsync(10, 11, 12, 13);
        relay.OnCurrentUser = () => new RelayUser { Balance = 0.1 };
        await monitor.CheckAsync();

        Assert.True((await monitor.CheckAsync()).ShouldNotify);

        clock.Advance(TimeSpan.FromMinutes(29));
        Assert.False((await monitor.CheckAsync()).ShouldNotify);

        clock.Advance(TimeSpan.FromMinutes(1));
        Assert.True((await monitor.CheckAsync()).ShouldNotify);
    }

    [Fact]
    public async Task BalanceRecoveryAllowsANewLowBalanceReminder()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 11, 12, 13);
        var balances = new Queue<double>([0.1, 0.3, 0.1]);
        relay.OnCurrentUser = () => new RelayUser { Balance = balances.Dequeue() };
        await monitor.CheckAsync();

        Assert.True((await monitor.CheckAsync()).ShouldNotify);
        Assert.False((await monitor.CheckAsync()).IsLowBalance);
        Assert.True((await monitor.CheckAsync()).ShouldNotify);
    }

    [Fact]
    public async Task ResetDropsThePreviousAccountRequestBaseline()
    {
        (BalanceActivityMonitor monitor, FakeRelayClient relay, _) = await BuildAsync(10, 11, 12);
        await monitor.CheckAsync();
        await monitor.CheckAsync();

        monitor.Reset();
        int balanceReadsBeforeResetObservation = relay.CurrentUserCallCount;
        BalanceActivityObservation observation = await monitor.CheckAsync();

        Assert.False(observation.IsActive);
        Assert.Equal(balanceReadsBeforeResetObservation, relay.CurrentUserCallCount);
    }

    private static async Task<(BalanceActivityMonitor Monitor, FakeRelayClient Relay, TestClock Clock)> BuildAsync(
        params long[] requestCounts)
    {
        var relay = new FakeRelayClient();
        var store = new FakeSessionStore();
        var clock = new TestClock();
        var session = new RelaySessionManager(relay, store, "https://relay.test/", clock.Read);
        await session.SignInAsync("a@b.com", "pw");

        var requests = new Queue<long>(requestCounts);
        relay.OnDashboardStats = () => new DashboardStats { TodayRequests = requests.Dequeue() };

        return (new BalanceActivityMonitor(relay, session, clock.Read), relay, clock);
    }
}
