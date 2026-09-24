using System.Net.Mail;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>State and validation for the in-client email registration form.</summary>
public sealed partial class RegistrationViewModel : ObservableObject
{
    private readonly RelaySessionManager _session;
    private readonly IRelayServerClient _client;
    private readonly IUiTimer _countdownTimer;
    private PublicSettings _settings = PublicSettings.Conservative;

    /// <param name="uiTimer">
    /// Supplies the one-second countdown tick. Required rather than defaulted: a
    /// no-op fallback would leave the "resend" button disabled forever with nothing
    /// to show for it, and the caller that forgot would never see an error.
    /// </param>
    internal RegistrationViewModel(
        RelaySessionManager session,
        IRelayServerClient client,
        UiTimerFactory uiTimer)
    {
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _client = client ?? throw new ArgumentNullException(nameof(client));
        ArgumentNullException.ThrowIfNull(uiTimer);
        _countdownTimer = uiTimer(TimeSpan.FromSeconds(1), OnCountdownTick);
    }

    /// <remarks>
    /// A method rather than a lambda in the constructor, so that the field it stops is
    /// definitely assigned by the time anything can call it.
    /// </remarks>
    private void OnCountdownTick()
    {
        if (VerifyCodeSecondsRemaining <= 1)
        {
            VerifyCodeSecondsRemaining = 0;
            _countdownTimer.Stop();
        }
        else
        {
            VerifyCodeSecondsRemaining--;
        }

        OnPropertyChanged(nameof(CanSendVerifyCode));
    }

