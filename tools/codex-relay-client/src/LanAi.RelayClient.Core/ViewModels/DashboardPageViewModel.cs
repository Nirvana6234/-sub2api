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

        Dashboard.PropertyChanged += (_, args) =>
        {
            switch (args.PropertyName)
            {
                case nameof(DashboardViewModel.CurrentGroupName):
                case nameof(DashboardViewModel.CurrentGroupRate):
                    OnPropertyChanged(nameof(CodexRouteText));
                    break;
                case nameof(DashboardViewModel.RequiresCodexAccountRestart):
                case nameof(DashboardViewModel.IsCodexRunning):
                case nameof(DashboardViewModel.CodexNotInstalled):
                case nameof(DashboardViewModel.IsInstallingCodex):
                    UpdateCodexStatus();
                    break;
            }
        };
        Dashboard.LocalProxy.PropertyChanged += (_, args) =>
        {
            switch (args.PropertyName)
            {
                case nameof(LocalProxyViewModel.CodexTarget):
                    OnPropertyChanged(nameof(CodexStatusText));
                    OnPropertyChanged(nameof(CodexRouteText));
                    break;
                case nameof(LocalProxyViewModel.ClaudeTarget):
                    OnPropertyChanged(nameof(ClaudeStatusText));
                    break;
                case nameof(LocalProxyViewModel.HasError):
                    Navigation.Item(ClientPage.LocalProxy).HasBadge = Dashboard.LocalProxy.HasError;
                    break;
            }
        };
        Dashboard.ClaudeCode.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName is nameof(ClaudeCodeViewModel.PluginSupportEnabled)
                or nameof(ClaudeCodeViewModel.PluginSupportActive)
                or nameof(ClaudeCodeViewModel.PluginSupportStatus))
            {
                OnPropertyChanged(nameof(ClaudeStatusText));
                UpdateClaudeBadge();
            }
        };
        Dashboard.Account.PropertyChanged += (_, args) =>
        {
            if (args.PropertyName == nameof(AccountCardViewModel.BalanceIsLow))
            {
                Navigation.Item(ClientPage.Account).HasBadge = Dashboard.Account.BalanceIsLow;
            }
        };
        UpdateCodexStatus();
    }

    public DashboardViewModel Dashboard { get; }

    /// <summary>Which page of the signed-in surface is showing.</summary>
    public NavigationViewModel Navigation { get; } = new();

    /// <summary>
    /// Where Codex's turns go, for the overview card and the tray: the group while they go
    /// through the relay server, the account while it is on a local proxy.
    /// </summary>
    public string CodexRouteText => Dashboard.LocalProxy.CodexTarget is { } t
        ? $"本地代理：{t.Name}"
        : $"{Dashboard.CurrentGroupName} {Dashboard.CurrentGroupRate}".Trim();

    /// <summary>One line on the overview's Codex card.</summary>
    public string CodexStatusText =>
        Dashboard.RequiresCodexAccountRestart ? "需要重启 ChatGPT"
        : Dashboard.IsInstallingCodex ? "正在安装"
        : Dashboard.CodexNotInstalled ? "未安装"
        : Dashboard.IsCodexRunning ? "运行中"
        : "未启动";

    /// <summary>One line on the overview's Claude Code card.</summary>
    /// <remarks>
    /// Short on purpose: the card is a third of the width. Anything other than 已接入 is
    /// explained in full on the Claude page, where the card's button leads.
    /// </remarks>
    public string ClaudeStatusText =>
        !Dashboard.ClaudeCode.PluginSupportEnabled ? "未开启"
        : Dashboard.ClaudeCode.PluginSupportActive && Dashboard.LocalProxy.ClaudeTarget is { } t ? $"已接入 · 本地代理：{t.Name}"
        : Dashboard.ClaudeCode.PluginSupportActive ? "已接入"
        : "待完成设置";

    /// <summary>
    /// Marks the Claude page when the user switched Claude Code on and the last sync
    /// answered with something other than 已接入 — a group still to choose, or a
    /// problem. Keyed on the sync having answered at all, so the dot does not flash
    /// during the moment between launch and the first sync.
    /// </summary>
    private void UpdateClaudeBadge()
    {
        ClaudeCodeViewModel claude = Dashboard.ClaudeCode;
        Navigation.Item(ClientPage.Claude).HasBadge =
            claude.PluginSupportEnabled && claude.HasPluginSupportStatus && !claude.PluginSupportActive;
    }

    private void UpdateCodexStatus()
    {
        OnPropertyChanged(nameof(CodexStatusText));
        Navigation.Item(ClientPage.Codex).HasBadge = Dashboard.RequiresCodexAccountRestart;
    }

    public ClientUpdateViewModel ClientUpdate { get; }

    public AnnouncementsViewModel Announcements { get; }

    [ObservableProperty]
    private string welcomeText = string.Empty;

    /// <summary>Re-reads the signed-in identity. Call on sign-in and on sign-out.</summary>
    /// <remarks>
    /// Also returns the rail to the overview, so the next account to sign in starts on
    /// the same page every account does rather than wherever the last one left off.
    /// </remarks>
    public void Refresh()
    {
        WelcomeText = _session.IsSignedIn ? $"你好，{_session.UserDisplayName}" : string.Empty;
        if (!_session.IsSignedIn)
        {
            Navigation.Navigate(ClientPage.Overview);
        }
    }
}
