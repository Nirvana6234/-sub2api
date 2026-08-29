namespace LanAi.Workspace.Core;

[Flags]
public enum ManagedClientTargets
{
    None = 0,
    Codex = 1,
    Claude = 2,
    Gemini = 4,
    Grok = 8,
    All = Codex | Claude | Gemini | Grok,
}

public enum McpTransportKind
{
    Stdio,
    Http,
    Sse,
}

public sealed record McpServerDefinition
{
    public required string Id { get; init; }

    public required string Name { get; init; }

    public string? Description { get; init; }

    public McpTransportKind Transport { get; init; } = McpTransportKind.Stdio;

    public string? Command { get; init; }

    public IReadOnlyList<string> Arguments { get; init; } = Array.Empty<string>();

    public string? Url { get; init; }

    public IReadOnlyDictionary<string, string> Environment { get; init; }
        = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

    public IReadOnlyDictionary<string, string> Headers { get; init; }
        = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

    public ManagedClientTargets Targets { get; init; }
}

public sealed record PromptPresetDefinition
{
    public required string Id { get; init; }

    public required string Name { get; init; }

    public required string Markdown { get; init; }

    public ManagedClientTargets Targets { get; init; }
}

public sealed record ManagedSkillDefinition
{
    public required string Id { get; init; }

    public required string Name { get; init; }

    public string? Description { get; init; }

    /// <summary>
    /// Relative directory below the application's managed skill store.  The
    /// absolute user path is never persisted in feature state.
    /// </summary>
    public required string StorageDirectoryName { get; init; }

    public string? SourceLabel { get; init; }

    public string? ContentSha256 { get; init; }

    public ManagedClientTargets Targets { get; init; }
}

public sealed record ProjectWorkspaceProfile
{
    public required string ProjectId { get; init; }

    public string? CodexConnectionProfileId { get; init; }

    public string? ClaudeConnectionProfileId { get; init; }

    public string? GeminiConnectionProfileId { get; init; }

    public string? GrokConnectionProfileId { get; init; }

    public string? PromptPresetId { get; init; }

    public string? CodexPromptPresetId { get; init; }

    public string? ClaudePromptPresetId { get; init; }

    public string? GeminiPromptPresetId { get; init; }

    public string? GrokPromptPresetId { get; init; }

    public IReadOnlyList<string> McpServerIds { get; init; } = Array.Empty<string>();

    public IReadOnlyList<string> SkillIds { get; init; } = Array.Empty<string>();

    public IReadOnlyDictionary<string, ManagedClientTargets> McpTargets { get; init; }
        = new Dictionary<string, ManagedClientTargets>(StringComparer.OrdinalIgnoreCase);

    public IReadOnlyDictionary<string, ManagedClientTargets> SkillTargets { get; init; }
        = new Dictionary<string, ManagedClientTargets>(StringComparer.OrdinalIgnoreCase);

    public DateTimeOffset UpdatedAt { get; init; }
}

public sealed record WorkspaceFeatureState
{
    public int SchemaVersion { get; init; } = 1;

    public IReadOnlyList<McpServerDefinition> McpServers { get; init; } = Array.Empty<McpServerDefinition>();

    public IReadOnlyList<PromptPresetDefinition> PromptPresets { get; init; } = Array.Empty<PromptPresetDefinition>();

    public IReadOnlyList<ManagedSkillDefinition> Skills { get; init; } = Array.Empty<ManagedSkillDefinition>();

    public IReadOnlyList<ProjectWorkspaceProfile> ProjectProfiles { get; init; } = Array.Empty<ProjectWorkspaceProfile>();

    public string? CurrentProjectProfileId { get; init; }
}

public sealed record WorkspaceDesktopSettings
{
    public int SchemaVersion { get; init; } = 1;

    public bool StartWithWindows { get; init; }

    public bool MinimizeToTray { get; init; } = true;

    public bool PreserveSessionIndex { get; init; } = true;

    public bool CollectAnonymousDiagnostics { get; init; }

    public int NetworkProbeIntervalMinutes { get; init; } = 3;

#if PUBLIC_RELEASE
    public bool CheckUpdatesAutomatically { get; init; }

    public string UpdateManifestUrl { get; init; } = string.Empty;
#else
    public bool CheckUpdatesAutomatically { get; init; } = true;

    public string UpdateManifestUrl { get; init; }
        = "https://raw.githubusercontent.com/Liuna-llf/web_transform/master/tools/manufactor_app/ai-switch-gui/update-manifest.json";
#endif
}

public sealed record ProviderTemplateDefinition(
    string Id,
    string Name,
    string Description,
    string? CodexBaseUrl,
    string? ClaudeBaseUrl,
    string? GeminiBaseUrl,
    string? GrokBaseUrl = null,
    string? DashboardUrl = null);

