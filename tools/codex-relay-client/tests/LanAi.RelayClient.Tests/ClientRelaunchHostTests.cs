using System.Diagnostics;
using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The one real-process test in this area, on purpose: everything else about applying an
/// update is exercised through <c>IClientRelaunchHost</c> as a seam (see
/// <c>ClientSelfUpdaterTests</c>), but the helper script itself — a string built in C#, handed
/// to a real <c>powershell.exe</c> — has no seam to fake. It has to actually run once.
/// </summary>
/// <remarks>
/// This is exactly the test that would have caught the bug it is named for: a real user's
/// install directory, and this repository's own test artifacts, routinely contain Chinese
/// characters. <see cref="File.WriteAllText(string, string)"/>'s default encoding writes no
/// BOM, and Windows PowerShell 5.1 — <c>powershell.exe</c>, not <c>pwsh.exe</c> — falls back to
/// the system codepage without one, silently mangling every non-ASCII character embedded in the
/// script: every path, including its own log path. The failure has no error message anywhere,
/// because from the script's perspective every line still parses — it copies from, deletes and
/// logs to paths that are simply the wrong bytes, so the observed result is "extraction
/// succeeded, the install directory was never touched, and not even a log to say why."
/// </remarks>
public sealed class ClientRelaunchHostTests
{
    [Fact]
    public async Task ARelaunchOverAChineseCharacterPathActuallySwapsTheFiles()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        string root = Path.Combine(Path.GetTempPath(), $"更新测试-{Guid.NewGuid():N}");
        string install = Path.Combine(root, "安装目录");
        string staging = Path.Combine(install, "update", "extracted");
        string exePath = Path.Combine(install, "app.exe");
        Directory.CreateDirectory(staging);
        await File.WriteAllTextAsync(exePath, "old");
        await File.WriteAllTextAsync(Path.Combine(staging, "app.exe"), "new");

        // Standing in for "the process about to be replaced": something this test can wait on
        // and be sure has exited, without touching the real test host process itself.
        using var placeholder = Process.Start(new ProcessStartInfo("powershell.exe", "-NoProfile -Command \"Start-Sleep -Seconds 2\"")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
        });

        try
        {
            var host = new ClientRelaunchHost();
            bool started = host.StartRelaunchHelper(staging, install, exePath, placeholder!.Id);
            Assert.True(started);

            // Waited for in this order on purpose: the script copies, then removes staging,
            // then attempts the relaunch (a no-op fake exe here, so it always fails) and only
            // then writes its last log line — so staging being gone is the earliest reliable
            // sign the copy step itself has actually finished, without racing it.
            bool stagingRemoved = await PollUntilAsync(
                () => !Directory.Exists(staging),
                removed => removed == true,
                TimeSpan.FromSeconds(20));
            Assert.True(stagingRemoved, "the helper must clean up what it copied from");

            string logPath = Path.Combine(install, "update", "helper.log");
            await PollUntilAsync(
                () => File.Exists(logPath) && File.ReadAllText(logPath).Contains("helper finished"),
                done => done == true,
                TimeSpan.FromSeconds(10));

            Assert.Equal("new", File.ReadAllText(exePath));
            Assert.True(File.Exists(logPath), "a run that reached this point must have left a log");
            string log = await File.ReadAllTextAsync(logPath);
            Assert.Contains("robocopy exit code", log);
        }
        finally
        {
            if (!placeholder!.HasExited)
            {
                placeholder.Kill();
            }

            try { Directory.Delete(root, recursive: true); } catch (IOException) { }
        }
    }

    private static async Task<T?> PollUntilAsync<T>(Func<T?> read, Func<T?, bool> isDone, TimeSpan timeout)
    {
        DateTime deadline = DateTime.UtcNow + timeout;
        T? value = read();
        while (!isDone(value) && DateTime.UtcNow < deadline)
        {
            await Task.Delay(200);
            value = read();
        }

        return value;
    }
}
