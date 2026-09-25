using System.Globalization;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>What the overlay shows for one judgement (docs §5.5).</summary>
internal sealed record IntentCard
{
    public string Chat { get; init; } = string.Empty;

    /// <summary>The message judged, shortened, so the card says what it is about.</summary>
    public string About { get; init; } = string.Empty;

    /// <summary>E.g. 「在考验你 93% · 想被在乎 5%」.</summary>
    public string Intent { get; init; } = string.Empty;

    /// <summary>The intent's distribution was flat (confidence under 0.5, TypeSafe's default floor).</summary>
    public bool IntentUncertain { get; init; }

    public string Emotion { get; init; } = string.Empty;

    /// <summary>0–4: the most probable risk level. Its text is <see cref="RiskText"/>.</summary>
    public int RiskLevel { get; init; }

    public string RiskText { get; init; } = string.Empty;

    /// <summary>Score at or above 3 of 4: the overlay's border turns red.</summary>
    public bool RiskHigh { get; init; }

    public bool ReplySoon { get; init; }

    /// <summary>E.g. 「解释清楚 80% · 先问清楚 8%」.</summary>
    public string Action { get; init; } = string.Empty;

    /// <summary>The batch sat under a 「昨天」 or dated stamp.</summary>
    public bool Stale { get; init; }

    /// <summary>The model that answered, e.g. jev-1.13.0.</summary>
    public string Model { get; init; } = string.Empty;

    public DateTimeOffset At { get; init; }

    /// <summary>One line for the folded history.</summary>
    public string Summary => $"{About} → {Intent.Split(' ')[0]}，{Emotion}，{RiskShort}";

    public string RiskShort => RiskLevel switch
    {
        0 => "放心回",
        1 => "注意语气",
        2 => "要认真回",
        3 => "容易吵起来",
        _ => "马上安抚",
    };

    /// <summary>Thresholds from §5.5: confidence floor 0.5, reply-soon 0.6, red border at score 3.</summary>
    public static IntentCard From(JevResponse response, string chat, IReadOnlyList<ChatItem> latest, bool stale, DateTimeOffset at)
    {
        ArgumentNullException.ThrowIfNull(response);
        response.Answers.TryGetValue(WeChatIntentQuestions.Intent, out JevAnswer? intent);
        response.Answers.TryGetValue(WeChatIntentQuestions.Emotion, out JevAnswer? emotion);
        response.Answers.TryGetValue(WeChatIntentQuestions.Risk, out JevAnswer? risk);
        response.Answers.TryGetValue(WeChatIntentQuestions.NeedsQuickReply, out JevAnswer? quick);
        response.Answers.TryGetValue(WeChatIntentQuestions.BestAction, out JevAnswer? action);

        int level = MostProbableLevel(risk);
        string about = latest.Count > 0 ? latest[^1].Text : string.Empty;
        return new IntentCard
        {
            Chat = chat,
            About = about.Length > 16 ? about[..16] + "…" : about,
            Intent = TopTwo(intent, WeChatIntentQuestions.IntentLabels),
            IntentUncertain = intent?.Confidence is double c && c < 0.5,
            Emotion = Top(emotion, WeChatIntentQuestions.EmotionLabels),
            RiskLevel = level,
            RiskText = level >= 0 && level < WeChatIntentQuestions.RiskLevels.Length ? WeChatIntentQuestions.RiskLevels[level] : string.Empty,
            RiskHigh = risk?.Score is double s && s >= 3.0,
            ReplySoon = quick?.Noul is double n && n > 0.6,
            Action = TopTwo(action, WeChatIntentQuestions.ActionLabels),
            Stale = stale,
            Model = response.Model,
            At = at,
        };
    }

    /// <summary>
    /// The level with the highest probability — not the score rounded. TypeSafe: "do not use score
    /// outputs to compute the exact magnitude of a number between two levels".
    /// </summary>
    private static int MostProbableLevel(JevAnswer? risk)
    {
        if (risk?.Probabilities is not { Count: > 0 } p)
        {
            return risk?.Score is double s ? (int)Math.Round(s) : 0;
        }

        KeyValuePair<string, double> best = p.MaxBy(kv => kv.Value);
        return int.TryParse(best.Key, NumberStyles.Integer, CultureInfo.InvariantCulture, out int level) ? level : 0;
    }

    private static string Top(JevAnswer? answer, IReadOnlyDictionary<string, string> labels)
    {
        string? key = answer?.Choice ?? answer?.Probabilities?.MaxBy(kv => kv.Value).Key;
        return key is null ? "—" : labels.GetValueOrDefault(key, key);
    }

    private static string TopTwo(JevAnswer? answer, IReadOnlyDictionary<string, string> labels)
    {
        if (answer?.Probabilities is not { Count: > 0 } p)
        {
            return Top(answer, labels);
        }

        return string.Join(" · ", p.OrderByDescending(kv => kv.Value).Take(2)
            .Where((kv, i) => i == 0 || kv.Value >= 0.05)
            .Select(kv => $"{labels.GetValueOrDefault(kv.Key, kv.Key)} {kv.Value.ToString("P0", CultureInfo.InvariantCulture).Replace(" ", string.Empty, StringComparison.Ordinal)}"));
    }
}
