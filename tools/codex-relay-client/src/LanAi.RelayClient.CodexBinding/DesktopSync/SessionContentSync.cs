using System.Text;
using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>Where a phone has read a conversation up to.</summary>
/// <remarks>
/// A byte offset into the rollout. The rollout is append-only and one file per
/// conversation (measured: forks become new conversations, compaction appends), so
/// resuming from an offset needs nothing cached anywhere.
/// </remarks>
public sealed record SyncCursor(string RolloutPath, long Offset);

public sealed record SessionHeader(
    string ThreadId,
    string? Title,
    string? Cwd,
    string? Model,
    PermissionMode Permission,
    string? OpenTurnId);

/// <param name="HasOlder">Earlier turns exist; fetch them with <see cref="SessionContentSync.History"/>.</param>
/// <param name="TruncatedTurnId">
/// A single turn held more than the item limit; its earliest items were left out and are
/// only visible on the desktop.
/// </param>
public sealed record SessionPage(IReadOnlyList<SyncItem> Items, bool HasOlder, string? TruncatedTurnId);

public sealed record SessionSnapshot(SessionHeader Header, SessionPage Page, SyncCursor Cursor);

/// <param name="Resync">The cursor no longer fits the file; the phone must open the conversation again.</param>
public sealed record SessionDelta(IReadOnlyList<SyncItem> Items, SyncCursor Cursor, bool Resync);

public enum DetailPart
{
    Output,
    Diff,
    Image,
}

public enum DetailOutcome
{
    Found,

    /// <summary>No such item in that conversation.</summary>
    NotFound,

    /// <summary>The item exists but its file is gone, e.g. a screenshot cleared from %TEMP%.</summary>
    Missing,

    /// <summary>Exists but will not be sent: wrong type or too large.</summary>
    Refused,
}

public sealed record SessionDetail(DetailOutcome Outcome, string? Text = null, byte[]? Bytes = null, string? MediaType = null, bool Truncated = false);

/// <summary>
/// Reads a desktop conversation's rollout into what the phone shows: a page of recent
/// turns, older pages, what was appended since a cursor, and large content on demand.
/// </summary>
/// <remarks>
/// <para>
/// Read-only. The rollout is the only complete record — <c>read_thread</c> leaves out
/// messages that arrived by delegation, which is how the phone's arrive — so everything
/// comes from here and nothing from the pipe.
/// </para>
/// <para>
/// Which conversations may be read is not decided here. Callers check the user's
/// selection first; this class answers for any id it is given.
/// </para>
/// </remarks>
public sealed class SessionContentSync
{
    public const int DefaultTurns = 10;
    public const int MaxItemsPerPage = 200;
    public const int MaxOutputChars = 64 * 1024;
    public const int MaxDiffChars = 200 * 1024;
    public const int MaxImageBytes = 1024 * 1024;

    private static readonly Dictionary<string, string> ImageTypes = new(StringComparer.OrdinalIgnoreCase)
    {
        [".png"] = "image/png",
        [".jpg"] = "image/jpeg",
        [".jpeg"] = "image/jpeg",
        [".gif"] = "image/gif",
        [".webp"] = "image/webp",
    };

    private readonly Func<string, CodexThreadRecord?> _findThread;
    private readonly Func<byte[], string, byte[]?> _shrinkImage;
    private readonly Dictionary<string, TurnIndex> _indexes = new(StringComparer.Ordinal);
    private readonly Dictionary<string, (long Offset, ProjectionState State)> _followStates = new(StringComparer.Ordinal);
    private readonly object _gate = new();

    /// <param name="findThread">Normally <see cref="CodexStateDatabase.FindThread"/>.</param>
    /// <param name="shrinkImage">
    /// Given image bytes and media type, a version under <see cref="MaxImageBytes"/>, or
    /// null. This project has no imaging; the host that does supplies it. Without one,
    /// only images already small enough are sent.
    /// </param>
    public SessionContentSync(Func<string, CodexThreadRecord?> findThread, Func<byte[], string, byte[]?>? shrinkImage = null)
    {
        _findThread = findThread;
        _shrinkImage = shrinkImage ?? ((bytes, _) => bytes.Length <= MaxImageBytes ? bytes : null);
    }

