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
    public static ChatLayout? Detect(Pixels p, double scale) => Detect(p, scale, out _);

    /// <param name="reason">When null is returned: which rule failed, with its numbers, for the log. Never text.</param>
    public static ChatLayout? Detect(Pixels p, double scale, out string reason)
    {
        reason = string.Empty;
        if (p.Width < 300 || p.Height < 300)
        {
            reason = $"截图太小（{p.Width}x{p.Height}）";
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
            reason = $"右半边主色 {background} 的横向长段只有 {starts.Count} 行达到宽度的 30%（需要 2 行）";
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
            reason = $"聊天区左边 x={left + pad} 往下找不到背景色 {background}（聊天区 x={left}..{right}）";
            return null;
        }

        // The hairline above the input box: the highest full-width row in the lower part of
        // the pane that is uniformly not background. Bubbles never span the whole width.
        int inputLine = FindFullWidthLine(p, left + pad, right - (int)(40 * scale), p.Height * 2 / 5, p.Height - 1, background, fromTop: true);
        if (inputLine < 0)
        {
            reason = $"找不到输入框上方的分隔线（聊天区 x={left}..{right}，背景 {background}）";
            return null;
        }

        // The header's lower edge: a full-width line or shadow near the top, else a fixed height.
        int headerLine = FindFullWidthLine(p, left + pad, right - (int)(40 * scale), headerTop + (int)(20 * scale), headerTop + (int)(90 * scale), background, fromTop: true);
        int messagesTop = headerLine > 0 ? headerLine + (int)(10 * scale) : headerTop + (int)(56 * scale);

        if (inputLine - messagesTop < 80 * scale)
        {
            reason = $"消息区太矮（y={messagesTop}..{inputLine}）";
            return null;
        }

        return new ChatLayout(left, right, headerTop, messagesTop, inputLine - 1, background);
    }

    /// <summary>
    /// A cheap fingerprint of the message list only. The header (typing indicator) and the input
    /// box (caret) are outside it, so neither keeps the picture from counting as settled.
    /// </summary>
    /// <remarks>
    /// Stops short of the window's right edge: WeChat's overlay scrollbar sits there, shown while
    /// the pointer moves over the list and faded out a moment later. Counted in, it made every
    /// mouse movement a "scroll" (measured on a 150 % display: the detected right edge flipping
    /// between 2243 and 2229 every second or two), and no screen ever stayed still long enough to
    /// be judged. A fixed distance from the window's edge rather than from <see cref="Right"/>,
    /// which itself moves with the scrollbar.
    /// </remarks>
    public ulong Hash(Pixels p, double scale)
    {
        const ulong Offset = 14695981039346656037;
        const ulong Prime = 1099511628211;
        int right = Math.Min(Right, p.Width - (int)Math.Round(40 * scale));
        ulong hash = Offset;
        for (int y = MessagesTop; y <= MessagesBottom; y += 3)
        {
            int row = y * p.Width * 4;
            for (int x = Left; x < right; x += 3)
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
    /// Same place for the log's purposes: the right edge moves by the scrollbar's width whenever it
    /// shows or hides, which is not worth a line each time.
    /// </summary>
    public bool SameAs(ChatLayout? other, double scale) =>
        other is not null && Left == other.Left && HeaderTop == other.HeaderTop && MessagesTop == other.MessagesTop
        && MessagesBottom == other.MessagesBottom && Background == other.Background
        && Math.Abs(Right - other.Right) <= 30 * scale;

    /// <summary>
    /// The conversation's title in the header: the first run of text from the left, with the
    /// rows above and below it that are clear of any line, for the recogniser to read on its own.
    /// </summary>
    /// <remarks>
    /// Windows' recogniser often finds nothing in a strip that is wide and mostly empty — the
    /// whole header of a maximised window on a 4K display is about 1790×86 pixels with a short
    /// name at one end. Measured on rendered names: over the whole strip even 「文件传输助手」 came
    /// back empty at some sizes; cut to the text with a text-height of margin, every name of two
    /// or more characters was read. The icons at the header's right are far enough away to end
    /// the run; a group's 「(12)」 is close enough to stay in it.
    /// </remarks>
    public TitleArea? FindTitle(Pixels p, double scale)
    {
        int x0 = Left, x1 = Right, y0 = HeaderTop, y1 = MessagesTop;
        int width = x1 - x0, height = y1 - y0;
        if (width < 10 || height < 4)
        {
            return null;
        }

        // Rows that are mostly ink are the header's lower edge or its shadow, not text.
        var lineRow = new bool[height];
        var inkPerColumn = new int[width];
        for (int y = y0; y < y1; y++)
        {
            int count = 0;
            for (int x = x0; x < x1; x++)
            {
                if (IsInk(p, x, y))
                {
                    count++;
                }
            }

            lineRow[y - y0] = count > width / 2;
        }

        for (int y = y0; y < y1; y++)
        {
            if (lineRow[y - y0])
            {
                continue;
            }

            for (int x = x0; x < x1; x++)
            {
                if (IsInk(p, x, y))
                {
                    inkPerColumn[x - x0]++;
                }
            }
        }

        int first = Array.FindIndex(inkPerColumn, c => c > 0);
        if (first < 0)
        {
            return null;
        }

        int maxGap = (int)Math.Round(24 * scale);
        int last = first, gap = 0;
        for (int i = first + 1; i < width; i++)
        {
            if (inkPerColumn[i] > 0)
            {
                last = i;
                gap = 0;
            }
            else if (++gap > maxGap)
            {
                break;
            }
        }

        int top = -1, bottom = -1;
        for (int y = y0; y < y1; y++)
        {
            if (lineRow[y - y0])
            {
                continue;
            }

            for (int x = x0 + first; x <= x0 + last; x++)
            {
                if (IsInk(p, x, y))
                {
                    top = top < 0 ? y : top;
                    bottom = y;
                    break;
                }
            }
        }

        if (top < 0)
        {
            return null;
        }

        // The clear rows around the text, up to the nearest line row or the header's edge.
        int clearTop = top, clearBottom = bottom;
        while (clearTop > y0 && !lineRow[clearTop - 1 - y0])
        {
            clearTop--;
        }

        while (clearBottom < y1 - 1 && !lineRow[clearBottom + 1 - y0])
        {
            clearBottom++;
        }

        return new TitleArea(x0 + first, top, last - first + 1, bottom - top + 1, clearTop, clearBottom);
    }

    private bool IsInk(Pixels p, int x, int y)
    {
        (int r, int g, int b) = p.At(x, y);
        return Math.Abs(r - Background.R) + Math.Abs(g - Background.G) + Math.Abs(b - Background.B) > 60;
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

/// <summary>The title's text block, and the rows clear of any line above and below it.</summary>
internal sealed record TitleArea(int X, int Y, int W, int H, int ClearTop, int ClearBottom);
