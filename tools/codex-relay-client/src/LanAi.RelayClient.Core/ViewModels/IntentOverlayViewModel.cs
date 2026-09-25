using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.WeChatIntent;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// What is drawn over WeChat (docs §6): one card beside each of the other person's messages on
/// screen. There is no panel of its own any more — the first self-test found one docked at
/// WeChat's side to be in the way; the controls live on the 「探索」 page and in each card's menu.
/// </summary>
public sealed partial class IntentOverlayViewModel : ObservableObject
{
    /// <summary>
    /// One card per message from the other person that is on screen now, top to bottom — a
    /// result, or a 「分析中」 placeholder while it is being judged. Cleared while the list scrolls
    /// and rebuilt from the next settled screen; the head shows one small window per entry.
    /// </summary>
    public ObservableCollection<InlineCardViewModel> InlineCards { get; } = [];

    /// <summary>Whether the cards should be on screen: the feature is running and WeChat is in front.</summary>
    [ObservableProperty]
    private bool isVisible;

    /// <summary>WeChat's window on screen, physical pixels. The cards are placed relative to it.</summary>
    [ObservableProperty]
    private int weChatX;

    [ObservableProperty]
    private int weChatY;

    [ObservableProperty]
    private int weChatWidth;

    [ObservableProperty]
    private int weChatHeight;

    /// <summary>Screen units per design pixel (Windows: DPI/96; macOS: 1, screen units are points).</summary>
    [ObservableProperty]
    private double weChatScale = 1.0;

    /// <summary>Captured-image pixels per screen unit (Windows: 1; macOS: 2 on Retina).</summary>
    [ObservableProperty]
    private double weChatImageScale = 1.0;

    /// <summary>The latest problem or refusal, shown on the 「探索」 page. Empty when all is well.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasStatus))]
    private string status = string.Empty;

    /// <summary>Red 「调试导出中」: conversation text is being written to disk (docs §4.7).</summary>
    [ObservableProperty]
    private bool isDumping;

    public bool HasStatus => Status.Length > 0;

    internal void Clear()
    {
        InlineCards.Clear();
        Status = string.Empty;
    }
}

/// <summary>A small card pinned beside one of the other person's messages.</summary>
public sealed class InlineCardViewModel
{
    /// <summary>Where the card goes on screen, physical pixels.</summary>
    public int ScreenX { get; init; }

    public int ScreenY { get; init; }

    /// <summary>E.g. 「在考验你 93% · 不耐烦」, or 「分析中…」.</summary>
    public string Headline { get; init; } = string.Empty;

    /// <summary>E.g. 「容易吵起来 · 建议：解释清楚」.</summary>
    public string Advice { get; init; } = string.Empty;

    /// <summary>0–4, for the stripe colour.</summary>
    public int RiskLevel { get; init; }

    public bool ReplySoon { get; init; }

    /// <summary>A placeholder while the message is being judged.</summary>
    public bool IsPending { get; init; }

    /// <summary>The user already said whether this one was right.</summary>
    public bool FeedbackGiven { get; init; }

    /// <summary>Everything, for the tooltip.</summary>
    public string Detail { get; init; } = string.Empty;

    public string Stripe => IsPending ? "#9CA3AF" : RiskLevel switch
    {
        >= 4 => "#DC2626",
        3 => "#F97316",
        2 => "#EAB308",
        _ => "#10B981",
    };

    /// <summary>The judgement behind the card, for 「判断得对 / 不对」. Null on a placeholder.</summary>
    internal AnchoredCard? Source { get; init; }
}
