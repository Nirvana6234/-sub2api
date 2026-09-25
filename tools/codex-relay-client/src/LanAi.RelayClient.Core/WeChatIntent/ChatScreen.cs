using System.Text.RegularExpressions;

namespace LanAi.RelayClient.WeChatIntent;

internal enum ChatSpeaker
{
    /// <summary>The other person.</summary>
    Them,

    /// <summary>The user.</summary>
    Me,

    /// <summary>A centred time stamp such as 「19:12」 or 「昨天 21:03」. Kept: it helps alignment (§4.5).</summary>
    Time,
}

/// <summary>One message bubble, or one time stamp, as read off the screen.</summary>
internal sealed record ChatItem(ChatSpeaker Speaker, string Text)
{
    /// <summary>The bubble ran past the top or bottom of the message list; only part of it was read.</summary>
    public bool Partial { get; init; }
}

/// <summary>One settled picture of the open conversation, in reading order.</summary>
internal sealed record ChatScreen(string Title, bool IsGroup, IReadOnlyList<ChatItem> Items)
{
    /// <summary>
    /// Whether the list is scrolled to the newest message. Null while that cannot be told from
    /// the picture — the 「N 条新消息」 pill and the resting gap are still unmeasured (§10.12),
    /// and the transcript's tail-overlap rule is safe without it.
    /// </summary>
    public bool? IsAtBottom { get; init; }

    /// <summary>
    /// Where each of <see cref="Items"/> sits, same index, in the captured window's pixels —
    /// what the inline cards are pinned to. Kept apart from the items so a message's identity
    /// (used to align screens) never depends on where it happens to be drawn.
    /// </summary>
    public IReadOnlyList<BubbleBounds> Bounds { get; init; } = [];

    /// <summary>The message list's right edge, in the captured window's pixels.</summary>
    public int AreaRight { get; init; }
}

/// <summary>A bubble's text extent in the captured window's pixels.</summary>
internal readonly record struct BubbleBounds(int Left, int Top, int Right, int Bottom);

/// <summary>Turns a reader frame's recognised lines into bubbles (§4.5 items 1–4 and 6).</summary>
/// <remarks>
/// Every threshold here was measured on WeChat 4.1.13, light theme, 100 % scale: the other
/// person's bubbles are grey (236,236,240) and start at a fixed indent; the user's are green
/// (about 160,240,164); time stamps are small centred text on the background. Dark mode is
/// not calibrated yet, and a line whose colour matches none of these is dropped rather than
/// guessed at.
/// </remarks>
internal static partial class ChatScreenParser
{
    public static ChatScreen Parse(ReaderEvent frame, double scale)
    {
        ArgumentNullException.ThrowIfNull(frame);
        ReaderRect area = frame.Area ?? new ReaderRect();
        string title = (frame.Title ?? string.Empty).Trim();
        if (frame.Lines is not { Length: > 0 } lines || area.W <= 0)
        {
            return new ChatScreen(title, IsGroupTitle(title), []);
        }

        scale = scale > 0 ? scale : 1.0;
        (int R, int G, int B) background = frame.Background is [int br, int bg, int bb] ? (br, bg, bb) : EstimateBackground(lines);

        var bubbles = new List<Bubble>();
        foreach (ReaderLine line in lines.OrderBy(l => l.Y).ThenBy(l => l.X))
        {
            ChatSpeaker? speaker = Classify(line, area, background);
            if (speaker is null)
            {
                continue;
            }

            Bubble? last = bubbles.Count > 0 ? bubbles[^1] : null;
            if (last is not null && last.Speaker == speaker && speaker != ChatSpeaker.Time && Continues(last, line, scale))
            {
                last.Add(line);
            }
            else
            {
                bubbles.Add(new Bubble(speaker.Value, line));
            }
        }

        MarkPartial(bubbles, area, scale);

        var items = new List<ChatItem>();
        var bounds = new List<BubbleBounds>();
        foreach (Bubble bubble in bubbles)
        {
            // A cut bubble is left for the picture where it shows whole — except one that fills
            // the list from edge to edge, which never will (§4.5: 超过一屏的长消息).
            if (bubble.Partial && bubbles.Count > 1)
            {
                continue;
            }

            string text = bubble.Text();
            if (!LooksLikeText(text))
            {
                continue;
            }

            items.Add(new ChatItem(bubble.Speaker, text) { Partial = bubble.Partial });
            bounds.Add(new BubbleBounds(
                bubble.Lines.Min(l => l.X),
                bubble.Lines[0].Y,
                bubble.Lines.Max(l => l.X + l.W),
                bubble.Lines[^1].Y + bubble.Lines[^1].H));
        }

        return new ChatScreen(title, IsGroupTitle(title), items) { Bounds = bounds, AreaRight = area.X + area.W };
    }

    /// <summary>A group's title ends in its member count: 「家人群(12)」 or 「家人群（12）」.</summary>
    internal static bool IsGroupTitle(string title) => GroupTitle().IsMatch(title);

