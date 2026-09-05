using System.Text.Json.Serialization;

namespace AiSwitchGui;

internal enum TargetMode
{
    Cloud,
    Local,
    Mixed
}

internal enum ClientSourceMode
{
    Cloud,
    Local
}

internal enum LiveStatusKind
{
    Unknown,
    Missing,
    Cloud,
    Local,
    SameProfileValues,
    Mixed,
    Custom
}

internal static class ProfileSourceIds
{
    public const string Cloud = "cloud-default";
    public const string LocalMachine = "local-machine";
    public const string LanDefault = "lan-default";
}

internal sealed class AppSettings
{
    public string ConfigRoot { get; set; } = @"C:\Users\Administrator\ai-switch-gui";
    public bool CloseToTrayOnClose { get; set; } = false;
    public StatsSettings Stats { get; set; } = new();
}

internal sealed class StatsSettings
{
    // 面向用户的流量统计：登录 Sub2API 看本账号消耗。
    public string GatewayBaseUrl { get; set; } = "http://192.168.31.247:8080";
    public string Email { get; set; } = string.Empty;
    public string Password { get; set; } = string.Empty;
    public int TrendDays { get; set; } = 7;
}


internal sealed class ProfileStore
{
    public ProfileDefinition Cloud { get; set; } = ProfileDefinition.CreateCloudDefaults();
    public List<ProfileDefinition> CloudSources { get; set; } =
    [
        ProfileDefinition.CreateCloudDefaults()
    ];
    public string SelectedCloudSourceId { get; set; } = ProfileSourceIds.Cloud;
    public ProfileDefinition Local { get; set; } = ProfileDefinition.CreateLocalDefaults();
    public ProfileDefinition Lan { get; set; } = ProfileDefinition.CreateLanDefaults();
    public List<ProfileDefinition> LocalSources { get; set; } =
    [
        ProfileDefinition.CreateLocalDefaults(),
        ProfileDefinition.CreateLanDefaults()
    ];
    public string SelectedLocalSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    // The WPF workspace launches graphical conversations from this explicit
    // source.  Keep it alongside the legacy per-tab selection so a WinForms
    // switch cannot leave the workspace using a stale cloud source.
    public string ActiveConnectionProfileId { get; set; } = ProfileSourceIds.LocalMachine;
    public List<string> BackupSourceIds { get; set; } = [];
    public bool? BackupUpstreamEnabled { get; set; }
    public MixedProfileDefinition Mixed { get; set; } = MixedProfileDefinition.CreateDefault();
}

internal sealed class ProfileDefinition
{
    public string Id { get; set; } = string.Empty;
    public string Name { get; set; } = string.Empty;
    public string Notes { get; set; } = string.Empty;
    // Browser UI address is intentionally distinct from API base URLs.  Keeping
    // it in the legacy model prevents a WinForms save from dropping the WPF
    // connection-center setting.
    public string DashboardUrl { get; set; } = string.Empty;
    public ClientProfile Codex { get; set; } = new();
    public ClientProfile Claude { get; set; } = new();
    public ClientProfile Gemini { get; set; } = new();
    public ClientProfile Grok { get; set; } = new();

    public static ProfileDefinition CreateCloudDefaults()
    {
        return new ProfileDefinition
        {
            Id = ProfileSourceIds.Cloud,
            Name = "远程来源",
            Notes = "发布版默认不提供远程中转地址。",
            Codex = new ClientProfile
            {
                BaseUrl = string.Empty,
                Secret = string.Empty
            },
            Claude = new ClientProfile
            {
                BaseUrl = string.Empty,
                Secret = string.Empty
            },
            Gemini = new ClientProfile
            {
                BaseUrl = string.Empty,
                Secret = string.Empty
            },
            Grok = new ClientProfile
            {
                BaseUrl = string.Empty,
                Secret = string.Empty
            }
        };
    }

