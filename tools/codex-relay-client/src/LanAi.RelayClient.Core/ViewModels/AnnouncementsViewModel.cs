using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// The one source of truth for announcements across every entry point.
/// </summary>
/// <remarks>
/// The title-bar bell, the tray menu item and the reader window all bind to this
/// same instance. Giving each its own copy is what would let the badge, the menu
/// label and the list disagree about how much is unread — and the badge is the
/// authoritative signal, because a tray balloon is silently suppressed whenever
/// Windows focus assist or the user's notification settings say so.
/// </remarks>
public sealed partial class AnnouncementsViewModel : ObservableObject
{
    private readonly AnnouncementMonitor _monitor;
    private readonly IRelayServerClient _relay;
    private readonly RelaySessionManager _session;

    internal AnnouncementsViewModel(
        AnnouncementMonitor monitor,
        IRelayServerClient relay,
        RelaySessionManager session)
    {
        _monitor = monitor ?? throw new ArgumentNullException(nameof(monitor));
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _session = session ?? throw new ArgumentNullException(nameof(session));
    }

    /// <summary>Raised when a poll finds announcements the user has not been told about.</summary>
    /// <remarks>
    /// An event rather than a direct tray call so the view model stays free of the
    /// notification area, which the test project cannot construct.
    /// </remarks>
    internal event EventHandler<AnnouncementArrival>? Arrived;

    public ObservableCollection<AnnouncementItemViewModel> Items { get; } = [];

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasSelection))]
    private AnnouncementItemViewModel? selected;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasUnread))]
    [NotifyPropertyChangedFor(nameof(BellLabel))]
    [NotifyPropertyChangedFor(nameof(TrayLabel))]
    private int unreadCount;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsEmpty))]
    private bool hasLoaded;

    public bool HasSelection => Selected is not null;

    public bool HasUnread => UnreadCount > 0;

    public bool IsEmpty => HasLoaded && Items.Count == 0;

    public string BellLabel => UnreadCount > 0 ? $"公告 {UnreadCount}" : "公告";

    public string TrayLabel => UnreadCount > 0 ? $"公告（{UnreadCount} 条未读）" : "公告";

    /// <summary>Re-reads the list and reports any arrival to the tray.</summary>
    public async Task RefreshAsync(CancellationToken cancellationToken = default)
    {
        AnnouncementObservation observation = await _monitor.CheckAsync(cancellationToken).ConfigureAwait(true);
        if (!observation.Succeeded || !observation.Changed)
        {
            // Two different reasons to leave the list exactly as it is: the poll
            // failed, or the summary matched and nothing was fetched. Blanking it
            // for the first would claim the announcements were withdrawn when the
            // network merely blinked; for the second it would discard a list just
            // confirmed to be current, including read marks made since.
            return;
        }

        Apply(observation.Announcements);
        HasLoaded = true;

        if (observation.ShouldNotify)
        {
            Arrived?.Invoke(this, new AnnouncementArrival(observation.NewCount, observation.LatestTitle));
        }
    }

    /// <summary>Marks one announcement read, updating the badge before the next poll.</summary>
    public async Task MarkReadAsync(AnnouncementItemViewModel? item, CancellationToken cancellationToken = default)
    {
        if (item is null || !item.IsUnread)
        {
            return;
        }

        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            await _relay.MarkAnnouncementReadAsync(token, item.Id, cancellationToken).ConfigureAwait(true);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return;
        }
        catch (Exception exception) when (exception is not (OutOfMemoryException or StackOverflowException))
        {
            // The user has read it either way; the badge is corrected by the next
            // poll if the server never recorded it.
            ClientLog.Warning($"标记公告已读失败：{item.Id}", exception);
            return;
        }

        item.IsUnread = false;
        RecountUnread();

        // Told to the monitor so its cached summary still matches the server's;
        // otherwise the next probe reads this as a change and refetches the list
        // every time the user opens an announcement.
        _monitor.NoteLocallyRead();
    }

    /// <summary>Drops the previous account's announcements.</summary>
    public void Reset()
    {
        Items.Clear();
        Selected = null;
        UnreadCount = 0;
        HasLoaded = false;
        _monitor.Reset();
    }

    /// <summary>
    /// Rebuilds the list in place, keeping the open announcement open.
    /// </summary>
    /// <remarks>
    /// Rebuilt rather than merged because the server decides both membership and
    /// order (unread first, newest first), and reproducing that ordering here
    /// would be a second copy of a rule that already exists server-side. The
    /// selection is restored by id so a poll landing while the user is reading
    /// does not throw them back to the top of the list.
    /// </remarks>
    private void Apply(IReadOnlyList<RelayAnnouncement> announcements)
    {
        long? selectedId = Selected?.Id;

        Items.Clear();
        foreach (RelayAnnouncement announcement in announcements)
        {
            Items.Add(new AnnouncementItemViewModel(announcement));
        }

        Selected = selectedId is long id
            ? Items.FirstOrDefault(item => item.Id == id)
            : null;

        RecountUnread();
        OnPropertyChanged(nameof(IsEmpty));
    }

    private void RecountUnread() => UnreadCount = Items.Count(item => item.IsUnread);
}

/// <summary>What one poll found worth interrupting the user for.</summary>
internal sealed record AnnouncementArrival(int Count, string? LatestTitle);
