namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Centralizes per-user paths used by the new application. Constructing this
/// object is side-effect free; callers explicitly create writable directories.
/// </summary>
public sealed class AppDataPaths
{
    public AppDataPaths(string? userProfile = null, string? localAppData = null)
    {
        UserProfile = ResolveRequiredDirectory(
            userProfile,
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "user profile");

        LocalAppData = ResolveRequiredDirectory(
            localAppData,
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "local application data");

        AppDataRoot = Path.Combine(LocalAppData, "LanAi.Workspace");
        DatabaseDirectory = Path.Combine(AppDataRoot, "Data");
        DatabasePath = Path.Combine(DatabaseDirectory, "workspace.db");
        TelemetryDatabasePath = Path.Combine(DatabaseDirectory, "telemetry.db");
        OfficialUsageImportDatabasePath = Path.Combine(DatabaseDirectory, "official-usage-import.db");
        LogsDirectory = Path.Combine(AppDataRoot, "Logs");
        BackupsDirectory = Path.Combine(AppDataRoot, "Backups");
        UpdatesDirectory = Path.Combine(AppDataRoot, "Updates");
        ManagedSkillsDirectory = Path.Combine(AppDataRoot, "Skills");
        FeatureStatePath = Path.Combine(AppDataRoot, "workspace-features.json");
        DesktopSettingsPath = Path.Combine(AppDataRoot, "settings.json");
        CrossClientRoutingPresetsPath = Path.Combine(AppDataRoot, "cross-client-routing-presets.json");
        ApplicationResumeStatePath = Path.Combine(AppDataRoot, "application-resume-state.json");

        LegacyProfilesPath = Path.Combine(UserProfile, "ai-switch-gui", "profiles.json");
        CodexHome = Path.Combine(UserProfile, ".codex");
        ClaudeHome = Path.Combine(UserProfile, ".claude");
        GeminiHome = Path.Combine(UserProfile, ".gemini");
    }

    public string UserProfile { get; }

    public string LocalAppData { get; }

    public string AppDataRoot { get; }

    public string DatabaseDirectory { get; }

    public string DatabasePath { get; }

    /// <summary>
    /// Separate bounded store for privacy-safe local usage and health summaries.
    /// It is intentionally independent from project metadata so it can be
    /// retained and purged without touching workspace configuration.
    /// </summary>
    public string TelemetryDatabasePath { get; }

    /// <summary>
    /// Opaque, privacy-safe checkpoint state for importing exact usage counters
    /// from official Codex/Claude JSONL history.  It never stores transcript
    /// content, project paths, URLs, credentials, or raw native session IDs.
    /// </summary>
    public string OfficialUsageImportDatabasePath { get; }

    public string LogsDirectory { get; }

    public string BackupsDirectory { get; }

    public string UpdatesDirectory { get; }

    public string ManagedSkillsDirectory { get; }

    public string FeatureStatePath { get; }

    public string DesktopSettingsPath { get; }

    public string CrossClientRoutingPresetsPath { get; }

    /// <summary>
    /// Stores the last connection/routing state selected inside the workspace.
    /// It contains source identifiers and model names only; credentials remain
    /// in the dedicated credential store and are never copied into this file.
    /// </summary>
    public string ApplicationResumeStatePath { get; }

    public string LegacyProfilesPath { get; }

    public string CodexHome { get; }

    public string CodexSessionIndexPath => Path.Combine(CodexHome, "session_index.jsonl");

    public string CodexSessionsDirectory => Path.Combine(CodexHome, "sessions");

    public string CodexArchivedSessionsDirectory => Path.Combine(CodexHome, "archived_sessions");

    public string CodexConfigPath => Path.Combine(CodexHome, "config.toml");

    public string ClaudeHome { get; }

    public string ClaudeProjectsDirectory => Path.Combine(ClaudeHome, "projects");

    public string ClaudeConfigPath => Path.Combine(UserProfile, ".claude.json");

    public string ClaudePromptPath => Path.Combine(ClaudeHome, "CLAUDE.md");

    public string ClaudeSkillsDirectory => Path.Combine(ClaudeHome, "skills");

    public string GeminiHome { get; }

    public string GeminiProjectsDirectory => Path.Combine(GeminiHome, "tmp");

    public string GeminiConfigPath => Path.Combine(GeminiHome, "settings.json");

    public string GeminiPromptPath => Path.Combine(GeminiHome, "GEMINI.md");

    public string GeminiSkillsDirectory => Path.Combine(GeminiHome, "skills");

    public string CodexPromptPath => Path.Combine(CodexHome, "AGENTS.md");

    public string CodexSkillsDirectory => Path.Combine(CodexHome, "skills");

    public static AppDataPaths CreateDefault() => new();

    public void EnsureWritableDirectories()
    {
        Directory.CreateDirectory(AppDataRoot);
        Directory.CreateDirectory(DatabaseDirectory);
        Directory.CreateDirectory(LogsDirectory);
        Directory.CreateDirectory(BackupsDirectory);
        Directory.CreateDirectory(UpdatesDirectory);
        Directory.CreateDirectory(ManagedSkillsDirectory);
    }

    private static string ResolveRequiredDirectory(string? supplied, string fallback, string description)
    {
        var value = string.IsNullOrWhiteSpace(supplied) ? fallback : supplied;
        if (string.IsNullOrWhiteSpace(value))
        {
            throw new InvalidOperationException($"Unable to resolve the current {description} directory.");
        }

        return Path.GetFullPath(Environment.ExpandEnvironmentVariables(value));
    }
}
