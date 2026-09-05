using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.ViewModels;

/// <summary>One model's share of the recent spend (F4).</summary>
/// <remarks>
/// Public, like every bound type: WPF cannot bind to internal members, and a
/// failed binding yields no value rather than an error.
/// </remarks>
public sealed class ModelUsageRowViewModel
{
    internal ModelUsageRowViewModel(ModelUsage usage)
    {
        ArgumentNullException.ThrowIfNull(usage);

        Model = string.IsNullOrWhiteSpace(usage.Model) ? "未知模型" : usage.Model;
        CostText = "$" + usage.ActualCost.ToString("0.####", System.Globalization.CultureInfo.InvariantCulture);
        DetailText = $"{usage.Requests:N0} 次 · {usage.TotalTokens:N0} token";
    }

    public string Model { get; }

    public string CostText { get; }

    public string DetailText { get; }
}