    /// <summary>The latest <paramref name="turns"/> turns, and a cursor to follow from.</summary>
    public SessionSnapshot? Open(string threadId, int turns = DefaultTurns)
    {
        if (Locate(threadId) is not (CodexThreadRecord record, TurnIndex index))
        {
            return null;
        }

        int first = Math.Max(0, index.Turns.Count - Math.Max(1, turns));
        long from = index.Turns.Count == 0 ? 0 : index.Turns[first].Start;
        (List<SyncItem> items, long next) = ProjectRange(record, index, from, to: null);
        SessionPage page = Trim(items, hasOlder: first > 0, dropStaleRunning: true);

        var header = new SessionHeader(
            record.Id,
            record.Title,
            record.Cwd,
            record.Model,
            PermissionModes.Classify(record.SandboxPolicy, record.ApprovalMode),
            index.OpenTurnId);

        return new SessionSnapshot(header, page, new SyncCursor(record.RolloutPath, next));
    }

    /// <summary>Up to <paramref name="turns"/> turns before <paramref name="beforeTurnId"/>.</summary>
    public SessionPage? History(string threadId, string beforeTurnId, int turns = DefaultTurns)
    {
        if (Locate(threadId) is not (CodexThreadRecord record, TurnIndex index))
        {
            return null;
        }

        int before = index.IndexOf(beforeTurnId);
        if (before <= 0)
        {
            return new SessionPage([], HasOlder: false, TruncatedTurnId: null);
        }

        int first = Math.Max(0, before - Math.Max(1, turns));
        (List<SyncItem> items, _) = ProjectRange(record, index, index.Turns[first].Start, index.Turns[before].Start);
        return Trim(items, hasOlder: first > 0, dropStaleRunning: true);
    }

    /// <summary>Everything completed since <paramref name="cursor"/>.</summary>
    public SessionDelta? ReadSince(string threadId, SyncCursor cursor)
    {
        if (Locate(threadId) is not (CodexThreadRecord record, TurnIndex index))
        {
            return null;
        }

        bool sameFile = string.Equals(record.RolloutPath, cursor.RolloutPath, StringComparison.OrdinalIgnoreCase);
        if (!sameFile || RolloutFile.LengthOf(record.RolloutPath) < cursor.Offset || cursor.Offset < 0)
        {
            return new SessionDelta([], new SyncCursor(record.RolloutPath, 0), Resync: true);
        }

        (List<SyncItem> items, long next) = ProjectRange(record, index, cursor.Offset, to: null);
        return new SessionDelta(items, new SyncCursor(record.RolloutPath, next), Resync: false);
    }

    /// <summary>A command's output, a change's diff, or an image — found by item id only.</summary>
    /// <remarks>
    /// The phone never names a file. The path of an image comes from the rollout record
    /// of that item in that conversation; otherwise this call would read any file on the
    /// machine for whoever controls the phone or the server.
    /// </remarks>
    public SessionDetail ReadDetail(string threadId, string turnId, string itemId, DetailPart part, int index = 0)
    {
        if (Locate(threadId) is not (CodexThreadRecord record, TurnIndex turns) ||
            turns.IndexOf(turnId) is var position && position < 0)
        {
            return new SessionDetail(DetailOutcome.NotFound);
        }

        // From the turn's start to the end of the file: a turn's lines can trail its end.
        (List<RolloutLine> lines, _) = RolloutFile.ReadLines(record.RolloutPath, turns.Turns[position].Start);
        foreach (RolloutLine line in lines)
        {
            using JsonDocument document = JsonDocument.Parse(line.Bytes);
            if (FindCompletedItem(document.RootElement, turnId, itemId) is JsonElement item)
            {
                return ReadDetailOf(item, part, index);
            }

            // A shell_command has no completed item; its output line is found by call id.
            if (part == DetailPart.Output && FindShellOutput(document.RootElement, itemId) is string shell)
            {
                string body = RolloutProjector.ShellOutputBody(shell);
                return body.Length > MaxOutputChars
                    ? new SessionDetail(DetailOutcome.Found, Text: body[^MaxOutputChars..], Truncated: true)
                    : new SessionDetail(DetailOutcome.Found, Text: body);
            }
        }

        return new SessionDetail(DetailOutcome.NotFound);
    }

    private static string? FindShellOutput(JsonElement line, string callId) =>
        RolloutProjector.Str(line, "type") == "response_item" &&
        line.TryGetProperty("payload", out JsonElement payload) &&
        RolloutProjector.Str(payload, "type") == "function_call_output" &&
        RolloutProjector.Str(payload, "call_id") == callId
            ? RolloutProjector.Str(payload, "output")
            : null;

