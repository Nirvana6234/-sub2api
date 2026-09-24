using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PaymentPollingCoordinatorTests
{
    [Theory]
    [InlineData(PaymentOrderStatus.Paid)]
    [InlineData(PaymentOrderStatus.Recharging)]
    [InlineData(PaymentOrderStatus.Completed)]
    public async Task PaidStatesStopPollingAsCompleted(PaymentOrderStatus status)
    {
        int calls = 0;
        var coordinator = new PaymentPollingCoordinator(
            _ =>
            {
                calls++;
                return Task.FromResult(Order(status));
            },
            (_, _) => Task.CompletedTask);

        PaymentPollingOutcome? result = await coordinator.PollAsync();

        Assert.NotNull(result);
        Assert.Equal(PaymentPollingResult.Completed, result!.Result);
        Assert.Equal(1, calls);
    }

    [Theory]
    [InlineData(PaymentOrderStatus.Expired, PaymentPollingResult.Expired)]
    [InlineData(PaymentOrderStatus.Cancelled, PaymentPollingResult.Cancelled)]
    [InlineData(PaymentOrderStatus.Failed, PaymentPollingResult.Failed)]
    public async Task TerminalFailureStatesStopPolling(PaymentOrderStatus status, PaymentPollingResult expected)
    {
        var coordinator = new PaymentPollingCoordinator(_ => Task.FromResult(Order(status)), (_, _) => Task.CompletedTask);

        PaymentPollingOutcome? result = await coordinator.PollAsync();

        Assert.NotNull(result);
        Assert.Equal(expected, result!.Result);
    }

    [Fact]
    public async Task OverlappingPollsDoNotIssueConcurrentQueries()
    {
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        int calls = 0;
        var coordinator = new PaymentPollingCoordinator(
            async cancellationToken =>
            {
                calls++;
                entered.SetResult();
                await release.Task.WaitAsync(cancellationToken);
                return Order(PaymentOrderStatus.Completed);
            });

        Task<PaymentPollingOutcome?> first = coordinator.PollAsync();
        await entered.Task.WaitAsync(TimeSpan.FromSeconds(5));
        PaymentPollingOutcome? overlapping = await coordinator.PollAsync();

        Assert.Null(overlapping);
        release.SetResult();
        await first;
        Assert.Equal(1, calls);
    }

    [Fact]
    public async Task CancellationStopsAPendingPollWithoutAnotherQuery()
    {
        using var cancellation = new CancellationTokenSource();
        int calls = 0;
        var coordinator = new PaymentPollingCoordinator(
            _ =>
            {
                calls++;
                return Task.FromResult(Order(PaymentOrderStatus.Pending));
            },
            (_, token) =>
            {
                cancellation.Cancel();
                return Task.FromCanceled(token);
            });

        PaymentPollingOutcome? result = await coordinator.PollAsync(cancellation.Token);

        Assert.Null(result);
        Assert.Equal(1, calls);
    }

    private static PaymentOrder Order(PaymentOrderStatus status) => new()
    {
        Id = 42,
        Status = status,
        OutTradeNo = "OUT42",
        ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
    };
}
