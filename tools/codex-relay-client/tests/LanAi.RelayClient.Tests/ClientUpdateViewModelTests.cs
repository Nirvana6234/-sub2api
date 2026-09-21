using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The 检查更新 button's flow: every outcome of asking the relay gets a word, an available
/// update is offered rather than only shown, and the passive banner check stays silent as
/// before.
/// </summary>
public sealed class ClientUpdateViewModelTests
{
    private static readonly Uri DownloadPage = new(ClientOptions.ServerAddress + "download");

    private static ClientUpdateInfo Windows(string version = "0.9") =>
        new(new Version(version), DownloadPage, ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(ClientOptions.ServerAddress + "api/v1/download/client"));

    private sealed class Rig
    {
        public ClientCheckResult NextCheck { get; set; } = new(ClientCheckStatus.UpToDate);

        public ClientSelfUpdateResult NextApply { get; set; } = new(ClientSelfUpdateOutcome.Problem);

        public bool ConfirmAnswer { get; set; } = true;

        public List<string> Confirmations { get; } = [];

        public List<string> Messages { get; } = [];

        public int ApplyCallCount { get; private set; }

        public int RestartCallCount { get; private set; }

        public ClientUpdateViewModel Build(bool withApply = true)
        {
            var viewModel = new ClientUpdateViewModel(
                _ => Task.FromResult(NextCheck),
                withApply ? (_, _) => { ApplyCallCount++; return Task.FromResult(NextApply); } : null)
            {
                ConfirmUpdate = message =>
                {
                    Confirmations.Add(message);
                    return Task.FromResult(ConfirmAnswer);
                },
                ShowMessage = message =>
                {
                    Messages.Add(message);
                    return Task.CompletedTask;
                },
                RestartForUpdate = () =>
                {
                    RestartCallCount++;
                    return Task.CompletedTask;
                },
            };
            return viewModel;
        }
    }

    [Fact]
    public async Task ThePassiveCheckNeverShowsADialogOnAnyOutcome()
    {
        var rig = new Rig { NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows()) };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAsync();

        Assert.True(viewModel.HasUpdate);
        Assert.Empty(rig.Confirmations);
        Assert.Empty(rig.Messages);
        Assert.Equal(0, rig.ApplyCallCount);
    }

    [Theory]
    [InlineData(ClientCheckStatus.UpToDate)]
    [InlineData(ClientCheckStatus.ChannelDisabled)]
    public async Task UpToDateOrDisabledTellsTheUserThereIsNothingToInstall(ClientCheckStatus status)
    {
        var rig = new Rig { NextCheck = new ClientCheckResult(status) };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(["当前已经是最新版本。"], rig.Messages);
        Assert.Empty(rig.Confirmations);
        Assert.False(viewModel.HasUpdate);
    }

    [Fact]
    public async Task ACheckFailureIsToldApartFromUpToDate()
    {
        var rig = new Rig { NextCheck = new ClientCheckResult(ClientCheckStatus.CheckFailed) };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(["检查更新失败，请稍后重试。"], rig.Messages);
    }

    [Fact]
    public async Task AnAvailableUpdateIsOfferedWithTheVersionNamed()
    {
        var rig = new Rig { NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows("0.9")) };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(["发现新版本 Ver0.9，是否现在更新？"], rig.Confirmations);
    }

    [Fact]
    public async Task DecliningTheConfirmationAppliesNothing()
    {
        var rig = new Rig
        {
            NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows()),
            ConfirmAnswer = false,
        };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(0, rig.ApplyCallCount);
        Assert.Empty(rig.Messages);
    }

    [Fact]
    public async Task ARestartingOutcomeReleasesAndTearsDownRatherThanShowingAMessage()
    {
        var rig = new Rig
        {
            NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows()),
            NextApply = new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Restarting),
        };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(1, rig.RestartCallCount);
        Assert.Empty(rig.Messages);
    }

    [Theory]
    [InlineData(ClientSelfUpdateOutcome.OpenedTerminal, "已打开终端，请按提示完成安装。")]
    [InlineData(ClientSelfUpdateOutcome.OpenedDownloadPage, "已打开下载页面，请手动下载安装。")]
    public async Task EveryOtherOutcomeTellsTheUserWhatHappened(ClientSelfUpdateOutcome outcome, string expected)
    {
        var rig = new Rig
        {
            NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows()),
            NextApply = new ClientSelfUpdateResult(outcome),
        };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal([expected], rig.Messages);
        Assert.Equal(0, rig.RestartCallCount);
    }

    [Fact]
    public async Task AProblemShowsItsOwnNote()
    {
        var rig = new Rig
        {
            NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows()),
            NextApply = new ClientSelfUpdateResult(ClientSelfUpdateOutcome.Problem, "磁盘空间不足"),
        };
        ClientUpdateViewModel viewModel = rig.Build();

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Equal(["磁盘空间不足"], rig.Messages);
    }

    /// <summary>
    /// An instance with no updater wired (the sign-in screen's own) must not offer a confirm
    /// dialog whose "yes" does nothing — it tells the user instead.
    /// </summary>
    [Fact]
    public async Task WithNoUpdaterWiredTheUserIsToldRatherThanAskedToConfirmNothing()
    {
        var rig = new Rig { NextCheck = new ClientCheckResult(ClientCheckStatus.Available, Windows("0.9")) };
        ClientUpdateViewModel viewModel = rig.Build(withApply: false);

        await viewModel.CheckAndOfferUpdateAsync();

        Assert.Empty(rig.Confirmations);
        Assert.Equal(["发现新版本 Ver0.9，请前往下载页面手动更新。"], rig.Messages);
    }
}
