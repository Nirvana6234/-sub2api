using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class RegistrationViewModelTests
{
    [Fact]
    public async Task RegistrationUsesEmailCodeWhenServerEnablesVerification()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";
        await viewModel.SendVerifyCodeAsync();
        viewModel.VerifyCode = "123456";

        Assert.True(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.Equal("123456", client.LastRegistration!.VerifyCode);
    }

    [Fact]
    public async Task RegistrationOmitsCodeAndOptionalFieldsWhenServerDisablesThem()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true });
        viewModel.Email = "new@example.com";
        viewModel.VerifyCode = "should-not-send";
        viewModel.InvitationCode = "should-not-send";
        viewModel.PromoCode = "should-not-send";

        Assert.True(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.Null(client.LastRegistration!.VerifyCode);
        Assert.Null(client.LastRegistration.InvitationCode);
        Assert.Null(client.LastRegistration.PromoCode);
    }

    [Fact]
    public async Task RegistrationIncludesOptionalFieldsWhenServerEnablesThem()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings
        {
            RegistrationEnabled = true,
            InvitationCodeEnabled = true,
            PromoCodeEnabled = true,
        });
        viewModel.Email = "new@example.com";
        viewModel.InvitationCode = "invite-123";
        viewModel.PromoCode = "promo-456";

        Assert.True(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.Equal("invite-123", client.LastRegistration!.InvitationCode);
        Assert.Equal("promo-456", client.LastRegistration.PromoCode);
    }

    [Fact]
    public async Task PasswordMismatchStopsBeforeCallingServer()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true });
        viewModel.Email = "new@example.com";

        Assert.False(await viewModel.SubmitAsync("secret123", "different"));
        Assert.Null(client.LastRegistration);
    }

    [Fact]
    public async Task VerifyCodeCountdownUsesServerSeconds()
    {
        var client = new FakeRelayClient { OnVerifyCodeRequest = _ => new VerifyCodeDispatch { CountdownSeconds = 17 } };
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";

        await viewModel.SendVerifyCodeAsync();

        Assert.Equal(17, viewModel.VerifyCodeSecondsRemaining);
        Assert.Equal(1, client.VerifyCodeCallCount);
        Assert.Equal("new@example.com", client.LastVerifyEmail);
    }

    /// <remarks>
    /// The countdown's own behaviour, reachable for the first time now that the timer
    /// is injected. Everything below — the per-second decrement, the stop at zero, and
    /// the resend button coming back — shipped untested, because a
    /// <c>DispatcherTimer</c> never ticks under xUnit.
    /// </remarks>
    [Fact]
    public async Task TheCountdownRunsDownAndReleasesTheResendButton()
    {
        var client = new FakeRelayClient { OnVerifyCodeRequest = _ => new VerifyCodeDispatch { CountdownSeconds = 3 } };
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        FakeUiTimer? timer = null;
        var viewModel = new RegistrationViewModel(session, client, (i, t) => timer = new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";

        await viewModel.SendVerifyCodeAsync();

        Assert.Equal(TimeSpan.FromSeconds(1), timer!.Interval);
        Assert.True(timer.IsRunning);
        Assert.False(viewModel.CanSendVerifyCode);

        timer.Tick();
        Assert.Equal(2, viewModel.VerifyCodeSecondsRemaining);
        Assert.True(viewModel.HasVerifyCodeCountdown);

        timer.Tick();
        Assert.Equal(1, viewModel.VerifyCodeSecondsRemaining);

        // The last tick lands on the <= 1 branch: it zeroes the counter and stops the
        // timer in one step, rather than decrementing to zero and firing once more.
        timer.Tick();
        Assert.Equal(0, viewModel.VerifyCodeSecondsRemaining);
        Assert.False(timer.IsRunning);
        Assert.False(viewModel.HasVerifyCodeCountdown);
        Assert.True(viewModel.CanSendVerifyCode);
    }

    /// <remarks>
    /// The countdown line is composed by the view model rather than by a
    /// <c>StringFormat</c> in the markup, so this is the test the WPF version could
    /// not have: a format string that silently produced nothing would have shown as a
    /// blank line under the field, with no error and nothing failing.
    /// </remarks>
    [Fact]
    public async Task TheCountdownLineNamesTheSecondsAndDisappearsAtZero()
    {
        var client = new FakeRelayClient { OnVerifyCodeRequest = _ => new VerifyCodeDispatch { CountdownSeconds = 2 } };
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        FakeUiTimer? timer = null;
        var viewModel = new RegistrationViewModel(session, client, (i, t) => timer = new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";

        Assert.Equal(string.Empty, viewModel.VerifyCodeCountdownText);

        await viewModel.SendVerifyCodeAsync();
        Assert.Equal("请等待 2 秒后重试", viewModel.VerifyCodeCountdownText);

        timer!.Tick();
        Assert.Equal("请等待 1 秒后重试", viewModel.VerifyCodeCountdownText);

        // Empty rather than "请等待 0 秒后重试": the line is hidden at zero, and text
        // that contradicts the hiding would be visible for the frame in between.
        timer.Tick();
        Assert.Equal(string.Empty, viewModel.VerifyCodeCountdownText);
    }

    /// <remarks>
    /// A stopped timer that keeps being ticked must not drive the counter negative.
    /// It cannot happen with a real timer, but it is the kind of thing that turns a
    /// countdown into "请等待 -4 秒后重试" if the stop is ever moved.
    /// </remarks>
    [Fact]
    public async Task TicksAfterTheCountdownEndsChangeNothing()
    {
        var client = new FakeRelayClient { OnVerifyCodeRequest = _ => new VerifyCodeDispatch { CountdownSeconds = 2 } };
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        FakeUiTimer? timer = null;
        var viewModel = new RegistrationViewModel(session, client, (i, t) => timer = new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";
        await viewModel.SendVerifyCodeAsync();

        timer!.Tick(10);

        Assert.Equal(0, viewModel.VerifyCodeSecondsRemaining);
        Assert.False(timer.IsRunning);
    }

    /// <remarks>
    /// A server that answers with no countdown must not leave a timer running with
    /// nothing to count, which would keep the resend button disabled indefinitely.
    /// </remarks>
    [Fact]
    public async Task NoCountdownFromTheServerLeavesTheTimerStopped()
    {
        var client = new FakeRelayClient { OnVerifyCodeRequest = _ => new VerifyCodeDispatch { CountdownSeconds = 0 } };
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        FakeUiTimer? timer = null;
        var viewModel = new RegistrationViewModel(session, client, (i, t) => timer = new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";

        await viewModel.SendVerifyCodeAsync();

        Assert.Equal(0, timer!.StartCount);
        Assert.False(timer.IsRunning);
        Assert.True(viewModel.CanSendVerifyCode);
    }

    [Fact]
    public async Task EmailVerificationEnabledWithoutCodeStopsBeforeCallingServer()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, EmailVerifyEnabled = true });
        viewModel.Email = "new@example.com";

        Assert.False(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.Null(client.LastRegistration);
    }

    [Fact]
    public async Task RegistrationSuccessAdoptsSession()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true });
        viewModel.Email = "new@example.com";

        Assert.True(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.True(session.IsSignedIn);
    }

    [Fact]
    public async Task TurnstileEnabledBlocksNativeSubmitWithoutToken()
    {
        var client = new FakeRelayClient();
        var session = new RelaySessionManager(client, new FakeSessionStore(), "https://relay.test/");
        var viewModel = new RegistrationViewModel(session, client, (i, t) => new FakeUiTimer(i, t));
        viewModel.ApplySettings(new PublicSettings { RegistrationEnabled = true, TurnstileEnabled = true });
        viewModel.Email = "new@example.com";

        Assert.False(await viewModel.SubmitAsync("secret123", "secret123"));
        Assert.True(viewModel.TurnstileBlocked);
        Assert.Null(client.LastRegistration);
    }
}
