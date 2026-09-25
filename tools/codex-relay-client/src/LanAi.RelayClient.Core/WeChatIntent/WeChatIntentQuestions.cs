namespace LanAi.RelayClient.WeChatIntent;

/// <summary>
/// The fixed question set and the model it was tuned on (docs §5.3). Shipped with the client;
/// phase 1 has no server-side copy.
/// </summary>
/// <remarks>
/// Changing a question's wording or options changes what its probabilities mean. Re-run the
/// evaluation (§5.4) before shipping a change, and when moving <see cref="Model"/> to a new
/// version. The option keys are also what <see cref="IntentCard"/> maps to Chinese labels.
/// </remarks>
internal static class WeChatIntentQuestions
{
    /// <summary>The version the questions and thresholds were tuned on.</summary>
    public const string Model = "jev-1.13.0";

    /// <summary>Used only when TypeSafe answers that <see cref="Model"/> no longer exists.</summary>
    public const string FallbackModel = "jev-latest";

    /// <summary>How many messages of context a judgement carries.</summary>
    public const int ContextSize = 10;

    public const string Intent = "intent";
    public const string Emotion = "emotion";
    public const string Risk = "risk";
    public const string NeedsQuickReply = "needs_quick_reply";
    public const string BestAction = "best_action";

    public const string Json = """
        {
          "intent": {"type": "choice",
            "instructions": "`latest_from_them` 是对方刚发来的消息。结合 `conversation`，对方发这条消息主要想要什么？",
            "criteria": {
              "reassurance": "想确认自己被在乎、被记住",
              "test": "在考验或逼对方证明自己，等着看对方怎么回答",
              "complaint": "在表达不满、抱怨对方做得不好",
              "request": "想让对方做某件具体的事或给出具体安排",
              "information": "只是在询问或交换事实信息",
              "chat": "闲聊、分享日常、维持联系",
              "end": "想结束当前话题或冷处理对方",
              "other": "以上都不符合"}},
          "emotion": {"type": "choice",
            "instructions": "对方写 `latest_from_them` 时的主要情绪是什么？",
            "criteria": {"calm": "平静", "happy": "开心、兴奋", "anxious": "不安、担心", "annoyed": "不耐烦、不满",
                         "angry": "生气", "sad": "难过、失望", "other": "无法判断"}},
          "risk": {"type": "score",
            "instructions": "如果下一条回复说得不好，这段对话会变成什么样？",
            "criteria": ["回什么都不会有问题",
                         "语气敷衍会让对方有点扫兴",
                         "回得不走心会让对方明显不高兴",
                         "回错一句就会吵起来或冷战",
                         "对方已经在爆发边缘，任何不当回复都会让关系受损"]},
          "needs_quick_reply": {"type": "noul",
            "instructions": "对方在等一个及时的回复，拖着不回会让情况变糟。"},
          "best_action": {"type": "choice",
            "instructions": "下一条回复最应该做什么？",
            "criteria": {"apologize": "先道歉", "act": "给出具体行动或安排", "explain": "把事情解释或回答清楚",
                         "comfort": "安抚对方情绪", "confirm": "明确表态，让对方安心", "ask": "先追问，弄清楚对方指什么",
                         "light": "轻松回应，接住话题", "wait": "暂时不回或晚点再回"}}
        }
        """;

    /// <summary>The risk levels, in the order sent, for the card (§5.5 shows the text of the most probable one).</summary>
    public static readonly string[] RiskLevels =
    [
        "回什么都不会有问题",
        "语气敷衍会让对方有点扫兴",
        "回得不走心会让对方明显不高兴",
        "回错一句就会吵起来或冷战",
        "对方已经在爆发边缘，任何不当回复都会让关系受损",
    ];

    public static readonly IReadOnlyDictionary<string, string> IntentLabels = new Dictionary<string, string>
    {
        ["reassurance"] = "想被在乎",
        ["test"] = "在考验你",
        ["complaint"] = "在抱怨",
        ["request"] = "想让你做点什么",
        ["information"] = "在问事情",
        ["chat"] = "闲聊分享",
        ["end"] = "想结束话题",
        ["other"] = "其他",
    };

    public static readonly IReadOnlyDictionary<string, string> EmotionLabels = new Dictionary<string, string>
    {
        ["calm"] = "平静",
        ["happy"] = "开心",
        ["anxious"] = "不安",
        ["annoyed"] = "不耐烦",
        ["angry"] = "生气",
        ["sad"] = "难过",
        ["other"] = "看不出来",
    };

    public static readonly IReadOnlyDictionary<string, string> ActionLabels = new Dictionary<string, string>
    {
        ["apologize"] = "先道歉",
        ["act"] = "给出具体安排",
        ["explain"] = "解释清楚",
        ["comfort"] = "安抚情绪",
        ["confirm"] = "明确表态",
        ["ask"] = "先问清楚",
        ["light"] = "轻松接话",
        ["wait"] = "晚点再回",
    };
}
