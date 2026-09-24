using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class SingleInstanceCoordinatorTests
{
    [Fact]
    public async Task FirstCoordinatorOwnsInstanceAndSecondCoordinatorDoesNot()
    {
        (string mutexName, string eventName) = Names();
        using var first = new SingleInstanceCoordinator(mutexName, eventName, () => { });
        bool secondIsPrimary = await Task.Run(() =>
        {
            using var second = new SingleInstanceCoordinator(mutexName, eventName, () => { });
            return second.IsPrimary;
        });

        Assert.True(first.IsPrimary);
        Assert.False(secondIsPrimary);
    }

    [Fact]
    public async Task SecondarySignalInvokesPrimaryActivationCallback()
    {
        (string mutexName, string eventName) = Names();
        using var signalReceived = new ManualResetEventSlim();
        using var first = new SingleInstanceCoordinator(
            mutexName,
            eventName,
            signalReceived.Set);
        first.StartListening();
        bool signaled = await Task.Run(() =>
        {
            using var second = new SingleInstanceCoordinator(mutexName, eventName, () => { });
            return second.TryActivateExistingInstance();
        });

        Assert.True(signaled);
        Assert.True(signalReceived.Wait(TimeSpan.FromSeconds(2)));

        await first.StopListeningAsync();
    }

    [Fact]
    public void ReleasingPrimaryAllowsAnotherProcessToOwnInstance()
    {
        (string mutexName, string eventName) = Names();
        using (var first = new SingleInstanceCoordinator(mutexName, eventName, () => { }))
        {
            Assert.True(first.IsPrimary);
        }

        using var replacement = new SingleInstanceCoordinator(mutexName, eventName, () => { });
        Assert.True(replacement.IsPrimary);
    }

    private static (string MutexName, string EventName) Names()
    {
        string suffix = Guid.NewGuid().ToString("N");
        return ($"Local\\LanAi.RelayClient.Tests.{suffix}", $"Local\\LanAi.RelayClient.Tests.Activate.{suffix}");
    }
}
