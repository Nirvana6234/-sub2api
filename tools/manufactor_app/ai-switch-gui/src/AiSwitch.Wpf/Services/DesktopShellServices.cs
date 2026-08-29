using System.Diagnostics;
using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.Win32;
using Forms = System.Windows.Forms;

namespace LanAi.Workspace.Wpf.Services;

public interface IWindowsStartupRegistrationService
{
    bool IsEnabled();

    void SetEnabled(bool enabled);
}

public sealed class WindowsStartupRegistrationService : IWindowsStartupRegistrationService
{
    private const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "LanAiWorkspace";

    public bool IsEnabled()
    {
        using RegistryKey? key = Registry.CurrentUser.OpenSubKey(RunKey, writable: false);
        return key?.GetValue(ValueName) is string value &&
               string.Equals(value, CreateCommand(), StringComparison.OrdinalIgnoreCase);
    }

    public void SetEnabled(bool enabled)
    {
        using RegistryKey key = Registry.CurrentUser.CreateSubKey(RunKey, writable: true)
                                ?? throw new InvalidOperationException("无法打开 Windows 启动项注册表。");
        if (enabled)
        {
            key.SetValue(ValueName, CreateCommand(), RegistryValueKind.String);
        }
        else
        {
            key.DeleteValue(ValueName, throwOnMissingValue: false);
        }
    }

    private static string CreateCommand()
    {
        string executable = Environment.ProcessPath
                            ?? throw new InvalidOperationException("无法确定工作台程序路径。");
        return $"\"{Path.GetFullPath(executable)}\" --background";
    }
}

public sealed class WorkspaceTrayIconService : IDisposable
{
    private readonly Forms.NotifyIcon _icon;
    private bool _disposed;

    public WorkspaceTrayIconService(
        Action openWorkspace,
        Action openConnections,
        Action openServiceControl,
        Action exit)
    {
        ArgumentNullException.ThrowIfNull(openWorkspace);
        ArgumentNullException.ThrowIfNull(openConnections);
        ArgumentNullException.ThrowIfNull(openServiceControl);
        ArgumentNullException.ThrowIfNull(exit);

        var menu = new Forms.ContextMenuStrip();
        menu.Items.Add("打开共飞AI工作台", null, (_, _) => openWorkspace());
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add("连接中心", null, (_, _) => openConnections());
        menu.Items.Add("中转服务", null, (_, _) => openServiceControl());
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add("退出", null, (_, _) => exit());

        _icon = new Forms.NotifyIcon
        {
            Text = "共飞AI工作台",
            ContextMenuStrip = menu,
            Icon = TryLoadIcon(),
            Visible = true,
        };
        _icon.DoubleClick += (_, _) => openWorkspace();
    }

    public void ShowMinimizedNotice()
    {
        if (_disposed) return;
        _icon.BalloonTipTitle = "共飞AI工作台仍在运行";
        _icon.BalloonTipText = "双击托盘图标可重新打开。";
        _icon.BalloonTipIcon = Forms.ToolTipIcon.Info;
        _icon.ShowBalloonTip(2000);
    }

    private static System.Drawing.Icon TryLoadIcon()
    {
        try
        {
            string? executable = Environment.ProcessPath;
            if (!string.IsNullOrWhiteSpace(executable) && File.Exists(executable))
            {
                return System.Drawing.Icon.ExtractAssociatedIcon(executable)
                       ?? System.Drawing.SystemIcons.Application;
            }
        }
        catch
        {
            // Fall back to the system application icon.
        }

        return System.Drawing.SystemIcons.Application;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _icon.Visible = false;
        _icon.ContextMenuStrip?.Dispose();
        _icon.Dispose();
    }
}

public sealed record AppUpdateManifest(
    string Product,
    string Version,
    string PackageUrl,
    string Sha256,
    string? ReleaseNotes = null);

public sealed record AppUpdateCheckResult(
    bool HasUpdate,
    Version CurrentVersion,
    Version LatestVersion,
    AppUpdateManifest? Manifest,
    string Message);

