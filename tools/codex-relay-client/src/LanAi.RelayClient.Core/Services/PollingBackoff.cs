namespace LanAi.RelayClient.Services;

/// <summary>Tracks panel rate-limit deadlines without sleeping the UI thread.</summary>
internal sealed class PollingBackoff
{
    private static readonly TimeSpan[] Delays =
    [
        TimeSpan.FromMinutes(1),
        TimeSpan.FromMinutes(2),
        TimeSpan.FromMinutes(4),
        TimeSpan.FromMinutes(8),
        TimeSpan.FromMinutes(15),
    ];

    private readonly Func<DateTimeOffset> _clock;
    private int _rateLimitCount;
    private DateTimeOffset? _nextAllowedAt;

    public PollingBackoff(Func<DateTimeOffset>? clock = null) =>
        _clock = clock ?? (() => DateTimeOffset.UtcNow);

    public bool CanAttempt => _nextAllowedAt is null || _clock() >= _nextAllowedAt;

    public TimeSpan Remaining
    {
        get
        {
            if (_nextAllowedAt is not { } deadline)
            {
                return TimeSpan.Zero;
            }

            TimeSpan remaining = deadline - _clock();
            return remaining > TimeSpan.Zero ? remaining : TimeSpan.Zero;
        }
    }

    public TimeSpan RecordRateLimited()
    {
        TimeSpan delay = Delays[Math.Min(_rateLimitCount, Delays.Length - 1)];
        _rateLimitCount++;
        _nextAllowedAt = _clock() + delay;
        return delay;
    }

    public void RecordSuccess()
    {
        _rateLimitCount = 0;
        _nextAllowedAt = null;
    }
}
