using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>The sign-in screen.</summary>
/// <remarks>
/// <para>
/// A <see cref="UserControl"/> rather than a panel inside the shell window, so that
/// the <c>x:DataType</c> the compiled bindings are checked against is the same type
/// the constructor takes. A compiled binding trusts its annotation and does not
/// verify the data context assigned at runtime; when the two disagree the bindings
/// resolve to nothing and the screen renders blank without an error anywhere.
/// </para>
/// <para>
/// Click handlers rather than commands, matching the WPF original. They exist because
/// the password is read from a control at the moment of submission — see
/// <see cref="SubmitAsync"/> — which a command binding cannot do without first putting
/// the password somewhere bindable.
/// </para>
/// </remarks>
public partial class SignInView : UserControl
{
    private readonly SignInPageViewModel? _page;
    private readonly SafeAsyncRunner? _safeAsync;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public SignInView()
    {
        InitializeComponent();
    }

    internal SignInView(SignInPageViewModel page, SafeAsyncRunner safeAsync)
    {
        _page = page ?? throw new ArgumentNullException(nameof(page));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));

        InitializeComponent();
        DataContext = page;
    }

    /// <summary>Raised once the user is signed in.</summary>
    public event EventHandler? SignedIn;

    /// <summary>Raised when the user asks for the registration form.</summary>
    public event EventHandler? RegistrationRequested;

    /// <summary>
    /// Raised whenever the server-driven public settings have been reloaded.
    /// </summary>
    /// <remarks>
    /// Every consumer of those settings has to be told, and there is more than one:
    /// the dashboard reads the low-balance threshold and server timezone from them,
    /// and the registration form reads which of its optional fields exist. Raising an
    /// event rather than having the composition root call both after the initial load
    /// is what makes 重新获取配置 work — that path reloads the surface, and before this
    /// nothing re-applied the result, so a retry updated the sign-in form and left
    /// everything downstream on the values it had failed with.
    /// </remarks>
    public event EventHandler? SurfaceLoaded;

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    /// <summary>Loads the server-driven surface and focuses the first empty field.</summary>
    internal async Task LoadAsync()
    {
        if (_page is null)
        {
            return;
        }

        await _page.SignIn.LoadSurfaceAsync().ConfigureAwait(true);
        SurfaceLoaded?.Invoke(this, EventArgs.Empty);
        await _page.ClientUpdate.CheckAsync().ConfigureAwait(true);

        this.FindControl<TextBox>("PasswordInput")?.Focus();
    }

    /// <summary>Clears the password field and puts the cursor back on the email box.</summary>
    /// <remarks>
    /// Needed because this view is a single long-lived instance that the shell swaps
    /// in and out, not a fresh one per visit: whatever was typed before leaving for the
    /// registration form is still in the control when the user comes back.
    /// </remarks>
    internal void ResetEntry()
    {
        TextBox? password = this.FindControl<TextBox>("PasswordInput");
        if (password is not null)
        {
            password.Text = string.Empty;
        }

        this.FindControl<TextBox>("EmailInput")?.Focus();
    }

    private void Submit_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(SubmitAsync);

    /// <remarks>
    /// Enter submits from either the password field or the two-factor field, so the
    /// form can be completed without reaching for the mouse.
    /// </remarks>
    private void Password_OnKeyDown(object? sender, KeyEventArgs e)
    {
        if (e.Key == Key.Enter)
        {
            _ = _safeAsync?.RunAsync(SubmitAsync);
        }
    }

    private async Task SubmitAsync()
    {
        if (_page is null)
        {
            return;
        }

        TextBox? passwordInput = this.FindControl<TextBox>("PasswordInput");

        // Read at the moment of use and passed straight through. The password is
        // never stored on the view model or exposed as a bindable property — which
        // is also why this control's Text has no binding on it.
        string password = passwordInput?.Text ?? string.Empty;

        if (await _page.SignIn.SubmitAsync(password).ConfigureAwait(true))
        {
            if (passwordInput is not null)
            {
                passwordInput.Text = string.Empty;
            }

            SignedIn?.Invoke(this, EventArgs.Empty);
        }
    }

    private void CancelTwoFactor_OnClick(object? sender, RoutedEventArgs e) =>
        _page?.SignIn.CancelTwoFactor();

    private void RetrySurface_OnClick(object? sender, RoutedEventArgs e) =>
        _ = _safeAsync?.RunAsync(RetrySurfaceAsync);

    private async Task RetrySurfaceAsync()
    {
        if (_page is not null)
        {
            await _page.SignIn.LoadSurfaceAsync().ConfigureAwait(true);
            SurfaceLoaded?.Invoke(this, EventArgs.Empty);
        }
    }

    private void Register_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_page?.SignIn.CanRegister == true)
        {
            RegistrationRequested?.Invoke(this, EventArgs.Empty);
        }
    }

    private void ResetPassword_OnClick(object? sender, RoutedEventArgs e) =>
        BrowserLauncher.TryOpenRelayPage("forgot-password");

    private void OpenUpdatePage_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_page?.ClientUpdate.DownloadPage is { } page)
        {
            BrowserLauncher.TryOpen(page);
        }
    }
}
