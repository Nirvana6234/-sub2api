using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Platform.Windows;

namespace LanAi.RelayClient.Platform;

/// <summary>Picks the desktop-notification mechanism for this platform.</summary>
/// <remarks>
/// <para>
/// The fourth of these factories, alongside <see cref="SingleInstance"/>,
/// <see cref="SecureStorage"/> and <see cref="StartupRegistrations"/>.
/// </para>
/// <para>
/// It follows the <see cref="StartupRegistrations"/> rule rather than the
/// <see cref="SecureStorage"/> one: an unknown platform gets
/// <see cref="NoOpNotificationPresenter"/> instead of an exception. Not being able to
/// store a session means credentials would have to go somewhere unsafe; not being able
/// to show a notification means the user reads the same thing off the dashboard a
/// minute later.
/// </para>
/// <para>
/// <b>Create this on the UI thread.</b> The Windows implementation owns a window whose
/// procedure receives the click callback, and that procedure runs on whichever thread
/// created it. Constructing it elsewhere would show notifications correctly and drop
/// every click, which is the kind of half-working nobody reports.
/// </para>
/// </remarks>
internal static class NotificationPresenters
{
    public static INotificationPresenter Create()
    {
        if (OperatingSystem.IsWindows())
        {
            return new ShellNotificationPresenter();
        }

        if (OperatingSystem.IsMacOS())
        {
            return new OsaScriptNotificationPresenter();
        }

        return new NoOpNotificationPresenter();
    }
}
