namespace LanAi.RelayClient.Controls;

/// <summary>
/// One display-ready observation in <see cref="UsageLineChart"/>.  The model
/// is intentionally small and immutable so a view model can expose an
/// <see cref="System.Collections.ObjectModel.ObservableCollection{T}"/> of
/// points directly to XAML without a charting-library dependency.
/// </summary>
public sealed record UsageLineChartPoint
{
    public UsageLineChartPoint(string? label, double value, string? detail)
    {
        Label = label ?? string.Empty;
        Value = value;
        Detail = detail ?? string.Empty;
    }

    /// <summary>Short bottom-axis label, normally a local date such as 7/14.</summary>
    public string Label { get; }

    /// <summary>Unscaled numeric observation used to position the point.</summary>
    public double Value { get; }

    /// <summary>Human-readable point detail, shown when the user hovers a dot.</summary>
    public string Detail { get; }
}
