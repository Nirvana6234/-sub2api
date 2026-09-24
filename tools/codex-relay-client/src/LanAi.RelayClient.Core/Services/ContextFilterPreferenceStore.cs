using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Remembers whether the user wants context compression on.</summary>
internal interface IContextFilterPreferenceStore
{
    /// <returns>The remembered choice, or null when the user has never expressed one.</returns>
    bool? Load();

    void Save(bool enabled);
}

/// <summary>
/// Keeps the 启用上下文压缩 choice in a small file of its own.
/// </summary>
/// <remarks>
/// <para>
/// Deliberately not folded into <see cref="GroupPreferenceStore"/>'s file. That one
/// is scoped to a relay address and is discarded when the client points at a
/// different server, because a group id means nothing across databases. This
/// setting is the opposite: it is about how much of the local conversation gets
/// compressed before it leaves the machine, which is the same question whichever
/// relay is in use. Sharing the file would make switching servers silently reset it.
/// </para>
/// <para>
/// Not encrypted, and every read failure degrades to "no preference" rather than
/// throwing — a display setting is not a credential.
/// </para>
/// </remarks>
internal sealed class ContextFilterPreferenceStore : IContextFilterPreferenceStore
{
    private readonly string _filePath;

    public ContextFilterPreferenceStore(string? filePath = null)
    {
        _filePath = filePath ?? DefaultFilePath();
    }

    internal static string DefaultFilePath() => AppPaths.InData("context-filter.json");

    public bool? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            Preferences? preferences = JsonSerializer.Deserialize(
                File.ReadAllBytes(_filePath),
                ClientJsonContext.Default.ContextFilterPreferences);

            return preferences?.Enabled;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    public void Save(bool enabled)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(
                    new Preferences { Enabled = enabled },
                    ClientJsonContext.Default.ContextFilterPreferences));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // The switch still applies to this run; forgetting it is the smaller harm.
        }
    }

    internal sealed record Preferences
    {
        public bool Enabled { get; init; }
    }
}
