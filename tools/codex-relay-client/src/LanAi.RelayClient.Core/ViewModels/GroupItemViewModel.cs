using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// One selectable group, labelled the way the web panel labels it (F5.1, F5.2).
/// </summary>
/// <remarks>
/// Public, like every bound type: WPF cannot bind to internal members, and a
/// failed binding yields no value rather than an error.
/// </remarks>
public sealed partial class GroupItemViewModel : ObservableObject
{
    internal GroupItemViewModel(RelayGroup group, GroupRate rate, string? serverUtcOffset)
    {
        Id = group.Id;
        Name = group.Name;
        Description = group.Description;
        Platform = group.Platform;
        IsSubscription = group.IsSubscription;

        // Subscription groups show the word "订阅" where standard groups show a
        // number — matching GroupBadge, which returns t('groups.subscription')
        // for them. Printing a multiplier here would disagree with the panel on
        // the very screen M2 is judged against.
        RateLabel = group.IsSubscription
            ? "订阅"
            : FormatMultiplier(rate.EffectiveMultiplier);
        RateDescription = group.IsSubscription
            ? "订阅额度按服务端订阅规则计算"
            : $"每 $1 Token 额度扣除 ￥{rate.EffectiveMultiplier.ToString("0.000", System.Globalization.CultureInfo.InvariantCulture)} 账户余额";

        // Only shown when a user-specific rate actually differs from the group's
        // own; the panel strikes the default through in that case and shows a
        // single value otherwise.
        StruckThroughRateLabel = !group.IsSubscription && rate.HasUserOverride
            ? FormatMultiplier(rate.DefaultMultiplier)
            : null;

        PeakLabel = rate.Peak?.Format(serverUtcOffset);
    }

    public long Id { get; }

    public string Name { get; }

    public string Description { get; }

    public string Platform { get; }

    public bool IsSubscription { get; }

    /// <summary>The multiplier in force, or "订阅" for subscription groups.</summary>
    public string RateLabel { get; }

    /// <summary>Explains the account-balance deduction for one dollar of Token quota.</summary>
    public string RateDescription { get; }

    public bool HasRateDescription => !string.IsNullOrWhiteSpace(RateDescription);

    /// <summary>The group default, shown struck through only when a personal rate overrides it.</summary>
    public string? StruckThroughRateLabel { get; }

    public bool HasStruckThroughRate => StruckThroughRateLabel is not null;

    /// <summary>The peak window, already carrying the server's timezone. Null when none applies.</summary>
    public string? PeakLabel { get; }

    public bool HasPeak => PeakLabel is not null;

    public bool HasDescription => !string.IsNullOrWhiteSpace(Description);

    /// <summary>
    /// Name and rate on one line, for the collapsed dropdown and each row.
    /// </summary>
    /// <remarks>
    /// The rate travels with the name everywhere the group is named, because for a
    /// novice user "which group" and "what does it cost" are the same question —
    /// a bare name would make them open the list to find out.
    /// </remarks>
    public string DisplayText => $"{Name}   {RateLabel}";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CurrentMarker))]
    private bool isCurrent;

    /// <summary>Marks the row that is actually in force, so the list says so in words.</summary>
    public string CurrentMarker => IsCurrent ? "当前使用中" : string.Empty;

    private static string FormatMultiplier(double value) =>
        value.ToString("0.000", System.Globalization.CultureInfo.InvariantCulture) + "x";
}
