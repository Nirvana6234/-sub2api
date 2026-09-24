using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

public enum PaymentPollingResult
{
    Pending,
    Completed,
    Expired,
    Cancelled,
    Failed,
}

public sealed record PaymentPollingOutcome(PaymentPollingResult Result, PaymentOrder Order);

/// <summary>Serializes payment status reads and stops at a terminal order state.</summary>
public sealed class PaymentPollingCoordinator
{
    private static readonly TimeSpan DefaultInterval = TimeSpan.FromSeconds(3);

    private readonly Func<CancellationToken, Task<PaymentOrder>> _query;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;
    private readonly TimeSpan _interval;
    private readonly SemaphoreSlim _gate = new(1, 1);

    public PaymentPollingCoordinator(
        Func<CancellationToken, Task<PaymentOrder>> query,
        Func<TimeSpan, CancellationToken, Task>? delay = null,
        TimeSpan? interval = null)
    {
        _query = query ?? throw new ArgumentNullException(nameof(query));
        _delay = delay ?? Task.Delay;
        _interval = interval ?? DefaultInterval;
    }

    public async Task<PaymentPollingOutcome?> PollAsync(CancellationToken cancellationToken = default)
    {
        if (!await _gate.WaitAsync(0, cancellationToken).ConfigureAwait(false))
        {
            return null;
        }

        try
        {
            while (true)
            {
                PaymentOrder order = await _query(cancellationToken).ConfigureAwait(false);
                PaymentPollingResult result = Map(order.Status);
                if (result != PaymentPollingResult.Pending)
                {
                    return new PaymentPollingOutcome(result, order);
                }

                await _delay(_interval, cancellationToken).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return null;
        }
        finally
        {
            _gate.Release();
        }
    }

    private static PaymentPollingResult Map(PaymentOrderStatus status) => status switch
    {
        PaymentOrderStatus.Paid or PaymentOrderStatus.Recharging or PaymentOrderStatus.Completed =>
            PaymentPollingResult.Completed,
        PaymentOrderStatus.Expired => PaymentPollingResult.Expired,
        PaymentOrderStatus.Cancelled => PaymentPollingResult.Cancelled,
        PaymentOrderStatus.Failed => PaymentPollingResult.Failed,
        _ => PaymentPollingResult.Pending,
    };
}
