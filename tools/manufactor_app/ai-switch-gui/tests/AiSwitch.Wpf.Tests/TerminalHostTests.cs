using System.Diagnostics;
using System.IO;
using LanAi.Workspace.Terminal;
using LanAi.Workspace.Wpf.Controls;

namespace AiSwitch.Wpf.Tests;

public sealed class TerminalHostTests
{
    [Fact]
    public async Task Shutdown_IsBoundedAndDoesNotRetainCommandEnvironment()
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 17763))
        {
            return;
        }

        var host = new TerminalHost();
        string shell = Environment.GetEnvironmentVariable("COMSPEC")
            ?? @"C:\Windows\System32\cmd.exe";
        const string secret = "must-not-remain-in-host";
        var command = new TerminalCommand(
            shell,
            ["/d", "/q", "/k"],
            Environment.CurrentDirectory,
            new Dictionary<string, string?> { ["WORKSPACE_TEST_SECRET"] = secret },
            "Host metadata test");

        await host.StartAsync(command, 80, 20);

        Assert.NotNull(host.ActiveMetadata);
        Assert.DoesNotContain(secret, host.ActiveMetadata!.ToString(), StringComparison.Ordinal);
        Assert.DoesNotContain(
            typeof(TerminalHost).GetProperties(),
            property => property.PropertyType == typeof(TerminalCommand));

        var stopwatch = Stopwatch.StartNew();
        await host.ShutdownAsync(TimeSpan.FromSeconds(2));
        stopwatch.Stop();

        Assert.True(stopwatch.Elapsed < TimeSpan.FromSeconds(3));
        Assert.True(host.IsShutdownRequested);
        Assert.Null(host.ActiveMetadata);
    }

    [Fact]
    public async Task FailedStart_ClearsDisplayMetadata()
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 17763))
        {
            return;
        }

        await using var host = new TerminalHost();
        string shell = Environment.GetEnvironmentVariable("COMSPEC")
            ?? @"C:\Windows\System32\cmd.exe";
        string missingDirectory = Path.Combine(
            Path.GetTempPath(),
            "lan-ai-missing-" + Guid.NewGuid().ToString("N"));

        await Assert.ThrowsAsync<DirectoryNotFoundException>(() => host.StartAsync(
            new TerminalCommand(shell, ["/d", "/c", "echo unreachable"], missingDirectory),
            80,
            20));

        Assert.Null(host.ActiveMetadata);
        Assert.Equal(TerminalHostState.Faulted, host.State);
    }

    [Fact]
    public async Task NaturalExit_ClearsDisplayMetadata()
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 17763))
        {
            return;
        }

        await using var host = new TerminalHost();
        string shell = Environment.GetEnvironmentVariable("COMSPEC")
            ?? @"C:\Windows\System32\cmd.exe";
        var exited = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        host.StateChanged += (_, args) =>
        {
            if (args.State == TerminalHostState.Exited)
            {
                exited.TrySetResult(true);
            }
        };

        await host.StartAsync(
            new TerminalCommand(
                shell,
                ["/d", "/s", "/c", "echo host-exit-ok"],
                Environment.CurrentDirectory,
                DisplayName: "Natural exit"),
            80,
            20);
        await exited.Task.WaitAsync(TimeSpan.FromSeconds(10));

        Assert.Null(host.ActiveMetadata);
        Assert.Equal(TerminalHostState.Exited, host.State);
    }
}
