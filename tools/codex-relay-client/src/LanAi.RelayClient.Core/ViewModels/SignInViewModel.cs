using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// The sign-in screen: the only way into the application.
/// </summary>
/// <remarks>
/// <para>
/// Must be public, as must every bound member. WPF cannot bind to internal
/// members, and a failed binding produces no value at all — for
/// <c>Visibility</c> that silently leaves an element at its default of visible,
/// which is how a modal card once ended up permanently pinned on screen.
/// </para>
/// <para>
/// The password is never held here. It is read from the PasswordBox at the
/// moment of submission and passed straight through, so it never lands in a
/// bindable property, a change notification, or a heap dump of this object.
/// </para>
/// </remarks>
public sealed partial class SignInViewModel : ObservableObject
{
    private const int SurfaceLoadAttempts = 3;

    private readonly RelaySessionManager _session;
    private readonly Func<CancellationToken, Task<PublicSettings>> _loadSettings;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;

    private string? _twoFactorTempToken;

    internal SignInViewModel(
        RelaySessionManager session,
        Func<CancellationToken, Task<PublicSettings>> loadSettings,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _loadSettings = loadSettings ?? throw new ArgumentNullException(nameof(loadSettings));
        _delay = delay ?? Task.Delay;
    }

    [ObservableProperty]
    private string email = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit), nameof(CanRetrySurface))]
    private bool isBusy;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string errorMessage = string.Empty;

    [ObservableProperty]
    private string totpCode = string.Empty;

    /// <summary>True once the server has asked for a second factor.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsPasswordStage))]
    private bool isTwoFactorStage;

    [ObservableProperty]
    private string maskedEmail = string.Empty;

    /// <summary>
    /// Whether the server offers registration at all.
    /// </summary>
    /// <remarks>
    /// Driven by <c>/settings/public</c>, never hard-coded: an operator who turns
    /// registration off must see the entry disappear, not have users click it and
    /// meet a rejection.
    /// </remarks>
    [ObservableProperty]
    private bool canRegister;

    [ObservableProperty]
    private bool canResetPassword;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanRetrySurface))]
    private bool hasSurfaceLoadFailure;

    [ObservableProperty]
    private string siteName = "共飞-ChatGPT助手";

    /// <summary>
    /// The settings this screen was built from, for reuse by the signed-in surface.
    /// </summary>
    /// <remarks>
    /// Shared rather than fetched twice: both screens need the same server-driven
    /// values, and a second call could disagree with the first if an operator
    /// changed a setting in between — leaving the two halves of one window
    /// configured differently.
    /// </remarks>
    public PublicSettings Settings { get; private set; } = PublicSettings.Conservative;

    public bool IsPasswordStage => !IsTwoFactorStage;

    public bool HasError => !string.IsNullOrWhiteSpace(ErrorMessage);

    public bool CanSubmit => !IsBusy;

    /// <summary>Whether a failed public-settings request can be retried by the user.</summary>
    public bool CanRetrySurface => HasSurfaceLoadFailure && !IsBusy;

    /// <summary>
    /// Loads the server-driven surface. Transient failures are retried before falling
    /// back to the most conservative form.
    /// </summary>
    public async Task LoadSurfaceAsync(CancellationToken cancellationToken = default)
    {
        ErrorMessage = string.Empty;
        HasSurfaceLoadFailure = false;
        IsBusy = true;

        PublicSettings settings = PublicSettings.Conservative;
        RelayApiException? lastFailure = null;
        try
        {
            for (int attempt = 1; attempt <= SurfaceLoadAttempts; attempt++)
            {
                try
                {
                    settings = await _loadSettings(cancellationToken).ConfigureAwait(true);
                    lastFailure = null;
                    break;
                }
                catch (RelayApiException ex) when (attempt < SurfaceLoadAttempts)
                {
                    lastFailure = ex;
                    await _delay(GetSurfaceRetryDelay(attempt), cancellationToken).ConfigureAwait(true);
                }
                catch (RelayApiException ex)
                {
                    lastFailure = ex;
                }
            }

            if (lastFailure is not null)
            {
                // Guessing which features exist would be worse than offering none:
                // showing a registration entry the server will reject teaches users
                // the client is unreliable.
                ErrorMessage = lastFailure.UserMessage;
                HasSurfaceLoadFailure = true;
            }

            Settings = settings;
            CanRegister = settings.RegistrationEnabled;
            CanResetPassword = settings.PasswordResetEnabled;
            if (!string.IsNullOrWhiteSpace(settings.SiteName))
            {
                SiteName = settings.SiteName!;
            }
        }
        finally
        {
            IsBusy = false;
        }
    }

    private static TimeSpan GetSurfaceRetryDelay(int failedAttempt) =>
        failedAttempt == 1 ? TimeSpan.FromMilliseconds(300) : TimeSpan.FromMilliseconds(900);

    /// <summary>
    /// Attempts sign-in. Returns true once the user is signed in.
    /// </summary>
    /// <param name="password">
    /// Read from the PasswordBox at call time and not retained by this instance.
    /// </param>
    public async Task<bool> SubmitAsync(string password, CancellationToken cancellationToken = default)
    {
        if (IsBusy)
        {
            return false;
        }

        ErrorMessage = string.Empty;
        IsBusy = true;
        try
        {
            if (IsTwoFactorStage)
            {
                await _session
                    .CompleteTwoFactorAsync(_twoFactorTempToken!, TotpCode.Trim(), cancellationToken)
                    .ConfigureAwait(true);
                return true;
            }

            LoginOutcome outcome = await _session
                .SignInAsync(Email.Trim(), password, cancellationToken)
                .ConfigureAwait(true);

            if (outcome.RequiresTwoFactor)
            {
                _twoFactorTempToken = outcome.TempToken;
                MaskedEmail = outcome.MaskedEmail ?? string.Empty;
                IsTwoFactorStage = true;
                return false;
            }

            return true;
        }
        catch (RelayApiException ex)
        {
            ErrorMessage = ex.UserMessage;
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>Returns to the password step, discarding the pending two-factor attempt.</summary>
    public void CancelTwoFactor()
    {
        _twoFactorTempToken = null;
        TotpCode = string.Empty;
        MaskedEmail = string.Empty;
        IsTwoFactorStage = false;
        ErrorMessage = string.Empty;
    }
}
