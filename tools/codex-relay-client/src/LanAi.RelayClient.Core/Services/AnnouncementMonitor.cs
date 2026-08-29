using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Fetches announcements and decides which of them the user has not been told about.
/// </summary>
/// <remarks>
/// The single fetch point for announcements: the list view, the unread badge and
/// the tray reminder all read one observation, so they cannot disagree about what
/// exists or how much of it is unread.
/// </remarks>
internal sealed class AnnouncementMonitor
{
    private readonly IRelayServerClient _relay;
    private readonly RelaySessionManager _session;
    private readonly IAnnouncementNotifyStateStore _store;
    private readonly SemaphoreSlim _checkGate = new(1, 1);

    /// <summary>Which account <see cref="_notified"/> was loaded for, if any.</summary>
    private string? _loadedAccountKey;

    private HashSet<long>? _notified;

    /// <summary>The summary as of the last full fetch; null until one succeeds.</summary>
    private AnnouncementHead? _lastHead;

    /// <summary>
    /// Cleared when the relay has no head endpoint, after which every poll is a
    /// full fetch.
    /// </summary>
    /// <remarks>
    /// The endpoint was added after this client's first release, so a client can
    /// legitimately be newer than the relay it talks to. Treating that 404 as a
    /// failure would stop announcements reaching the user entirely, which is a
    /// far worse outcome than losing an optimisation.
    /// </remarks>
    private bool _headProbeSupported = true;

    public AnnouncementMonitor(
        IRelayServerClient relay,
        RelaySessionManager session,
        IAnnouncementNotifyStateStore store)
    {
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _store = store ?? throw new ArgumentNullException(nameof(store));
    }

    /// <summary>Reads the visible announcements and reports what is newly arrived.</summary>
    public async Task<AnnouncementObservation> CheckAsync(CancellationToken cancellationToken = default)
    {
        // Non-blocking: a poll that overlaps a manual refresh should be skipped,
        // not queued up behind it.
        if (!await _checkGate.WaitAsync(0, cancellationToken).ConfigureAwait(true))
        {
            return AnnouncementObservation.None;
        }

        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);

            // Cheap probe first: most polls find nothing new, and a body can carry
            // embedded base64 images, so pulling the list every time to discover
            // that is the expensive way to learn nothing.
            if (_lastHead is { } previous)
            {
                AnnouncementHead? head = await ProbeAsync(token, cancellationToken).ConfigureAwait(true);
                if (head == previous)
                {
                    return AnnouncementObservation.Unchanged;
                }
            }

            IReadOnlyList<RelayAnnouncement> announcements =
                await _relay.ListAnnouncementsAsync(token, cancellationToken).ConfigureAwait(true);

            string accountKey = AnnouncementNotifyStateStore.AccountKey(_session.UserEmail);
            LoadNotifiedSet(accountKey);

            long[] visibleIds = announcements.Select(item => item.Id).Distinct().ToArray();

            // Unread is the test, and the server owns it. There is deliberately no
            // "first run for this account" grace period: the read state already
            // encodes what this user has seen, on the web or anywhere else, so a
            // client-side baseline on top of it would swallow the first
            // announcement after every install and every client upgrade — which is
            // exactly the announcement most worth delivering.
            //
            // Every visible id is recorded, including the silent ones: the set means
            // "this client has already had the chance to surface it", not "a balloon
            // was shown". Tracking only the popup ones would fire a balloon for a
            // long-standing announcement the moment an operator flipped its mode.
            RelayAnnouncement[] arrived = announcements
                .Where(item => item.IsUnread && !_notified!.Contains(item.Id))
                .ToArray();

            // Pruned to what is visible now, so an announcement that expires and is
            // later re-activated is treated as new again — and the file stays bounded
            // by the number of live announcements rather than growing forever.
            _notified = [.. visibleIds];
            _store.Save(accountKey, visibleIds);

            RelayAnnouncement[] toAnnounce = arrived.Where(item => item.WantsPopup).ToArray();
            RelayAnnouncement? latest = toAnnounce
                .OrderByDescending(item => item.CreatedAt)
                .ThenByDescending(item => item.Id)
                .FirstOrDefault();

            int unread = announcements.Count(item => item.IsUnread);

