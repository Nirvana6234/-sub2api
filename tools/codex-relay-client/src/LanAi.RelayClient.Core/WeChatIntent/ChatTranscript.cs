namespace LanAi.RelayClient.WeChatIntent;

/// <summary>What one screen meant for a conversation's transcript.</summary>
internal sealed record TranscriptUpdate
{
    public static TranscriptUpdate Nothing { get; } = new();

    /// <summary>
    /// The other person's messages that are genuinely new — appeared below the known end, or
    /// the last batch on first sight. Empty when nothing should be judged.
    /// </summary>
    public IReadOnlyList<ChatItem> NewFromThem { get; init; } = [];

    /// <summary>This was the first screen seen of the conversation.</summary>
    public bool FirstSight { get; init; }

    /// <summary>The newest batch sits under a 「昨天」 or dated time stamp (§4.5).</summary>
    public bool Stale { get; init; }

    /// <summary>The screen could not be placed in the transcript at all.</summary>
    public bool Unaligned { get; init; }
}

/// <summary>
/// One conversation's messages as far as they have been seen, stitched together from screens
/// that scroll (docs §4.5 item 5). Memory only; never written anywhere.
/// </summary>
/// <remarks>
/// <para>
/// A screen is a window onto the conversation that moves when the user scrolls and when a new
/// message pushes everything up. So a screen is never taken as the conversation: it is placed
/// in the transcript by the longest <em>run</em> of consecutive items the two share.
/// </para>
/// <para>
/// A run and not a set, because 「好」 and 「嗯」 repeat: matching single messages by content
/// would take a new 「好」 for an old one. At least <see cref="MinimumRun"/> consecutive items
/// must agree (all of them when the screen holds fewer), and time stamps take part in the run,
/// which makes a false placement much less likely.
/// </para>
/// </remarks>
internal sealed class ChatTranscript
{
    public const int Capacity = 50;
    public const int MinimumRun = 3;

    private readonly List<ChatItem> _items = [];

    /// <summary>Set while the user is reading history too far back to place against the live transcript.</summary>
    private List<ChatItem>? _detached;

    public IReadOnlyList<ChatItem> Items => _items;

    /// <summary>The last <paramref name="count"/> messages, time stamps left out, oldest first — the context sent to Jev.</summary>
    public IReadOnlyList<ChatItem> Recent(int count) =>
        _items.Where(i => i.Speaker != ChatSpeaker.Time).TakeLast(count).ToList();

    /// <summary>
    /// The context for judging <paramref name="message"/>: the messages up to and including it, time
    /// stamps left out, the last <paramref name="count"/>. Nothing after it — the model is not to
    /// see the replies it drew (measured: with them, its answers drift). Null when the message is
    /// not in the live transcript (the user is reading history far back).
    /// </summary>
    public IReadOnlyList<ChatItem>? ContextUpTo(ChatItem message, int count)
    {
        for (int i = _items.Count - 1; i >= 0; i--)
        {
            if (_items[i].Speaker == message.Speaker && Same(_items[i], message))
            {
                return _items.Take(i + 1).Where(item => item.Speaker != ChatSpeaker.Time).TakeLast(count).ToList();
            }
        }

        return null;
    }

    /// <summary>
    /// The other person's messages after the user's last one — the whole batch being answered,
    /// not only the part that arrived on the latest screen.
    /// </summary>
    public IReadOnlyList<ChatItem> TrailingFromThem()
    {
        var batch = new List<ChatItem>();
        for (int i = _items.Count - 1; i >= 0; i--)
        {
            ChatItem item = _items[i];
            if (item.Speaker == ChatSpeaker.Me)
            {
                break;
            }

            if (item.Speaker == ChatSpeaker.Them)
            {
                batch.Insert(0, item);
            }
        }

        return batch;
    }

