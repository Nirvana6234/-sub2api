using Avalonia;

namespace LanAi.RelayClient.App;

internal static class Program
{
    /// <remarks>
    /// STAThread is required on Windows for the clipboard and native dialogs, and is
    /// harmless on macOS — so it stays unconditional rather than becoming a platform
    /// branch in the one method that must never fail to run.
    /// </remarks>
    [STAThread]
    public static void Main(string[] args) =>
        BuildAvaloniaApp().StartWithClassicDesktopLifetime(args);

    /// <remarks>Also used by the Avalonia designer, which is why it is public.</remarks>
    public static AppBuilder BuildAvaloniaApp() =>
        AppBuilder.Configure<App>()
            .UsePlatformDetect()
            .LogToTrace();
}
