using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Wpf.Services;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// Read-only view of the signed-in account, reached from the sidebar identity badge.
/// </summary>
/// <remarks>
/// Everything shown here comes from the login token already held by
/// <see cref="ISub2ApiSessionManager"/>; that token is the identity credential and is
/// unrelated to the relay API keys used to forward AI traffic.
/// </remarks>
public sealed partial class ProfileViewModel : PageViewModel
{
    private readonly ISub2ApiSessionManager _sessionManager;

    internal ProfileViewModel(ISub2ApiSessionManager sessionManager)
        : base("个人信息", "查看当前登录的账号信息。")
    {
        _sessionManager = sessionManager ?? throw new ArgumentNullException(nameof(sessionManager));
        _sessionManager.SessionChanged += (_, _) => Refresh();
    }

    public bool IsSignedIn => _sessionManager.Current.IsAuthenticated;

    public bool IsLocalControl => _sessionManager.Current.IsLocalControl;

    public string Username
    {
        get
        {
            Sub2ApiSessionState session = _sessionManager.Current;
            if (!session.IsAuthenticated)
            {
                return "未登录";
            }

            return string.IsNullOrWhiteSpace(session.Username) ? "—" : session.Username;
        }
    }

    public string Email
        => string.IsNullOrWhiteSpace(_sessionManager.Current.Email)
            ? "—"
            : _sessionManager.Current.Email;

    public string RoleLabel => _sessionManager.Current.RoleLabel;

    public string BalanceText => $"{_sessionManager.Current.Balance:0.##}";

    public string FrozenBalanceText => $"{_sessionManager.Current.FrozenBalance:0.##}";

    public string SourceText => _sessionManager.Current.ApiBaseUri?.AbsoluteUri ?? "—";

    /// <summary>
    /// Explains where the identity came from. A machine-local control session is
    /// not a cloud account, and saying so avoids the user assuming otherwise.
    /// </summary>
    public string SessionKindText
        => _sessionManager.Current switch
        {
            { IsAuthenticated: false } => "当前未登录。",
            { IsLocalControl: true } => "本机管理员会话，由本机控制令牌自动建立，未使用云端账号密码。",
            _ => "已使用账号密码登录，登录令牌仅用于查看账号信息，与中转授权 Key 无关。",
        };

    [RelayCommand]
    private async Task SignOutAsync()
    {
        try
        {
            await _sessionManager.LogoutAsync(CancellationToken.None).ConfigureAwait(true);
        }
        catch (Sub2ApiSessionException)
        {
            // Local sign-out is authoritative even when the gateway rejects the call.
        }

        Refresh();
    }

    private void Refresh()
    {
        OnPropertyChanged(nameof(IsSignedIn));
        OnPropertyChanged(nameof(IsLocalControl));
        OnPropertyChanged(nameof(Username));
        OnPropertyChanged(nameof(Email));
        OnPropertyChanged(nameof(RoleLabel));
        OnPropertyChanged(nameof(BalanceText));
        OnPropertyChanged(nameof(FrozenBalanceText));
        OnPropertyChanged(nameof(SourceText));
        OnPropertyChanged(nameof(SessionKindText));
    }
}
