using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Remembers which announcements this client has already announced.</summary>
internal interface IAnnouncementNotifyStateStore
{
    /// <summary>
    /// The ids already surfaced for <paramref name="accountKey"/>, or null when
    /// this account has no record yet.
    /// </summary>
    IReadOnlyCollection<long>? Load(string accountKey);

    void Save(string accountKey, IReadOnlyCollection<long> notifiedIds);
}

/// <summary>
/// Keeps the already-notified announcement ids in a small file beside the session.
/// </summary>
/// <remarks>
/// <para>
/// A set of ids rather than a high-water mark, because "new to this user" and
/// "higher id" are not the same thing. An announcement written last week with
/// its <c>starts_at</c> today enters the visible list now with an id below ones
/// already seen; so does one an operator switches from draft to active, or one
/// whose targeting is widened until this user becomes eligible. A watermark
/// silently drops all three.
/// </para>
/// <para>
/// Scoped per account as well as per relay. The client supports signing out and
/// switching accounts, and one account's notified set must never suppress
/// another's reminder.
/// </para>
/// <para>
/// Not encrypted: announcement ids are not credentials. Every read failure
/// degrades to "no record", on the same reasoning as
/// <see cref="GroupPreferenceStore"/> — losing this file costs one redundant
/// baseline, never a working client.
/// </para>
/// </remarks>
internal sealed class AnnouncementNotifyStateStore : IAnnouncementNotifyStateStore
{
    /// <summary>
    /// How many accounts keep a record on a shared machine.
    /// </summary>
    /// <remarks>
    /// The id set per account is pruned to what is currently visible, so the only
    /// unbounded axis is the number of accounts that have ever signed in here.
    /// </remarks>
    private const int MaxAccounts = 8;

    private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web);

    private readonly string _filePath;
    private readonly string _serverAddress;
    private readonly Func<DateTimeOffset> _clock;

    /// <param name="serverAddress">
    /// The relay these ids belong to. Announcement ids are allocated per relay
    /// database, so an id carried across servers would suppress an unrelated
    /// announcement. The whole file is discarded when the server changes.
    /// </param>
    public AnnouncementNotifyStateStore(
        string serverAddress,
        string? filePath = null,
        Func<DateTimeOffset>? clock = null)
    {
        _serverAddress = serverAddress ?? throw new ArgumentNullException(nameof(serverAddress));
        _filePath = filePath ?? DefaultFilePath();
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    internal static string DefaultFilePath() => AppPaths.InData("announcements.json");

    /// <summary>
    /// Derives the per-account key from the signed-in email.
    /// </summary>
    /// <remarks>
    /// Hashed rather than used directly: the email is personal data and this file
    /// is written in the clear, and an address is not a legal file-safe key
    /// anyway. Truncated because this only has to separate the handful of
    /// accounts that share one machine, not resist an adversary.
    /// </remarks>
    public static string AccountKey(string? userEmail)
    {
        string normalized = (userEmail ?? string.Empty).Trim().ToLowerInvariant();
        if (normalized.Length == 0)
        {
            return "anonymous";
        }

        byte[] digest = SHA256.HashData(Encoding.UTF8.GetBytes(normalized));
        return Convert.ToHexString(digest, 0, 8).ToLowerInvariant();
    }

    public IReadOnlyCollection<long>? Load(string accountKey)
    {
        State? state = ReadState();
        if (state?.Accounts is null)
        {
            return null;
        }

        return state.Accounts.TryGetValue(accountKey, out AccountState? account) && account.NotifiedIds is not null
            ? account.NotifiedIds
            : null;
    }

    public void Save(string accountKey, IReadOnlyCollection<long> notifiedIds)
    {
        ArgumentNullException.ThrowIfNull(notifiedIds);

        try
        {
            State state = ReadState() ?? new State { ServerAddress = _serverAddress };
            Dictionary<string, AccountState> accounts = state.Accounts is null
                ? new Dictionary<string, AccountState>(StringComparer.Ordinal)
                : new Dictionary<string, AccountState>(state.Accounts, StringComparer.Ordinal);

            accounts[accountKey] = new AccountState
            {
                NotifiedIds = notifiedIds.Distinct().Order().ToArray(),
                LastSeenAt = _clock(),
            };

            // Oldest-first eviction, so the account in front of the user is the last
            // one to lose its record.
            foreach (string stale in accounts
                         .OrderByDescending(entry => entry.Value.LastSeenAt)
                         .Skip(MaxAccounts)
                         .Select(entry => entry.Key)
                         .ToArray())
            {
                accounts.Remove(stale);
            }

            WriteState(new State { ServerAddress = _serverAddress, Accounts = accounts });
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or NotSupportedException)
        {
            // Worst case one announcement is announced twice after a restart. That
            // is a far smaller harm than failing the poll it was recorded during.
            ClientLog.Warning("无法保存公告提醒记录", ex);
        }
    }

    private State? ReadState()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            State? state = JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), ClientJsonContext.Default.NotifyState);
            if (state is null)
            {
                return null;
            }

            // A record with no server, or one from a different relay, cannot be
            // attributed to these announcement ids and is dropped rather than
            // guessed at — the same boundary the session and group stores keep.
            return string.Equals(state.ServerAddress, _serverAddress, StringComparison.OrdinalIgnoreCase)
                ? state
                : null;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    private void WriteState(State state)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

        string temporaryPath = _filePath + ".tmp";
        File.WriteAllBytes(temporaryPath, JsonSerializer.SerializeToUtf8Bytes(state, ClientJsonContext.Default.NotifyState));
        File.Move(temporaryPath, _filePath, overwrite: true);
    }

    internal sealed record State
    {
        public string? ServerAddress { get; init; }

        public Dictionary<string, AccountState>? Accounts { get; init; }
    }

    internal sealed record AccountState
    {
        public long[]? NotifiedIds { get; init; }

        public DateTimeOffset LastSeenAt { get; init; }
    }
}
