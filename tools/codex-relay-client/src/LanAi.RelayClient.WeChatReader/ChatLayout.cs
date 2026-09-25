namespace LanAi.RelayClient.WeChatReader;

/// <summary>Where the parts of the open conversation are, in the captured window's pixels.</summary>
/// <param name="Left">Left edge of the chat pane (right of the conversation list).</param>
/// <param name="Right">Right edge of the chat pane.</param>
/// <param name="HeaderTop">Top of the chat header, where the conversation's title is.</param>
/// <param name="MessagesTop">Top of the message list: just under the header.</param>
/// <param name="MessagesBottom">Bottom of the message list: just above the input box.</param>
/// <param name="Background">The chat pane's background colour.</param>
internal sealed record ChatLayout(int Left, int Right, int HeaderTop, int MessagesTop, int MessagesBottom, (int R, int G, int B) Background)
{
    /// <summary>
    /// Finds the chat pane from colour structure alone — never from text, and never from fixed
    /// proportions, which the first probe showed break as soon as the window is narrower.
    /// </summary>
    /// <remarks>
    /// Measured on WeChat 4.1.13, light theme (docs §4.5): a nav strip and the conversation list
    /// on the left, then the chat pane in one flat colour. The pane has a header, a full-width
    /// hairline above the input box, and the input box below it. Returns null when no such
    /// pane is found — the window is showing something else (contacts, moments, a login screen).
    /// </remarks>
    public static ChatLayout? Detect(Pixels p, double scale)
    {
        if (p.Width < 300 || p.Height < 300)
        {
            return null;
        }

        (int R, int G, int B) background = DominantColor(p, p.Width / 2, p.Width - 1, p.Height / 4, p.Height * 3 / 4);

        // Left edge: on several rows, where the longest run of the background colour starts.
        // Several rows because a bubble crossing one row splits its run.
        var starts = new List<int>();
        var ends = new List<int>();
        foreach (double f in new[] { 0.3, 0.45, 0.6, 0.75, 0.9 })
        {
            (int start, int end) = LongestRun(p, (int)(p.Height * f), background);
            if (end - start >= p.Width * 0.3)
            {
                starts.Add(start);
                ends.Add(end);
            }
        }

        if (starts.Count < 2)
        {
            return null;
        }

        int left = starts.Min();
        int right = Median(ends);
        int pad = (int)Math.Round(12 * scale);

        // Top of the pane: the first row, scanning down the left padding, that is background.
        int headerTop = -1;
        for (int y = 0; y < p.Height / 3; y++)
        {
            if (Near(p.At(left + pad, y), background, 3))
            {
                headerTop = y;
                break;
            }
        }

        if (headerTop < 0)
        {
            return null;
        }

        // The hairline above the input box: the highest full-width row in the lower part of
        // the pane that is uniformly not background. Bubbles never span the whole width.
        int inputLine = FindFullWidthLine(p, left + pad, right - (int)(40 * scale), p.Height * 2 / 5, p.Height - 1, background, fromTop: true);
        if (inputLine < 0)
        {
            return null;
        }

        // The header's lower edge: a full-width line or shadow near the top, else a fixed height.
        int headerLine = FindFullWidthLine(p, left + pad, right - (int)(40 * scale), headerTop + (int)(20 * scale), headerTop + (int)(90 * scale), background, fromTop: true);
        int messagesTop = headerLine > 0 ? headerLine + (int)(10 * scale) : headerTop + (int)(56 * scale);

        if (inputLine - messagesTop < 80 * scale)
        {
            return null;
        }

        return new ChatLayout(left, right, headerTop, messagesTop, inputLine - 1, background);
    }

    /// <summary>
    /// A cheap fingerprint of the message list only. The header (typing indicator) and the input
    /// box (caret) are outside it, so neither keeps the picture from counting as settled.
    /// </summary>
    public ulong Hash(Pixels p)
    {
        const ulong Offset = 14695981039346656037;
        const ulong Prime = 1099511628211;
        ulong hash = Offset;
        for (int y = MessagesTop; y <= MessagesBottom; y += 3)
        {
            int row = y * p.Width * 4;
            for (int x = Left; x < Right; x += 3)
            {
                int i = row + (x * 4);
                hash = (hash ^ p.Bgra[i]) * Prime;
                hash = (hash ^ p.Bgra[i + 1]) * Prime;
                hash = (hash ^ p.Bgra[i + 2]) * Prime;
            }
        }

        return hash;
    }

