using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Remembers which group the user picked, for when there is no key to write it to yet.</summary>
internal interface IGroupPreferenceStore
{
    long? Load();

    void Save(long groupId);

    /// <summary>True when the user last chose automatic routing rather than a fixed group.</summary>
    /// <remarks>
    /// The mode lives here rather than on the server because it only decides which
    /// <c>X-Paw-Group-Id</c> this client sends. The server's <c>auto_group</c> flag
    /// cannot stand in for it: that flag has to stay on for the internal key's
    /// candidate list to remain readable at all, so it can never mean "off".
    /// </remarks>
    bool LoadAutomatic();

    void SaveAutomatic(bool automatic);

    /// <summary>
    /// The Claude group last chosen for the editor plug-ins (支持插件), independent of
    /// whichever group Codex is on. Null when the user has never chosen one.
    /// </summary>
    long? LoadClaudeGroup();

    void SaveClaudeGroup(long groupId);
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
        Preferences? preferences = ReadForThisServer();
        return preferences?.GroupId is > 0 ? preferences.GroupId : null;
    }

    public bool LoadAutomatic() => ReadForThisServer()?.Automatic == true;

    public void Save(long groupId) =>
        Write(current => current with { GroupId = groupId });

    public void SaveAutomatic(bool automatic) =>
        Write(current => current with { Automatic = automatic });

    public long? LoadClaudeGroup()
    {
        Preferences? preferences = ReadForThisServer();
        return preferences?.ClaudeGroupId is > 0 ? preferences.ClaudeGroupId : null;
    }

    public void SaveClaudeGroup(long groupId) =>
        Write(current => current with { ClaudeGroupId = groupId });

    /// <summary>
    /// The stored preferences when they belong to the relay in use, otherwise null.
    /// </summary>
    /// <remarks>
    /// A preference with no server recorded predates this scoping and cannot be
    /// attributed to any relay, so it is discarded rather than guessed at.
    /// </remarks>
    private Preferences? ReadForThisServer()
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

            return preferences is not null &&
                   string.Equals(preferences.ServerAddress, _serverAddress, StringComparison.OrdinalIgnoreCase)
                ? preferences
                : null;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    /// <summary>
    /// Applies one change without dropping the other field.
    /// </summary>
    /// <remarks>
    /// Read-modify-write rather than a plain overwrite: the group id and the mode
    /// are saved by different call sites, and a whole-record write from either one
    /// would silently reset the other.
    /// </remarks>
    private void Write(Func<Preferences, Preferences> change)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            Preferences current = ReadForThisServer() ?? new Preferences { ServerAddress = _serverAddress };
            Preferences preferences = change(current) with { ServerAddress = _serverAddress };

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

        /// <summary>Absent in files written before automatic routing existed, which reads as false.</summary>
        public bool Automatic { get; init; }

        /// <summary>Absent in files written before the editor plug-ins had their own group, reads as 0/none.</summary>
        public long ClaudeGroupId { get; init; }
    }
}
