using CommunityToolkit.Mvvm.ComponentModel;

namespace LanAi.Workspace.Wpf.ViewModels;

public sealed partial class TransitCenterViewModel : PageViewModel
{
    public TransitCenterViewModel(
        ConnectionsViewModel externalSources,
        AccountCenterViewModel personalAccounts)
        : base("中转中心", "统一管理个人账号和外部来源")
    {
        ExternalSources = externalSources ?? throw new ArgumentNullException(nameof(externalSources));
        PersonalAccounts = personalAccounts ?? throw new ArgumentNullException(nameof(personalAccounts));
    }

    public ConnectionsViewModel ExternalSources { get; }

    public AccountCenterViewModel PersonalAccounts { get; }

    [ObservableProperty]
    private int selectedSectionIndex;
}
