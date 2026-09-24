using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>Balance, recharge and subscription — the money half of the account (F4).</summary>
/// <remarks>
/// Two cards, each loaded and failed on its own (F4.2): the balance and the
/// subscription come from different endpoints and one being down must not grey the
/// other. Both report failures to the cycle's <see cref="RefreshState"/>.
/// </remarks>
public sealed partial class AccountCardViewModel : ObservableObject
{
    private readonly IRelayServerClient _client;
    private readonly RefreshState _refresh;
    private PublicSettings _settings = PublicSettings.Conservative;

    internal AccountCardViewModel(IRelayServerClient client, RefreshState refresh)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
    }

    [ObservableProperty]
    private string userDisplayName = string.Empty;

    [ObservableProperty]
    private string balanceText = "—";

    [ObservableProperty]
    private string frozenBalanceText = "—";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(AccountUnavailable))]
    private bool accountReady;

    /// <summary>True when this card could not be loaded and should be greyed out.</summary>
    public bool AccountUnavailable => !AccountReady;

    [ObservableProperty]
    private bool balanceIsLow;

    /// <summary>Where the top-up button should send the user; supplied by the server.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanRecharge))]
    private string rechargeUrl = string.Empty;

    public bool CanRecharge => _settings.PaymentEnabled || !string.IsNullOrWhiteSpace(RechargeUrl);

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(SubscriptionUnavailable))]
    [NotifyPropertyChangedFor(nameof(HasSubscription))]
    private bool subscriptionReady;

    public bool SubscriptionUnavailable => !SubscriptionReady;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasSubscription))]
    private string subscriptionName = string.Empty;

    [ObservableProperty]
    private string subscriptionProgressText = string.Empty;

    public bool HasSubscription => SubscriptionReady && !string.IsNullOrWhiteSpace(SubscriptionName);

    /// <summary>Reads the server-driven settings this card depends on.</summary>
    internal void ApplySettings(PublicSettings settings)
    {
        _settings = settings;
        RechargeUrl = _settings.BalanceLowNotifyRechargeUrl ?? string.Empty;
        OnPropertyChanged(nameof(CanRecharge));
    }

    internal async Task LoadAccountAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            RelayUser user = await _client.GetCurrentUserAsync(token, cancellationToken).ConfigureAwait(true);

            UserDisplayName = user.DisplayName;
            BalanceText = FormatBalance(user.Balance);
            FrozenBalanceText = FormatBalance(user.FrozenBalance);

            // The threshold is the server's to decide (F4.3); the client must not
            // invent one, or operators changing it would need a client release.
            BalanceIsLow = _settings.BalanceLowNotifyEnabled &&
                           user.Balance < _settings.BalanceLowNotifyThreshold;

            AccountReady = true;
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            BalanceText = "—";
            FrozenBalanceText = "—";
            BalanceIsLow = false;
            AccountReady = false;
            ClientLog.Warning("账户卡取数失败", ex);
        }
    }

    internal async Task LoadSubscriptionAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<SubscriptionSummaryItem> subscriptions = await _client
                .GetSubscriptionSummaryAsync(token, cancellationToken)
                .ConfigureAwait(true);
            SubscriptionSummaryItem? subscription = subscriptions.FirstOrDefault();

            if (subscription is null)
            {
                SubscriptionName = string.Empty;
                SubscriptionProgressText = string.Empty;
            }
            else
            {
                SubscriptionName = string.IsNullOrWhiteSpace(subscription.GroupName)
                    ? "订阅"
                    : subscription.GroupName;
                SubscriptionProgressText = FormatSubscriptionProgress(subscription);
            }

            SubscriptionReady = true;
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            SubscriptionName = string.Empty;
            SubscriptionProgressText = string.Empty;
            SubscriptionReady = false;
            ClientLog.Warning("订阅卡取数失败", ex);
        }
    }

    /// <summary>Greys the balance card when no card could be tried at all.</summary>
    internal void MarkUnavailable() => AccountReady = false;

    /// <summary>Drops everything belonging to the account that just signed out.</summary>
    internal void Reset()
    {
        UserDisplayName = string.Empty;
        BalanceText = "—";
        FrozenBalanceText = "—";
        BalanceIsLow = false;
        AccountReady = false;
        SubscriptionName = string.Empty;
        SubscriptionProgressText = string.Empty;
        SubscriptionReady = false;
    }

    private static string FormatSubscriptionProgress(SubscriptionSummaryItem subscription)
    {
        if (subscription.MonthlyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.MonthlyUsedUsd)} / {FormatMoney(subscription.MonthlyLimitUsd)} 本月";
        }

        if (subscription.WeeklyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.WeeklyUsedUsd)} / {FormatMoney(subscription.WeeklyLimitUsd)} 本周";
        }

        if (subscription.DailyLimitUsd > 0)
        {
            return $"{FormatMoney(subscription.DailyUsedUsd)} / {FormatMoney(subscription.DailyLimitUsd)} 今日";
        }

        return "使用中";
    }

    private static string FormatBalance(double value) =>
        "￥" + value.ToString("0.####", System.Globalization.CultureInfo.InvariantCulture);

    internal static string FormatMoney(double value) =>
        "$" + value.ToString("0.####", System.Globalization.CultureInfo.InvariantCulture);
}
