using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Tests;

/// <summary>A <see cref="IUiTimer"/> whose ticks are driven by the test.</summary>
/// <remarks>
/// <para>
/// Replaces a <c>DispatcherTimer</c> that could never fire here. A dispatcher timer
/// needs a running dispatcher loop, and xUnit does not provide one, so before the
/// view model took its timer by injection the entire countdown — the decrement, the
/// stop at zero, and the re-enabling of the resend button — was unreachable by any
/// test. <c>VerifyCodeCountdownUsesServerSeconds</c> asserted only the value the
/// server handed back.
/// </para>
/// <para>
/// <see cref="Tick"/> advances one interval. Nothing here measures real time.
/// </para>
/// </remarks>
internal sealed class FakeUiTimer : IUiTimer
{
    private readonly Action _onTick;

    public FakeUiTimer(TimeSpan interval, Action onTick)
    {
        Interval = interval;
        _onTick = onTick;
    }

    public TimeSpan Interval { get; }

    public bool IsRunning { get; private set; }

    public bool IsDisposed { get; private set; }

    /// <summary>How many times the timer has been started.</summary>
    public int StartCount { get; private set; }

    public void Start()
    {
        IsRunning = true;
        StartCount++;
    }

    public void Stop() => IsRunning = false;

    public void Dispose()
    {
        IsDisposed = true;
        IsRunning = false;
    }

    /// <summary>Fires one tick, as a real timer would after <see cref="Interval"/>.</summary>
    /// <remarks>
    /// Ignored while stopped, matching a real timer: a test that ticks a stopped
    /// timer should see nothing happen rather than drive state the user never could.
    /// </remarks>
    public void Tick()
    {
        if (IsRunning)
        {
            _onTick();
        }
    }

    /// <summary>Fires <paramref name="count"/> ticks, stopping early if the timer stops.</summary>
    public void Tick(int count)
    {
        for (int i = 0; i < count && IsRunning; i++)
        {
            _onTick();
        }
    }
}
