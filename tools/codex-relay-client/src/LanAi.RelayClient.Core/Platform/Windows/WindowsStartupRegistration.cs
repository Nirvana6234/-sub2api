using System.Runtime.Versioning;
using Microsoft.Win32;

namespace LanAi.RelayClient.Services;

/// <summary>Manages the current user's Windows startup entry without elevation.</summary>
/// <remarks>
/// Moved out of the WPF head so both heads can reach it. <c>Microsoft.Win32.Registry</c>
/// needs no package reference on plain <c>net8.0</c> — it is in the shared framework,
/// and an <c>osx-arm64</c> publish succeeds — so only the calls are Windows-only, which
/// the attribute below states rather than leaves to a runtime surprise.
/// </remarks>
[SupportedOSPlatform("windows")]
internal sealed class WindowsStartupRegistration : IStartupRegistration
{
    private const string RunSubKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string PreferenceSubKey = @"Software\Gongfei\ChatGPTAssistant";
    private const string RunValueName = "Gongfei-ChatGPT-Assistant";
    private const string PreferenceValueName = "StartWithWindows";

    private readonly Func<string?> _processPath;

    public WindowsStartupRegistration(Func<string?>? processPath = null)
    {
        _processPath = processPath ?? (() => Environment.ProcessPath);
    }

    public bool EnsureDefaultEnabled()
    {
        try
        {
            using RegistryKey? preferences = Registry.CurrentUser.OpenSubKey(PreferenceSubKey);
            string? preference = preferences?.GetValue(PreferenceValueName) as string;
            return StartupRegistrationPolicy.DefaultEnabled(preference) && SetEnabled(true);
        }
        catch (Exception ex) when (IsRegistryFailure(ex))
        {
            ClientLog.Warning("无法读取系统启动偏好", ex);
            return false;
        }
    }

    public bool SetEnabled(bool enabled)
    {
        try
        {
            using RegistryKey preferences = Registry.CurrentUser.CreateSubKey(PreferenceSubKey, writable: true)
                ?? throw new IOException("无法创建启动偏好注册表项。");
            preferences.SetValue(PreferenceValueName, enabled ? "enabled" : "disabled", RegistryValueKind.String);

            using RegistryKey run = Registry.CurrentUser.CreateSubKey(RunSubKey, writable: true)
                ?? throw new IOException("无法创建系统启动注册表项。");
            if (enabled)
            {
                string executablePath = _processPath()?.Trim() ?? string.Empty;
                if (string.IsNullOrWhiteSpace(executablePath))
                {
                    throw new InvalidOperationException("当前进程没有可用的 EXE 路径。");
                }

                run.SetValue(RunValueName, StartupRegistrationPolicy.CommandFor(executablePath), RegistryValueKind.String);
            }
            else
            {
                run.DeleteValue(RunValueName, throwOnMissingValue: false);
            }

            return true;
        }
        catch (Exception ex) when (IsRegistryFailure(ex))
        {
            ClientLog.Warning("无法更新系统启动设置", ex);
            return false;
        }
    }

    private static bool IsRegistryFailure(Exception exception) =>
        exception is IOException or UnauthorizedAccessException or System.Security.SecurityException or InvalidOperationException;
}
