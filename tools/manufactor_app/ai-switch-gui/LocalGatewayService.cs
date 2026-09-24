using System.Diagnostics;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Text.Json;

namespace AiSwitchGui;

internal sealed class LocalGatewayService
{
    private const int SwMinimize = 6;
    private static readonly TimeSpan QuickCommandTimeout = TimeSpan.FromSeconds(6);
    private static readonly TimeSpan ComposeCommandTimeout = TimeSpan.FromSeconds(15);
    // Native startup may need to wait for PostgreSQL crash recovery after an
    // unexpected Windows shutdown.  Do not kill the launcher midway through
    // that recovery window.
    private static readonly TimeSpan NativeCommandTimeout = TimeSpan.FromSeconds(150);

    public const string DockerWebUrl = "http://localhost:8080";
    public const string NativeWebUrl = "http://127.0.0.1:8080";

    private readonly HttpClient _httpClient;
    private readonly string _nativeHintFile;
    private readonly bool _refreshNativeRootFromHint;

    public LocalGatewayService()
        : this(FindComposeFile(), FindNativeRoot())
    {
    }

    internal LocalGatewayService(
        string? composeFile,
        string? nativeRoot,
        HttpClient? httpClient = null,
        string? nativeHintFile = null)
    {
        ComposeFile = composeFile;
        NativeRoot = nativeRoot;
        _httpClient = httpClient ?? new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
        _nativeHintFile = string.IsNullOrWhiteSpace(nativeHintFile)
            ? GetNativeHintFilePath()
            : Path.GetFullPath(nativeHintFile);
        _refreshNativeRootFromHint = nativeHintFile is not null;
    }

    public string? ComposeFile { get; }
    public string? NativeRoot { get; private set; }
    public string WebUrl => UsesNativeRuntime ? NativeWebUrl : DockerWebUrl;
    public bool UsesNativeControl => !string.IsNullOrWhiteSpace(NativeRoot);
    public bool UsesNativeRuntime => UsesNativeControl || string.IsNullOrWhiteSpace(ComposeFile);

    public bool DockerInstalled => IsDockerInstalled();

