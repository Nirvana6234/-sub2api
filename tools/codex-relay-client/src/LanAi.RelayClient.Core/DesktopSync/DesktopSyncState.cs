using System.IO;
using System.Text.Json;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.DesktopSync;

/// <summary>A desktop conversation the user chose to reach from the phone.</summary>
/// <param name="Provider"><c>codex</c> for now; Claude Code later (design §5.7).</param>
/// <param name="Title">As it was when chosen, for showing it while the desktop app is closed.</param>
internal sealed record SyncedSession(string Provider, string ThreadId, string? Title, DateTimeOffset SelectedAt);

/// <summary>
/// A phone the user approved on this computer, and the key its sends must be signed with.
/// </summary>
/// <remarks>
/// The key is the copy shown to the user at approval time — never the server's. A
/// compromised server could otherwise substitute its own key and sign whatever it likes.
/// </remarks>
internal sealed record ApprovedPhone(long PairingId, string PhoneLabel, string PublicKey, DateTimeOffset ApprovedAt);

/// <summary>Everything phone sync keeps across restarts, in one file.</summary>
internal sealed record DesktopSyncState
{
    /// <summary>The master switch. Off unless the user turned it on.</summary>
    public bool Enabled { get; init; }

    public IReadOnlyList<SyncedSession> Sessions { get; init; } = [];

    public IReadOnlyList<ApprovedPhone> Phones { get; init; } = [];

    public static DesktopSyncState Empty { get; } = new();
}

internal interface IDesktopSyncStateStore
{
    DesktopSyncState Load();

    void Save(DesktopSyncState state);
}

/// <summary>
/// Keeps <see cref="DesktopSyncState"/> in <c>desktop-sync.json</c>.
/// </summary>
/// <remarks>
/// Local and authoritative: the server never sees which conversations are selected or
/// which keys are trusted, so it cannot widen either. A file that cannot be read gives
/// the empty state — switched off, nothing selected, no phone trusted — which is the
/// safe way to fail.
/// </remarks>
internal sealed class DesktopSyncStateStore : IDesktopSyncStateStore
{
    private readonly string _filePath;

    public DesktopSyncStateStore(string? filePath = null) =>
        _filePath = filePath ?? AppPaths.InData("desktop-sync.json");

    public DesktopSyncState Load()
    {
        if (!File.Exists(_filePath))
        {
            return DesktopSyncState.Empty;
        }

        try
        {
            return JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), ClientJsonContext.Default.DesktopSyncState)
                ?? DesktopSyncState.Empty;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("读取手机同步设置失败，按未开启处理", ex);
            return DesktopSyncState.Empty;
        }
    }

    public void Save(DesktopSyncState state)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, JsonSerializer.SerializeToUtf8Bytes(state, ClientJsonContext.Default.DesktopSyncState));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("保存手机同步设置失败", ex);
        }
    }
}

/// <summary>One line of what the phone did here.</summary>
/// <param name="Outcome"><c>ok</c>, or why it was refused or failed.</param>
internal sealed record SyncAuditEntry(
    DateTimeOffset At,
    long PairingId,
    string? PhoneLabel,
    string Command,
    string? ThreadId,
    string? Summary,
    string Outcome);

/// <summary>
/// What phones did on this computer: every command, allowed or not.
/// </summary>
/// <remarks>
/// Also how a message sent from the phone is recognised later. The rollout records
/// only the text of a delegated message, not where it came from, so "from the phone"
/// is decided by matching text and time against this log.
/// </remarks>
internal sealed class SyncAuditLog
{
    public const int Capacity = 200;

    private readonly string? _filePath;
    private readonly object _gate = new();
    private readonly List<SyncAuditEntry> _entries;

    public SyncAuditLog(string? filePath = null, bool persist = true)
    {
        _filePath = persist ? filePath ?? AppPaths.InData("desktop-sync-audit.json") : null;
        _entries = Load();
    }

    public event Action? Changed;

    public IReadOnlyList<SyncAuditEntry> Recent(int count)
    {
        lock (_gate)
        {
            return _entries.TakeLast(count).Reverse().ToList();
        }
    }

    public void Add(SyncAuditEntry entry)
    {
        lock (_gate)
        {
            _entries.Add(entry);
            if (_entries.Count > Capacity)
            {
                _entries.RemoveRange(0, _entries.Count - Capacity);
            }

            Save();
        }

        Changed?.Invoke();
    }

    /// <summary>Whether a message with this text was sent from a phone (and is still in the log).</summary>
    public bool WasSentFromPhone(string text)
    {
        string summary = SummaryOf(text);
        lock (_gate)
        {
            return _entries.Any(e => e.Command == DesktopSyncCommands.SendMessage && e.Outcome == "ok" && e.Summary == summary);
        }
    }

    /// <summary>What the log keeps of a message: enough to recognise it, not all of it.</summary>
    public static string SummaryOf(string text) => text.Length <= 120 ? text : text[..120] + "…";

    private List<SyncAuditEntry> Load()
    {
        if (_filePath is null || !File.Exists(_filePath))
        {
            return [];
        }

        try
        {
            return JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), ClientJsonContext.Default.ListSyncAuditEntry) ?? [];
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return [];
        }
    }

    private void Save()
    {
        if (_filePath is null)
        {
            return;
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, JsonSerializer.SerializeToUtf8Bytes(_entries, ClientJsonContext.Default.ListSyncAuditEntry));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("保存手机同步记录失败", ex);
        }
    }
}
