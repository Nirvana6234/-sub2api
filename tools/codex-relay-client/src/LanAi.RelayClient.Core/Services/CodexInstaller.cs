using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Text.Json;

namespace LanAi.RelayClient.Services;

internal interface ICodexInstaller
{
    CodexInstallerInspection Inspect();

    CodexInstallerResult Launch();

    Task<CodexInstallerResult> EnsureAndLaunchAsync(
        IProgress<CodexDownloadProgress>? progress = null,
        CancellationToken cancellationToken = default);
}

internal sealed record CodexInstallerInspection(
    bool DirectoryExists,
    string DirectoryPath,
    string? PackagePath,
    string Message)
{
    public bool PackageAvailable => !string.IsNullOrWhiteSpace(PackagePath);
}

internal sealed record CodexInstallerResult(bool Started, string Message, Process? InstallerProcess = null);

internal sealed record CodexDownloadProgress(long BytesReceived, long? TotalBytes)
{
    public int? Percent => TotalBytes is > 0
        ? (int)Math.Min(100, BytesReceived * 100 / TotalBytes.Value)
        : null;
}

internal interface IProcessLauncher
{
    Process? Start(ProcessStartInfo startInfo);
}

internal sealed class ShellProcessLauncher : IProcessLauncher
{
    public Process? Start(ProcessStartInfo startInfo)
    {
        return Process.Start(startInfo);
    }
}

/// <summary>Locates and opens a user-supplied ChatGPT desktop installer.</summary>
/// <remarks>
/// Which package it looks for, and which one it downloads, come from
/// <see cref="CodexPackageProfile"/> rather than being compiled in. They used to be
/// <c>win-x64</c> constants, which meant a Mac downloaded a Windows <c>.msix</c>
/// successfully and then could not open it.
/// </remarks>
internal sealed class CodexInstaller : ICodexInstaller
{
    private readonly string _directoryPath;
    private readonly IProcessLauncher _processLauncher;
    private readonly HttpClient _httpClient;
    private readonly CodexPackageProfile? _profile;
    private readonly Uri? _downloadUri;

    public CodexInstaller(
        string? directoryPath = null,
        IProcessLauncher? processLauncher = null,
        HttpClient? httpClient = null,
        Uri? downloadUri = null,
        CodexPackageProfile? profile = null)
    {
        _directoryPath = Path.GetFullPath(directoryPath ?? Path.Combine(AppContext.BaseDirectory, "codex-installer"));
        _processLauncher = processLauncher ?? new ShellProcessLauncher();
        _httpClient = httpClient ?? new HttpClient();
        _profile = profile ?? CodexPackageProfile.ForCurrentPlatform();
        _downloadUri = downloadUri ?? _profile?.DownloadUri;
    }

    /// <remarks>
    /// Falls back to the Windows set on an unsupported host so that a package the user
    /// placed by hand is still recognised. Recognising a file is harmless; downloading
    /// the wrong one is not, and that path is refused separately.
    /// </remarks>
    private IReadOnlySet<string> SupportedExtensions =>
        _profile?.SupportedExtensions ?? WindowsFallbackExtensions;

