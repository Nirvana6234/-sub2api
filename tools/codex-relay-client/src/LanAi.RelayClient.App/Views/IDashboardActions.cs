using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>What a page's buttons can ask the signed-in surface to do.</summary>
/// <remarks>
/// Implemented by <see cref="DashboardView"/>, which owns the dialogs, the confirmation
/// callbacks and the events the composition root listens to. Pages only forward clicks,
/// so a button on two pages (启动 ChatGPT on the overview and on the Codex page) cannot
/// take two different routes — including the restart confirmation.
/// </remarks>
internal interface IDashboardActions
{
    void Navigate(ClientPage page);

    void StartOrInstallCodex();

    void RepairCodexStartup();

    void ConfigureAutoGroup();

    void ShowGroupModels(GroupItemViewModel group);

    void Recharge();

    void ShowAnnouncements();

    void CheckUpdate();

    void OpenContactPage();

    void ToggleLocalProxy(LocalProxyAccountItem item);

    /// <summary>Signs in to an official account within the client for a tool, or — with <paramref name="reauthorize"/> — again to that one.</summary>
    void StartLocalProxySignIn(LanAi.RelayClient.Server.LocalProxyKind kind, LocalProxyAccountItem? reauthorize);

    void RemoveLocalProxyAccount(LocalProxyAccountItem item);
}