    public PublicSettings Settings => _settings;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasError))]
    private string errorMessage = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSendVerifyCode))]
    private bool isBusy;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSendVerifyCode))]
    private bool isSendingCode;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSendVerifyCode))]
    [NotifyPropertyChangedFor(nameof(HasVerifyCodeCountdown))]
    [NotifyPropertyChangedFor(nameof(VerifyCodeCountdownText))]
    private int verifyCodeSecondsRemaining;

    [ObservableProperty]
    private bool turnstileBlocked;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSendVerifyCode))]
    private string email = string.Empty;

    [ObservableProperty]
    private string verifyCode = string.Empty;

    [ObservableProperty]
    private string invitationCode = string.Empty;

    [ObservableProperty]
    private string promoCode = string.Empty;

    public bool HasError => !string.IsNullOrWhiteSpace(ErrorMessage);

    public bool HasVerifyCodeCountdown => VerifyCodeSecondsRemaining > 0;

    /// <summary>The "wait n seconds" line shown under the verification code field.</summary>
    /// <remarks>
    /// Composed here rather than by a <c>StringFormat</c> in the view, which is how the
    /// WPF window did it. Two reasons: a format string carrying Chinese text and braces
    /// through XAML markup is a silent-blank hazard rather than a build error, and the
    /// text is worth a test. It follows the same convention as <c>BalanceText</c> and
    /// <c>TrayLabel</c> — the view model says what to display, the view only places it.
    /// </remarks>
    public string VerifyCodeCountdownText =>
        HasVerifyCodeCountdown ? $"请等待 {VerifyCodeSecondsRemaining} 秒后重试" : string.Empty;

    public bool ShowVerifyCode => _settings.EmailVerifyEnabled;

    public bool ShowInvitationCode => _settings.InvitationCodeEnabled;

    public bool ShowPromoCode => _settings.PromoCodeEnabled;

    public bool CanSendVerifyCode =>
        ShowVerifyCode &&
        !IsBusy &&
        !IsSendingCode &&
        VerifyCodeSecondsRemaining == 0 &&
        IsValidEmail(Email);

    public void ApplySettings(PublicSettings settings)
    {
        _settings = settings ?? PublicSettings.Conservative;
        OnPropertyChanged(nameof(Settings));
        OnPropertyChanged(nameof(ShowVerifyCode));
        OnPropertyChanged(nameof(ShowInvitationCode));
        OnPropertyChanged(nameof(ShowPromoCode));
        OnPropertyChanged(nameof(CanSendVerifyCode));
    }

    public async Task SendVerifyCodeAsync(
        CancellationToken cancellationToken = default,
        string? turnstileToken = null)
    {
        ErrorMessage = string.Empty;
        TurnstileBlocked = false;

        if (!ShowVerifyCode)
        {
            ErrorMessage = "当前服务器不要求邮箱验证码。";
            return;
        }

        if (!EnsureTurnstile(turnstileToken) || !ValidateEmail())
        {
            return;
        }

        if (!CanSendVerifyCode)
        {
            return;
        }

        IsSendingCode = true;
        try
        {
            VerifyCodeDispatch dispatch = await _client
                .SendVerifyCodeAsync(Email.Trim(), turnstileToken, cancellationToken)
                .ConfigureAwait(true);
            VerifyCodeSecondsRemaining = Math.Max(0, dispatch.CountdownSeconds);
            if (VerifyCodeSecondsRemaining > 0)
            {
                _countdownTimer.Start();
            }
        }
        catch (RelayApiException ex)
        {
            ErrorMessage = ex.UserMessage;
        }
        finally
        {
            IsSendingCode = false;
            OnPropertyChanged(nameof(CanSendVerifyCode));
        }
    }

    public async Task<bool> SubmitAsync(
        string password,
        string confirmPassword,
        string? turnstileToken = null,
        CancellationToken cancellationToken = default)
    {
        ErrorMessage = string.Empty;
        TurnstileBlocked = false;

        if (!_settings.RegistrationEnabled)
        {
            ErrorMessage = "当前服务器未开放注册。";
            return false;
        }

        if (!EnsureTurnstile(turnstileToken) ||
            !ValidateEmail() ||
            !ValidatePassword(password, confirmPassword))
        {
            return false;
        }

        if (_settings.EmailVerifyEnabled && string.IsNullOrWhiteSpace(VerifyCode))
        {
            ErrorMessage = "请输入邮箱验证码。";
            return false;
        }

        IsBusy = true;
        try
        {
            var request = new RegistrationRequest
            {
                Email = Email.Trim(),
                Password = password,
                VerifyCode = _settings.EmailVerifyEnabled ? VerifyCode.Trim() : null,
                InvitationCode = _settings.InvitationCodeEnabled ? Optional(InvitationCode) : null,
                PromoCode = _settings.PromoCodeEnabled ? Optional(PromoCode) : null,
                TurnstileToken = Optional(turnstileToken),
            };
            await _session.RegisterAsync(request, cancellationToken).ConfigureAwait(true);
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

    public void Reset()
    {
        _countdownTimer.Stop();
        Email = string.Empty;
        VerifyCode = string.Empty;
        InvitationCode = string.Empty;
        PromoCode = string.Empty;
        VerifyCodeSecondsRemaining = 0;
        ErrorMessage = string.Empty;
        TurnstileBlocked = false;
        IsBusy = false;
        IsSendingCode = false;
    }

    private bool EnsureTurnstile(string? token)
    {
        if (!_settings.TurnstileEnabled || !string.IsNullOrWhiteSpace(token))
        {
            return true;
        }

        TurnstileBlocked = true;
        ErrorMessage = "请使用网页版完成安全验证后再注册。";
        return false;
    }

    private bool ValidateEmail()
    {
        if (!IsValidEmail(Email))
        {
            ErrorMessage = "请输入有效的邮箱地址。";
            return false;
        }

        if (!_settings.IsEmailSuffixAllowed(Email))
        {
            ErrorMessage = "该邮箱后缀暂不支持注册。";
            return false;
        }

        return true;
    }

    private bool ValidatePassword(string password, string confirmPassword)
    {
        if (password.Length < 6)
        {
            ErrorMessage = "密码至少需要 6 位。";
            return false;
        }

        if (!string.Equals(password, confirmPassword, StringComparison.Ordinal))
        {
            ErrorMessage = "两次输入的密码不一致。";
            return false;
        }

        return true;
    }

    private static bool IsValidEmail(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        try
        {
            MailAddress parsed = new(value.Trim());
            return string.Equals(parsed.Address, value.Trim(), StringComparison.OrdinalIgnoreCase);
        }
        catch (FormatException)
        {
            return false;
        }
    }

    private static string? Optional(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim();
}
