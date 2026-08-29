using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Remembers which group the user picked, for when there is no key to write it to yet.</summary>
internal interface IGroupPreferenceStore
{
    long? Load();

    void Save(long groupId);
}

/// <summary>
/// Keeps the chosen group id in a small file beside the session.
/// </summary>
/// <remarks>
/// <para>
/// Needed because group selection exists before any key does: the managed key is
/// created with <c>group_id</c> already set (F3.2.2), so the choice has to
/// survive from the moment the user makes it until M3 issues the key. When a key
/// does exist the server is authoritative and this file is only a fallback.
/// </para>
/// <para>
/// Not encrypted, unlike the session: a group id is a preference, not a
/// credential. Every read failure degrades to "no preference" rather than
/// throwing, on the same reasoning as the session store.
/// </para>
/// </remarks>
internal sealed class GroupPreferenceStore : IGroupPreferenceStore
{
    private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web);

    private readonly string _filePath;
    private readonly string _serverAddress;

    /// <param name="serverAddress">
    /// The relay this preference belongs to. Group ids are allocated per relay
    /// database, so id 3 on a development server and id 3 in production are
    /// unrelated groups — a preference carried across would silently select the
    /// wrong one. The session store already refuses to cross that boundary; this
    /// store must not be the hole in the same rule.
    /// </param>
    public GroupPreferenceStore(string serverAddress, string? filePath = null)
    {
        _serverAddress = serverAddress ?? throw new ArgumentNullException(nameof(serverAddress));
        _filePath = filePath ?? DefaultFilePath();
    }

    internal static string DefaultFilePath() => AppPaths.InData("preferences.json");

    public long? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            Preferences? preferences = JsonSerializer.Deserialize(
                File.ReadAllBytes(_filePath),
                ClientJsonContext.Default.GroupPreferences);

            if (preferences?.GroupId is not > 0)
            {
                return null;
            }

            // A preference with no server recorded predates this scoping and cannot
            // be attributed to any relay, so it is discarded rather than guessed at.
            return string.Equals(preferences.ServerAddress, _serverAddress, StringComparison.OrdinalIgnoreCase)
                ? preferences.GroupId
                : null;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    public void Save(long groupId)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            var preferences = new Preferences { ServerAddress = _serverAddress, GroupId = groupId };

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(preferences, ClientJsonContext.Default.GroupPreferences));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Losing a preference is a far smaller harm than failing the switch the
            // user just asked for; the selection still applies for this run.
        }
    }

    internal sealed record Preferences
    {
        public string? ServerAddress { get; init; }

        public long GroupId { get; init; }
    }
}
