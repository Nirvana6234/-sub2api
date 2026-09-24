using System.Text.Json;
using AiSwitchGui;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Persists non-sensitive cross-client model choices per connection source.
/// API keys and endpoint credentials never enter this document.
/// </summary>
internal sealed class CrossClientRoutingPresetStore
{
    private const int CurrentVersion = 1;
    private const int MaximumFileBytes = 256 * 1024;
    private readonly string _path;
    private readonly object _sync = new();

    public CrossClientRoutingPresetStore(string path)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        _path = Path.GetFullPath(path);
    }

    public ClaudeGptModelMapping? ReadClaudeGpt(string profileId)
    {
        if (!TryNormalizeProfileId(profileId, out string? normalizedId))
        {
            return null;
        }

        lock (_sync)
        {
            PresetDocument document = ReadDocument();
            return document.ClaudeGpt.TryGetValue(normalizedId!, out ClaudeGptPreset? preset) &&
                   TryCreateClaudeGptMapping(preset, out ClaudeGptModelMapping? mapping)
                ? mapping
                : null;
        }
    }

    public CodexClaudeModelMapping? ReadCodexClaude(string profileId, string targetPlatform)
    {
        if (!TryNormalizeProfileId(profileId, out string? normalizedId))
        {
            return null;
        }

        lock (_sync)
        {
            PresetDocument document = ReadDocument();
            string target = SwitchService.NormalizeCodexClaudeTarget(targetPlatform);
            string key = BuildCodexClaudePresetKey(normalizedId!, target);
            CodexClaudePreset? preset = document.CodexClaude.GetValueOrDefault(key)
                ?? document.CodexClaude.GetValueOrDefault(normalizedId!);
            return preset is not null &&
                   TryCreateCodexClaudeMapping(preset, target, out CodexClaudeModelMapping? mapping)
                ? mapping
                : null;
        }
    }

    public bool SaveClaudeGpt(string profileId, ClaudeGptModelMapping mapping)
    {
        ArgumentNullException.ThrowIfNull(mapping);
        if (!TryNormalizeProfileId(profileId, out string? normalizedId) ||
            !TryNormalizeModel(mapping.OpusModel, out string? opus) ||
            !TryNormalizeModel(mapping.SonnetModel, out string? sonnet) ||
            !TryNormalizeModel(mapping.HaikuModel, out string? haiku))
        {
            return false;
        }

        lock (_sync)
        {
            PresetDocument document = ReadDocument();
            document.ClaudeGpt[normalizedId!] = new ClaudeGptPreset
            {
                OpusModel = opus!,
                SonnetModel = sonnet!,
                HaikuModel = haiku!,
            };
            return WriteDocument(document);
        }
    }

    public bool SaveCodexClaude(string profileId, CodexClaudeModelMapping mapping)
    {
        ArgumentNullException.ThrowIfNull(mapping);
        string targetPlatform = SwitchService.NormalizeCodexClaudeTarget(mapping.TargetPlatform);
        if (!TryNormalizeProfileId(profileId, out string? normalizedId) ||
            !TryNormalizeModel(mapping.DefaultModel, out string? defaultModel) ||
            !TryNormalizeModel(mapping.ReviewModel, out string? reviewModel) ||
            mapping.ReasoningEffort is not ("low" or "medium" or "high" or "xhigh"))
        {
            return false;
        }

        lock (_sync)
        {
            PresetDocument document = ReadDocument();
            document.CodexClaude[BuildCodexClaudePresetKey(normalizedId!, targetPlatform)] = new CodexClaudePreset
            {
                TargetPlatform = targetPlatform,
                DefaultModel = defaultModel!,
                ReviewModel = reviewModel!,
                ReasoningEffort = mapping.ReasoningEffort,
            };
            return WriteDocument(document);
        }
    }

    private PresetDocument ReadDocument()
    {
        try
        {
            if (!File.Exists(_path))
            {
                return new PresetDocument();
            }

            var info = new FileInfo(_path);
            if (info.Length is <= 0 or > MaximumFileBytes)
            {
                return new PresetDocument();
            }

            PresetDocument? document = JsonSerializer.Deserialize<PresetDocument>(File.ReadAllBytes(_path));
            return document?.Version == CurrentVersion ? document : new PresetDocument();
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or JsonException)
        {
            return new PresetDocument();
        }
    }

    private bool WriteDocument(PresetDocument document)
    {
        string? temporaryPath = null;
        try
        {
            document.Version = CurrentVersion;
            string directory = Path.GetDirectoryName(_path)
                ?? throw new InvalidOperationException("无法确定模型映射保存目录。");
            Directory.CreateDirectory(directory);
            temporaryPath = _path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllBytes(temporaryPath, JsonSerializer.SerializeToUtf8Bytes(document));
            File.Move(temporaryPath, _path, overwrite: true);
            return true;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or JsonException or InvalidOperationException)
        {
            return false;
        }
        finally
        {
            if (!string.IsNullOrWhiteSpace(temporaryPath))
            {
                try { File.Delete(temporaryPath); } catch (Exception exception) when (exception is IOException or UnauthorizedAccessException) { }
            }
        }
    }

    private static bool TryCreateClaudeGptMapping(ClaudeGptPreset preset, out ClaudeGptModelMapping? mapping)
    {
        mapping = null;
        if (!TryNormalizeModel(preset.OpusModel, out string? opus) ||
            !TryNormalizeModel(preset.SonnetModel, out string? sonnet) ||
            !TryNormalizeModel(preset.HaikuModel, out string? haiku))
        {
            return false;
        }

        mapping = new ClaudeGptModelMapping { OpusModel = opus!, SonnetModel = sonnet!, HaikuModel = haiku! };
        return true;
    }

    private static bool TryCreateCodexClaudeMapping(
        CodexClaudePreset preset,
        string requestedTargetPlatform,
        out CodexClaudeModelMapping? mapping)
    {
        mapping = null;
        if (!TryNormalizeModel(preset.DefaultModel, out string? defaultModel) ||
            !TryNormalizeModel(preset.ReviewModel, out string? reviewModel) ||
            preset.ReasoningEffort is not ("low" or "medium" or "high" or "xhigh"))
        {
            return false;
        }

        mapping = new CodexClaudeModelMapping
        {
            TargetPlatform = SwitchService.NormalizeCodexClaudeTarget(
                string.IsNullOrWhiteSpace(preset.TargetPlatform)
                    ? requestedTargetPlatform
                    : preset.TargetPlatform),
            DefaultModel = defaultModel!,
            ReviewModel = reviewModel!,
            ReasoningEffort = preset.ReasoningEffort,
        };
        return true;
    }

    private static bool TryNormalizeProfileId(string? value, out string? normalized)
        => TryNormalizeText(value, 256, out normalized);

    private static bool TryNormalizeModel(string? value, out string? normalized)
        => TryNormalizeText(value, 512, out normalized);

    private static string BuildCodexClaudePresetKey(string profileId, string targetPlatform) =>
        $"{profileId}::{SwitchService.NormalizeCodexClaudeTarget(targetPlatform)}";

    private static bool TryNormalizeText(string? value, int maximumLength, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        string candidate = value.Trim();
        if (candidate.Length > maximumLength || candidate.Any(char.IsControl))
        {
            return false;
        }

        normalized = candidate;
        return true;
    }

    private sealed class PresetDocument
    {
        public int Version { get; set; } = CurrentVersion;
        public Dictionary<string, ClaudeGptPreset> ClaudeGpt { get; set; } = new(StringComparer.OrdinalIgnoreCase);
        public Dictionary<string, CodexClaudePreset> CodexClaude { get; set; } = new(StringComparer.OrdinalIgnoreCase);
    }

    private sealed class ClaudeGptPreset
    {
        public string OpusModel { get; set; } = string.Empty;
        public string SonnetModel { get; set; } = string.Empty;
        public string HaikuModel { get; set; } = string.Empty;
    }

    private sealed class CodexClaudePreset
    {
        public string TargetPlatform { get; set; } = string.Empty;
        public string DefaultModel { get; set; } = string.Empty;
        public string ReviewModel { get; set; } = string.Empty;
        public string ReasoningEffort { get; set; } = "high";
    }
}