    public Task<CommandResult> ConfigureNativeRootAsync(string selectedPath, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        string? candidate = ResolveNativeRoot(selectedPath, requireProtected: false);
        if (candidate is not null && !TryProtectNativeRoot(candidate, out string? protectionError))
        {
            return Task.FromResult(new CommandResult
            {
                ExitCode = -1,
                StdErr = $"无法保护所选本机中转目录：{protectionError}",
            });
        }

        string? resolved = candidate is null ? null : ResolveNativeRoot(candidate);
        if (resolved is null)
        {
            return Task.FromResult(new CommandResult
            {
                ExitCode = -1,
                StdErr = "所选目录不是有效的本机中转工作区。请选择包含 start-sub2api-local.ps1、stop-sub2api-local.ps1 和 sub2api 目录的位置。"
            });
        }

        try
        {
            string? hintDirectory = Path.GetDirectoryName(_nativeHintFile);
            if (!string.IsNullOrWhiteSpace(hintDirectory))
            {
                Directory.CreateDirectory(hintDirectory);
            }
            File.WriteAllText(_nativeHintFile, resolved, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
            NativeRoot = resolved;
            return Task.FromResult(new CommandResult
            {
                ExitCode = 0,
                StdOut = $"本机中转目录已设置为 {resolved}"
            });
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return Task.FromResult(new CommandResult
            {
                ExitCode = -1,
                StdErr = $"保存本机中转目录失败：{exception.Message}"
            });
        }
    }

    public LocalGatewayStatus GetStartupStatus()
    {
        RefreshNativeRootFromHint();
        return new LocalGatewayStatus
        {
            ComposeFile = ComposeFile ?? string.Empty,
            NativeRoot = NativeRoot ?? string.Empty,
            NativeMode = UsesNativeRuntime,
            ControlAvailable = UsesNativeControl || (!string.IsNullOrWhiteSpace(ComposeFile) && LooksLikeDockerInstalled()),
            WebUrl = WebUrl,
            DockerInstalled = LooksLikeDockerInstalled(),
            Summary = "等待刷新"
        };
    }

    public async Task<LocalGatewayStatus> GetStatusAsync(CancellationToken cancellationToken, bool includeDeepDiagnostics = false)
    {
        RefreshNativeRootFromHint();
        var status = new LocalGatewayStatus
        {
            ComposeFile = ComposeFile ?? string.Empty,
            NativeRoot = NativeRoot ?? string.Empty,
            NativeMode = UsesNativeRuntime,
            ControlAvailable = UsesNativeControl || (!string.IsNullOrWhiteSpace(ComposeFile) && LooksLikeDockerInstalled()),
            WebUrl = WebUrl,
            DockerInstalled = LooksLikeDockerInstalled()
        };

        if (UsesNativeControl)
        {
            AddNativeServiceStatus(status, "sub2api", 8080);
            AddNativeServiceStatus(status, "postgres", GetNativePostgresPort(NativeRoot));
            AddNativeServiceStatus(status, "redis", 6380);
            status.WebReachable = await IsWebReachableAsync(cancellationToken);
            status.Diagnostics = await DiagnoseAsync(status, cancellationToken, includeDeepDiagnostics);
            status.Summary = BuildNativeSummary(status);
            return status;
        }

        if (string.IsNullOrWhiteSpace(ComposeFile) || !File.Exists(ComposeFile))
        {
            status.WebReachable = await IsWebReachableAsync(cancellationToken);
            status.Summary = status.WebReachable
                ? "本机中转运行正常"
                : "本机中转未运行";
            return status;
        }

        var dockerInfo = await RunProcessAsync("docker", "info", Path.GetDirectoryName(ComposeFile), cancellationToken, QuickCommandTimeout);
        status.DockerAvailable = dockerInfo.Success;
        if (!dockerInfo.Success)
        {
            status.Summary = "Docker 未运行或不可用";
            status.CommandOutput = dockerInfo.CombinedOutput;
            return status;
        }

        var ps = await RunComposeAsync(["ps", "--format", "json"], cancellationToken);
        status.CommandOutput = ps.CombinedOutput;
        if (ps.Success)
        {
            status.Services.AddRange(ParseComposeServices(ps.StdOut));
        }

        status.WebReachable = await IsWebReachableAsync(cancellationToken);
        status.Diagnostics = await DiagnoseAsync(status, cancellationToken, includeDeepDiagnostics);
        status.Summary = BuildSummary(status);
        return status;
    }

    public Task<CommandResult> StartAsync(CancellationToken cancellationToken)
    {
        RefreshNativeRootFromHint();
        if (UsesNativeRuntime)
        {
            return RunNativeScriptAsync("start-sub2api-local.ps1", cancellationToken);
        }
        return RunComposeAsync(["up", "-d"], cancellationToken);
    }

    public Task<CommandResult> RestartAsync(CancellationToken cancellationToken)
    {
        RefreshNativeRootFromHint();
        if (UsesNativeControl)
        {
            return RestartNativeAsync(cancellationToken);
        }
        return RunComposeAsync(["restart"], cancellationToken);
    }

    public Task<CommandResult> StopAsync(CancellationToken cancellationToken)
    {
        RefreshNativeRootFromHint();
        if (UsesNativeControl)
        {
            return RunNativeScriptAsync("stop-sub2api-local.ps1", cancellationToken);
        }
        return RunComposeAsync(["down"], cancellationToken);
    }

    public async Task<bool> WaitForWebAsync(TimeSpan timeout, CancellationToken cancellationToken)
    {
        var deadline = DateTime.UtcNow.Add(timeout);
        while (DateTime.UtcNow < deadline)
        {
            if (await IsWebReachableAsync(cancellationToken))
            {
                return true;
            }

            await Task.Delay(TimeSpan.FromSeconds(2), cancellationToken);
        }

        return false;
    }

    private async Task<bool> IsWebReachableAsync(CancellationToken cancellationToken)
    {
        if (UsesNativeControl)
        {
            return await IsUrlReachableAsync(WebUrl, cancellationToken) &&
                   await IsUrlReachableAsync("http://127.0.0.1:8080/health", cancellationToken);
        }

        if (await IsUrlReachableAsync($"{WebUrl}/health", cancellationToken))
        {
            return true;
        }

        return await IsUrlReachableAsync(WebUrl, cancellationToken);
    }

    private async Task<bool> IsUrlReachableAsync(string url, CancellationToken cancellationToken)
    {
        try
        {
            using var response = await _httpClient.GetAsync(url, cancellationToken);
            return (int)response.StatusCode >= 200 && (int)response.StatusCode < 500;
        }
        catch
        {
            return false;
        }
    }

    private async Task<LocalGatewayDiagnostics> DiagnoseAsync(LocalGatewayStatus status, CancellationToken cancellationToken, bool includePortOwners)
    {
        var diagnostics = new LocalGatewayDiagnostics
        {
            LoopbackHealthReachable = await IsUrlReachableAsync("http://127.0.0.1:8080/health", cancellationToken),
            LocalhostHealthReachable = await IsUrlReachableAsync("http://localhost:8080/health", cancellationToken)
        };

        foreach (var listener in GetTcpListeners(8080, includePortOwners))
        {
            diagnostics.PortListeners.Add(listener);
        }

        var lanAddress = GetPrimaryLanAddress();
        if (!string.IsNullOrWhiteSpace(lanAddress))
        {
            diagnostics.LanHealthUrl = $"http://{lanAddress}:8080/health";
            diagnostics.LanHealthReachable = await IsUrlReachableAsync(diagnostics.LanHealthUrl, cancellationToken);
        }

        diagnostics.HasLoopbackInterception =
            !diagnostics.LoopbackHealthReachable &&
            diagnostics.LanHealthReachable &&
            diagnostics.PortListeners.Any(listener =>
                string.Equals(listener.LocalAddress, "127.0.0.1", StringComparison.OrdinalIgnoreCase) &&
                string.Equals(listener.ProcessName, "wslrelay", StringComparison.OrdinalIgnoreCase));

        if (diagnostics.HasLoopbackInterception)
        {
            diagnostics.Messages.Add("127.0.0.1:8080 被 wslrelay.exe 监听，LAN IP 可访问但 localhost 不可访问，疑似 WSL/Docker loopback 转发截流。");
        }

        if (status.Services.Count > 0 && status.Services.All(x => x.IsHealthyEnough) && !status.WebReachable)
        {
            diagnostics.Messages.Add("容器均健康，但宿主机网页不可访问，优先检查端口监听、防火墙或 WSL 转发。");
        }

        return diagnostics;
    }

    private async Task<CommandResult> RunComposeAsync(string[] args, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(ComposeFile))
        {
            return new CommandResult
            {
                ExitCode = -1,
                StdErr = "未找到 sub2api\\deploy\\docker-compose.local.yml"
            };
        }

        var docker = await EnsureDockerForCommandAsync(cancellationToken);
        if (!docker.Success)
        {
            return docker;
        }

        var composeArgs = new List<string> { "compose", "-f", ComposeFile };
        composeArgs.AddRange(args);
        return await RunProcessAsync("docker", BuildArguments(composeArgs), Path.GetDirectoryName(ComposeFile), cancellationToken, ComposeCommandTimeout);
    }

