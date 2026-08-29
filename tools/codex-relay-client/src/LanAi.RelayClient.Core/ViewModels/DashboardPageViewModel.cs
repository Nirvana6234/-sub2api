using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>What the signed-in screen binds to.</summary>
/// <remarks>
/// <para>
/// The counterpart of <see cref="SignInPageViewModel"/>, and for the same reason: the
/// WPF window reached three separate view models from one surface using
/// <c>ElementName=RootWindow</c> hops, which compiled bindings cannot verify. One root
/// object per screen means <c>x:DataType</c> is checked against what the constructor
/// actually receives.
/// </para>
/// <para>
/// <see cref="WelcomeText"/> is a property here rather than a line of code-behind
/// assigning <c>WelcomeText.Text</c>. It has to be refreshed on sign-in and cleared on
/// sign-out — as a bound property that is one call to <see cref="Refresh"/>; as a
/// direct control assignment it was a step that could be forgotten, leaving one
/// account's name above another account's figures.
/// </para>
/// </remarks>
public sealed partial class DashboardPageViewModel : ObservableObject
{
    private readonly RelaySessionManager _session;

    internal DashboardPageViewModel(
        DashboardViewModel dashboard,
        ClientUpdateViewModel clientUpdate,
        AnnouncementsViewModel announcements,
        RelaySessionManager session)
    {
        Dashboard = dashboard ?? throw new ArgumentNullException(nameof(dashboard));
        ClientUpdate = clientUpdate ?? throw new ArgumentNullException(nameof(clientUpdate));
        Announcements = announcements ?? throw new ArgumentNullException(nameof(announcements));
        _session = session ?? throw new ArgumentNullException(nameof(session));
    }

    public DashboardViewModel Dashboard { get; }

    public ClientUpdateViewModel ClientUpdate { get; }

    public AnnouncementsViewModel Announcements { get; }

    [ObservableProperty]
    private string welcomeText = string.Empty;

    /// <summary>Re-reads the signed-in identity. Call on sign-in and on sign-out.</summary>
    public void Refresh() =>
        WelcomeText = _session.IsSignedIn ? $"你好，{_session.UserDisplayName}" : string.Empty;
}
