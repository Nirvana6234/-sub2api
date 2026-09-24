namespace LanAi.RelayClient.Platform;

/// <summary>How prominently a notification should read.</summary>
internal enum NotificationSeverity
{
    /// <summary>Something happened that the user may want to look at.</summary>
    Information,

    /// <summary>Something is going to stop working if ignored.</summary>
    Warning,
}

/// <summary>One notification to put in front of the user.</summary>
/// <param name="Title">The bold first line.</param>
/// <param name="Body">The message. Kept short — every platform truncates.</param>
/// <param name="Severity">Which icon the platform should use, where it has a choice.</param>
/// <param name="OnActivated">
/// Runs if the user clicks the notification. <b>May never run</b>, even when the user
/// does click — see <see cref="INotificationPresenter"/>.
/// </param>
internal readonly record struct NotificationRequest(
    string Title,
    string Body,
    NotificationSeverity Severity = NotificationSeverity.Information,
    Action? OnActivated = null);

/// <summary>
/// Shows a desktop notification outside the client's own window.
/// </summary>
/// <remarks>
/// <para>
/// Exists because the two platforms have nothing in common here. Windows wants
/// <c>Shell_NotifyIcon</c> with <c>NIF_INFO</c>; the macOS menu bar has no balloon at
/// all and needs <c>osascript</c> or <c>UNUserNotificationCenter</c>. Avalonia's
/// <c>TrayIcon</c> abstracts the icon and the menu but not this, so the split has to
/// live somewhere, and putting it here keeps the callers platform-blind.
/// </para>
/// <para>
/// <b>Every notification is best-effort, and that is part of the contract rather than
/// an implementation detail.</b> Windows suppresses balloons under focus assist or
/// when the user has switched notifications off for the app; macOS requires the user
/// to have granted permission. <see cref="NotificationRequest.OnActivated"/> is weaker
/// still — the macOS route has no click callback whatsoever, so it never runs there.
/// </para>
/// <para>
/// Callers must therefore treat this as an accelerator on top of something the user
/// can always find on their own. The unread badge on the bell and the balance on the
/// dashboard card are the real channels; a notification only shortens the path to
/// them. Nothing may depend on one having been seen.
/// </para>
/// </remarks>
internal interface INotificationPresenter : IDisposable
{
    /// <summary>Shows a notification, or does nothing if the platform will not.</summary>
    /// <remarks>Never throws: a failed notification is not worth a broken client.</remarks>
    void Show(NotificationRequest request);
}

/// <summary>The presenter for platforms with no route to a desktop notification.</summary>
/// <remarks>
/// Silent by design. The one thing it must not do is throw, because the alternative
/// to a missing notification is a crash in the middle of a balance check.
/// </remarks>
internal sealed class NoOpNotificationPresenter : INotificationPresenter
{
    public void Show(NotificationRequest request)
    {
        // Deliberately empty.
    }

    public void Dispose()
    {
        // Nothing to release.
    }
}