    private async Task<CommandResult> RestartNativeAsync(CancellationToken cancellationToken)
    {
        var stop = await RunNativeScriptAsync("stop-sub2api-local.ps1", cancellationToken);
        if (!stop.Success)
        {
            return stop;
        }

        return await RunNativeScriptAsync("start-sub2api-local.ps1", cancellationToken);
    }

    private Task<CommandResult> RunNativeScriptAsync(string scriptName, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(NativeRoot))
        {
            return Task.FromResult(new CommandResult { ExitCode = -1, StdErr = "未找到 Sub2API 原生启动目录。" });
        }

        var script = Path.Combine(NativeRoot, scriptName);
        if (!File.Exists(script))
        {
            return Task.FromResult(new CommandResult { ExitCode = -1, StdErr = $"未找到原生控制脚本: {script}" });
        }

        var arguments = BuildArguments(["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script]);
        return RunProcessAsync("powershell.exe", arguments, NativeRoot, cancellationToken, NativeCommandTimeout);
    }

    private static void AddNativeServiceStatus(LocalGatewayStatus status, string name, int port, bool optional = false)
    {
        var running = IPGlobalProperties.GetIPGlobalProperties().GetActiveTcpListeners().Any(x => x.Port == port);
        status.Services.Add(new LocalGatewayServiceStatus
        {
            Service = name,
            Name = name,
            State = running ? "running" : optional ? "optional" : "stopped",
            Health = running ? "healthy" : optional ? "optional" : string.Empty,
            Status = running ? "监听中" : optional ? "未配置/未启动" : "未启动",
            Ports = $"127.0.0.1:{port}"
        });
    }

    internal static string BuildNativeSummary(LocalGatewayStatus status)
    {
        if (status.WebReachable && status.Services.All(service => service.IsHealthyEnough))
        {
            return "原生模式运行正常";
        }

        return status.Services.Any(service =>
            string.Equals(service.State, "running", StringComparison.OrdinalIgnoreCase))
            ? "原生服务启动中或不完整"
            : "原生模式未启动";
    }

    internal static int GetNativePostgresPort(string? nativeRoot)
    {
        if (string.IsNullOrWhiteSpace(nativeRoot))
        {
            return 5433;
        }

        string recoveredVersionFile = Path.Combine(
            nativeRoot,
            ".local",
            "pgdata-recovered-reset",
            "PG_VERSION");
        return File.Exists(recoveredVersionFile) ? 55434 : 5433;
    }

    private static async Task<CommandResult> EnsureDockerForCommandAsync(CancellationToken cancellationToken)
    {
        var firstCheck = await RunProcessAsync("docker", "info", null, cancellationToken, QuickCommandTimeout);
        if (firstCheck.Success)
        {
            return firstCheck;
        }

        var dockerDesktop = FindDockerDesktop();
        if (dockerDesktop is null)
        {
            return new CommandResult
            {
                ExitCode = -1,
                StdErr = "Docker 未运行，并且未找到 Docker Desktop。请先安装或打开 Docker Desktop。"
            };
        }

        try
        {
            Process.Start(new ProcessStartInfo
            {
                FileName = dockerDesktop,
                UseShellExecute = true,
                WindowStyle = ProcessWindowStyle.Minimized
            });
        }
        catch (Exception ex)
        {
            return new CommandResult
            {
                ExitCode = -1,
                StdErr = $"尝试启动 Docker Desktop 失败: {ex.Message}"
            };
        }

        var deadline = DateTime.UtcNow.AddMinutes(2);
        while (DateTime.UtcNow < deadline)
        {
            await Task.Delay(TimeSpan.FromSeconds(3), cancellationToken);
            var check = await RunProcessAsync("docker", "info", null, cancellationToken, QuickCommandTimeout);
            if (check.Success)
            {
                await MinimizeDockerDesktopWindowsAsync(cancellationToken);
                return check;
            }
        }

        return new CommandResult
        {
            ExitCode = -1,
            StdErr = "已尝试启动 Docker Desktop，但 Docker 在 2 分钟内仍未就绪。"
        };
    }

    private static string? FindDockerDesktop()
    {
        var candidates = new List<string>
        {
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),
                "Docker",
                "Docker",
                "Docker Desktop.exe"),
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86),
                "Docker",
                "Docker",
                "Docker Desktop.exe"),
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "Docker",
                "Docker",
                "Docker Desktop.exe"),
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.CommonPrograms),
                "Docker",
                "Docker Desktop.lnk"),
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.Programs),
                "Docker",
                "Docker Desktop.lnk")
        };

        var directHit = candidates.FirstOrDefault(File.Exists);
        if (!string.IsNullOrWhiteSpace(directHit))
        {
            return directHit;
        }

        var searchRoots = new[]
        {
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86),
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData)
        };

        return searchRoots
            .Where(Directory.Exists)
            .Select(root => FindFileByName(root, "Docker Desktop.exe", maxDepth: 5))
            .FirstOrDefault(path => !string.IsNullOrWhiteSpace(path));
    }

    private static bool IsDockerInstalled()
    {
        return LooksLikeDockerInstalled();
    }

    private static bool LooksLikeDockerInstalled()
    {
        return FindDockerDesktop() is not null || FindExecutableOnPath("docker.exe") is not null;
    }

    private static string? FindExecutableOnPath(string fileName)
    {
        var path = Environment.GetEnvironmentVariable("PATH");
        if (string.IsNullOrWhiteSpace(path))
        {
            return null;
        }

        foreach (var directory in path.Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries))
        {
            try
            {
                var candidate = Path.Combine(directory.Trim(), fileName);
                if (File.Exists(candidate))
                {
                    return candidate;
                }
            }
            catch
            {
                // Ignore malformed PATH entries.
            }
        }

        return null;
    }

    private static string? FindFileByName(string root, string fileName, int maxDepth)
    {
        var pending = new Queue<(string Directory, int Depth)>();
        pending.Enqueue((root, 0));

        while (pending.Count > 0)
        {
            var (directory, depth) = pending.Dequeue();
            try
            {
                var match = Directory.EnumerateFiles(directory, fileName, SearchOption.TopDirectoryOnly).FirstOrDefault();
                if (!string.IsNullOrWhiteSpace(match))
                {
                    return match;
                }

                if (depth >= maxDepth)
                {
                    continue;
                }

                foreach (var child in Directory.EnumerateDirectories(directory))
                {
                    pending.Enqueue((child, depth + 1));
                }
            }
            catch
            {
                // Some Program Files folders are protected or virtualized; skip them and continue searching.
            }
        }

        return null;
    }

    private static async Task MinimizeDockerDesktopWindowsAsync(CancellationToken cancellationToken)
    {
        for (var attempt = 0; attempt < 12; attempt++)
        {
            foreach (var process in Process.GetProcessesByName("Docker Desktop"))
            {
                try
                {
                    process.Refresh();
                    if (process.MainWindowHandle != IntPtr.Zero)
                    {
                        ShowWindow(process.MainWindowHandle, SwMinimize);
                    }
                }
                catch
                {
                    // The process can exit or deny window inspection while Docker Desktop is starting.
                }
                finally
                {
                    process.Dispose();
                }
            }

            await Task.Delay(TimeSpan.FromMilliseconds(500), cancellationToken);
        }
    }

    [DllImport("user32.dll")]
    private static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);

    private static async Task<CommandResult> RunProcessAsync(
        string fileName,
        string arguments,
        string? workingDirectory,
        CancellationToken cancellationToken,
        TimeSpan? timeout = null)
    {
        var result = new CommandResult();
        var startInfo = new ProcessStartInfo
        {
            FileName = fileName,
            Arguments = arguments,
            WorkingDirectory = string.IsNullOrWhiteSpace(workingDirectory) ? Environment.CurrentDirectory : workingDirectory,
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            CreateNoWindow = true,
            StandardOutputEncoding = Encoding.UTF8,
            StandardErrorEncoding = Encoding.UTF8
        };

        using var process = new Process { StartInfo = startInfo };
        try
        {
            process.Start();
        }
        catch (Exception ex)
        {
            result.ExitCode = -1;
            result.StdErr = ex.Message;
            return result;
        }

        using var timeoutCts = timeout.HasValue
            ? new CancellationTokenSource(timeout.Value)
            : new CancellationTokenSource();
        using var linkedCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken, timeoutCts.Token);

        var stdoutTask = process.StandardOutput.ReadToEndAsync(linkedCts.Token);
        var stderrTask = process.StandardError.ReadToEndAsync(linkedCts.Token);
        try
        {
            await process.WaitForExitAsync(linkedCts.Token);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested && timeout.HasValue)
        {
            try
            {
                process.Kill(entireProcessTree: true);
            }
            catch
            {
                // Best effort cleanup for a timed-out external command.
            }

            result.ExitCode = -1;
            result.StdErr = $"{fileName} {arguments} 超时（{timeout.Value.TotalSeconds:0} 秒）。";
            return result;
        }

        result.ExitCode = process.ExitCode;
        result.StdOut = await SafeReadAsync(stdoutTask);
        result.StdErr = await SafeReadAsync(stderrTask);
        return result;
    }

    private static async Task<string> SafeReadAsync(Task<string> task)
    {
        try
        {
            return await task;
        }
        catch
        {
            return string.Empty;
        }
    }

    private static List<LocalGatewayServiceStatus> ParseComposeServices(string output)
    {
        var services = new List<LocalGatewayServiceStatus>();
        foreach (var rawLine in output.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries))
        {
            var line = rawLine.Trim();
            if (string.IsNullOrWhiteSpace(line))
            {
                continue;
            }

            try
            {
                using var document = JsonDocument.Parse(line);
                var root = document.RootElement;
                services.Add(new LocalGatewayServiceStatus
                {
                    Service = GetString(root, "Service"),
                    Name = GetString(root, "Name"),
                    State = GetString(root, "State"),
                    Health = GetString(root, "Health"),
                    Status = GetString(root, "Status"),
                    Ports = GetString(root, "Ports")
                });
            }
            catch
            {
                // Docker Compose emits NDJSON. Ignore a malformed line instead of hiding all service status.
            }
        }

        return services
            .OrderBy(x => x.Service == "sub2api" ? 0 : 1)
            .ThenBy(x => x.Service, StringComparer.OrdinalIgnoreCase)
            .ToList();
    }

    private static IEnumerable<LocalGatewayPortListener> GetTcpListeners(int port, bool includePortOwners)
    {
        IPGlobalProperties properties;
        try
        {
            properties = IPGlobalProperties.GetIPGlobalProperties();
        }
        catch
        {
            yield break;
        }

        foreach (var connection in properties.GetActiveTcpListeners().Where(x => x.Port == port))
        {
            var listener = new LocalGatewayPortListener
            {
                LocalAddress = connection.Address.ToString(),
                LocalPort = connection.Port
            };

            if (includePortOwners)
            {
                FillOwningProcess(listener);
            }
            yield return listener;
        }
    }

    private static void FillOwningProcess(LocalGatewayPortListener listener)
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        var address = listener.LocalAddress == "0.0.0.0" || listener.LocalAddress == "::"
            ? listener.LocalAddress
            : listener.LocalAddress.Replace(":", "\\:", StringComparison.Ordinal);
        var command = $"Get-NetTCPConnection -LocalPort {listener.LocalPort} -State Listen | " +
                      $"Where-Object {{ $_.LocalAddress -eq '{address}' }} | " +
                      "Select-Object -First 1 -ExpandProperty OwningProcess";

        try
        {
            var result = RunProcessAsync(
                    "powershell.exe",
                    BuildArguments(["-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command]),
                    null,
                    CancellationToken.None,
                    QuickCommandTimeout)
                .GetAwaiter()
                .GetResult();

            if (!result.Success || !int.TryParse(result.StdOut.Trim(), out var processId))
            {
                return;
            }

            listener.ProcessId = processId;
            using var process = Process.GetProcessById(processId);
            listener.ProcessName = process.ProcessName;
            try
            {
                listener.ProcessPath = process.MainModule?.FileName ?? string.Empty;
            }
            catch
            {
                // Some protected system processes do not expose MainModule to non-elevated callers.
            }
        }
        catch
        {
            // Port ownership is a diagnostic hint, not a hard requirement.
        }
    }

    private static string? GetPrimaryLanAddress()
    {
        try
        {
            return NetworkInterface.GetAllNetworkInterfaces()
                .Where(x => x.OperationalStatus == OperationalStatus.Up &&
                            x.NetworkInterfaceType != NetworkInterfaceType.Loopback &&
                            x.NetworkInterfaceType != NetworkInterfaceType.Tunnel)
                .SelectMany(x => x.GetIPProperties().UnicastAddresses)
                .Where(x => x.Address.AddressFamily == AddressFamily.InterNetwork)
                .Select(x => x.Address)
                .Where(x => !IPAddress.IsLoopback(x) && !x.ToString().StartsWith("169.254.", StringComparison.Ordinal))
                .Select(x => x.ToString())
                .FirstOrDefault();
        }
        catch
        {
            return null;
        }
    }

    private static string GetString(JsonElement element, string propertyName)
    {
        return element.TryGetProperty(propertyName, out var value) && value.ValueKind != JsonValueKind.Null
            ? value.ToString()
            : string.Empty;
    }

    private static string BuildSummary(LocalGatewayStatus status)
    {
        if (status.Services.Count == 0)
        {
            return status.WebReachable ? "网页可访问，服务列表为空" : "未发现运行中的 compose 服务";
        }

        var unhealthy = status.Services
            .Where(x => !x.IsHealthyEnough)
            .Select(x => x.Service)
            .ToList();

        if (unhealthy.Count > 0)
        {
            return $"异常: {string.Join(", ", unhealthy)}";
        }

        return status.WebReachable ? "运行正常" : "容器正常，网页暂不可访问";
    }

    private static string BuildArguments(IEnumerable<string> args)
    {
        return string.Join(" ", args.Select(QuoteArgument));
    }

    private static string QuoteArgument(string arg)
    {
        if (string.IsNullOrEmpty(arg))
        {
            return "\"\"";
        }

        return arg.Any(char.IsWhiteSpace) || arg.Contains('"')
            ? $"\"{arg.Replace("\"", "\\\"", StringComparison.Ordinal)}\""
            : arg;
    }

    private static string? FindComposeFile()
    {
        var candidates = new List<string>
        {
            Environment.CurrentDirectory,
            AppContext.BaseDirectory
        };

        foreach (var compose in ReadConfiguredComposeCandidates())
        {
            if (File.Exists(compose))
            {
                return Path.GetFullPath(compose);
            }
        }

        foreach (var origin in new[] { AppContext.BaseDirectory, Environment.CurrentDirectory })
        {
            var current = new DirectoryInfo(origin);
            for (var i = 0; i < 12 && current is not null; i++)
            {
                candidates.Add(current.FullName);
                current = current.Parent;
            }
        }

        foreach (var candidate in candidates.Distinct(StringComparer.OrdinalIgnoreCase))
        {
            foreach (var compose in BuildComposeCandidates(candidate))
            {
                if (File.Exists(compose))
                {
                    return Path.GetFullPath(compose);
                }
            }
        }

        return null;
    }

    private static IEnumerable<string> ReadConfiguredComposeCandidates()
    {
        var fileNames = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "compose-path.txt"),
            Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
                "ai-switch-gui",
                "compose-path.txt")
        };

        foreach (var fileName in fileNames)
        {
            string? raw;
            try
            {
                raw = File.Exists(fileName) ? File.ReadAllText(fileName).Trim() : null;
            }
            catch
            {
                continue;
            }

            if (!string.IsNullOrWhiteSpace(raw))
            {
                yield return raw;
            }
        }
    }

    private static IEnumerable<string> BuildComposeCandidates(string root)
    {
        yield return Path.Combine(root, "sub2api", "deploy", "docker-compose.local.yml");
        yield return Path.Combine(root, "deploy", "docker-compose.local.yml");

        // Published EXE lives under tools/manufactor_app/ai-switch-gui/bin/Release/.../publish.
        // This relative candidate finds the repo root without hard-coding the drive or user path.
        yield return Path.Combine(
            root,
            "..",
            "..",
            "..",
            "..",
            "..",
            "..",
            "..",
            "..",
            "sub2api",
            "deploy",
            "docker-compose.local.yml");
    }

    private static string? FindNativeRoot()
    {
        // The running executable's own package is authoritative. A stale
        // machine-wide hint from an older installation must not redirect a
        // freshly installed app to another database and control token.
        foreach (var origin in new[] { AppContext.BaseDirectory, Environment.CurrentDirectory })
        {
            var current = new DirectoryInfo(origin);
            for (var i = 0; i < 12 && current is not null; i++)
            {
                var resolved = ResolveNativeRoot(current.FullName);
                if (resolved is not null)
                {
                    return resolved;
                }
                current = current.Parent;
            }
        }

        foreach (var configured in ReadConfiguredNativeCandidates())
        {
            var resolved = ResolveConfiguredNativeRoot(configured);
            if (resolved is not null)
            {
                return resolved;
            }
        }

        return null;
    }

    private static IEnumerable<string> ReadConfiguredNativeCandidates()
    {
        var fileNames = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "native-path.txt"),
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "ai-switch-gui", "native-path.txt"),
            GetNativeHintFilePath(),
        };

        foreach (var fileName in fileNames)
        {
            string? raw;
            try
            {
                raw = File.Exists(fileName) ? File.ReadAllText(fileName).Trim() : null;
            }
            catch
            {
                continue;
            }

            if (!string.IsNullOrWhiteSpace(raw))
            {
                yield return raw;
            }
        }
    }

    private static string? ResolveNativeRoot(string candidate, bool requireProtected = true)
    {
        try
        {
            var fullPath = Path.GetFullPath(candidate);
            var directory = new DirectoryInfo(File.Exists(fullPath)
                ? Path.GetDirectoryName(fullPath) ?? fullPath
                : fullPath);
            for (int depth = 0; depth < 5 && directory is not null; depth++, directory = directory.Parent)
            {
                if (File.Exists(Path.Combine(directory.FullName, "start-sub2api-local.ps1")) &&
                    File.Exists(Path.Combine(directory.FullName, "stop-sub2api-local.ps1")) &&
                    Directory.Exists(Path.Combine(directory.FullName, "sub2api")) &&
                    (!requireProtected || IsNativeRootProtected(directory.FullName)))
                {
                    return directory.FullName;
                }
            }
        }
        catch
        {
            // Ignore invalid configured paths.
        }

        return null;
    }

    private static string? ResolveConfiguredNativeRoot(string configuredPath)
    {
        string? protectedRoot = ResolveNativeRoot(configuredPath);
        if (protectedRoot is not null)
        {
            return protectedRoot;
        }

        // A previously saved path is an explicit user choice, unlike the old
        // fixed-drive discovery. Migrate it once by applying the same ACL used
        // by the manual picker before it can be executed again.
        string? candidate = ResolveNativeRoot(configuredPath, requireProtected: false);
        return candidate is not null &&
               TryProtectNativeRoot(candidate, out _) ?
            ResolveNativeRoot(candidate) :
            null;
    }

    private static bool TryProtectNativeRoot(string nativeRoot, out string? error)
    {
        error = null;
        if (!OperatingSystem.IsWindows())
        {
            error = "当前平台不支持 Windows 访问控制列表。";
            return false;
        }

        try
        {
            SecurityIdentifier currentUser = WindowsIdentity.GetCurrent().User
                ?? throw new UnauthorizedAccessException("无法确定当前用户。");
            var security = new DirectorySecurity();
            security.SetAccessRuleProtection(true, false);
            foreach (IdentityReference identity in new IdentityReference[]
            {
                currentUser,
                new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null),
                new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null),
            })
            {
                security.AddAccessRule(new FileSystemAccessRule(
                    identity,
                    FileSystemRights.FullControl,
                    InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit,
                    PropagationFlags.None,
                    AccessControlType.Allow));
            }

            new DirectoryInfo(nativeRoot).SetAccessControl(security);
            return true;
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException or PlatformNotSupportedException)
        {
            error = exception.Message;
            return false;
        }
    }

    private static bool IsNativeRootProtected(string nativeRoot)
    {
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }

        try
        {
            SecurityIdentifier currentUser = WindowsIdentity.GetCurrent().User
                ?? throw new UnauthorizedAccessException("无法确定当前用户。");
            var allowed = new HashSet<SecurityIdentifier>
            {
                currentUser,
                new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null),
                new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null),
            };
            const FileSystemRights writeRights =
                FileSystemRights.WriteData |
                FileSystemRights.AppendData |
                FileSystemRights.WriteAttributes |
                FileSystemRights.WriteExtendedAttributes |
                FileSystemRights.Delete |
                FileSystemRights.DeleteSubdirectoriesAndFiles |
                FileSystemRights.ChangePermissions |
                FileSystemRights.TakeOwnership |
                FileSystemRights.FullControl |
                FileSystemRights.Modify;
            DirectorySecurity security = new DirectoryInfo(nativeRoot).GetAccessControl();
            foreach (FileSystemAccessRule rule in security.GetAccessRules(true, true, typeof(SecurityIdentifier)))
            {
                if (rule.AccessControlType == AccessControlType.Allow &&
                    (rule.FileSystemRights & writeRights) != 0 &&
                    rule.IdentityReference is SecurityIdentifier identity &&
                    !allowed.Contains(identity))
                {
                    return false;
                }
            }

            return true;
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException or PlatformNotSupportedException)
        {
            return false;
        }
    }

    private void RefreshNativeRootFromHint()
    {
        if (!_refreshNativeRootFromHint)
        {
            return;
        }

        try
        {
            if (!File.Exists(_nativeHintFile))
            {
                return;
            }

            string configured = File.ReadAllText(_nativeHintFile).Trim();
            string? resolved = ResolveConfiguredNativeRoot(configured);
            if (resolved is not null)
            {
                NativeRoot = resolved;
            }
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            // Keep the last known valid path when the local hint cannot be read.
        }
    }

    private static string GetNativeHintFilePath() => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "LanAi.Workspace",
        "native-path.txt");

    private static IEnumerable<string> EnumerateFixedDriveNativeCandidates()
    {
        string[] relativeCandidates =
        [
            "edge_web__trans",
            "web_transform",
            "web-transform",
            Path.Combine("projects", "web_transform"),
            Path.Combine("workspace", "web_transform"),
            Path.Combine("work", "web_transform"),
        ];
        DriveInfo[] drives;
        try
        {
            drives = DriveInfo.GetDrives();
        }
        catch
        {
            yield break;
        }

        foreach (DriveInfo drive in drives)
        {
            bool usable;
            try
            {
                usable = drive.IsReady && drive.DriveType == DriveType.Fixed;
            }
            catch
            {
                usable = false;
            }
            if (!usable)
            {
                continue;
            }

            foreach (string relative in relativeCandidates)
            {
                yield return Path.Combine(drive.RootDirectory.FullName, relative);
            }
        }
    }

    private static void TryPersistDiscoveredNativeRoot(string path)
    {
        try
        {
            string hintFile = GetNativeHintFilePath();
            Directory.CreateDirectory(Path.GetDirectoryName(hintFile)!);
            File.WriteAllText(hintFile, path, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
        }
    }
}

