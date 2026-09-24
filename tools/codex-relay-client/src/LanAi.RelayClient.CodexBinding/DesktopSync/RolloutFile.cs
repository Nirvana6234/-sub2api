using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>One complete line of a rollout and where it starts.</summary>
public readonly record struct RolloutLine(long Offset, ReadOnlyMemory<byte> Bytes);

/// <summary>Reads a rollout the desktop app is still appending to.</summary>
public static class RolloutFile
{
    /// <summary>
    /// The complete lines from <paramref name="from"/> up to <paramref name="to"/> (or the
    /// end), and the offset to continue from.
    /// </summary>
    /// <remarks>
    /// Opened for shared read/write because the desktop app holds the file open. A line
    /// without its newline yet is half-written: it is left for the next read, and the
    /// returned offset stops before it.
    /// </remarks>
    public static (List<RolloutLine> Lines, long Next) ReadLines(string path, long from, long? to = null)
    {
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        long end = Math.Min(to ?? stream.Length, stream.Length);
        var lines = new List<RolloutLine>();
        if (from >= end)
        {
            return (lines, from);
        }

        stream.Seek(from, SeekOrigin.Begin);
        byte[] data = new byte[end - from];
        stream.ReadExactly(data);

        long next = from;
        int start = 0;
        for (int i = 0; i < data.Length; i++)
        {
            if (data[i] != (byte)'\n')
            {
                continue;
            }

            int length = i - start;
            if (length > 0 && data[start + length - 1] == (byte)'\r')
            {
                length--;
            }

            if (length > 0)
            {
                lines.Add(new RolloutLine(from + start, data.AsMemory(start, length)));
            }

            start = i + 1;
            next = from + start;
        }

        return (lines, next);
    }

    public static long LengthOf(string path)
    {
        try
        {
            return new FileInfo(path).Length;
        }
        catch (IOException)
        {
            return -1;
        }
    }
}

/// <summary>Where each turn starts in a rollout, maintained incrementally.</summary>
/// <remarks>
/// Built by one scan on first use and then extended from where it stopped. Only the
/// turn boundary lines are parsed: a byte search finds the candidates first, so the
/// largest rollout measured (93.6 MB, 51 turns) indexes in well under a second.
/// </remarks>
public sealed class TurnIndex
{
    private static readonly byte[] TaskStarted = "\"task_started\""u8.ToArray();
    private static readonly byte[] TaskComplete = "\"task_complete\""u8.ToArray();
    private static readonly byte[] TurnAborted = "\"turn_aborted\""u8.ToArray();

    private readonly List<TurnSpan> _turns = [];
    private readonly Dictionary<string, int> _byId = new(StringComparer.Ordinal);

    public TurnIndex(string path) => Path = path;

    public string Path { get; }

    /// <summary>How far the file has been indexed; everything before this is in the index.</summary>
    public long IndexedTo { get; private set; }

    public IReadOnlyList<TurnSpan> Turns => _turns;

    /// <summary>Indexes whatever was appended since the last call.</summary>
    public void CatchUp()
    {
        (List<RolloutLine> lines, long next) = RolloutFile.ReadLines(Path, IndexedTo);
        foreach (RolloutLine line in lines)
        {
            ReadOnlySpan<byte> bytes = line.Bytes.Span;
            bool started = bytes.IndexOf(TaskStarted) >= 0;
            if (!started && bytes.IndexOf(TaskComplete) < 0 && bytes.IndexOf(TurnAborted) < 0)
            {
                continue;
            }

            using JsonDocument document = JsonDocument.Parse(line.Bytes);
            if (!document.RootElement.TryGetProperty("payload", out JsonElement payload) ||
                RolloutProjector.Str(payload, "turn_id") is not string turnId)
            {
                continue;
            }

            switch (RolloutProjector.Str(payload, "type"))
            {
                case "task_started" when !_byId.ContainsKey(turnId):
                    _byId[turnId] = _turns.Count;
                    _turns.Add(new TurnSpan(turnId, line.Offset, EndedAt: null));
                    break;
                case "task_complete" or "turn_aborted" when _byId.TryGetValue(turnId, out int index):
                    _turns[index] = _turns[index] with { EndedAt = line.Offset };
                    break;
            }
        }

        IndexedTo = next;
    }

    public int IndexOf(string turnId) => _byId.TryGetValue(turnId, out int index) ? index : -1;

    /// <summary>The turn still open at the end of what is indexed, if any.</summary>
    public string? OpenTurnId => _turns.Count > 0 && _turns[^1].EndedAt is null ? _turns[^1].TurnId : null;

    /// <summary>The turn open at <paramref name="offset"/>, for resuming projection mid-file.</summary>
    public string? OpenTurnAt(long offset)
    {
        for (int i = _turns.Count - 1; i >= 0; i--)
        {
            if (_turns[i].Start < offset)
            {
                return _turns[i].EndedAt is null || _turns[i].EndedAt >= offset ? _turns[i].TurnId : null;
            }
        }

        return null;
    }
}

/// <param name="EndedAt">
/// Offset of its <c>task_complete</c>/<c>turn_aborted</c>. Lines belonging to the turn
/// can still follow it — a background command that finishes late — so a turn's lines are
/// found by its id, not by this range.
/// </param>
public sealed record TurnSpan(string TurnId, long Start, long? EndedAt);