    public static ProfileDefinition CreateLocalDefaults()
    {
        return new ProfileDefinition
        {
            Name = "本机中转",
            Id = ProfileSourceIds.LocalMachine,
            Notes = "用于当前机器本机运行的 sub2api。",
            Codex = new ClientProfile
            {
                BaseUrl = "http://127.0.0.1:8080/v1",
                Secret = string.Empty
            },
            Claude = new ClientProfile
            {
                BaseUrl = "http://127.0.0.1:8080",
                Secret = string.Empty
            },
            Gemini = new ClientProfile
            {
                BaseUrl = "http://127.0.0.1:8080",
                Secret = string.Empty
            },
            Grok = new ClientProfile
            {
                BaseUrl = "http://127.0.0.1:8080/v1",
                Secret = string.Empty
            }
        };
    }

    public static ProfileDefinition CreateLanDefaults()
    {
        return new ProfileDefinition
        {
            Name = "局域网中转",
            Id = ProfileSourceIds.LanDefault,
            Notes = "用于另一台局域网机器上的 sub2api，请把 192.168.x.x 改成中转站机器 IP。",
            DashboardUrl = "http://192.168.x.x:3000/dashboard",
            Codex = new ClientProfile
            {
                BaseUrl = "http://192.168.x.x:8080/v1",
                Secret = string.Empty
            },
            Claude = new ClientProfile
            {
                BaseUrl = "http://192.168.x.x:8080",
                Secret = string.Empty
            },
            Gemini = new ClientProfile
            {
                BaseUrl = "http://192.168.x.x:8080",
                Secret = string.Empty
            },
            Grok = new ClientProfile
            {
                BaseUrl = "http://192.168.x.x:8080/v1",
                Secret = string.Empty
            }
        };
    }
}

internal sealed class MixedProfileDefinition
{
    public ClientSourceMode CodexSource { get; set; } = ClientSourceMode.Local;
    public ClientSourceMode ClaudeSource { get; set; } = ClientSourceMode.Local;
    public ClientSourceMode GeminiSource { get; set; } = ClientSourceMode.Local;
    public ClientSourceMode GrokSource { get; set; } = ClientSourceMode.Local;
    public string CodexSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    public string ClaudeSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    public string GeminiSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    public string GrokSourceId { get; set; } = ProfileSourceIds.LocalMachine;

    public static MixedProfileDefinition CreateDefault()
    {
        return new MixedProfileDefinition
        {
            CodexSource = ClientSourceMode.Local,
            ClaudeSource = ClientSourceMode.Local,
            GeminiSource = ClientSourceMode.Local,
            GrokSource = ClientSourceMode.Local,
            CodexSourceId = ProfileSourceIds.LocalMachine,
            ClaudeSourceId = ProfileSourceIds.LocalMachine,
            GeminiSourceId = ProfileSourceIds.LocalMachine,
            GrokSourceId = ProfileSourceIds.LocalMachine
        };
    }
}

internal sealed class ClientProfile
{
    public string BaseUrl { get; set; } = string.Empty;
    public string Secret { get; set; } = string.Empty;
}

internal sealed class LiveStatus
{
    public string CodexBaseUrl { get; set; } = "<missing>";
    public string ClaudeBaseUrl { get; set; } = "<missing>";
    public string GeminiBaseUrl { get; set; } = "<missing>";
    public string GrokBaseUrl { get; set; } = "<missing>";
    public string ActiveTarget { get; set; } = "未知";
    public string Summary { get; set; } = "未识别";
    public string HealthText { get; set; } = "状态: 未知";
    public LiveStatusKind Kind { get; set; } = LiveStatusKind.Unknown;
    public string? CodexMatchedProfile { get; set; }
    public string? ClaudeMatchedProfile { get; set; }
    public string? GeminiMatchedProfile { get; set; }
    public string? GrokMatchedProfile { get; set; }
    public bool CodexConfigPresent { get; set; }
    public bool ClaudeConfigPresent { get; set; }
    public bool GeminiConfigPresent { get; set; }
    public bool GrokConfigPresent { get; set; }
    public ClientSourceMode MixedCodexSource { get; set; } = ClientSourceMode.Cloud;
    public ClientSourceMode MixedClaudeSource { get; set; } = ClientSourceMode.Local;
    public ClientSourceMode MixedGeminiSource { get; set; } = ClientSourceMode.Local;
    public ClientSourceMode MixedGrokSource { get; set; } = ClientSourceMode.Local;
    public string MixedCodexSourceId { get; set; } = ProfileSourceIds.Cloud;
    public string MixedClaudeSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    public string MixedGeminiSourceId { get; set; } = ProfileSourceIds.LocalMachine;
    public string MixedGrokSourceId { get; set; } = ProfileSourceIds.LocalMachine;
}

