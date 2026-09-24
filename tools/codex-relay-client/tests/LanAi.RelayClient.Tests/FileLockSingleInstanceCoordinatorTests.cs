using System;
using System.IO;
using LanAi.RelayClient.Platform;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Covers the exclusion mechanism macOS will rely on.
/// </summary>
/// <remarks>
/// <para>
/// These run on Windows and mean something there: <see cref="FileShare.None"/> is
/// enforced by the runtime on every platform, which is the whole reason this was
/// chosen over a named mutex. The macOS-only parts of Phase 2 could not be tested
/// without a Mac; this one could, so it is.
/// </para>
/// <para>
/// What is <i>not</i> covered is activation — it is unimplemented by design, and the
/// test below pins that as a deliberate answer rather than an oversight.
/// </para>
/// </remarks>
public sealed class FileLockSingleInstanceCoordinatorTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "lock-" + Guid.NewGuid().ToString("N"));

    private string LockPath => Path.Combine(_root, "instance.lock");

    [Fact]
    public void TheFirstInstanceIsPrimary()
    {
        using var first = new FileLockSingleInstanceCoordinator(LockPath);
        Assert.True(first.IsPrimary);
    }

    /// <remarks>
    /// The case that matters. Two clients running at once both write
    /// <c>~/.codex/config.toml</c> and both try to own the managed key — the failure
    /// this class exists to prevent.
    /// </remarks>
    [Fact]
    public void ASecondInstanceIsNotPrimaryWhileTheFirstHoldsTheLock()
    {
        using var first = new FileLockSingleInstanceCoordinator(LockPath);
        using var second = new FileLockSingleInstanceCoordinator(LockPath);

        Assert.True(first.IsPrimary);
        Assert.False(second.IsPrimary);
    }

    /// <remarks>
    /// Releasing must hand the slot over. A lock that outlived its process would make
    /// the client unstartable after a crash, with no way for a novice to recover.
    /// </remarks>
    [Fact]
    public void DisposingReleasesTheSlotForTheNextInstance()
    {
        using (var first = new FileLockSingleInstanceCoordinator(LockPath))
        {
            Assert.True(first.IsPrimary);
        }

        using var replacement = new FileLockSingleInstanceCoordinator(LockPath);
        Assert.True(replacement.IsPrimary);
    }

    /// <remarks>
    /// The lock file is intentionally left on disk. Only an open exclusive handle
    /// means anything, so a leftover file must not be read as "already running".
    /// </remarks>
    [Fact]
    public void ALeftoverLockFileDoesNotBlockAFreshStart()
    {
        Directory.CreateDirectory(_root);
        File.WriteAllText(LockPath, "stale");

        using var coordinator = new FileLockSingleInstanceCoordinator(LockPath);
        Assert.True(coordinator.IsPrimary);
    }

    [Fact]
    public void ActivationIsDeclinedRatherThanFaked()
    {
        using var first = new FileLockSingleInstanceCoordinator(LockPath);
        using var second = new FileLockSingleInstanceCoordinator(LockPath);

        Assert.False(second.TryActivateExistingInstance());
        Assert.False(first.TryActivateExistingInstance());
    }

    [Fact]
    public void MissingDirectoriesAreCreated()
    {
        string nested = Path.Combine(_root, "a", "b", "instance.lock");

        using var coordinator = new FileLockSingleInstanceCoordinator(nested);

        Assert.True(coordinator.IsPrimary);
        Assert.True(File.Exists(nested));
    }

    [Fact]
    public void DisposingTwiceIsSafe()
    {
        var coordinator = new FileLockSingleInstanceCoordinator(LockPath);
        coordinator.Dispose();
        coordinator.Dispose();
    }

    public void Dispose()
    {
        try
        {
            if (Directory.Exists(_root))
            {
                Directory.Delete(_root, recursive: true);
            }
        }
        catch (IOException)
        {
        }
    }
}
