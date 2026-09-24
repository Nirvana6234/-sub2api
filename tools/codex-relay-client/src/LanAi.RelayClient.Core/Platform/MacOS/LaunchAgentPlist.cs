namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Builds the property list macOS reads to start the client at login.
/// </summary>
/// <remarks>
/// <para>
/// The macOS counterpart to the Windows <c>HKCU\…\Run</c> value. Kept as a pure
/// string function, separate from the file write, because this is the half that can
/// be wrong in ways a Mac would only reveal at the next login — and the half that can
/// be tested anywhere.
/// </para>
/// <para>
/// The escaping matters more than it looks. A plist is XML, and an application path
/// contains whatever the user named their disk and home folder. An unescaped
/// <c>&amp;</c> in a volume name produces a malformed plist, <c>launchd</c> ignores
/// the file entirely, and autostart never happens — with the client's own checkbox
/// still showing "enabled".
/// </para>
/// </remarks>
internal static class LaunchAgentPlist
{
    /// <summary>
    /// Reverse-DNS identifier for the agent, and the plist's file name.
    /// </summary>
    /// <remarks>
    /// Must match the bundle identifier in Info.plist. <c>launchctl</c> keys agents by
    /// label, so changing it orphans the previous agent rather than replacing it: the
    /// old plist keeps launching the old path, and the user ends up with two copies
    /// starting at login.
    /// </remarks>
    public const string Label = "com.gongfeiai.chatgpt-assistant";

    public static string FileName => Label + ".plist";

    /// <summary>Builds the plist that launches <paramref name="executablePath"/> at login.</summary>
    /// <param name="executablePath">
    /// Absolute path to the executable <i>inside</i> the bundle, not the <c>.app</c>
    /// directory. <c>launchd</c> execs this directly instead of going through
    /// <c>open</c>, so a path to the bundle itself would start nothing.
    /// </param>
    public static string Build(string executablePath)
    {
        if (string.IsNullOrWhiteSpace(executablePath))
        {
            throw new ArgumentException("An executable path is required.", nameof(executablePath));
        }

        return $"""
            <?xml version="1.0" encoding="UTF-8"?>
            <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
            <plist version="1.0">
            <dict>
                <key>Label</key>
                <string>{Escape(Label)}</string>
                <key>ProgramArguments</key>
                <array>
                    <string>{Escape(executablePath)}</string>
                </array>
                <key>RunAtLoad</key>
                <true/>
            </dict>
            </plist>

            """.ReplaceLineEndings("\n");
    }

    /// <remarks>
    /// Three entities, not five: quotes and apostrophes need no escaping in element
    /// content, and rewriting them would only make the file harder to read. Ampersand
    /// is replaced first, otherwise it would escape the escapes.
    /// </remarks>
    private static string Escape(string value) => value
        .Replace("&", "&amp;", StringComparison.Ordinal)
        .Replace("<", "&lt;", StringComparison.Ordinal)
        .Replace(">", "&gt;", StringComparison.Ordinal);
}
