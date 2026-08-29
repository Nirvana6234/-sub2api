using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>Picks the single-instance mechanism that works on this platform.</summary>
/// <remarks>
/// Windows keeps the kernel-object implementation because it is the only one that can
/// also carry the activation signal — a second launch there raises the running
/// window, which is the behaviour users already have and should not lose to a port.
/// Everything else falls back to the file lock, which enforces exclusion but cannot
/// signal.
/// </remarks>
internal static class SingleInstance
{
    /// <param name="mutexName">Windows-only: the named mutex.</param>
    /// <param name="eventName">Windows-only: the named activation event.</param>
    /// <param name="activate">Invoked on the primary when another launch asks it to surface.</param>
    public static ISingleInstanceCoordinator Create(string mutexName, string eventName, Action activate)
    {
        if (OperatingSystem.IsWindows())
        {
            return new SingleInstanceCoordinator(mutexName, eventName, activate);
        }

        return new FileLockSingleInstanceCoordinator(AppPaths.InData("instance.lock"));
    }
}