    private static JsonElement? FindCompletedItem(JsonElement line, string turnId, string itemId) =>
        RolloutProjector.Str(line, "type") == "event_msg" &&
        line.TryGetProperty("payload", out JsonElement payload) &&
        RolloutProjector.Str(payload, "type") == "item_completed" &&
        RolloutProjector.Str(payload, "turn_id") == turnId &&
        payload.TryGetProperty("item", out JsonElement item) &&
        RolloutProjector.Str(item, "id") == itemId
            ? item
            : null;

    private SessionDetail ReadDetailOf(JsonElement item, DetailPart part, int index)
    {
        switch (part, RolloutProjector.Str(item, "type"))
        {
            case (DetailPart.Output, "CommandExecution"):
                string output = RolloutProjector.Str(item, "aggregated_output")
                    ?? RolloutProjector.Str(item, "stdout") + RolloutProjector.Str(item, "stderr");
                return output.Length > MaxOutputChars
                    ? new SessionDetail(DetailOutcome.Found, Text: output[^MaxOutputChars..], Truncated: true)
                    : new SessionDetail(DetailOutcome.Found, Text: output);

            case (DetailPart.Diff, "FileChange"):
                var diff = new StringBuilder();
                if (item.TryGetProperty("changes", out JsonElement changes) && changes.ValueKind == JsonValueKind.Object)
                {
                    foreach (JsonProperty change in changes.EnumerateObject())
                    {
                        diff.Append("--- ").Append(change.Name).Append('\n')
                            .Append(RolloutProjector.Str(change.Value, "unified_diff")).Append('\n');
                    }
                }

                return diff.Length > MaxDiffChars
                    ? new SessionDetail(DetailOutcome.Found, Text: diff.ToString(0, MaxDiffChars), Truncated: true)
                    : new SessionDetail(DetailOutcome.Found, Text: diff.ToString());

            case (DetailPart.Image, "UserMessage"):
                string? attached = item.TryGetProperty("content", out JsonElement content) && content.ValueKind == JsonValueKind.Array
                    ? content.EnumerateArray()
                        .Where(c => RolloutProjector.Str(c, "type") == "local_image")
                        .Select(c => RolloutProjector.Str(c, "path"))
                        .ElementAtOrDefault(index)
                    : null;
                return ReadImage(attached);

            case (DetailPart.Image, "ImageView") when index == 0:
                return ReadImage(RolloutProjector.Str(item, "path"));

            default:
                return new SessionDetail(DetailOutcome.NotFound);
        }
    }

    private SessionDetail ReadImage(string? recorded)
    {
        if (ToLocalPath(recorded) is not string path)
        {
            return new SessionDetail(DetailOutcome.NotFound);
        }

        // UNC paths are refused: opening one sends the user's credentials to that host.
        if (path.StartsWith(@"\\", StringComparison.Ordinal) ||
            !ImageTypes.TryGetValue(Path.GetExtension(path), out string? mediaType))
        {
            return new SessionDetail(DetailOutcome.Refused);
        }

        if (!File.Exists(path))
        {
            return new SessionDetail(DetailOutcome.Missing);
        }

        byte[] bytes = File.ReadAllBytes(path);
        return _shrinkImage(bytes, mediaType) is byte[] sendable && sendable.Length <= MaxImageBytes
            ? new SessionDetail(DetailOutcome.Found, Bytes: sendable, MediaType: mediaType)
            : new SessionDetail(DetailOutcome.Refused);
    }

    /// <summary>Rollouts record image paths either plainly or as <c>file:///</c> URIs.</summary>
    private static string? ToLocalPath(string? recorded)
    {
        if (string.IsNullOrWhiteSpace(recorded))
        {
            return null;
        }

        if (recorded.StartsWith("file:", StringComparison.OrdinalIgnoreCase))
        {
            return Uri.TryCreate(recorded, UriKind.Absolute, out Uri? uri) && uri.IsFile && !uri.IsUnc ? uri.LocalPath : null;
        }

        return Path.IsPathFullyQualified(recorded) ? recorded : null;
    }

