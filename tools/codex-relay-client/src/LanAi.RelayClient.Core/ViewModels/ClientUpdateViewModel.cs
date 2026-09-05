using System.Net.Http;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

public sealed partial class ClientUpdateViewModel : ObservableObject
{
    private readonly Func<CancellationToken, Task<ClientUpdateInfo?>> _checkForUpdate;

    public ClientUpdateViewModel(Func<CancellationToken, Task<ClientUpdateInfo?>> checkForUpdate)
    {
        _checkForUpdate = checkForUpdate ?? throw new ArgumentNullException(nameof(checkForUpdate));
    }

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

    public async Task CheckAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            Update = await _checkForUpdate(cancellationToken).ConfigureAwait(true);
        }
        catch (Exception ex) when (ex is HttpRequestException or OperationCanceledException)
        {
            Update = null;
        }
    }
}
