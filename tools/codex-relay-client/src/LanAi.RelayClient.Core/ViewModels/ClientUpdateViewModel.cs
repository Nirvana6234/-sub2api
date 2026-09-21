using System.Net.Http;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

public sealed partial class ClientUpdateViewModel : ObservableObject
{
    private readonly Func<CancellationToken, Task<ClientCheckResult>> _checkForUpdate;
    private readonly Func<ClientUpdateInfo, CancellationToken, Task<ClientSelfUpdateResult>>? _applyUpdate;

    /// <param name="applyUpdate">
    /// Null keeps this view model usable for the passive banner alone — the sign-in surface
    /// constructs one of these before the rest of the app exists to supply an updater.
    /// <see cref="CheckAndOfferUpdateAsync"/> falls back to only informing the user when this
    /// is absent, rather than offering a confirm dialog that would do nothing.
    /// </param>
    public ClientUpdateViewModel(
        Func<CancellationToken, Task<ClientCheckResult>> checkForUpdate,
        Func<ClientUpdateInfo, CancellationToken, Task<ClientSelfUpdateResult>>? applyUpdate = null)
    {
        _checkForUpdate = checkForUpdate ?? throw new ArgumentNullException(nameof(checkForUpdate));
        _applyUpdate = applyUpdate;
    }

    /// <summary>Asks 是否更新, yes/no. Provided by the host, which owns the dialog APIs.</summary>
    public Func<string, Task<bool>>? ConfirmUpdate { get; set; }

    /// <summary>Shows a message with only an acknowledgement — the outcome of a check or an apply.</summary>
    public Func<string, Task>? ShowMessage { get; set; }

    /// <summary>
    /// Releases this session's own state (the managed key, the plug-ins' configuration, the
    /// managed relay) and ends the process — the same teardown 退出 already performs. Called
    /// only once a Windows update has staged itself and is waiting for this process to exit; a
    /// mac or fallback outcome never touches this, since nothing here needs the process gone.
    /// </summary>
    public Func<Task>? RestartForUpdate { get; set; }

    /// <summary>The version shown under the sign-in title.</summary>
    /// <remarks>
    /// Derived, not written out. It was the literal "Ver0.1", which meant bumping
    /// <see cref="ClientOptions.CurrentVersion"/> left the number on screen showing the
    /// previous release — a client that reports one version to the update check and a
    /// different one to the user, with nothing to reveal the disagreement.
    /// </remarks>
    public string CurrentVersionText =>
        $"Ver{ClientOptions.CurrentVersion.Major}.{ClientOptions.CurrentVersion.Minor}";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasUpdate))]
    [NotifyPropertyChangedFor(nameof(UpdateMessage))]
    [NotifyPropertyChangedFor(nameof(DownloadPage))]
    private ClientUpdateInfo? update;

    public bool HasUpdate => Update is not null;

    public string UpdateMessage => Update is null
        ? string.Empty
        : $"发现新版本 {Update.VersionLabel}，点击更新";

    public Uri? DownloadPage => Update?.DownloadPage;

    /// <summary>
    /// The passive check behind the sign-in banner. Silent on every outcome, as before —
    /// this is not something the user asked for, so a network hiccup must not interrupt them.
    /// </summary>
    public async Task CheckAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            ClientCheckResult result = await _checkForUpdate(cancellationToken).ConfigureAwait(true);
            Update = result.Status == ClientCheckStatus.Available ? result.Update : null;
        }
        catch (Exception ex) when (ex is HttpRequestException or OperationCanceledException)
        {
            Update = null;
        }
    }

    /// <summary>
    /// The 检查更新 button's own flow. Unlike <see cref="CheckAsync"/>, every outcome gets a
    /// word — a check the user just asked for must not answer a network failure with silence,
    /// the one thing that let a broken update channel go unnoticed in production before.
    /// </summary>
    public async Task CheckAndOfferUpdateAsync(CancellationToken cancellationToken = default)
    {
        ClientCheckResult result;
        try
        {
            result = await _checkForUpdate(cancellationToken).ConfigureAwait(true);
        }
        catch (Exception ex) when (ex is HttpRequestException or OperationCanceledException)
        {
            result = new ClientCheckResult(ClientCheckStatus.CheckFailed);
        }

        Update = result.Status == ClientCheckStatus.Available ? result.Update : null;

        switch (result.Status)
        {
            case ClientCheckStatus.CheckFailed:
                await InformAsync("检查更新失败，请稍后重试。").ConfigureAwait(true);
                break;

            // Both read the same to the user: there is nothing they can do about a switch the
            // operator turned off, and offering to explain the difference is not worth a
            // second message for what is, from here, one outcome — "nothing to install".
            case ClientCheckStatus.ChannelDisabled:
            case ClientCheckStatus.UpToDate:
                await InformAsync("当前已经是最新版本。").ConfigureAwait(true);
                break;

            case ClientCheckStatus.Available:
                await OfferAsync(result.Update!, cancellationToken).ConfigureAwait(true);
                break;
        }
    }

    private async Task OfferAsync(ClientUpdateInfo update, CancellationToken cancellationToken)
    {
        if (_applyUpdate is null)
        {
            // No updater wired up for this instance. Confirming would lead nowhere, so this
            // says so instead of asking a question that does nothing on "yes".
            await InformAsync($"发现新版本 {update.VersionLabel}，请前往下载页面手动更新。").ConfigureAwait(true);
            return;
        }

        bool confirmed = ConfirmUpdate is not null &&
            await ConfirmUpdate($"发现新版本 {update.VersionLabel}，是否现在更新？").ConfigureAwait(true);
        if (!confirmed)
        {
            return;
        }

        ClientSelfUpdateResult result = await _applyUpdate(update, cancellationToken).ConfigureAwait(true);
        switch (result.Outcome)
        {
            case ClientSelfUpdateOutcome.Restarting when RestartForUpdate is not null:
                await RestartForUpdate().ConfigureAwait(true);
                break;
            case ClientSelfUpdateOutcome.OpenedTerminal:
                await InformAsync("已打开终端，请按提示完成安装。").ConfigureAwait(true);
                break;
            case ClientSelfUpdateOutcome.OpenedDownloadPage:
                await InformAsync("已打开下载页面，请手动下载安装。").ConfigureAwait(true);
                break;
            default:
                await InformAsync(result.Note ?? "更新失败，请稍后重试。").ConfigureAwait(true);
                break;
        }
    }

    private Task InformAsync(string message) =>
        ShowMessage is not null ? ShowMessage(message) : Task.CompletedTask;
}
