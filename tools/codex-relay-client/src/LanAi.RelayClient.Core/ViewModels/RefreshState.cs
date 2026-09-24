using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// The bookkeeping every card load shares within one refresh cycle: the backoff, the
/// rate-limit banner, and whether a card saw its token rejected.
/// </summary>
/// <remarks>
/// <para>
/// Owned by <see cref="DashboardViewModel"/>, which still decides the order the cards
/// load in. Split out so that each card — wherever it lives — reports its failure to
/// one place through <see cref="Observe"/>, used as the card's <c>catch</c> filter.
/// </para>
/// <para>
/// A 401 from a card never signs the user out here. <see cref="StopForUnauthenticatedAsync"/>
/// hands the rejected token to <see cref="RelaySessionManager.NotifyAccessTokenRejectedAsync"/>,
/// which forces the renewal check the local clock alone would not have triggered;
/// ending the session remains that renewal's decision.
/// </para>
/// </remarks>
public sealed partial class RefreshState : ObservableObject
{
    private readonly PollingBackoff _backoff;
    private readonly RelaySessionManager _session;

    private bool _hadFailure;
    private bool _wasRateLimited;
    private bool _sawUnauthenticated;

    internal RefreshState(PollingBackoff backoff, RelaySessionManager session)
    {
        _backoff = backoff ?? throw new ArgumentNullException(nameof(backoff));
        _session = session ?? throw new ArgumentNullException(nameof(session));
    }

    [ObservableProperty]
    private bool isRefreshing;

    [ObservableProperty]
    private bool isRateLimited;

    [ObservableProperty]
    private string refreshMessage = string.Empty;

    /// <summary>Whether the backoff allows a call to the panel endpoints right now.</summary>
    internal bool CanAttempt => _backoff.CanAttempt;

    /// <summary>Whether a card in this cycle has been rate limited.</summary>
    /// <remarks>Read mid-card, too: the group card stops between its two calls.</remarks>
    internal bool WasRateLimited => _wasRateLimited;

    /// <summary>
    /// Whether a card's failure is one the panel absorbs by greying that card.
    /// </summary>
    /// <remarks>
    /// F4.2 forbids one card taking down the page, and a <c>catch</c> narrowed to
    /// <see cref="RelayApiException"/> does not deliver that: any other escape —
    /// a cancellation, a serialization fault, a bug in a mapper — would propagate
    /// out of the loader and abandon the cards queued behind it. The filter is
    /// deliberately broad, and deliberately still excludes the exceptions that
    /// indicate the process itself is unsound.
    /// </remarks>
    internal static bool IsCardFailure(Exception ex) =>
        ex is not (OutOfMemoryException or StackOverflowException or ThreadAbortException);

    /// <summary>Starts a cycle with nothing observed yet.</summary>
    internal void BeginCycle()
    {
        _hadFailure = false;
        _wasRateLimited = false;
        _sawUnauthenticated = false;
    }

    /// <summary>
    /// Records a card's failure. Returns whether the card should absorb it, so it can
    /// be used directly as a <c>catch</c> filter.
    /// </summary>
    internal bool Observe(Exception ex)
    {
        if (!IsCardFailure(ex))
        {
            return false;
        }

        _hadFailure = true;
        if (ex is RelayApiException relayEx)
        {
            if (relayEx.Failure == RelayFailure.RateLimited)
            {
                _wasRateLimited = true;
            }
            else if (relayEx.Failure == RelayFailure.Unauthenticated)
            {
                _sawUnauthenticated = true;
            }
        }

        return true;
    }

    /// <summary>Ends the cycle early if a card was rate limited, starting the backoff.</summary>
    internal bool StopForRateLimit()
    {
        if (!_wasRateLimited)
        {
            return false;
        }

        RecordRateLimited();
        return true;
    }

    /// <summary>
    /// Reports a token a card just watched get rejected, and ends this refresh cycle if
    /// so — every remaining card shares the same (now known-bad) token, so trying them
    /// is only more failed calls before the next poll retries clean.
    /// </summary>
    internal async Task<bool> StopForUnauthenticatedAsync(string accessToken, CancellationToken cancellationToken)
    {
        if (!_sawUnauthenticated)
        {
            return false;
        }

        await _session.NotifyAccessTokenRejectedAsync(accessToken, cancellationToken).ConfigureAwait(true);
        return true;
    }

    /// <summary>Starts (or extends) the backoff and says so in the banner.</summary>
    internal void RecordRateLimited() => ApplyBackoffMessage(_backoff.RecordRateLimited());

    /// <summary>Repeats the banner for a poll the backoff has just refused.</summary>
    internal void ShowBackoff() => ApplyBackoffMessage(_backoff.Remaining);

    /// <summary>Clears the backoff and the banner when every card in the cycle loaded.</summary>
    internal void CompleteCycle()
    {
        if (!_hadFailure)
        {
            Clear();
        }
    }

    /// <summary>Forgets everything, for the next account.</summary>
    internal void Reset()
    {
        IsRefreshing = false;
        BeginCycle();
        Clear();
    }

    private void Clear()
    {
        _backoff.RecordSuccess();
        IsRateLimited = false;
        RefreshMessage = string.Empty;
    }

    private void ApplyBackoffMessage(TimeSpan remaining)
    {
        int minutes = Math.Max(1, (int)Math.Ceiling(remaining.TotalMinutes));
        IsRateLimited = true;
        RefreshMessage = $"请求频繁，请在约 {minutes} 分钟后重试。";
    }
}
