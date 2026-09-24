using System.ComponentModel;
using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>Owns a bundled context-filter process for one client session.</summary>
/// <remarks>
/// <para>
/// The filter stays in the chain for the whole session whenever the binary is
/// present; the user's 启用上下文压缩 switch changes the filter's own
/// <c>[filter] enabled</c> rather than whether this process exists.
/// </para>
/// <para>
/// That is not an arbitrary choice. Taking the filter out of the chain would move
/// the address Codex talks to, and whether a <em>running</em> ChatGPT re-reads
/// <c>config.toml</c> has never been verified on a real machine (G-1 in the macOS
/// plan). Keeping the port fixed for the session means the switch never depends on
/// that answer. Measured, on the shipped binary: <c>enabled = false</c> is a
/// transparent pass-through — it still proxies and simply stops adding its
/// <c>x-context-filter-*</c> headers — and the same port rebinds cleanly across
/// restarts.
/// </para>
/// </remarks>
internal sealed class ContextFilterProcess : IAsyncDisposable
{
    private readonly string _executable;
    private string? _upstreamBaseUrl;
    private Process? _process;
    private string? _directory;
    private int _port;
    private bool _filterEnabled = true;

    public ContextFilterProcess(string executable)
    {
        if (string.IsNullOrWhiteSpace(executable)) throw new ArgumentException("Executable is required", nameof(executable));
        _executable = executable;
    }

    public void SetUpstream(Uri upstreamBaseUrl)
    {
        ArgumentNullException.ThrowIfNull(upstreamBaseUrl);
        if (!upstreamBaseUrl.IsLoopback || upstreamBaseUrl.Scheme != Uri.UriSchemeHttp)
            throw new ArgumentException("Context Filter upstream must be a loopback HTTP URL", nameof(upstreamBaseUrl));
        _upstreamBaseUrl = upstreamBaseUrl.GetLeftPart(UriPartial.Authority);
    }

    public Uri? BaseAddress { get; private set; }

    public bool IsRunning => _process is { HasExited: false };

    /// <summary>Whether compression is on in the process that is currently running.</summary>
    public bool FilterEnabled => _filterEnabled;

    public async Task StartAsync(bool filterEnabled, CancellationToken cancellationToken = default)
    {
        if (IsRunning)
        {
            await ApplyFilterEnabledAsync(filterEnabled, cancellationToken).ConfigureAwait(false);
            return;
        }

        await StopAsync().ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(_upstreamBaseUrl))
            throw new InvalidOperationException("Context Filter upstream has not been configured");

        // Allocated once and then kept: the address Codex was configured with has to
        // outlive a restart, or toggling compression would silently point Codex at a
        // port with nothing on it.
        if (_port == 0) _port = GetFreePort();
        _directory ??= Path.Combine(Path.GetTempPath(), "gongfei-context-filter", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(_directory);

        _filterEnabled = filterEnabled;
        string configPath = Path.Combine(_directory, "config.toml");
        // No BOM: the Go TOML parser rejects one outright ("invalid character at
        // start of key"), and the failure surfaces only in the filter's stderr.
        await File.WriteAllTextAsync(
            configPath,
            $"listen_addr = \"127.0.0.1:{_port}\"\n" +
            $"upstream_base_url = \"{EscapeToml(_upstreamBaseUrl)}\"\n" +
            "\n[filter]\n" +
            $"enabled = {(filterEnabled ? "true" : "false")}\n",
            new UTF8Encoding(false),
            cancellationToken).ConfigureAwait(false);

        var start = new ProcessStartInfo
        {
            FileName = _executable,
            UseShellExecute = false,
            CreateNoWindow = true,
        };
        start.ArgumentList.Add("-config");
        start.ArgumentList.Add(configPath);
        _process = Process.Start(start) ?? throw new InvalidOperationException("无法启动 Context Filter");
        ChildProcessJob.TryAdopt(_process);
        BaseAddress = new Uri($"http://127.0.0.1:{_port}/v1/");
        try
        {
            await WaitForPortAsync(_port, cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            await StopAsync().ConfigureAwait(false);
            throw;
        }
    }

    /// <summary>
    /// Turns compression on or off in place, keeping the port Codex is pointed at.
    /// </summary>
    /// <remarks>
    /// Restarting is the only mechanism the filter offers — it reads its config once
    /// at startup and has no reload signal on Windows. A turn in flight at that
    /// moment is lost, which is why this is only ever driven by an explicit click.
    /// </remarks>
    public async Task ApplyFilterEnabledAsync(bool filterEnabled, CancellationToken cancellationToken = default)
    {
        if (IsRunning && _filterEnabled == filterEnabled) return;

        bool wasRunning = IsRunning;
        _filterEnabled = filterEnabled;
        if (!wasRunning) return;

        await StopProcessAsync().ConfigureAwait(false);
        await StartAsync(filterEnabled, cancellationToken).ConfigureAwait(false);
    }

    private async Task WaitForPortAsync(int port, CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(TimeSpan.FromSeconds(5));
        while (true)
        {
            timeout.Token.ThrowIfCancellationRequested();
            if (_process?.HasExited == true) throw new InvalidOperationException("Context Filter 启动失败");
            try
            {
                using var client = new TcpClient();
                await client.ConnectAsync(IPAddress.Loopback, port, timeout.Token).ConfigureAwait(false);
                return;
            }
            catch (SocketException) { await Task.Delay(40, timeout.Token).ConfigureAwait(false); }
        }
    }

    private async Task StopProcessAsync()
    {
        if (_process is null) return;
        try
        {
            if (!_process.HasExited)
            {
                _process.Kill(entireProcessTree: true);
                await _process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(2)).ConfigureAwait(false);
            }
        }
        catch (InvalidOperationException) { }
        catch (TimeoutException) { }
        finally { _process.Dispose(); _process = null; }
    }

