using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Controls;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>Today's usage, and the last seven days' spend by day and by model (F4).</summary>
/// <remarks>
/// Two cards, each loaded and failed on its own (F4.2), reporting to the cycle's
/// <see cref="RefreshState"/>. Inside the trend card the per-model split may fail
/// without taking the chart with it.
/// </remarks>
public sealed partial class UsageCardViewModel : ObservableObject
{
    /// <summary>Days covered by the trend chart and the model breakdown.</summary>
    private const int TrendDays = 7;

    /// <summary>How many models the breakdown lists.</summary>
    /// <remarks>
    /// Five. The point of this card is "where is my money going", and a list long
    /// enough to need scrolling stops answering that at a glance.
    /// </remarks>
    private const int TopModels = 5;

    private readonly IRelayServerClient _client;
    private readonly RefreshState _refresh;

    internal UsageCardViewModel(IRelayServerClient client, RefreshState refresh)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
    }

    [ObservableProperty]
    private string todayRequestsText = "—";

    [ObservableProperty]
    private string todayTokensText = "—";

    [ObservableProperty]
    private string todayCostText = "—";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(UsageUnavailable))]
    private bool usageReady;

    public bool UsageUnavailable => !UsageReady;

    public ObservableCollection<UsageLineChartPoint> CostTrend { get; } = [];

    public ObservableCollection<ModelUsageRowViewModel> TopModelUsage { get; } = [];

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(TrendUnavailable))]
    [NotifyPropertyChangedFor(nameof(HasNoUsageYet))]
    private bool trendReady;

    public bool TrendUnavailable => !TrendReady;

    public bool HasTrend => CostTrend.Count > 0;

    public bool HasModelUsage => TopModelUsage.Count > 0;

    /// <summary>
    /// True when the card loaded fine and there is simply nothing to show yet.
    /// </summary>
    /// <remarks>
    /// Distinct from the failure state, and it needs words of its own: an account
    /// that has not sent any traffic gets an empty chart, and to a novice an empty
    /// card is indistinguishable from a broken one. The same omission already
    /// caught us once with the group dropdown.
    /// </remarks>
    public bool HasNoUsageYet => TrendReady && CostTrend.Count == 0;

    internal async Task LoadTodayAsync(string token, CancellationToken cancellationToken)
    {
        try
        {
            DashboardStats stats = await _client.GetDashboardStatsAsync(token, cancellationToken).ConfigureAwait(true);

            TodayRequestsText = stats.TodayRequests.ToString("N0");
            TodayTokensText = stats.TodayTokens.ToString("N0");
            TodayCostText = AccountCardViewModel.FormatMoney(stats.TodayActualCost);
            UsageReady = true;
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            TodayRequestsText = "—";
            TodayTokensText = "—";
            TodayCostText = "—";
            UsageReady = false;
            ClientLog.Warning("用量卡取数失败", ex);
        }
    }

    /// <remarks>
    /// Its own card, loaded on its own, for the same reason as the others (F4.2) —
    /// and the chart is the most likely of them to fail, since it asks for the
    /// widest date range.
    /// </remarks>
    internal async Task LoadTrendAsync(string accessToken, CancellationToken cancellationToken)
    {
        try
        {
            IReadOnlyList<UsageTrendPoint> trend =
                await _client.GetUsageTrendAsync(accessToken, TrendDays, cancellationToken).ConfigureAwait(true);

            CostTrend.Clear();
            foreach (UsageTrendPoint point in trend)
            {
                // Labelled by day-of-month alone: seven full dates will not fit
                // under a chart this narrow, and the year is never in question.
                string label = point.Date.Length >= 10 ? point.Date[8..10] : point.Date;
                CostTrend.Add(new UsageLineChartPoint(
                    label,
                    point.ActualCost,
                    $"{point.Date}  ${point.ActualCost:0.####}  {point.Requests} 次"));
            }

            // Fails on its own inside the same card: the chart is still worth
            // showing when the per-model split is unavailable.
            try
            {
                IReadOnlyList<ModelUsage> models =
                    await _client.GetModelUsageAsync(accessToken, TrendDays, cancellationToken).ConfigureAwait(true);

                TopModelUsage.Clear();
                foreach (ModelUsage model in models
                    .OrderByDescending(m => m.ActualCost)
                    .ThenByDescending(m => m.Requests)
                    .Take(TopModels))
                {
                    TopModelUsage.Add(new ModelUsageRowViewModel(model));
                }
            }
            catch (Exception ex) when (_refresh.Observe(ex))
            {
                TopModelUsage.Clear();
                ClientLog.Warning("按模型用量取数失败", ex);
            }

            TrendReady = true;
            OnPropertyChanged(nameof(HasTrend));
            OnPropertyChanged(nameof(HasModelUsage));
            OnPropertyChanged(nameof(HasNoUsageYet));
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            TrendReady = false;
            ClientLog.Warning("用量趋势取数失败", ex);
        }
    }

    /// <summary>Greys today's card when no card could be tried at all.</summary>
    internal void MarkUnavailable() => UsageReady = false;

    /// <summary>Drops everything belonging to the account that just signed out.</summary>
    internal void Reset()
    {
        TodayRequestsText = "—";
        TodayTokensText = "—";
        TodayCostText = "—";
        UsageReady = false;
        CostTrend.Clear();
        TopModelUsage.Clear();
        TrendReady = false;
        OnPropertyChanged(nameof(HasTrend));
        OnPropertyChanged(nameof(HasModelUsage));
    }
}