    private static ChatSpeaker? Classify(ReaderLine line, ReaderRect area, (int R, int G, int B) background)
    {
        if (line.Bg is not [int r, int g, int b])
        {
            return null;
        }

        if (g > r + 40 && g > b + 40)
        {
            return ChatSpeaker.Me;
        }

        int centre = line.X + (line.W / 2);
        int areaCentre = area.X + (area.W / 2);
        bool onBackground = Math.Abs(r - background.R) <= 4 && Math.Abs(g - background.G) <= 4 && Math.Abs(b - background.B) <= 4;
        if (onBackground)
        {
            // Centred and small: a time stamp or a system notice. Anything else on the bare
            // background (a quoted reply, a sender name, a link card's caption) is dropped.
            return Math.Abs(centre - areaCentre) <= area.W * 0.08 ? ChatSpeaker.Time : null;
        }

        // A light neutral bubble starting in the left half: the other person.
        bool neutral = Math.Abs(r - g) <= 8 && Math.Abs(g - b) <= 8 && r >= 200;
        if (neutral && line.X - area.X < area.W * 0.45)
        {
            return ChatSpeaker.Them;
        }

        return null;
    }

    /// <summary>The next line of the same bubble: same colour, same left edge, directly below.</summary>
    private static bool Continues(Bubble bubble, ReaderLine line, double scale)
    {
        ReaderLine last = bubble.Lines[^1];
        bool sameColour = last.Bg.Length == 3 && line.Bg.Length == 3
            && Math.Abs(last.Bg[0] - line.Bg[0]) <= 8 && Math.Abs(last.Bg[1] - line.Bg[1]) <= 8 && Math.Abs(last.Bg[2] - line.Bg[2]) <= 8;
        int gap = line.Y - (last.Y + last.H);
        int lineHeight = Math.Max(last.H, line.H);
        return sameColour && gap <= lineHeight * 1.1 && Math.Abs(line.X - bubble.Lines[0].X) <= 8 * scale + 6;
    }

    /// <summary>
    /// The top and bottom bubbles may be cut by the list's edges. A bubble has about ten pixels
    /// of padding above and below its text; when the first line of the topmost bubble sits
    /// closer to the edge than a padding plus a line, a line above it may be hidden, so the
    /// bubble is marked partial. Measured: a cut bubble's text sat 10 px under the edge.
    /// </summary>
    private static void MarkPartial(List<Bubble> bubbles, ReaderRect area, double scale)
    {
        if (bubbles.Count == 0)
        {
            return;
        }

        Bubble top = bubbles[0];
        if (top.Speaker != ChatSpeaker.Time && top.Lines[0].Y - area.Y < (12 * scale) + top.Lines[0].H + (8 * scale))
        {
            top.Partial = true;
        }

        Bubble bottom = bubbles[^1];
        ReaderLine lastLine = bottom.Lines[^1];
        if (bottom.Speaker != ChatSpeaker.Time && area.Bottom - (lastLine.Y + lastLine.H) < 12 * scale)
        {
            bottom.Partial = true;
        }
    }

    /// <summary>The list's background: the most common colour behind centred or unclassified lines, else WeChat's light grey.</summary>
    private static (int R, int G, int B) EstimateBackground(ReaderLine[] lines)
    {
        var counts = new Dictionary<(int, int, int), int>();
        foreach (ReaderLine line in lines)
        {
            if (line.Bg is [int r, int g, int b] && r >= 240 && g >= 240 && b >= 240)
            {
                var key = (r, g, b);
                counts[key] = counts.GetValueOrDefault(key) + 1;
            }
        }

        return counts.Count > 0 ? counts.MaxBy(kv => kv.Value).Key : (248, 248, 248);
    }

    /// <summary>
    /// Stickers and images come out of OCR as short runs of symbols. A bubble with less than two
    /// characters of text, or mostly neither CJK nor Latin, is dropped.
    /// </summary>
    internal static bool LooksLikeText(string text)
    {
        int meaningful = 0, total = 0;
        foreach (char c in text)
        {
            if (char.IsWhiteSpace(c))
            {
                continue;
            }

            total++;
            if (char.IsLetterOrDigit(c) || (c >= '一' && c <= '鿿'))
            {
                meaningful++;
            }
        }

        return meaningful >= 1 && total >= 1 && meaningful * 2 >= total && (total >= 2 || IsCjk(text.Trim()[0]));
    }

    private static bool IsCjk(char c) => c >= '一' && c <= '鿿';

    [GeneratedRegex(@"[（(]\d+[)）]\s*$")]
    private static partial Regex GroupTitle();

    private sealed class Bubble(ChatSpeaker speaker, ReaderLine first)
    {
        public ChatSpeaker Speaker { get; } = speaker;

        public List<ReaderLine> Lines { get; } = [first];

        public bool Partial { get; set; }

        public void Add(ReaderLine line) => Lines.Add(line);

        /// <summary>Wrapped lines are joined without a separator: Chinese wraps mid-sentence.</summary>
        public string Text()
        {
            var parts = Lines.Select(l => l.Text.Trim()).Where(t => t.Length > 0).ToList();
            if (parts.Count == 0)
            {
                return string.Empty;
            }

            var text = new System.Text.StringBuilder(parts[0]);
            for (int i = 1; i < parts.Count; i++)
            {
                if (IsLatinBoundary(text[^1]) && IsLatinBoundary(parts[i][0]))
                {
                    text.Append(' ');
                }

                text.Append(parts[i]);
            }

            return text.ToString();
        }

        private static bool IsLatinBoundary(char c) => c < 128 && char.IsLetterOrDigit(c);
    }
}
