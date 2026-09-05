using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;
using Xunit;

namespace AiSwitch.Wpf.Tests;

public sealed class SignInPromptViewModelTests
{
    private static readonly Uri Gateway = new("http://127.0.0.1:8080/");

    [Fact]
    public void ShowClearsPreviousAttempt()
    {
        var session = new StubSessionManager();
        var prompt = new SignInPromptViewModel(session, () => Gateway);

        prompt.Email = "stale@example.test";
        prompt.ErrorMessage = "旧的错误";
        prompt.Show();

        Assert.True(prompt.IsVisible);
        Assert.Equal(string.Empty, prompt.Email);
        Assert.Null(prompt.ErrorMessage);
        Assert.False(prompt.HasError);
    }

    [Fact]
    public async Task SubmitRejectsEmptyInputWithoutCallingGateway()
    {
        var session = new StubSessionManager();
        var prompt = new SignInPromptViewModel(session, () => Gateway);
        prompt.Show();

        bool signedIn = await prompt.SubmitAsync(string.Empty, CancellationToken.None);

        Assert.False(signedIn);
        Assert.Equal(0, session.LoginCalls);
        Assert.True(prompt.HasError);
        Assert.True(prompt.IsVisible);
    }

    [Fact]
    public async Task SubmitReportsMissingBackendAddress()
    {
        var session = new StubSessionManager();
        var prompt = new SignInPromptViewModel(session, () => null);
        prompt.Show();
        prompt.Email = "user@example.test";

        bool signedIn = await prompt.SubmitAsync("secret", CancellationToken.None);

        Assert.False(signedIn);
        Assert.Equal(0, session.LoginCalls);
        Assert.True(prompt.HasError);
    }

    [Fact]
    public async Task SuccessfulSubmitClosesCardAndForgetsAccount()
    {
        var session = new StubSessionManager();
        var prompt = new SignInPromptViewModel(session, () => Gateway);
        prompt.Show();
        prompt.Email = " user@example.test ";

        bool signedIn = await prompt.SubmitAsync("secret", CancellationToken.None);

        Assert.True(signedIn);
        Assert.Equal(1, session.LoginCalls);
        Assert.Equal("user@example.test", session.LastEmail);
        Assert.False(prompt.IsVisible);
        Assert.Equal(string.Empty, prompt.Email);
        Assert.False(prompt.HasError);
    }

    [Fact]
    public async Task FailedSubmitKeepsCardOpenWithReadableMessage()
    {
        var session = new StubSessionManager
        {
            Failure = Sub2ApiSessionFailure.InvalidCredentials,
        };
        var prompt = new SignInPromptViewModel(session, () => Gateway);
        prompt.Show();
        prompt.Email = "user@example.test";

        bool signedIn = await prompt.SubmitAsync("wrong", CancellationToken.None);

        Assert.False(signedIn);
        Assert.True(prompt.IsVisible);
        Assert.Equal("账号或密码不正确。", prompt.ErrorMessage);
        Assert.True(prompt.CanSubmit);
    }

    [Fact]
    public void CancelClearsTheForm()
    {
        var session = new StubSessionManager();
        var prompt = new SignInPromptViewModel(session, () => Gateway);
        prompt.Show();
        prompt.Email = "user@example.test";
        prompt.ErrorMessage = "错误";

        prompt.CancelCommand.Execute(null);

        Assert.False(prompt.IsVisible);
        Assert.Equal(string.Empty, prompt.Email);
        Assert.Null(prompt.ErrorMessage);
    }

    private sealed class StubSessionManager : ISub2ApiSessionManager
    {
        public Sub2ApiSessionState Current { get; private set; } = Sub2ApiSessionState.SignedOut;

        public event EventHandler? SessionChanged;

        public int LoginCalls { get; private set; }

        public string? LastEmail { get; private set; }

        public Sub2ApiSessionFailure? Failure { get; init; }

        public Task RestoreAsync(Uri apiBaseUri, CancellationToken cancellationToken) => Task.CompletedTask;

        public Task<Sub2ApiSessionAccess> LoginAsync(
            Uri apiBaseUri,
            string email,
            string password,
            CancellationToken cancellationToken)
            => LoginAsync(apiBaseUri, email, password, false, cancellationToken);

        public Task<Sub2ApiSessionAccess> LoginAsync(
            Uri apiBaseUri,
            string email,
            string password,
            bool allowInsecurePublicHttp,
            CancellationToken cancellationToken)
        {
            LoginCalls++;
            LastEmail = email;
            if (Failure is { } failure)
            {
                throw new Sub2ApiSessionException(failure);
            }

            var access = new Sub2ApiSessionAccess(
                apiBaseUri,
                "access-token",
                7,
                "user",
                5m,
                0m,
                DateTimeOffset.UtcNow.AddMinutes(30))
            {
                Username = "zhoubo",
                Email = email,
            };
            Current = new Sub2ApiSessionState(
                true,
                false,
                false,
                "普通用户",
                5m,
                0m,
                access.ExpiresAtUtc,
                access.ApiBaseUri,
                "已登录")
            {
                Username = access.Username,
                Email = access.Email,
            };
            SessionChanged?.Invoke(this, EventArgs.Empty);
            return Task.FromResult(access);
        }

        public Task<Sub2ApiSessionAccess> GetAccessAsync(Uri apiBaseUri, CancellationToken cancellationToken)
            => throw new InvalidOperationException("Not used.");

        public Task LogoutAsync(CancellationToken cancellationToken)
        {
            Current = Sub2ApiSessionState.SignedOut;
            SessionChanged?.Invoke(this, EventArgs.Empty);
            return Task.CompletedTask;
        }

        public void Dispose()
        {
        }
    }
}
