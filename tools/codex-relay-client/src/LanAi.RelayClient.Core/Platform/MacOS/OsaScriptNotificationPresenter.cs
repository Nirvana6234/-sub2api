using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.Versioning;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// macOS desktop notifications, through <c>osascript</c>.
/// </summary>
/// <remarks>
/// <para>
/// <b>Written without a Mac to run it on.</b> The script text is covered by unit
/// tests; that <c>osascript</c> is present, that the notification is permitted, and
/// what the user is asked the first time are all unverified, and are the parts most
/// likely to need changing. It is wired up rather than left unreachable so the macOS
/// build exercises it the moment there is hardware to run it on.
/// </para>
/// <para>
/// <b>There is no click callback.</b> <c>display notification</c> shows a banner and
/// tells the caller nothing about it afterwards, so
/// <see cref="NotificationRequest.OnActivated"/> is never invoked here. That is a
/// permitted degradation under the interface's contract — the unread badge and the
/// balance card remain the ways to reach the same information — but it is the reason
/// nothing may be built that depends on the click arriving.
/// </para>
/// <para>
/// Sending an Apple Event requires the user's consent under TCC, and the consent
/// prompt names this client. A refusal is logged and otherwise ignored: a user who
/// declined notifications should not then be shown an error about notifications.
/// </para>
/// </remarks>
[SupportedOSPlatform("macos")]
internal sealed class OsaScriptNotificationPresenter : INotificationPresenter
{
    public void Show(NotificationRequest request)
    {
        try
        {
            var startInfo = new ProcessStartInfo("/usr/bin/osascript")
            {
                // ArgumentList, never a shell command line: the script contains
                // operator-authored text, and a shell would add a second quoting layer
                // for that text to escape from.
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardError = true,
            };

            startInfo.ArgumentList.Add("-e");
            startInfo.ArgumentList.Add(AppleScriptNotification.Compose(request.Title, request.Body));

            using Process? process = Process.Start(startInfo);

            // Not awaited and not waited on. The notification is fire-and-forget, and
            // blocking the UI thread on osascript would freeze the client if the TCC
            // prompt is sitting on screen waiting for the user.
            _ = process;
        }
        catch (Exception ex) when (ex is Win32Exception
            or InvalidOperationException
            or PlatformNotSupportedException
            or System.IO.FileNotFoundException)
        {
            ClientLog.Warning("桌面通知发送失败：" + request.Title, ex);
        }
    }

    public void Dispose()
    {
        // Nothing is held between notifications.
    }
}