/// <summary>
/// Manifest updater intentionally stops at a verified download. It never
/// executes an unverified package or silently replaces the running binary.
/// </summary>
public sealed class ApplicationUpdateService : IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
    };

    private readonly HttpClient _client;
    private readonly string _updatesDirectory;
    private bool _disposed;

    public ApplicationUpdateService(string updatesDirectory, HttpMessageHandler? handler = null)
    {
        _updatesDirectory = Path.GetFullPath(updatesDirectory);
        _client = handler is null ? new HttpClient() : new HttpClient(handler, disposeHandler: true);
        _client.Timeout = TimeSpan.FromSeconds(15);
        _client.DefaultRequestHeaders.UserAgent.ParseAdd("LanAiWorkspace/1.0");
    }

    public async Task<AppUpdateCheckResult> CheckAsync(string manifestUrl, CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        Uri uri = RequireHttps(manifestUrl, "更新清单");
        string json = await _client.GetStringAsync(uri, cancellationToken).ConfigureAwait(false);
        AppUpdateManifest manifest = JsonSerializer.Deserialize<AppUpdateManifest>(json, JsonOptions)
                                     ?? throw new InvalidDataException("更新清单内容为空。");
        if (!string.Equals(manifest.Product, "LanAi.Workspace", StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidDataException("更新清单不属于共飞AI工作台。");
        }

        _ = RequireHttps(manifest.PackageUrl, "更新包");
        if (manifest.Sha256.Length != 64 || !manifest.Sha256.All(Uri.IsHexDigit))
        {
            throw new InvalidDataException("更新清单缺少有效的 SHA-256。");
        }

        Version current = Assembly.GetEntryAssembly()?.GetName().Version ?? new Version(0, 0, 0, 0);
        if (!Version.TryParse(manifest.Version, out Version? latest))
        {
            throw new InvalidDataException("更新清单版本号无效。");
        }

        bool hasUpdate = latest > current;
        return new AppUpdateCheckResult(
            hasUpdate,
            current,
            latest,
            manifest,
            hasUpdate ? $"发现新版本 {latest}" : $"当前已是最新版 {current}");
    }

    public async Task<string> DownloadVerifiedAsync(AppUpdateManifest manifest, CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        ArgumentNullException.ThrowIfNull(manifest);
        Uri packageUri = RequireHttps(manifest.PackageUrl, "更新包");
        Directory.CreateDirectory(_updatesDirectory);
        string fileName = Path.GetFileName(packageUri.LocalPath);
        if (string.IsNullOrWhiteSpace(fileName)) fileName = $"LanAi.Workspace-{manifest.Version}.zip";
        string destination = Path.Combine(_updatesDirectory, fileName);
        string temporary = $"{destination}.{Guid.NewGuid():N}.download";
        try
        {
            await using Stream source = await _client.GetStreamAsync(packageUri, cancellationToken).ConfigureAwait(false);
            await using (FileStream target = new(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None, 64 * 1024, true))
            {
                await source.CopyToAsync(target, cancellationToken).ConfigureAwait(false);
                await target.FlushAsync(cancellationToken).ConfigureAwait(false);
            }

            string actual;
            await using (FileStream verify = File.OpenRead(temporary))
            {
                actual = Convert.ToHexString(await SHA256.HashDataAsync(verify, cancellationToken).ConfigureAwait(false));
            }
            if (!string.Equals(actual, manifest.Sha256, StringComparison.OrdinalIgnoreCase))
            {
                throw new InvalidDataException("更新包 SHA-256 校验失败，文件已丢弃。");
            }

            File.Move(temporary, destination, overwrite: true);
            return destination;
        }
        finally
        {
            if (File.Exists(temporary)) File.Delete(temporary);
        }
    }

    private static Uri RequireHttps(string value, string label)
    {
        if (!Uri.TryCreate(value, UriKind.Absolute, out Uri? uri) || uri.Scheme != Uri.UriSchemeHttps)
        {
            throw new InvalidOperationException($"{label}必须使用 HTTPS。 ");
        }

        return uri;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _client.Dispose();
    }
}
