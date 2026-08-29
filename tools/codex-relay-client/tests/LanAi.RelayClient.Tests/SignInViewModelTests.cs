using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class SignInViewModelTests
{
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
