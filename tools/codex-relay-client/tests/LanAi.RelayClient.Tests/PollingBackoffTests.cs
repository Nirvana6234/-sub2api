using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PollingBackoffTests
{
    [Fact]
    public void RateLimitsAdvanceExponentiallyAndCapAtFifteenMinutes()
    {
        var clock = new TestClock();
        var backoff = new PollingBackoff(clock.Read);
        TimeSpan[] expected =
        [
            TimeSpan.FromMinutes(1),
            TimeSpan.FromMinutes(2),
            TimeSpan.FromMinutes(4),
            TimeSpan.FromMinutes(8),
            TimeSpan.FromMinutes(15),
            TimeSpan.FromMinutes(15),
        ];

        foreach (TimeSpan delay in expected)
        {
            Assert.Equal(delay, backoff.RecordRateLimited());
            clock.Advance(delay);
        }
    }

    [Fact]
    public void AttemptsAreBlockedUntilTheRecordedDeadline()
    {
        var clock = new TestClock();
        var backoff = new PollingBackoff(clock.Read);

        backoff.RecordRateLimited();

        Assert.False(backoff.CanAttempt);
        Assert.Equal(TimeSpan.FromMinutes(1), backoff.Remaining);
        clock.Advance(TimeSpan.FromSeconds(59));
        Assert.False(backoff.CanAttempt);
        clock.Advance(TimeSpan.FromSeconds(1));
        Assert.True(backoff.CanAttempt);
        Assert.Equal(TimeSpan.Zero, backoff.Remaining);
    }

    [Fact]
    public void ASuccessfulCycleResetsTheSequence()
    {
        var clock = new TestClock();
        var backoff = new PollingBackoff(clock.Read);
        backoff.RecordRateLimited();
        clock.Advance(TimeSpan.FromMinutes(1));
        backoff.RecordRateLimited();

        backoff.RecordSuccess();

        Assert.True(backoff.CanAttempt);
        Assert.Equal(TimeSpan.FromMinutes(1), backoff.RecordRateLimited());
    }
}
