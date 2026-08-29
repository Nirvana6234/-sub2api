namespace LanAi.RelayClient.Platform;

/// <summary>
/// The one place that decides where this client keeps its files.
/// </summary>
/// <remarks>
/// <para>
/// Nine call sites used to ask <see cref="Environment.GetFolderPath"/> directly. That
/// is correct on Windows and quietly wrong on macOS: .NET maps
/// <see cref="Environment.SpecialFolder.LocalApplicationData"/> to
/// <c>~/.local/share</c> there — a Linux convention — rather than
/// <c>~/Library/Application Support</c>, where a Mac user and every backup tool
/// expect an application's data to live.
/// </para>
/// <para>
/// <b>The Windows branch must keep returning the historical path, byte for byte.</b>
/// Existing installations hold their session, install id, group preference and
/// announcement read-state under <c>%LOCALAPPDATA%\LanAi.RelayClient</c>. "Tidying
/// up" that folder name would not migrate those users — it would silently present
/// itself as a fresh install and sign every one of them out. The name therefore
/// stays as the assembly was originally called, product renames notwithstanding.
/// </para>
/// <para>
/// Note this is <i>not</i> the only directory the client owns:
/// <c>CodexInstaller</c> remembers a hand-placed package under Roaming
/// <c>%APPDATA%\Gongfei\ChatGPTAssistant</c>. That one is deliberately left where it
/// is; it is written by a different flow and shares nothing with the data below.
/// </para>
/// </remarks>
public static class AppPaths
{
    /// <summary>
    /// Historical folder name. Matches the assembly name, not the product name — see
    /// the remarks above before changing it.
    /// </summary>
    private const string Folder = "LanAi.RelayClient";

    /// <summary>Per-user data root, created on demand by the callers that write there.</summary>
    public static string Data { get; } = Path.Combine(Root(), Folder);

    public static string InData(params string[] segments) =>
        Path.Combine([Data, .. segments]);

    /// <summary>
    /// Where the untouched copy of the user's own Codex configuration is kept.
    /// </summary>
    /// <remarks>
    /// <para>
    /// <b>Two segments, not one.</b> Everything else lives under
    /// <c>LanAi.RelayClient</c>; this one has always been <c>LanAi\RelayClient</c>.
    /// The split is historical and is reproduced here deliberately rather than
    /// tidied away.
    /// </para>
    /// <para>
    /// This is the most consequential directory the client owns. It holds the
    /// snapshot of the user's original <c>config.toml</c> and <c>auth.json</c> —
    /// the copy that gets restored when the client releases Codex on exit. Point
    /// this somewhere new and the client can no longer put a user back on their own
    /// ChatGPT account. Nothing reports an error; they simply find, at some later
    /// point, that their own account no longer works and no way to undo it.
    /// </para>
    /// <para>
    /// Unifying the two roots is a migration — read the old location, move it, prove
    /// it moved — not a rename. Until someone does that work, this stays as it is.
    /// </para>
    /// </remarks>
    public static string CodexSnapshotRoot { get; } =
        Path.Combine(Root(), "LanAi", "RelayClient", "codex-snapshot");

    /// <summary>Pre-encryption copy of the user's original <c>auth.json</c>.</summary>
    /// <remarks>Unlike <see cref="CodexSnapshotRoot"/>, this one does sit under the
    /// single-segment root — another reason not to assume the two are related.</remarks>
    public static string CodexAuthSnapshotFile { get; } = InData("codex-auth-original.json");

    private static string Root()
    {
        if (OperatingSystem.IsMacOS())
        {
            // Resolved from HOME rather than SpecialFolder, because the framework's
            // LocalApplicationData is the Linux-shaped path on this platform.
            string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            return Path.Combine(home, "Library", "Application Support");
        }

        return Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
    }
}
