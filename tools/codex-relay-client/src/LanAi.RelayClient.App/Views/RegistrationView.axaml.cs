using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>The in-client email registration form.</summary>
/// <remarks>
/// <para>
/// A <see cref="UserControl"/> for the same reason <see cref="SignInView"/> is one: the
/// <c>x:DataType</c> the compiled bindings are checked against has to be the type that
/// is actually assigned at runtime. A compiled binding trusts its annotation and does
/// not verify the data context, so a mismatch renders a blank form with no error.
/// </para>
/// <para>
/// There is no success handler here. <c>RelaySessionManager.RegisterAsync</c> adopts the
/// returned tokens and raises <c>StateChanged</c>, which is what the shell listens to
/// when deciding which surface to show — so a completed registration lands on the
/// dashboard by the same route a sign-in does. Switching surfaces from this class as
/// well would give that transition two owners.
/// </para>
/// </remarks>
public partial class RegistrationView : UserControl
{
    private readonly RegistrationViewModel? _registration;
    private readonly SafeAsyncRunner? _safeAsync;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public RegistrationView()
    {
        InitializeComponent();
    }

    internal RegistrationView(RegistrationViewModel registration, SafeAsyncRunner safeAsync)
    {
        _registration = registration ?? throw new ArgumentNullException(nameof(registration));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));

        InitializeComponent();
        DataContext = registration;
    }

    /// <summary>Raised when the user asks to go back to the sign-in form.</summary>
    public event EventHandler? BackToSignInRequested;

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Clears the form and focuses the first field.</summary>
    /// <remarks>
    /// Called every time the form is shown, not once at construction. The view is
    /// reused across visits, so without this a user who backed out after a failed
    /// attempt would return to their own half-filled form and its error message.
    /// </remarks>
    internal void Prepare()
    {
        _registration?.Reset();
        ClearPasswords();
        this.FindControl<TextBox>("EmailInput")?.Focus();
    }

    private void ClearPasswords()
    {
        TextBox? password = this.FindControl<TextBox>("PasswordInput");
        TextBox? confirm = this.FindControl<TextBox>("ConfirmPasswordInput");

        if (password is not null)
        {
            password.Text = string.Empty;
        }

        if (confirm is not null)
        {
            confirm.Text = string.Empty;
        }
    }

    /// <remarks>
    /// Enter from the confirm-password field submits, so the form can be finished
    /// without reaching for the mouse — matching the sign-in form's behaviour.
    /// </remarks>
    private void Field_OnKeyDown(object? sender, KeyEventArgs e)
    {
        if (e.Key == Key.Enter)
        {
            _ = _safeAsync?.RunAsync(SubmitAsync);
        }
    }

    private void SendVerifyCode_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(() => _registration!.SendVerifyCodeAsync());

    private void SubmitRegistration_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(SubmitAsync);

    private async Task SubmitAsync()
    {
        if (_registration is null)
        {
            return;
        }

        // Read at the moment of use and passed straight through, never stored on the
        // view model — which is also why neither control's Text has a binding on it.
        string password = this.FindControl<TextBox>("PasswordInput")?.Text ?? string.Empty;
        string confirm = this.FindControl<TextBox>("ConfirmPasswordInput")?.Text ?? string.Empty;

        if (await _registration.SubmitAsync(password, confirm).ConfigureAwait(true))
        {
            ClearPasswords();
        }
    }

    private void OpenRegistrationInBrowser_OnClick(object? sender, RoutedEventArgs e) =>
        BrowserLauncher.TryOpenRelayPage("register");

    /// <remarks>
    /// Clears on the way out as well as on the way in. <see cref="Prepare"/> would
    /// catch it at the next visit, but that leaves a typed password sitting in a live
    /// control for as long as the client runs — and if there is no next visit, forever.
    /// </remarks>
    private void BackToLogin_OnClick(object? sender, RoutedEventArgs e)
    {
        _registration?.Reset();
        ClearPasswords();
        BackToSignInRequested?.Invoke(this, EventArgs.Empty);
    }
}