    private (CodexThreadRecord, TurnIndex)? Locate(string threadId)
    {
        if (_findThread(threadId) is not CodexThreadRecord record || !File.Exists(record.RolloutPath))
        {
            return null;
        }

        lock (_gate)
        {
            if (!_indexes.TryGetValue(threadId, out TurnIndex? index) ||
                !string.Equals(index.Path, record.RolloutPath, StringComparison.OrdinalIgnoreCase) ||
                RolloutFile.LengthOf(record.RolloutPath) < index.IndexedTo)
            {
                index = new TurnIndex(record.RolloutPath);
                _indexes[threadId] = index;
            }

            index.CatchUp();
            return (record, index);
        }
    }

    /// <remarks>
    /// Projection carries state across lines: a <c>shell_command</c>'s result is built
    /// from its call line and its output line, which a follower may see in different
    /// reads. The state at the end of the last read is kept per conversation; a cursor
    /// that does not match it (another phone, a reconnect) rebuilds the state by
    /// replaying the turn open at that point from its start.
    /// </remarks>
    private (List<SyncItem> Items, long Next) ProjectRange(CodexThreadRecord record, TurnIndex index, long from, long? to)
    {
        ProjectionState state = StateAt(record, index, from);
        (List<RolloutLine> lines, long next) = RolloutFile.ReadLines(record.RolloutPath, from, to);
        List<SyncItem> items = Project(lines, state);

        if (to is null)
        {
            lock (_gate)
            {
                _followStates[record.Id] = (next, state);
            }
        }

        return (items, next);
    }

    private ProjectionState StateAt(CodexThreadRecord record, TurnIndex index, long offset)
    {
        lock (_gate)
        {
            if (_followStates.TryGetValue(record.Id, out var kept) && kept.Offset == offset)
            {
                _followStates.Remove(record.Id);
                return kept.State;
            }
        }

        string? openTurn = index.OpenTurnAt(offset);
        var state = new ProjectionState(null, record.Cwd);
        if (openTurn is not null && index.Turns[index.IndexOf(openTurn)].Start is long start && start < offset)
        {
            (List<RolloutLine> replay, _) = RolloutFile.ReadLines(record.RolloutPath, start, offset);
            _ = Project(replay, state);
        }
        else
        {
            state.OpenTurnId = openTurn;
        }

        return state;
    }

    private static List<SyncItem> Project(List<RolloutLine> lines, ProjectionState state)
    {
        var items = new List<SyncItem>();
        foreach (RolloutLine line in lines)
        {
            using JsonDocument document = JsonDocument.Parse(line.Bytes);
            if (RolloutProjector.Project(document.RootElement, line.Offset, state) is SyncItem item)
            {
                items.Add(item);
            }
        }

        return items;
    }

    /// <summary>
    /// Caps a page at <see cref="MaxItemsPerPage"/>, dropping whole early turns first.
    /// </summary>
    /// <param name="dropStaleRunning">
    /// A page shows history. A "running" card is only true if nothing followed it in its
    /// turn; any earlier one would show a command as still running that long finished.
    /// </param>
    private static SessionPage Trim(List<SyncItem> items, bool hasOlder, bool dropStaleRunning)
    {
        if (dropStaleRunning)
        {
            var lastInTurn = new Dictionary<string, long>(StringComparer.Ordinal);
            foreach (SyncItem item in items.Where(i => i.TurnId is not null))
            {
                lastInTurn[item.TurnId!] = item.Seq;
            }

            items = items
                .Where(i => i.Kind != SyncItemKind.Running || (i.TurnId is not null && lastInTurn[i.TurnId] == i.Seq))
                .ToList();
        }

        if (items.Count <= MaxItemsPerPage)
        {
            return new SessionPage(items, hasOlder, TruncatedTurnId: null);
        }

        // Drop whole turns from the front while that is enough.
        var turnOrder = items.Where(i => i.Kind == SyncItemKind.TurnStarted).Select(i => i.TurnId!).ToList();
        foreach (string turn in turnOrder.Take(Math.Max(0, turnOrder.Count - 1)))
        {
            items = items.Where(i => i.TurnId != turn).ToList();
            hasOlder = true;
            if (items.Count <= MaxItemsPerPage)
            {
                return new SessionPage(items, hasOlder, TruncatedTurnId: null);
            }
        }

        // One turn alone is too long: keep its latest items.
        List<SyncItem> kept = items.Skip(items.Count - MaxItemsPerPage).ToList();
        return new SessionPage(kept, hasOlder, TruncatedTurnId: kept[0].TurnId);
    }
}
