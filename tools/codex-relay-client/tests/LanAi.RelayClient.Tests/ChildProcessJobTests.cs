using System.Diagnostics;
using System.Runtime.InteropServices;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class ChildProcessJobTests
{
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool IsProcessInJob(IntPtr process, IntPtr job, [MarshalAs(UnmanagedType.Bool)] out bool result);

    /// <summary>
    /// Kill-on-close itself cannot be observed without ending the test host, so this pins the part that
    /// can: an adopted child is in a job, where a plain one (under a non-job parent) is not.
    /// </summary>
    [Fact]
    public void AnAdoptedChildIsPutInAJob()
    {
        if (!OperatingSystem.IsWindows()) return;

        using Process child = Process.Start(new ProcessStartInfo("ping", "-n 30 127.0.0.1") { CreateNoWindow = true, UseShellExecute = false })!;
        try
        {
            ChildProcessJob.TryAdopt(child);

            Assert.True(IsProcessInJob(child.Handle, IntPtr.Zero, out bool inJob));
            Assert.True(inJob);
        }
        finally
        {
            child.Kill(entireProcessTree: true);
        }
    }
}