    /// <summary>Stops the process but keeps the reserved port, so a later start reuses it.</summary>
    public async Task StopAsync()
    {
        await StopProcessAsync().ConfigureAwait(false);
        BaseAddress = null;
    }

    public async ValueTask DisposeAsync()
    {
        await StopProcessAsync().ConfigureAwait(false);
        if (_directory is not null)
        {
            try { if (Directory.Exists(_directory)) Directory.Delete(_directory, recursive: true); }
            catch (IOException) { }
            catch (UnauthorizedAccessException) { }
            _directory = null;
        }

        BaseAddress = null;
        _port = 0;
    }

    private static int GetFreePort()
    {
        using var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        return ((IPEndPoint)listener.LocalEndpoint).Port;
    }

    private static string EscapeToml(string value) => value.Replace("\\", "\\\\").Replace("\"", "\\\"");
}

/// <summary>
/// Ties a child process's life to this one, so the filter cannot outlive the client.
/// </summary>
/// <remarks>
/// <para>
/// The orderly exit already stops the filter (<c>CodexStartup.ReleaseAsync</c>), but that
/// only helps when the client gets to run it. A crash, a task-manager kill or an
/// installer replacing the exe skips it and leaves <c>context-filter.exe</c> holding its
/// port with nothing to talk to. A job object with kill-on-close has no such gap: Windows
/// closes the handle when this process dies, however it dies, and takes every process in
/// the job with it.
/// </para>
/// <para>
/// Best effort. If the job cannot be created or the child cannot be assigned (for
/// instance it is already in a job that forbids breakaway on an old Windows), the orderly
/// exit is still the fallback — it was the only mechanism before this.
/// </para>
/// </remarks>
internal static class ChildProcessJob
{
    private const uint JobObjectLimitKillOnJobClose = 0x2000;
    private const int ExtendedLimitInformationClass = 9;

    private static readonly object Gate = new();
    private static IntPtr _job;
    private static bool _failed;

    public static void TryAdopt(Process process)
    {
        if (!OperatingSystem.IsWindows()) return;
        try
        {
            lock (Gate)
            {
                if (_failed) return;
                if (_job == IntPtr.Zero && !TryCreateJob()) { _failed = true; return; }
                if (!AssignProcessToJobObject(_job, process.Handle))
                    ClientLog.Warning($"无法把 Context Filter 绑定到客户端的作业对象（错误 {Marshal.GetLastWin32Error()}），退出时仍会主动结束它");
            }
        }
        catch (Exception ex) when (ex is InvalidOperationException or Win32Exception or DllNotFoundException or EntryPointNotFoundException)
        {
            ClientLog.Warning("绑定 Context Filter 到作业对象失败，退出时仍会主动结束它", ex);
        }
    }

    private static bool TryCreateJob()
    {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) return false;

        var info = new JobObjectExtendedLimitInformation
        {
            BasicLimitInformation = new JobObjectBasicLimitInformation { LimitFlags = JobObjectLimitKillOnJobClose },
        };
        int length = Marshal.SizeOf<JobObjectExtendedLimitInformation>();
        IntPtr buffer = Marshal.AllocHGlobal(length);
        try
        {
            Marshal.StructureToPtr(info, buffer, fDeleteOld: false);
            if (!SetInformationJobObject(job, ExtendedLimitInformationClass, buffer, (uint)length))
            {
                CloseHandle(job);
                return false;
            }
        }
        finally
        {
            Marshal.FreeHGlobal(buffer);
        }

        // Deliberately never closed: closing it is what kills the children, and that is to
        // happen when the process ends, not before.
        _job = job;
        return true;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateJobObject(IntPtr attributes, string? name);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetInformationJobObject(IntPtr job, int infoClass, IntPtr info, uint length);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseHandle(IntPtr handle);

    [StructLayout(LayoutKind.Sequential)]
    private struct JobObjectBasicLimitInformation
    {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
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
    private struct JobObjectExtendedLimitInformation
    {
        public JobObjectBasicLimitInformation BasicLimitInformation;
        public IoCounters IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }
}