    /// <summary>
    /// The colour behind a line of text: sampled in the padding just outside the text on both
    /// sides at three heights, most common value wins. One point lands on a glyph often enough
    /// that the first probe misread several lines that way.
    /// </summary>
    public static int[] SampleBehind(Pixels p, int x, int y, int w, int h, double scale)
    {
        int gap = Math.Max(3, (int)Math.Round(5 * scale));
        var counts = new Dictionary<(int, int, int), int>();
        foreach (int sx in new[] { x - gap, x - gap - 2, x + w + gap, x + w + gap + 2 })
        {
            foreach (double fy in new[] { 0.2, 0.5, 0.8 })
            {
                (int r, int g, int b) = p.At(sx, y + (int)(h * fy));
                var key = (r & ~3, g & ~3, b & ~3);
                counts[key] = counts.GetValueOrDefault(key) + 1;
            }
        }

        (int R, int G, int B) best = counts.MaxBy(kv => kv.Value).Key;
        return [best.R, best.G, best.B];
    }

    private static (int R, int G, int B) DominantColor(Pixels p, int x0, int x1, int y0, int y1)
    {
        var counts = new Dictionary<(int, int, int), int>();
        for (int y = y0; y < y1; y += 7)
        {
            for (int x = x0; x < x1; x += 7)
            {
                (int r, int g, int b) = p.At(x, y);
                var key = (r, g, b);
                counts[key] = counts.GetValueOrDefault(key) + 1;
            }
        }

        return counts.MaxBy(kv => kv.Value).Key;
    }

    private static (int Start, int End) LongestRun(Pixels p, int y, (int R, int G, int B) color)
    {
        int bestStart = 0, bestEnd = -1, start = -1;
        for (int x = 0; x < p.Width; x++)
        {
            if (Near(p.At(x, y), color, 3))
            {
                if (start < 0)
                {
                    start = x;
                }

                if (x - start > bestEnd - bestStart)
                {
                    bestStart = start;
                    bestEnd = x;
                }
            }
            else
            {
                start = -1;
            }
        }

        return (bestStart, bestEnd);
    }

    private static int FindFullWidthLine(Pixels p, int x0, int x1, int y0, int y1, (int R, int G, int B) background, bool fromTop)
    {
        if (x1 - x0 < 50)
        {
            return -1;
        }

        int step = Math.Max(1, (x1 - x0) / 60);
        int from = fromTop ? y0 : y1;
        int to = fromTop ? y1 : y0;
        int dir = fromTop ? 1 : -1;
        // The line need not reach either end (WeChat insets it), so the test is that nearly
        // every sample along the row is one and the same non-background colour.
        var counts = new Dictionary<(int, int, int), int>();
        for (int y = from; fromTop ? y <= to : y >= to; y += dir)
        {
            counts.Clear();
            int total = 0;
            for (int x = x0; x <= x1; x += step)
            {
                total++;
                (int r, int g, int b) = p.At(x, y);
                var key = (r & ~1, g & ~1, b & ~1);
                counts[key] = counts.GetValueOrDefault(key) + 1;
            }

            KeyValuePair<(int, int, int), int> top = counts.MaxBy(kv => kv.Value);
            if (top.Value >= total * 0.85 && !Near(top.Key, background, 2))
            {
                return y;
            }
        }

        return -1;
    }

    private static bool Near((int R, int G, int B) a, (int R, int G, int B) b, int tolerance) =>
        Math.Abs(a.R - b.R) <= tolerance && Math.Abs(a.G - b.G) <= tolerance && Math.Abs(a.B - b.B) <= tolerance;

    private static int Median(List<int> values)
    {
        values.Sort();
        return values[values.Count / 2];
    }
}
