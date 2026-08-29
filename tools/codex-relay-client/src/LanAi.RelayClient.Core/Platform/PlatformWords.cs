namespace LanAi.RelayClient.Platform;

/// <summary>
/// The handful of words that name a different thing on each platform.
/// </summary>
/// <remarks>
/// <para>
/// Only one so far, and it earns its place: the client tells the user to leave it
/// running and points at 托盘 — a Windows notification area. macOS has no tray; the
/// same icon lives in the 菜单栏 at the top of the screen. A Mac user told to look in
/// the tray is being sent somewhere that does not exist, in the one message whose
/// whole purpose is to explain where the window went.
/// </para>
/// <para>
/// A property rather than a compile-time constant because both heads build from one
/// source and the same assembly runs on both targets.
/// </para>
/// </remarks>
internal static class PlatformWords
{
    /// <summary>What the notification area is called here: 托盘 or 菜单栏.</summary>
    public static string NotificationArea => Resolve(OperatingSystem.IsMacOS());

    /// <remarks>Split out so both answers are covered by tests from either platform.</remarks>
    internal static string Resolve(bool isMacOS) => isMacOS ? "菜单栏" : "托盘";
}