    public TranscriptUpdate Apply(ChatScreen screen)
    {
        ArgumentNullException.ThrowIfNull(screen);
        List<ChatItem> seen = screen.Items.Where(i => !i.Partial || screen.Items.Count == 1).ToList();
        if (seen.Count == 0)
        {
            return TranscriptUpdate.Nothing;
        }

        if (_items.Count == 0)
        {
            return FirstSight(seen, screen.IsAtBottom);
        }

        // Back from a jump into history? Only a screen that joins the live transcript ends it.
        if (_detached is not null)
        {
            if (!Joins(_items, seen))
            {
                Place(_detached, seen, judge: false, screen.IsAtBottom);
                return new TranscriptUpdate { Unaligned = true };
            }

            _detached = null;
        }

        if (Place(_items, seen, judge: true, screen.IsAtBottom) is { } placed)
        {
            return placed;
        }

        // No place for it: a jump far back in history, or a different conversation under the
        // same title. It is followed in a detached copy while the live transcript — whose end is
        // the conversation's real end — stays as it was. Scrolling back down then continues the
        // detached copy, which looks exactly like new messages arriving; judging from it would
        // take history for news, so nothing is judged until a screen joins the live end again.
        _detached = [.. seen];
        return new TranscriptUpdate { Unaligned = true };
    }

    /// <summary>Whether <paramref name="seen"/> continues <paramref name="items"/>' end or lies inside it.</summary>
    private static bool Joins(List<ChatItem> items, List<ChatItem> seen)
    {
        int need = Math.Min(MinimumRun, seen.Count);
        for (int run = Math.Min(items.Count, seen.Count); run >= need; run--)
        {
            if (Matches(items, items.Count - run, seen, 0, run))
            {
                return true;
            }
        }

        for (int start = 0; start + seen.Count <= items.Count; start++)
        {
            if (Matches(items, start, seen, 0, seen.Count))
            {
                return true;
            }
        }

        return false;
    }

    /// <summary>
    /// Places <paramref name="seen"/> in <paramref name="items"/> by the three rules. Null when it
    /// fits nowhere — except for a detached copy (<paramref name="judge"/> false), which is then
    /// simply replaced.
    /// </summary>
    private static TranscriptUpdate? Place(List<ChatItem> items, List<ChatItem> seen, bool judge, bool? atBottom)
    {
        int need = Math.Min(MinimumRun, seen.Count);

        // 1. The screen continues the transcript: its first items are the transcript's last.
        //    Whatever follows the overlap is new.
        for (int run = Math.Min(items.Count, seen.Count); run >= need; run--)
        {
            if (Matches(items, items.Count - run, seen, 0, run))
            {
                List<ChatItem> added = seen.Skip(run).ToList();
                if (added.Count == 0)
                {
                    return TranscriptUpdate.Nothing;
                }

                items.AddRange(added);
                Trim(items);

                // Scrolled up, a new message is announced by a pill, not shown in the list, so
                // anything "added" then is the user scrolling back down past what was read.
                return !judge || atBottom == false ? TranscriptUpdate.Nothing : Judge(added);
            }
        }

        // 2. The screen lies inside the transcript: the user scrolled up to something already read.
        for (int start = 0; start + seen.Count <= items.Count; start++)
        {
            if (Matches(items, start, seen, 0, seen.Count))
            {
                return TranscriptUpdate.Nothing;
            }
        }

        // 3. The screen reaches further back than the transcript: earlier history, kept as context.
        for (int run = Math.Min(items.Count, seen.Count - 1); run >= need; run--)
        {
            if (Matches(seen, seen.Count - run, items, 0, run))
            {
                // Only as much as fits: trimming works from the front, and would throw the
                // just-added history straight back out, leaving the next screen up unplaceable.
                int room = Capacity - items.Count;
                if (room > 0)
                {
                    int earlier = seen.Count - run;
                    int take = Math.Min(room, earlier);
                    items.InsertRange(0, seen.Skip(earlier - take).Take(take));
                }

                return TranscriptUpdate.Nothing;
            }
        }

        if (!judge)
        {
            items.Clear();
            items.AddRange(seen);
            return TranscriptUpdate.Nothing;
        }

        return null;
    }

