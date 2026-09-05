using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Wpf.Services;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// The sign-in card raised from the sidebar identity badge.
/// </summary>
/// <remarks>
/// This is an in-window overlay rather than a separate <see cref="System.Windows.Window"/>
/// because the workspace has no standalone dialog anywhere else; the relay switch
/// prompt uses the same shape. Passwords are never stored on the view model — the
/// code-behind hands the <c>PasswordBox</c> content straight to
/// <see cref="SubmitAsync"/> and clears the box afterwards.
/// </remarks>
// Must be public: WPF data binding cannot reach internal members, and a failed
// binding silently leaves Visibility at its default (Visible), which would pin
// the card on screen permanently.
public sealed partial class SignInPromptViewModel : ObservableObject
{
    private readonly ISub2ApiSessionManager _sessionManager;
    private readonly Func<Uri?> _resolveApiBaseUri;

    internal SignInPromptViewModel(
        ISub2ApiSessionManager sessionManager,
        Func<Uri?> resolveApiBaseUri)
    {
        _sessionManager = sessionManager ?? throw new ArgumentNullException(nameof(sessionManager));
        _resolveApiBaseUri = resolveApiBaseUri ?? throw new ArgumentNullException(nameof(resolveApiBaseUri));
    }

    [ObservableProperty]
    private bool isVisible;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    private bool isBusy;

    [ObservableProperty]
    private string email = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string? errorMessage;

    /// <summary>The workspace only registers a boolean-to-visibility converter,
    /// so the view binds these derived flags instead of null/inverse converters.</summary>
    public bool HasError => !string.IsNullOrWhiteSpace(ErrorMessage);

    public bool CanSubmit => !IsBusy;

    /// <summary>Opens the card with a cleared form.</summary>
    public void Show()
    {
        Email = string.Empty;
        ErrorMessage = null;
        IsBusy = false;
        IsVisible = true;
    }

    [RelayCommand]
    private void Cancel()
    {
        Email = string.Empty;
        ErrorMessage = null;
        IsVisible = false;
    }

    /// <summary>
    /// Signs in with the supplied password. Returns true when the card closed
    /// because the session became authenticated.
    /// </summary>
    public async Task<bool> SubmitAsync(string password, CancellationToken cancellationToken)
    {
        if (IsBusy)
        {
            return false;
        }

        string account = Email.Trim();
        if (account.Length == 0 || string.IsNullOrEmpty(password))
        {
            ErrorMessage = "请输入账号和密码。";
            return false;
        }

        Uri? apiBaseUri = _resolveApiBaseUri();
        if (apiBaseUri is null)
        {
            ErrorMessage = "当前来源没有可用的后台地址，请先在连接中心配置。";
            return false;
        }

        IsBusy = true;
        ErrorMessage = null;
        try
        {
            await _sessionManager
                .LoginAsync(apiBaseUri, account, password, cancellationToken)
                .ConfigureAwait(true);
            Email = string.Empty;
            IsVisible = false;
            return true;
        }
        catch (Sub2ApiSessionException exception)
        {
            ErrorMessage = Describe(exception.Failure);
            return false;
        }
        catch (OperationCanceledException)
        {
            ErrorMessage = "登录已取消。";
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }

    internal static string Describe(Sub2ApiSessionFailure failure) => failure switch
    {
        Sub2ApiSessionFailure.InvalidCredentials => "账号或密码不正确。",
        Sub2ApiSessionFailure.RequiresTwoFactor => "该账号启用了两步验证，请在网页后台登录。",
        Sub2ApiSessionFailure.Forbidden => "该账号没有访问权限。",
        Sub2ApiSessionFailure.GatewayUnavailable => "连接不上后台，请检查中转服务是否已启动。",
        Sub2ApiSessionFailure.SecureStorageUnavailable => "无法安全保存登录状态，请稍后重试。",
        Sub2ApiSessionFailure.ProtocolMismatch => "后台返回的数据无法识别，请确认版本是否匹配。",
        _ => "登录失败，请稍后重试。",
    };
}
