using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class SignInViewModelTests
{
    private static SignInViewModel BuildWith(
        FakeRelayClient? relay = null,
        FakeLastAccountPreferenceStore? lastAccount = null)
    {
        relay ??= new FakeRelayClient();
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        return new SignInViewModel(
            session,
            _ => Task.FromResult(PublicSettings.Conservative),
            (_, _) => Task.CompletedTask,
            lastAccount);
    }

    [Fact]
    public void TheRememberedEmailFillsTheFieldOnConstruction()
    {
        var lastAccount = new FakeLastAccountPreferenceStore { Saved = "ann@example.com" };

        SignInViewModel viewModel = BuildWith(lastAccount: lastAccount);

        Assert.Equal("ann@example.com", viewModel.Email);
    }

    [Fact]
    public void WithNothingRememberedTheFieldStartsBlank()
    {
        SignInViewModel viewModel = BuildWith(lastAccount: new FakeLastAccountPreferenceStore());

        Assert.Equal(string.Empty, viewModel.Email);
    }

    [Fact]
    public async Task ACorrectPasswordSavesTheEmailEvenBehindTwoFactor()
    {
        // Reaching a 2FA challenge already proves the password — withholding the
        // save until the code is entered would make the field forget itself on
        // every single sign-in for anyone with 2FA on.
        var relay = new FakeRelayClient { OnLogin = () => LoginOutcome.TwoFactorRequired("tmp", "a***@b.com") };
        var lastAccount = new FakeLastAccountPreferenceStore();
        SignInViewModel viewModel = BuildWith(relay, lastAccount);
        viewModel.Email = "  ann@example.com  ";

        await viewModel.SubmitAsync("pw");

        Assert.Equal("ann@example.com", lastAccount.Saved);
    }

    [Fact]
    public async Task AFullSignInSavesTheEmail()
    {
        var lastAccount = new FakeLastAccountPreferenceStore();
        SignInViewModel viewModel = BuildWith(lastAccount: lastAccount);
        viewModel.Email = "ann@example.com";

        await viewModel.SubmitAsync("pw");

        Assert.Equal("ann@example.com", lastAccount.Saved);
    }

    [Fact]
    public async Task ARejectedPasswordDoesNotSaveTheEmail()
    {
        var relay = new FakeRelayClient
        {
            OnLogin = () => throw new RelayApiException(RelayFailure.InvalidCredentials, "密码不正确"),
        };
        var lastAccount = new FakeLastAccountPreferenceStore();
        SignInViewModel viewModel = BuildWith(relay, lastAccount);
        viewModel.Email = "ann@example.com";

        await viewModel.SubmitAsync("wrong");

        Assert.Null(lastAccount.Saved);
    }

    [Fact]
    public async Task LoadSurfaceRetriesTransientFailuresBeforeApplyingRegistrationSetting()
    {
        int attempts = 0;
        var session = new RelaySessionManager(new FakeRelayClient(), new FakeSessionStore(), "https://relay.test/");
        var viewModel = new SignInViewModel(
            session,
            _ =>
            {
                attempts++;
                if (attempts < 3)
                {
                    throw new RelayApiException(RelayFailure.NetworkUnreachable, "网络暂时不可用");
                }

                return Task.FromResult(new PublicSettings { RegistrationEnabled = true });
            },
            (_, _) => Task.CompletedTask);

        await viewModel.LoadSurfaceAsync();

        Assert.Equal(3, attempts);
        Assert.True(viewModel.CanRegister);
        Assert.False(viewModel.HasSurfaceLoadFailure);
    }

    [Fact]
    public async Task LoadSurfaceKeepsManualRetryAvailableAfterAllAutomaticAttemptsFail()
    {
        int attempts = 0;
        var session = new RelaySessionManager(new FakeRelayClient(), new FakeSessionStore(), "https://relay.test/");
        var viewModel = new SignInViewModel(
            session,
            _ =>
            {
                attempts++;
                throw new RelayApiException(RelayFailure.NetworkUnreachable, "网络暂时不可用");
            },
            (_, _) => Task.CompletedTask);

        await viewModel.LoadSurfaceAsync();

        Assert.Equal(3, attempts);
        Assert.False(viewModel.CanRegister);
        Assert.True(viewModel.HasSurfaceLoadFailure);
        Assert.True(viewModel.CanRetrySurface);
    }
}

internal sealed class FakeLastAccountPreferenceStore : ILastAccountPreferenceStore
{
    public string? Saved { get; set; }

    public string? Load() => Saved;

    public void Save(string email) => Saved = email;
}
