using LanAi.RelayClient.CodexBinding.DesktopSync;

namespace LanAi.RelayClient.DesktopSync;

/// <summary>
/// The status the assistant reports when it connects; the server shows it to the phone
/// in the device list as-is.
/// </summary>
internal static class DesktopSyncHello
{
    public static byte[] Write(AppToolsCapabilities capabilities, string clientVersion) => SyncJson.Write(w =>
    {
        w.WriteBoolean("desktop_running", capabilities.Any);
        w.WriteBoolean("can_send", capabilities.CanSend);
        w.WriteString("client_version", clientVersion);
    });
}
