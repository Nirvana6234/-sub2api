using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>Detects active relay use from request counters and checks low balance only then.</summary>
internal sealed class BalanceActivityMonitor
{
    internal const double LowBalanceThreshold = 0.2;
    internal static readonly TimeSpan ReminderCooldown = TimeSpan.FromMinutes(30);

    private readonly IRelayServerClient _relay;
    private readonly RelaySessionManager _session;
    private readonly Func<DateTimeOffset> _clock;
    private readonly SemaphoreSlim _checkGate = new(1, 1);

    private long? _lastRequestCount;
    private DateTimeOffset? _lastReminderAt;

    public BalanceActivityMonitor(
        IRelayServerClient relay,
        RelaySessionManager session,
        Func<DateTimeOffset>? clock = null)
    {
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    /// <summary>
    /// Establishes a request baseline or reports a newly active request and its balance state.
    /// </summary>
    public async Task<BalanceActivityObservation> CheckAsync(CancellationToken cancellationToken = default)
    {
        if (!await _checkGate.WaitAsync(0, cancellationToken).ConfigureAwait(true))
        {
            return BalanceActivityObservation.None;
        }

        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            DashboardStats stats = await _relay.GetDashboardStatsAsync(token, cancellationToken).ConfigureAwait(true);

            bool isActive = _lastRequestCount is long previous && stats.TodayRequests > previous;
            _lastRequestCount = stats.TodayRequests;
            if (!isActive)
            {
                return BalanceActivityObservation.None;
            }

            RelayUser user = await _relay.GetCurrentUserAsync(token, cancellationToken).ConfigureAwait(true);
            bool isLowBalance = user.Balance < LowBalanceThreshold;
            DateTimeOffset now = _clock();
            if (!isLowBalance)
            {
                _lastReminderAt = null;
            }

            bool shouldNotify = isLowBalance &&
                                (_lastReminderAt is null || now - _lastReminderAt >= ReminderCooldown);
            if (shouldNotify)
            {
                _lastReminderAt = now;
            }

            return new BalanceActivityObservation(
                IsActive: true,
                IsLowBalance: isLowBalance,
                ShouldNotify: shouldNotify,
                Balance: user.Balance);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return BalanceActivityObservation.None;
        }
        catch (Exception exception) when (IsBackgroundFailure(exception))
        {
            ClientLog.Warning("活动状态余额监控失败", exception);
            return BalanceActivityObservation.None;
        }
        finally
        {
            _checkGate.Release();
        }
    }

    /// <summary>Forgets the previous account's baseline and notification cooldown.</summary>
    public void Reset()
    {
        _lastRequestCount = null;
        _lastReminderAt = null;
    }

    private static bool IsBackgroundFailure(Exception exception) =>
        exception is not (OutOfMemoryException or StackOverflowException or ThreadAbortException);
}

/// <summary>The outcome of one active-use observation.</summary>
internal sealed record BalanceActivityObservation(
    bool IsActive,
    bool IsLowBalance,
    bool ShouldNotify,
    double Balance)
{
    public static BalanceActivityObservation None { get; } = new(false, false, false, 0);
}