            // Recorded from what actually arrived rather than from the probe, so a
            // change landing between the two calls is not skipped next time.
            _lastHead = new AnnouncementHead
            {
                MaxId = visibleIds.Length == 0 ? 0 : visibleIds.Max(),
                UnreadCount = unread,
                Total = announcements.Count,
            };

            return new AnnouncementObservation(
                Succeeded: true,
                Changed: true,
                Announcements: announcements,
                UnreadCount: unread,
                NewCount: toAnnounce.Length,
                LatestTitle: latest?.Title);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return AnnouncementObservation.None;
        }
        catch (Exception exception) when (IsBackgroundFailure(exception))
        {
            // A failed poll must leave the cards and the badge as they were rather
            // than blanking them; the caller distinguishes that by Succeeded.
            ClientLog.Warning("公告拉取失败", exception);
            return AnnouncementObservation.None;
        }
        finally
        {
            _checkGate.Release();
        }
    }

    /// <summary>Forgets the loaded account so the next check re-reads its record.</summary>
    /// <remarks>
    /// Only the in-memory copy is dropped. The stored record survives, so signing
    /// out and back in does not re-announce what has already been announced.
    /// </remarks>
    public void Reset()
    {
        _loadedAccountKey = null;
        _notified = null;

        // Counts are per account, so the next poll must fetch rather than compare
        // the new account's summary against the previous one's.
        _lastHead = null;
    }

    /// <summary>
    /// Records that one announcement was just marked read by this client.
    /// </summary>
    /// <remarks>
    /// Keeps the cached summary in step with the server so reading something does
    /// not make the next probe look like a change and force a pointless refetch.
    /// </remarks>
    public void NoteLocallyRead()
    {
        if (_lastHead is { } head && head.UnreadCount > 0)
        {
            _lastHead = head with { UnreadCount = head.UnreadCount - 1 };
        }
    }

    /// <summary>Reads the summary, or returns null when it cannot be used.</summary>
    private async Task<AnnouncementHead?> ProbeAsync(string token, CancellationToken cancellationToken)
    {
        if (!_headProbeSupported)
        {
            return null;
        }

        try
        {
            return await _relay.GetAnnouncementHeadAsync(token, cancellationToken).ConfigureAwait(true);
        }
        catch (RelayApiException exception) when (exception.Failure == RelayFailure.NotFound)
        {
            _headProbeSupported = false;
            ClientLog.Info("服务端没有公告摘要接口，改为每次拉取完整列表");
            return null;
        }
    }

    /// <summary>Loads the account's record if it is not already loaded.</summary>
    private void LoadNotifiedSet(string accountKey)
    {
        if (_loadedAccountKey == accountKey && _notified is not null)
        {
            return;
        }

        _loadedAccountKey = accountKey;
        _notified = _store.Load(accountKey) is { } stored ? [.. stored] : [];
    }

    private static bool IsBackgroundFailure(Exception exception) =>
        exception is not (OutOfMemoryException or StackOverflowException or ThreadAbortException);
}

/// <summary>The outcome of one announcement poll.</summary>
/// <param name="Succeeded">
/// False when the poll could not complete. Callers must leave what they are
/// showing untouched in that case: an empty list from a failed fetch is not the
/// same as an account with no announcements.
/// </param>
/// <param name="NewCount">
/// How many newly arrived announcements asked to interrupt the user. Zero does
/// not mean nothing arrived — silent announcements still reach the list and the
/// unread badge.
/// </param>
internal sealed record AnnouncementObservation(
    bool Succeeded,
    bool Changed,
    IReadOnlyList<RelayAnnouncement> Announcements,
    int UnreadCount,
    int NewCount,
    string? LatestTitle)
{
    public static AnnouncementObservation None { get; } = new(false, false, [], 0, 0, null);

    /// <summary>
    /// The poll completed and the summary matched, so nothing was fetched.
    /// </summary>
    /// <remarks>
    /// Distinct from <see cref="None"/>: both leave the caller's list alone, but
    /// only this one means the list is known to be current. It carries no
    /// announcements, so a caller that overwrote its state from it would wipe
    /// the list it just confirmed was correct.
    /// </remarks>
    public static AnnouncementObservation Unchanged { get; } = new(true, false, [], 0, 0, null);

    /// <summary>Whether a tray reminder is warranted for this poll.</summary>
    public bool ShouldNotify => Succeeded && Changed && NewCount > 0;
}