internal sealed class SourceOption
{
    public string Id { get; }
    public string DisplayName { get; }

    public SourceOption(string id, string displayName)
    {
        Id = id;
        DisplayName = displayName;
    }

    public override string ToString() => DisplayName;
}

internal sealed class ValidationDetail
{
    public string Name { get; set; } = string.Empty;
    public bool Success { get; set; }
    public string Message { get; set; } = string.Empty;
}

internal sealed class OperationResult
{
    public bool Success { get; set; }
    public string Summary { get; set; } = string.Empty;
    public List<ValidationDetail> Details { get; } = [];
}

internal sealed class ClaudeGptRoutingStatus
{
    public bool Enabled { get; init; }
    public string SourceId { get; init; } = string.Empty;
    public string SourceName { get; init; } = string.Empty;
    public string TargetPlatform { get; init; } = "GPT";
    public ClaudeGptModelMapping Mapping { get; init; } = new();
}

internal sealed class ClaudeGptModelMapping
{
    public string OpusModel { get; init; } = string.Empty;
    public string SonnetModel { get; init; } = string.Empty;
    public string HaikuModel { get; init; } = string.Empty;

    [JsonIgnore]
    public bool IsComplete =>
        !string.IsNullOrWhiteSpace(OpusModel) &&
        !string.IsNullOrWhiteSpace(SonnetModel) &&
        !string.IsNullOrWhiteSpace(HaikuModel);

    public IReadOnlyList<string> DistinctModels() =>
        new[] { OpusModel, SonnetModel, HaikuModel }
            .Where(model => !string.IsNullOrWhiteSpace(model))
            .Select(model => model.Trim())
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
}

internal sealed class CodexClaudeRoutingStatus
{
    public bool Enabled { get; init; }
    public string SourceId { get; init; } = string.Empty;
    public string SourceName { get; init; } = string.Empty;
    public CodexClaudeModelMapping Mapping { get; init; } = new();
}

internal sealed class CodexClaudeModelMapping
{
    public string TargetPlatform { get; init; } = "Claude";
    public string DefaultModel { get; init; } = string.Empty;
    public string ReviewModel { get; init; } = string.Empty;
    public string ReasoningEffort { get; init; } = "high";

    [JsonIgnore]
    public bool IsComplete =>
        TargetPlatform is "Claude" or "Grok" &&
        !string.IsNullOrWhiteSpace(DefaultModel) &&
        !string.IsNullOrWhiteSpace(ReviewModel) &&
        ReasoningEffort is "low" or "medium" or "high" or "xhigh";

    public IReadOnlyList<string> DistinctModels() =>
        new[] { DefaultModel, ReviewModel }
            .Where(model => !string.IsNullOrWhiteSpace(model))
            .Select(model => model.Trim())
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
}

internal sealed class BackupSnapshot
{
    public string Folder { get; set; } = string.Empty;

    [JsonIgnore]
    public bool Exists => !string.IsNullOrWhiteSpace(Folder) && Directory.Exists(Folder);
}

internal sealed class ImportedLiveConfig
{
    public ClientProfile? Codex { get; set; }
    public ClientProfile? Claude { get; set; }
    public ClientProfile? Gemini { get; set; }
    public ClientProfile? Grok { get; set; }
}

internal sealed class SessionConfigSnapshot
{
    public DateTime CreatedAt { get; set; } = DateTime.Now;
    public List<SessionFileSnapshot> Files { get; set; } = [];
    public List<SessionEnvironmentVariableSnapshot> EnvironmentVariables { get; set; } = [];
}

internal sealed class SessionFileSnapshot
{
    public string Path { get; set; } = string.Empty;
    public bool Existed { get; set; }
    public string Content { get; set; } = string.Empty;
}

internal sealed class SessionEnvironmentVariableSnapshot
{
    public string Name { get; set; } = string.Empty;
    public bool Existed { get; set; }
    public string Value { get; set; } = string.Empty;
}

