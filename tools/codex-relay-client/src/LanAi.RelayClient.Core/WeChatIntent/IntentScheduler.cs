namespace LanAi.RelayClient.WeChatIntent;

/// <summary>Messages the queue has decided to judge now, all from one conversation.</summary>
internal sealed record DueBatch(string Chat, IReadOnlyList<ChatItem> Items);

/// <summary>
/// Which of the other person's messages to judge, and when (docs §5.1, as revised after the first
/// self-test): every one of their messages on screen gets a card, so every one without a card is
/// queued. Pure logic over an explicit clock.
/// </summary>
/// <remarks>
/// <list type="bullet">
/// <item>A screen's messages are judged once it has been settled for a second — a scroll that is
/// still going produces a new screen before that, which replaces the old wait.</item>
/// <item>At most <see cref="MaxPerBatch"/> per screen, newest first, and
/// <see cref="PerMinute"/> requests a minute overall. Jev is cheap (about US$0.00005 a message) and
/// TypeSafe allows 1200 a minute; the limit only stops a fault from spinning.</item>
/// <item>A message being judged is not queued again, and one whose judgement failed waits
/// <see cref="RetryAfter"/> before it is tried again.</item>
/// </list>
/// </remarks>
internal sealed class IntentScheduler
{
    public static readonly TimeSpan Settle = TimeSpan.FromSeconds(1);
    public static readonly TimeSpan RetryAfter = TimeSpan.FromSeconds(60);
    public static readonly TimeSpan Window = TimeSpan.FromMinutes(1);
    public const int PerMinute = 60;
    public const int MaxPerBatch = 10;

    private readonly Queue<DateTimeOffset> _recent = new();
    private readonly List<(string Chat, ChatItem Item)> _inFlight = [];
    private readonly List<(string Chat, ChatItem Item, DateTimeOffset Until)> _failed = [];
    private Pending? _pending;

    /// <summary>Conversations the user excluded, by title.</summary>
    public ISet<string> Muted { get; } = new HashSet<string>(StringComparer.Ordinal);

    public DateTimeOffset? PausedUntil { get; set; }

    public bool IsPaused(DateTimeOffset now) => PausedUntil is { } until && now < until;

    /// <summary>A settled screen of <paramref name="chat"/>, with its messages from the other person that have no card yet.</summary>
    public void OnScreen(string chat, IReadOnlyList<ChatItem> withoutCard, DateTimeOffset now)
    {
        _pending = withoutCard.Count == 0 ? null : new Pending(chat, withoutCard, now + Settle);
    }

    /// <summary>The user switched away or the feature stopped: forget what was waiting.</summary>
    public void Cancel() => _pending = null;

    /// <summary>Called on a timer. The messages to judge now, if any; they count as in flight until <see cref="Done"/>.</summary>
    public DueBatch? Poll(DateTimeOffset now)
    {
        if (_pending is not { } pending || now < pending.DueAt || IsPaused(now) || Muted.Contains(pending.Chat))
        {
            return null;
        }

        _pending = null;
        while (_recent.Count > 0 && now - _recent.Peek() >= Window)
        {
            _recent.Dequeue();
        }

        _failed.RemoveAll(f => now >= f.Until);
        int room = Math.Min(MaxPerBatch, PerMinute - _recent.Count);
        var take = new List<ChatItem>();
        for (int i = pending.Items.Count - 1; i >= 0 && take.Count < room; i--)
        {
            ChatItem item = pending.Items[i];
            if (_inFlight.Any(f => f.Chat == pending.Chat && ChatTranscript.Same(f.Item, item))
                || _failed.Any(f => f.Chat == pending.Chat && ChatTranscript.Same(f.Item, item)))
            {
                continue;
            }

            take.Insert(0, item);
        }

        if (take.Count == 0)
        {
            return null;
        }

        foreach (ChatItem item in take)
        {
            _recent.Enqueue(now);
            _inFlight.Add((pending.Chat, item));
        }

        return new DueBatch(pending.Chat, take);
    }

    /// <summary>A judgement finished. A failure keeps the message from being retried for a minute.</summary>
    public void Done(string chat, ChatItem item, bool succeeded, DateTimeOffset now)
    {
        _inFlight.RemoveAll(f => f.Chat == chat && ReferenceEquals(f.Item, item));
        if (!succeeded)
        {
            _failed.Add((chat, item, now + RetryAfter));
        }
    }

    /// <summary>Whether <paramref name="item"/> is being judged now — it shows a 「分析中」 placeholder.</summary>
    public bool IsInFlight(string chat, ChatItem item) =>
        _inFlight.Any(f => f.Chat == chat && ChatTranscript.Same(f.Item, item));

    /// <summary>Everything forgotten: signed out, or switched off.</summary>
    public void Reset()
    {
        _pending = null;
        _inFlight.Clear();
        _failed.Clear();
    }

    private sealed record Pending(string Chat, IReadOnlyList<ChatItem> Items, DateTimeOffset DueAt);
}
