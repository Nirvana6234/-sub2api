namespace AiSwitchGui;

internal sealed class ConfigPaths
{
    private readonly string _userProfile;
    private readonly string _localAppData;

    public ConfigPaths(string root, string? userProfile = null, string? localAppData = null)
    {
        Root = root;
        _userProfile = string.IsNullOrWhiteSpace(userProfile)
            ? Environment.GetFolderPath(Environment.SpecialFolder.UserProfile)
            : Path.GetFullPath(userProfile);
        _localAppData = string.IsNullOrWhiteSpace(localAppData)
            ? Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData)
            : Path.GetFullPath(localAppData);
        ProfilesFile = Path.Combine(root, "profiles.json");
        SettingsFile = Path.Combine(root, "appsettings.json");
        BackupRoot = Path.Combine(root, "backups");
        FallbackBackupRoot = Path.Combine(_localAppData, "LanAi.Workspace", "Backups");
    }

    public string Root { get; }
    public string ProfilesFile { get; }
    public string SettingsFile { get; }
    public string BackupRoot { get; }
    public string FallbackBackupRoot { get; }

    public string ClaudeGptRoutingStatePath => Path.Combine(
        _localAppData,
        "LanAi.Workspace",
        "Auth",
        "claude-gpt-routing.bin");

    public string CodexClaudeRoutingStatePath => Path.Combine(
        _localAppData,
        "LanAi.Workspace",
        "Auth",
        "codex-claude-routing.bin");

    public string CrossClientRoutingStatePath => Path.Combine(
        _localAppData,
        "LanAi.Workspace",
        "Auth",
        "cross-client-routing.bin");

    public string ApplicationSessionStatePath => Path.Combine(
        _localAppData,
        "LanAi.Workspace",
        "Auth",
        "client-session-snapshot.bin");

    public string CodexConfigPath => Path.Combine(
        _userProfile,
        ".codex",
        "config.toml");

    public string CodexAuthPath => Path.Combine(
        _userProfile,
        ".codex",
        "auth.json");

    public string ClaudeSettingsPath => Path.Combine(
        _userProfile,
        ".claude",
        "settings.json");

    public string GeminiSettingsPath => Path.Combine(
        _userProfile,
        ".gemini",
        "settings.json");

    public string GrokConfigPath => Path.Combine(
        _userProfile,
        ".grok",
        "config.toml");

    public string VsCodeUserSettingsPath => Path.Combine(
        Path.Combine(_userProfile, "AppData", "Roaming"),
        "Code",
        "User",
        "settings.json");
}