    /// <summary>
    /// First sight: the screen becomes the transcript. When it ends with the other person's
    /// messages — they wrote while the window was elsewhere — that batch is judged once (§4.5).
    /// </summary>
    private TranscriptUpdate FirstSight(List<ChatItem> seen, bool? atBottom)
    {
        Append(seen);
        if (atBottom == false || seen[^1].Speaker != ChatSpeaker.Them)
        {
            return new TranscriptUpdate { FirstSight = true };
        }

        return Judge(seen) with { FirstSight = true };
    }

    /// <summary>The trailing run of the other person's messages in <paramref name="added"/>, and whether it is old.</summary>
    private static TranscriptUpdate Judge(List<ChatItem> added)
    {
        if (added[^1].Speaker != ChatSpeaker.Them)
        {
            return TranscriptUpdate.Nothing;
        }

        int start = added.Count - 1;
        while (start > 0 && added[start - 1].Speaker == ChatSpeaker.Them)
        {
            start--;
        }

        bool stale = start > 0 && added[start - 1].Speaker == ChatSpeaker.Time && IsOldStamp(added[start - 1].Text);
        return new TranscriptUpdate { NewFromThem = added.Skip(start).ToList(), Stale = stale };
    }

    /// <summary>「昨天」, a weekday, or a date. Today's stamps are a bare time such as 「19:12」.</summary>
    internal static bool IsOldStamp(string stamp) =>
        stamp.Contains("昨天", StringComparison.Ordinal)
        || stamp.Contains("星期", StringComparison.Ordinal)
        || stamp.Contains("周", StringComparison.Ordinal)
        || stamp.Contains('月')
        || stamp.Contains('/')
        || stamp.Contains('年');

    private void Append(IEnumerable<ChatItem> items)
    {
        _items.AddRange(items);
        Trim(_items);
    }

    private static void Trim(List<ChatItem> items)
    {
        if (items.Count > Capacity)
        {
            items.RemoveRange(0, items.Count - Capacity);
        }
    }

    private static bool Matches(IReadOnlyList<ChatItem> a, int aStart, IReadOnlyList<ChatItem> b, int bStart, int length)
    {
        for (int i = 0; i < length; i++)
        {
            if (!Same(a[aStart + i], b[bStart + i]))
            {
                return false;
            }
        }

        return true;
    }

    /// <summary>
    /// Same speaker and the same text, allowing the small differences OCR makes when the same
    /// bubble is read at a slightly different position: a character or so in ten.
    /// </summary>
    internal static bool Same(ChatItem a, ChatItem b)
    {
        if (a.Speaker != b.Speaker)
        {
            return false;
        }

        string x = Normalize(a.Text), y = Normalize(b.Text);
        if (x == y)
        {
            return true;
        }

        int longer = Math.Max(x.Length, y.Length);
        if (longer < 5)
        {
            return false;
        }

        return Distance(x, y) <= longer / 10 + (longer >= 20 ? 1 : 0);
    }

    /// <summary>Two readings of one conversation title: equal, or one character apart in a title of three or more.</summary>
    internal static bool SameTitle(string a, string b)
    {
        string x = Normalize(a), y = Normalize(b);
        return x == y || (Math.Min(x.Length, y.Length) >= 3 && Distance(x, y) <= 1);
    }

    private static string Normalize(string text)
    {
        var s = new System.Text.StringBuilder(text.Length);
        foreach (char c in text)
        {
            if (char.IsWhiteSpace(c))
            {
                continue;
            }

            // Full-width ASCII forms read as their half-width selves.
            s.Append(c is >= '！' and <= '～' ? (char)(c - 0xfee0) : c);
        }

        return s.ToString();
    }

    private static int Distance(string a, string b)
    {
        var previous = new int[b.Length + 1];
        var current = new int[b.Length + 1];
        for (int j = 0; j <= b.Length; j++)
        {
            previous[j] = j;
        }

        for (int i = 1; i <= a.Length; i++)
        {
            current[0] = i;
            for (int j = 1; j <= b.Length; j++)
            {
                int cost = a[i - 1] == b[j - 1] ? 0 : 1;
                current[j] = Math.Min(Math.Min(current[j - 1] + 1, previous[j] + 1), previous[j - 1] + cost);
            }

            (previous, current) = (current, previous);
        }

        return previous[b.Length];
    }
}
