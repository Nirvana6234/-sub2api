using System.ComponentModel;
using System.Diagnostics;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>Opens a URL in the user's default browser.</summary>
/// <remarks>
/// <para>
/// Portable as written, which is not obvious: <see cref="ProcessStartInfo.UseShellExecute"/>
/// is not a Windows-only switch. On macOS the runtime turns it into a call to
/// <c>/usr/bin/open</c>, which is precisely what is wanted, so this needs no platform
/// branch at all.
/// </para>
/// <para>
/// Returns false rather than throwing. Every caller here is a click handler, and a
/// browser that will not open should leave the user with the address in front of them,
/// not tear down the window they were using.
/// </para>
/// </remarks>
internal static class BrowserLauncher
{
    public static bool TryOpen(Uri uri)
    {
        ArgumentNullException.ThrowIfNull(uri);

        try
        {
            Process.Start(new ProcessStartInfo(uri.ToString()) { UseShellExecute = true });
            return true;
        }
        catch (Exception ex) when (ex is Win32Exception
            or InvalidOperationException
            or PlatformNotSupportedException
            or System.IO.FileNotFoundException)
        {
            ClientLog.Warning("无法打开浏览器：" + uri, ex);
            return false;
        }
    }

    /// <summary>Opens a path relative to the configured relay, such as "forgot-password".</summary>
    public static bool TryOpenRelayPage(string relativePath) =>
        TryOpen(new Uri(new Uri(ClientOptions.ServerAddress), relativePath));
}
