using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class SafeAsyncRunnerTests
{
    [Fact]
    public async Task AnAsyncFailureIsObservedAndReported()
    {
        Exception? logged = null;
        Exception? reported = null;
        var runner = new SafeAsyncRunner(
            log: ex => logged = ex,
            report: ex =>
            {
                reported = ex;
                return Task.CompletedTask;
            });

        await runner.RunAsync(() => Task.FromException(new InvalidOperationException("boom")));

        Assert.Same(logged, reported);
        Assert.Equal("boom", reported!.Message);
    }

    [Fact]
    public async Task ASynchronousFailureIsAlsoObserved()
    {
        int reportCount = 0;
        var runner = new SafeAsyncRunner(
            log: _ => { },
            report: _ =>
            {
                reportCount++;
                return Task.CompletedTask;
            });

        await runner.RunAsync(() => throw new InvalidOperationException("boom"));

        Assert.Equal(1, reportCount);
    }

    [Fact]
    public async Task CancellationRequestedByTheCallerIsSilent()
    {
        int reportCount = 0;
        using var cancellation = new CancellationTokenSource();
        cancellation.Cancel();
        var runner = new SafeAsyncRunner(
            log: _ => { },
            report: _ =>
            {
                reportCount++;
                return Task.CompletedTask;
            });

        await runner.RunAsync(
            () => Task.FromCanceled(cancellation.Token),
            cancellation.Token);

        Assert.Equal(0, reportCount);
    }
}
