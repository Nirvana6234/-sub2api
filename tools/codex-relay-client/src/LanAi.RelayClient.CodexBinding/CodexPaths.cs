namespace LanAi.RelayClient.CodexBinding;

/// <summary>Where Codex keeps the two files this client writes.</summary>
/// <remarks>
/// Constructible with an explicit home so the first run of anything that writes
/// here can be exercised against a scratch directory rather than the user's live
/// configuration.
/// </remarks>
public sealed class CodexPaths
{
    public CodexPaths(string? codexHome = null) =>
        Home = codexHome ?? DefaultHome();

    public string Home { get; }

    public string ConfigPath => Path.Combine(Home, "config.toml");

    /// <summary>
    /// Holds the credential Codex sends upstream — and, for a signed-in user, the
    /// tokens of their ChatGPT account. Never replaced wholesale.
    /// </summary>
    public string AuthPath => Path.Combine(Home, "auth.json");

    internal static string DefaultHome() =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".codex");
}
