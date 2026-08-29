using System.Text;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Builds the AppleScript that shows one macOS notification.
/// </summary>
/// <remarks>
/// <para>
/// Separate from the presenter so it can be tested on Windows. The presenter itself
/// cannot be — it needs a Mac — but this is the half that can actually be wrong in a
/// damaging way, and it is pure string work.
/// </para>
/// <para>
/// <b>Escaping here is a security boundary, not a formatting nicety.</b> Announcement
/// titles are written by the relay operator and arrive over the network, and they land
/// inside an AppleScript string literal. An unescaped quotation mark would end the
/// literal and let whatever followed be parsed as script — with the user's own
/// privileges, on their own machine. Both the escape character and the quotation mark
/// are escaped, in that order, and the script is handed to <c>osascript</c> as a
/// process argument rather than through a shell, so there is no second layer to get
/// wrong.
/// </para>
/// </remarks>
internal static class AppleScriptNotification
{
    /// <summary>Composes a <c>display notification</c> statement.</summary>
    public static string Compose(string title, string body)
    {
        var script = new StringBuilder();
        script.Append("display notification \"").Append(Escape(body)).Append('"');
        script.Append(" with title \"").Append(Escape(title)).Append('"');
        return script.ToString();
    }

    /// <remarks>
    /// The backslash must be replaced first. Doing the quotation mark first would let
    /// the backslash pass introduced by that replacement be escaped a second time,
    /// turning <c>"</c> into <c>\\"</c> — a literal backslash followed by an
    /// unescaped quotation mark, which is the exact hole this is here to close.
    /// </remarks>
    private static string Escape(string? value) =>
        (value ?? string.Empty).Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal)
            .Replace("\r", " ", StringComparison.Ordinal)
            .Replace("\n", " ", StringComparison.Ordinal);
}
