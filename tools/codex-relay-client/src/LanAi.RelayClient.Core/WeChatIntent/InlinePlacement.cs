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
}