internal sealed class LocalGatewayStatus
{
    public bool NativeMode { get; set; }
    public bool ControlAvailable { get; set; }
    public string NativeRoot { get; set; } = string.Empty;
    public bool DockerInstalled { get; set; }
    public bool DockerAvailable { get; set; }
    public bool WebReachable { get; set; }
    public string Summary { get; set; } = "未知";
    public string WebUrl { get; set; } = LocalGatewayService.DockerWebUrl;
    public string ComposeFile { get; set; } = string.Empty;
    public string CommandOutput { get; set; } = string.Empty;
    public LocalGatewayDiagnostics Diagnostics { get; set; } = new();
    public List<LocalGatewayServiceStatus> Services { get; } = [];
}

internal sealed class LocalGatewayServiceStatus
{
    public string Service { get; set; } = string.Empty;
    public string Name { get; set; } = string.Empty;
    public string State { get; set; } = string.Empty;
    public string Health { get; set; } = string.Empty;
    public string Status { get; set; } = string.Empty;
    public string Ports { get; set; } = string.Empty;

    public bool IsHealthyEnough =>
        string.Equals(State, "running", StringComparison.OrdinalIgnoreCase) &&
        (string.IsNullOrWhiteSpace(Health) || string.Equals(Health, "healthy", StringComparison.OrdinalIgnoreCase));
}

