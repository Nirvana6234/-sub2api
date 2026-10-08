namespace LanAi.RelayClient.WeChatIntent;

/// <summary>A batch the queue has decided to judge now, in one request.</summary>
/// <param name="Chat">The conversation.</param>
/// <param name="Items">The batch's messages, at most <see cref="IntentScheduler.MaxPerBatch"/>, the newest.</param>
internal sealed record DueBatch(string Chat, IReadOnlyList<ChatItem> Items)
{
    /// <summary>The message the batch ends with — what its card is pinned to, and what it is known by.</summary>
    public ChatItem Last => Items[^1];

    /// <summary>The conversation up to and including <see cref="Last"/>.</summary>
    public IReadOnlyList<ChatItem> Context { get; init; } = [];

    /// <summary>The batch sits under a 「昨天」 or dated stamp.</summary>
    public bool Stale { get; init; }
}

/// <summary>
/// When to judge the batches on screen (docs §5.1, §6): the other person's messages between two of
/// the user's, each batch judged in one request once the screen has settled. Pure logic over an
/// explicit clock.
/// </summary>
/// <remarks>
/// <list type="bullet">
/// <item>A screen's batches are judged once it has been settled for a second — a scroll that is
/// still going produces a new screen before that, which replaces the old wait.</item>
/// <item>A batch is known by its last message, the transcript's own item. One whose last message
/// is being judged is not queued again; one that changed is the caller's to offer again.</item>
/// <item>At most the newest <see cref="MaxPerBatch"/> messages of a batch, and
/// <see cref="PerMinute"/> requests a minute overall. Jev is cheap (about US$0.00005 a message) and
/// TypeSafe allows 1200 a minute; the limit only stops a fault from spinning.</item>
/// <item>A batch whose judgement failed waits <see cref="RetryAfter"/> before it is tried again.</item>
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

    /// <summary>A settled screen of <paramref name="chat"/>, with the batches on it waiting for a judgement.</summary>
    /// <remarks>
    /// A screen still showing some of the waiting messages keeps their time: the picture can change
    /// without the conversation moving (a scrollbar fading in and out under the pointer, a typing
    /// indicator), and restarting the wait on each of those meant it never ran out — on one test
    /// machine nothing was judged after the first screen.
    /// </remarks>
    public void OnScreen(string chat, IReadOnlyList<ChatBatch> batches, DateTimeOffset now)
    {
        if (batches.Count == 0)
        {
            _pending = null;
            return;
        }

        DateTimeOffset due = now + Settle;
        if (_pending is { } waiting && waiting.Chat == chat && waiting.DueAt < due
            && batches.SelectMany(b => b.Items).Any(i => waiting.Batches.SelectMany(b => b.Items).Any(w => ChatTranscript.Same(w, i))))
        {
            due = waiting.DueAt;
        }

        _pending = new Pending(chat, batches, due);
    }

    /// <summary>One batch, its messages as their own context: for tests.</summary>
    internal void OnScreen(string chat, IReadOnlyList<ChatItem> batch, DateTimeOffset now) =>
        OnScreen(chat, batch.Count == 0 ? [] : [new ChatBatch(batch, batch, Stale: false)], now);

    /// <summary>The user switched away or the feature stopped: forget what was waiting.</summary>
    public void Cancel() => _pending = null;

    /// <summary>
    /// Called on a timer. The batches to judge now, the lowest on screen first; each counts as in
    /// flight until <see cref="Done"/>.
    /// </summary>
    public IReadOnlyList<DueBatch> Poll(DateTimeOffset now)
    {
        if (_pending is not { } pending || now < pending.DueAt || IsPaused(now) || Muted.Contains(pending.Chat))
        {
            return [];
        }

        _pending = null;
        while (_recent.Count > 0 && now - _recent.Peek() >= Window)
        {
            _recent.Dequeue();
        }

        _failed.RemoveAll(f => now >= f.Until);
        var due = new List<DueBatch>();
        foreach (ChatBatch batch in pending.Batches.Reverse())
        {
            if (_recent.Count >= PerMinute)
            {
                break;
            }

            if (IsJudging(pending.Chat, batch.Last) || _failed.Any(f => f.Chat == pending.Chat && ReferenceEquals(f.Item, batch.Last)))
            {
                continue;
            }

            _recent.Enqueue(now);
            _inFlight.Add((pending.Chat, batch.Last));
            due.Add(new DueBatch(pending.Chat, batch.Items.TakeLast(MaxPerBatch).ToList()) { Context = batch.Context, Stale = batch.Stale });
        }

        return due;
    }

    /// <summary>A judgement finished. A failure keeps the batch from being retried for a minute.</summary>
    public void Done(string chat, ChatItem last, bool succeeded, DateTimeOffset now)
    {
        _inFlight.RemoveAll(f => f.Chat == chat && ReferenceEquals(f.Item, last));
        if (!succeeded)
        {
            _failed.Add((chat, last, now + RetryAfter));
        }
    }

    /// <summary>
    /// Whether the batch ending in this very message — the transcript's item, not its text — is
    /// being judged. By identity: 「好」 then another 「好」 is a batch that grew, not the same one.
    /// </summary>
    public bool IsJudging(string chat, ChatItem last) =>
        _inFlight.Any(f => f.Chat == chat && ReferenceEquals(f.Item, last));

    /// <summary>
    /// Whether a bubble on screen reads like the end of a batch being judged — it shows a 「分析中」
    /// placeholder. By text, since a screen's bubbles are not the transcript's items.
    /// </summary>
    public bool IsInFlight(string chat, ChatItem item) =>
        _inFlight.Any(f => f.Chat == chat && ChatTranscript.Same(f.Item, item));

    /// <summary>Everything forgotten: signed out, or switched off.</summary>
    public void Reset()
    {
        _pending = null;
        _inFlight.Clear();
        _failed.Clear();
    }

    private sealed record Pending(string Chat, IReadOnlyList<ChatBatch> Batches, DateTimeOffset DueAt);
}
