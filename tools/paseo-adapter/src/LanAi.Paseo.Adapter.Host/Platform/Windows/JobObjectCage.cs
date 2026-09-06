using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Runtime.Versioning;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>
/// Windows job object with <c>JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE</c>.
/// </summary>
/// <remarks>
/// <para>
/// When the last handle to the job closes — including because this process died
/// without running any cleanup — Windows terminates everything in it. That is the
/// only mechanism that survives a Task Manager kill, which is precisely the case
/// that matters: no finalizer, no <c>finally</c>, and no exit hook runs there.
/// </para>
/// <para>
/// Descendants are covered without being added explicitly: a process created by a
/// process in a job belongs to the same job unless it was created breakaway. That
/// matters here because the daemon is a four-process chain
/// (<c>cli → supervisor → daemon-worker → terminal-worker</c>) and only the first
/// one is ever handed to <see cref="Hold"/>.
/// </para>
/// <para>
/// P/Invoke rather than a package: this assembly takes no NuGet dependency, and
/// the three calls needed here are stable Win32 surface.
/// </para>
/// </remarks>
[SupportedOSPlatform("windows")]
public sealed class JobObjectCage : IProcessCage
{
    private const int JobObjectExtendedLimitInformation = 9;
    private const uint JobObjectLimitKillOnJobClose = 0x2000;

    private nint _handle;
    private bool _disposed;

    public JobObjectCage()
    {
        _handle = CreateJobObjectW(nint.Zero, null);
        if (_handle == nint.Zero)
        {
            throw new InvalidOperationException(
                $"CreateJobObject failed (win32 {Marshal.GetLastWin32Error()})");
        }

        var limits = new JobObjectExtendedLimitInformationStruct
        {
            BasicLimitInformation = new JobObjectBasicLimitInformation
            {
                LimitFlags = JobObjectLimitKillOnJobClose,
            },
        };

        var size = Marshal.SizeOf<JobObjectExtendedLimitInformationStruct>();
        var buffer = Marshal.AllocHGlobal(size);
        try
        {
            Marshal.StructureToPtr(limits, buffer, fDeleteOld: false);
            if (!SetInformationJobObject(_handle, JobObjectExtendedLimitInformation, buffer, (uint)size))
            {
                var error = Marshal.GetLastWin32Error();
                CloseHandle(_handle);
                _handle = nint.Zero;
                throw new InvalidOperationException(
                    $"SetInformationJobObject failed (win32 {error})");
            }
        }
        finally
        {
            Marshal.FreeHGlobal(buffer);
        }
    }

    public void Hold(Process process)
    {
        ArgumentNullException.ThrowIfNull(process);
        ObjectDisposedException.ThrowIf(_disposed, this);

        if (!AssignProcessToJobObject(_handle, process.Handle))
        {
            var error = Marshal.GetLastWin32Error();
            // A process that already exited cannot be assigned, and that is not a
            // caging failure — the supervisor will see the exit on its own.
            if (process.HasExited)
            {
                return;
            }

            throw new InvalidOperationException(
                $"AssignProcessToJobObject failed for pid {process.Id} (win32 {error})");
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        if (_handle != nint.Zero)
        {
            // Closing the last handle is what kills the job's processes. There is
            // deliberately no "stop nicely first" here: ordered shutdown belongs to
            // the supervisor, and the cage is the backstop for when it did not run.
            CloseHandle(_handle);
            _handle = nint.Zero;
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct JobObjectBasicLimitInformation
    {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public nuint MinimumWorkingSetSize;
        public nuint MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public nuint Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IoCounters
    {
        public ulong ReadOperationCount;
        public ulong WriteOperationCount;
        public ulong OtherOperationCount;
        public ulong ReadTransferCount;
        public ulong WriteTransferCount;
        public ulong OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct JobObjectExtendedLimitInformationStruct
    {
        public JobObjectBasicLimitInformation BasicLimitInformation;
        public IoCounters IoInfo;
        public nuint ProcessMemoryLimit;
        public nuint JobMemoryLimit;
        public nuint PeakProcessMemoryUsed;
        public nuint PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern nint CreateJobObjectW(nint lpJobAttributes, string? lpName);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetInformationJobObject(
        nint hJob,
        int jobObjectInformationClass,
        nint lpJobObjectInformation,
        uint cbJobObjectInformationLength);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool AssignProcessToJobObject(nint hJob, nint hProcess);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseHandle(nint hObject);
}

/// <summary>Picks the right cage for the running platform.</summary>
public static class ProcessCage
{
    /// <summary>
    /// A Windows job object where available.
    /// </summary>
    /// <remarks>
    /// On other platforms this currently returns <see cref="NullProcessCage"/>:
    /// the macOS/Linux equivalent (a process group plus a signal on exit) is not
    /// written yet, and pretending otherwise would hide the gap. The Avalonia
    /// build must not ship until it exists.
    /// </remarks>
    public static IProcessCage Create() =>
        OperatingSystem.IsWindows() ? new JobObjectCage() : new NullProcessCage();
}
