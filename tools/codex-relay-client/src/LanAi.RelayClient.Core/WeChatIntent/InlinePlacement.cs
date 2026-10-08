namespace LanAi.RelayClient.WeChatIntent;

/// <summary>A card and the message it was made for.</summary>
internal sealed record AnchoredCard(ChatItem Anchor, IntentCard Card);

/// <summary>A card placed on the current screen: which bubble it belongs to.</summary>
internal sealed record PlacedCard(AnchoredCard Card, BubbleBounds Bubble, bool IsLatest);

/// <summary>
/// Pins each card next to the bubble it was made for, as far as that bubble is on screen now.
/// </summary>
/// <remarks>
/// The screen scrolls, so a card is never placed by where its message used to be: every settled
/// screen is searched again, by content. Cards are matched newest first and each bubble takes at
/// most one card, so two judgements of the same short text (「好」, 「好」) land on the two
/// bubbles, newest on the lowest, rather than both on one.
/// </remarks>
internal static class InlinePlacement
{
    public static IReadOnlyList<PlacedCard> Place(IReadOnlyList<AnchoredCard> cards, ChatScreen screen)
    {
        ArgumentNullException.ThrowIfNull(cards);
        ArgumentNullException.ThrowIfNull(screen);
        var placed = new List<PlacedCard>();
        if (cards.Count == 0 || screen.Bounds.Count != screen.Items.Count)
        {
            return placed;
        }

        var used = new HashSet<int>();
        for (int c = cards.Count - 1; c >= 0; c--)
        {
            AnchoredCard card = cards[c];
            for (int i = screen.Items.Count - 1; i >= 0; i--)
            {
                ChatItem item = screen.Items[i];
                if (item.Speaker == ChatSpeaker.Them && !used.Contains(i) && ChatTranscript.Same(item, card.Anchor))
                {
                    used.Add(i);
                    placed.Add(new PlacedCard(card, screen.Bounds[i], IsLatest: c == cards.Count - 1));
                    break;
                }
            }
        }

        placed.Reverse();
        return placed;
    }

    /// <summary>
    /// Keeps each card level with its message and moves it right, one card-width at a time, until
    /// it overlaps no card above it — beyond the chat area and past WeChat's window when it must.
    /// Positions are the cards' top-left corners, every card <paramref name="width"/> by
    /// <paramref name="height"/>; the result is top to bottom.
    /// </summary>
    /// <remarks>
    /// Sideways rather than up or down: a card that leaves its message's height points at another
    /// message, and one dropped for lack of room is a message without an answer. Room outside the
    /// window is fine — the cards are windows of their own.
    /// </remarks>
    public static List<(int X, int Y, T Item)> Arrange<T>(IEnumerable<(int X, int Y, T Item)> cards, int width, int height, int gap)
    {
        ArgumentNullException.ThrowIfNull(cards);
        return Arrange(cards.Select(c => (c.X, c.Y, width, height, c.Item)), gap);
    }

    /// <summary>
    /// As above, each card with its own size: the card still waiting for a reply is drawn larger
    /// than the ones of answered batches.
    /// </summary>
    public static List<(int X, int Y, T Item)> Arrange<T>(IEnumerable<(int X, int Y, int Width, int Height, T Item)> cards, int gap)
    {
        ArgumentNullException.ThrowIfNull(cards);
        var placed = new List<(int X, int Y, int W, int H, T Item)>();
        foreach ((int x0, int y, int w, int h, T item) in cards.OrderBy(c => c.Y))
        {
            // Past the right edge of whatever it overlaps — with cards of different widths, one
            // card-width to the right can still be on top of a wider card.
            int x = x0;
            while (true)
            {
                var blocking = placed.Where(o => x < o.X + o.W + gap && o.X < x + w + gap && y < o.Y + o.H + gap && o.Y < y + h + gap).ToList();
                if (blocking.Count == 0)
                {
                    break;
                }

                x = blocking.Max(o => o.X + o.W + gap);
            }

            placed.Add((x, y, w, h, item));
        }

        return placed.Select(p => (p.X, p.Y, p.Item)).ToList();
    }
}
