using System.Runtime.CompilerServices;

namespace LanAi.Workspace.Wpf;

internal static class RuntimeEnvironmentBootstrap
{
    [ModuleInitializer]
    internal static void EnsureWindowsDirectoryEnvironment()
    {
        if (!string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable("windir")))
        {
            return;
        }

        var windowsDirectory = Environment.GetEnvironmentVariable("SystemRoot");
        if (string.IsNullOrWhiteSpace(windowsDirectory))
        {
            windowsDirectory = Environment.GetFolderPath(Environment.SpecialFolder.Windows);
        }

        if (!string.IsNullOrWhiteSpace(windowsDirectory))
        {
            Environment.SetEnvironmentVariable("windir", windowsDirectory, EnvironmentVariableTarget.Process);
        }
    }
}
