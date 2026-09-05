using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.ViewModels;

/// <summary>One row in the announcement list.</summary>
public sealed partial class AnnouncementItemViewModel : ObservableObject
{
    internal AnnouncementItemViewModel(RelayAnnouncement announcement)
    {
        Announcement = announcement ?? throw new ArgumentNullException(nameof(announcement));
        isUnread = announcement.IsUnread;
    }

    internal RelayAnnouncement Announcement { get; }

    public long Id => Announcement.Id;

    public string Title => string.IsNullOrWhiteSpace(Announcement.Title) ? "(无标题)" : Announcement.Title;

    public string Content => Announcement.Content;

    /// <remarks>
    /// Shown in the machine's local time. The server sends an absolute instant, so
    /// unlike the billing windows on the dashboard there is no server timezone to
    /// reconcile — an announcement is not evaluated against an operator clock.
    /// </remarks>
    public string PublishedText => Announcement.CreatedAt.ToLocalTime().ToString("yyyy-MM-dd HH:mm");

    /// <summary>
    /// Whether this row still counts towards the unread badge.
    /// </summary>
    /// <remarks>
    /// Held separately from <see cref="RelayAnnouncement.ReadAt"/> so marking one
    /// read updates the list and the badge immediately, without waiting for the
    /// next poll to confirm what the server already accepted.
    /// </remarks>
    [ObservableProperty]
    private bool isUnread;
}