    private static readonly IReadOnlySet<string> WindowsFallbackExtensions =
        new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            ".exe",
            ".msi",
            ".msix",
            ".appx",
            ".msixbundle",
            ".appxbundle",
        };

    public CodexInstallerInspection Inspect()
    {
        try
        {
            if (!Directory.Exists(_directoryPath))
            {
                string? rememberedPackage = ReadRememberedPackagePath();
                if (rememberedPackage is not null)
                {
                    return new CodexInstallerInspection(
                        false,
                        _directoryPath,
                        rememberedPackage,
                        $"已找到记录的安装包：{Path.GetFileName(rememberedPackage)}");
                }

                return new CodexInstallerInspection(
                    false,
                    _directoryPath,
                    null,
                    "还没有准备 ChatGPT 安装包目录。");
            }

            string? package = Directory
                .EnumerateFiles(_directoryPath, "*", SearchOption.TopDirectoryOnly)
                .Where(path => SupportedExtensions.Contains(Path.GetExtension(path)))
                .OrderByDescending(File.GetLastWriteTimeUtc)
                .ThenBy(Path.GetFileName, StringComparer.OrdinalIgnoreCase)
                .FirstOrDefault();

            package ??= ReadRememberedPackagePath();

            return package is null
                ? new CodexInstallerInspection(
                    true,
                    _directoryPath,
                    null,
                    $"请把 ChatGPT 桌面版安装包复制到：{_directoryPath}")
                : new CodexInstallerInspection(
                    true,
                    _directoryPath,
                    package,
                    $"已找到安装包：{Path.GetFileName(package)}");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("读取 ChatGPT 安装包目录失败", ex);
            return new CodexInstallerInspection(
                Directory.Exists(_directoryPath),
                _directoryPath,
                null,
                $"无法读取 ChatGPT 安装包目录：{_directoryPath}");
        }
    }

    private string? ReadRememberedPackagePath()
    {
        try
        {
            string settingsPath = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
                "Gongfei",
                "ChatGPTAssistant",
                "installer-settings.json");
            if (!File.Exists(settingsPath))
            {
                return null;
            }

            using JsonDocument document = JsonDocument.Parse(File.ReadAllText(settingsPath));
            if (!document.RootElement.TryGetProperty("chatGptPackagePath", out JsonElement value))
            {
                return null;
            }

            string? path = value.GetString();
            return !string.IsNullOrWhiteSpace(path)
                && File.Exists(path)
                && SupportedExtensions.Contains(Path.GetExtension(path))
                ? path
                : null;
        }
        catch (JsonException)
        {
            return null;
        }
        catch (IOException)
        {
            return null;
        }
        catch (UnauthorizedAccessException)
        {
            return null;
        }
    }

    public CodexInstallerResult Launch()
    {
        CodexInstallerInspection inspection = Inspect();
        try
        {
            if (!inspection.PackageAvailable)
            {
                Directory.CreateDirectory(_directoryPath);
                _processLauncher.Start(new ProcessStartInfo(_directoryPath) { UseShellExecute = true });
                return new CodexInstallerResult(false, $"请把 ChatGPT 安装包复制到：{_directoryPath}");
            }

            string packagePath = inspection.PackagePath!;

            // msiexec exists only on Windows; on macOS UseShellExecute maps to
            // /usr/bin/open, which mounts a .dmg and shows it in Finder.
            ProcessStartInfo startInfo = OperatingSystem.IsWindows() && string.Equals(
                Path.GetExtension(packagePath),
                ".msi",
                StringComparison.OrdinalIgnoreCase)
                ? new ProcessStartInfo("msiexec.exe")
                {
                    Arguments = $"/i \"{packagePath}\" /qn /norestart",
                    UseShellExecute = true,
                }
                : new ProcessStartInfo(packagePath) { UseShellExecute = true };

            Process? installerProcess = _processLauncher.Start(startInfo);
            ClientLog.Info($"已拉起 ChatGPT 安装包：{Path.GetFileName(packagePath)}");

            // A .dmg does not install anything by opening it — it mounts, and the user
            // still has to drag the app across. Saying "按安装向导完成安装" there would
            // leave them waiting for a wizard that never appears.
            string message = _profile?.IsMac == true
                ? $"已打开 {Path.GetFileName(packagePath)}，请把 ChatGPT 拖入「应用程序」文件夹完成安装。"
                : $"已打开 {Path.GetFileName(packagePath)}，请按安装向导完成安装。";

            return new CodexInstallerResult(true, message, installerProcess);
        }
        catch (Exception ex) when (
            ex is IOException or UnauthorizedAccessException or Win32Exception or InvalidOperationException)
        {
            ClientLog.Error("拉起 ChatGPT 安装包失败", ex);
            return new CodexInstallerResult(false, "无法打开 ChatGPT 安装包，请检查文件是否完整。详情见客户端日志。");
        }
    }

    public async Task<CodexInstallerResult> EnsureAndLaunchAsync(
        IProgress<CodexDownloadProgress>? progress = null,
        CancellationToken cancellationToken = default)
    {
        if (Inspect().PackageAvailable)
        {
            return Launch();
        }

        if (_downloadUri is null || _profile is null)
        {
            // Refused rather than defaulted. The default that used to be here sent
            // every non-Windows host a Windows .msix, which downloads fine and then
            // does nothing — a far harder failure to report than this one.
            ClientLog.Warning("当前系统没有对应的 ChatGPT 安装包下载地址");
            return new CodexInstallerResult(
                false,
                $"当前系统暂不支持自动下载 ChatGPT，请手动下载安装包并放到：{_directoryPath}");
        }

        string packagePath = Path.Combine(_directoryPath, _profile.DownloadFileName);
        string partialPath = packagePath + ".part";
        try
        {
            Directory.CreateDirectory(_directoryPath);
            DeletePartialDownload(partialPath);

            using var request = new HttpRequestMessage(HttpMethod.Get, _downloadUri);
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
            response.EnsureSuccessStatusCode();

            packagePath = Path.Combine(_directoryPath, DownloadFileNameFrom(response));
            partialPath = packagePath + ".part";
            DeletePartialDownload(partialPath);

            long? totalBytes = response.Content.Headers.ContentLength;
            long bytesReceived = 0;
            progress?.Report(new CodexDownloadProgress(bytesReceived, totalBytes));

            await using (Stream source = await response.Content
                .ReadAsStreamAsync(cancellationToken)
                .ConfigureAwait(false))
            {
                await using var destination = new FileStream(
                    partialPath,
                    FileMode.Create,
                    FileAccess.Write,
                    FileShare.None,
                    bufferSize: 81920,
                    useAsync: true);
                byte[] buffer = new byte[81920];
                while (true)
                {
                    int read = await source.ReadAsync(buffer, cancellationToken).ConfigureAwait(false);
                    if (read == 0)
                    {
                        break;
                    }

                    await destination.WriteAsync(buffer.AsMemory(0, read), cancellationToken).ConfigureAwait(false);
                    bytesReceived += read;
                    progress?.Report(new CodexDownloadProgress(bytesReceived, totalBytes));
                }

                await destination.FlushAsync(cancellationToken).ConfigureAwait(false);
            }
            if (!File.Exists(packagePath))
            {
                File.Move(partialPath, packagePath);
            }
            else
            {
                DeletePartialDownload(partialPath);
            }

            progress?.Report(new CodexDownloadProgress(totalBytes ?? bytesReceived, totalBytes ?? bytesReceived));
            ClientLog.Info($"ChatGPT 安装包下载完成：{Path.GetFileName(packagePath)}");
            return Launch();
        }
        catch (OperationCanceledException)
        {
            DeletePartialDownload(partialPath);
            throw;
        }
        catch (Exception ex) when (ex is HttpRequestException or IOException or UnauthorizedAccessException)
        {
            DeletePartialDownload(partialPath);
            ClientLog.Error("下载 ChatGPT 安装包失败", ex);
            return new CodexInstallerResult(false, "下载 ChatGPT 安装包失败，请检查网络后重试。");
        }
    }

    private string DownloadFileNameFrom(HttpResponseMessage response)
    {
        string fallback = _profile?.DownloadFileName ?? "ChatGPT-Windows-x64.msix";
        string? supplied = response.Content.Headers.ContentDisposition?.FileNameStar
            ?? response.Content.Headers.ContentDisposition?.FileName;
        string candidate = Path.GetFileName((supplied ?? fallback).Trim('"'));
        return !string.IsNullOrWhiteSpace(candidate) &&
            SupportedExtensions.Contains(Path.GetExtension(candidate))
            ? candidate
            : fallback;
    }

    private static void DeletePartialDownload(string partialPath)
    {
        if (File.Exists(partialPath))
        {
            File.Delete(partialPath);
        }
    }
}
