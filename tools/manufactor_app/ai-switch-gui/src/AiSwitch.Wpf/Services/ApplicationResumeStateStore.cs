using System.Text.Json;
using AiSwitchGui;

namespace LanAi.Workspace.Wpf.Services;

internal enum ApplicationBaseRoutingMode
{
    None,
    UnifiedSource,
    MixedRouting,
}

internal sealed class ApplicationResumeState
{
    public int Version { get; set; } = 1;
    public DateTime SavedAtUtc { get; set; } = DateTime.UtcNow;
    public ApplicationBaseRoutingMode BaseRoutingMode { get; set; }
    public string UnifiedSourceId { get; set; } = string.Empty;
    public bool ClaudeGptEnabled { get; set; }
    public string ClaudeGptSourceId { get; set; } = string.Empty;
    public string ClaudeGptTargetPlatform { get; set; } = "GPT";
    public ClaudeGptModelMapping ClaudeGptMapping { get; set; } = new();
    public bool CodexClaudeEnabled { get; set; }
    public string CodexClaudeSourceId { get; set; } = string.Empty;
    public CodexClaudeModelMapping CodexClaudeMapping { get; set; } = new();
}

internal sealed class ApplicationResumeStateStore
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true,
        WriteIndented = true,
    };

    private readonly string _path;

    public ApplicationResumeStateStore(string path)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        _path = Path.GetFullPath(path);
    }

    public ApplicationResumeState? Load()
    {
        if (!File.Exists(_path))
        {
            return null;
        }

        try
        {
            ApplicationResumeState? state = JsonSerializer.Deserialize<ApplicationResumeState>(
                File.ReadAllText(_path),
                JsonOptions);
            return state?.Version == 1 ? state : null;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or JsonException)
        {
            return null;
        }
    }

    public void Save(ApplicationResumeState state)
    {
        ArgumentNullException.ThrowIfNull(state);
        state.Version = 1;
        state.SavedAtUtc = DateTime.UtcNow;

        string directory = Path.GetDirectoryName(_path)
            ?? throw new InvalidOperationException("无法确定工作状态保存目录。");
        Directory.CreateDirectory(directory);
        string temporaryPath = _path + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try
        {
            File.WriteAllText(temporaryPath, JsonSerializer.Serialize(state, JsonOptions));
            File.Move(temporaryPath, _path, overwrite: true);
        }
        finally
        {
            if (File.Exists(temporaryPath))
            {
                try { File.Delete(temporaryPath); }
                catch (IOException) { }
                catch (UnauthorizedAccessException) { }
            }
        }
    }
}
