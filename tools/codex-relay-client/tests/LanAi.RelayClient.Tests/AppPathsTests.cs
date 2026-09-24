using System;
using System.IO;
using LanAi.RelayClient.Platform;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Pins the on-disk location of existing installations.
/// </summary>
/// <remarks>
/// <para>
/// Centralising nine <c>Environment.GetFolderPath</c> calls into <c>AppPaths</c> was
/// done so macOS could stop writing to <c>~/.local/share</c>. The risk it introduces
/// is on the other platform: any edit to the Windows branch relocates data that
/// existing users already have.
/// </para>
/// <para>
/// Nothing about that failure looks like a failure. The client finds no session and
/// shows the sign-in page, finds no install id and mints a new one — presenting
/// itself as a clean install to someone who just took an update. This test is here so
/// that renaming the folder has to be a decision rather than an accident.
/// </para>
/// </remarks>
public class AppPathsTests
{
    [Fact]
    public void WindowsDataRootKeepsItsHistoricalLocation()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        string expected = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "LanAi.RelayClient");

        Assert.Equal(expected, AppPaths.Data);
    }

    /// <remarks>
    /// The four files below are the ones whose loss is user-visible: the saved
    /// session, the install identity that owns the managed key, the selected group,
    /// and which announcements have already been shown.
    /// </remarks>
    [Theory]
    [InlineData("install-id")]
    [InlineData("preferences.json")]
    [InlineData("announcements.json")]
    [InlineData("codex-account.json")]
    public void StateFilesResolveUnderTheDataRoot(string fileName)
    {
        Assert.Equal(Path.Combine(AppPaths.Data, fileName), AppPaths.InData(fileName));
    }

    /// <summary>
    /// Pins the Codex snapshot root, including the fact that it does not match the
    /// root everything else uses.
    /// </summary>
    /// <remarks>
    /// <para>
    /// <c>LanAi\RelayClient</c>, two segments — not the <c>LanAi.RelayClient</c> the
    /// rest of the client writes to. It looks like a typo and is not safe to treat as
    /// one: this directory holds the copy of the user's own Codex configuration that
    /// gets restored when the client hands ChatGPT back.
    /// </para>
    /// <para>
    /// Losing it does not fail loudly. The client keeps working; the user simply
    /// cannot return to their own ChatGPT account afterwards, and nothing on screen
    /// connects that to an update they installed weeks earlier.
    /// </para>
    /// </remarks>
    [Fact]
    public void CodexSnapshotRootKeepsItsSeparateTwoSegmentLocation()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        string expected = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "LanAi",
            "RelayClient",
            "codex-snapshot");

        Assert.Equal(expected, AppPaths.CodexSnapshotRoot);
        Assert.NotEqual(Path.Combine(AppPaths.Data, "codex-snapshot"), AppPaths.CodexSnapshotRoot);
    }

    /// <remarks>
    /// The auth snapshot, unlike the config snapshot above, does live under the
    /// single-segment root. Pinned so the two are not "harmonised" into each other.
    /// </remarks>
    [Fact]
    public void CodexAuthSnapshotStaysUnderTheMainDataRoot()
    {
        Assert.Equal(
            Path.Combine(AppPaths.Data, "codex-auth-original.json"),
            AppPaths.CodexAuthSnapshotFile);
    }

    [Fact]
    public void NestedSegmentsAreSupportedForTheLogFile()
    {
        Assert.Equal(
            Path.Combine(AppPaths.Data, "logs", "client.log"),
            AppPaths.InData("logs", "client.log"));
    }
}