internal sealed class CommandResult
{
    public int ExitCode { get; set; }
    public string StdOut { get; set; } = string.Empty;
    public string StdErr { get; set; } = string.Empty;
    public bool Success => ExitCode == 0;
    public string CombinedOutput => string.Join(Environment.NewLine, new[] { StdOut, StdErr }.Where(x => !string.IsNullOrWhiteSpace(x))).Trim();
}

internal sealed class LocalGatewayDiagnostics
{
    public bool LoopbackHealthReachable { get; set; }
    public bool LocalhostHealthReachable { get; set; }
    public bool LanHealthReachable { get; set; }
    public bool HasLoopbackInterception { get; set; }
    public string LanHealthUrl { get; set; } = string.Empty;
    public List<LocalGatewayPortListener> PortListeners { get; } = [];
    public List<string> Messages { get; } = [];
}

internal sealed class LocalGatewayPortListener
{
    public string LocalAddress { get; set; } = string.Empty;
    public int LocalPort { get; set; }
    public int ProcessId { get; set; }
    public string ProcessName { get; set; } = string.Empty;
    public string ProcessPath { get; set; } = string.Empty;

    public string DisplayName =>
        ProcessId > 0
            ? $"{LocalAddress}:{LocalPort} -> {ProcessName}({ProcessId})"
            : $"{LocalAddress}:{LocalPort}";
}
