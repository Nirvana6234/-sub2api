using System.Windows;
using LanAi.Workspace.Wpf.Controls;

namespace AiSwitch.Wpf.Tests;

public sealed class UsageLineChartLayoutTests
{
    [Fact]
    public void StatsView_UsesEditorialTableStylesWithoutAlternatingRows()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "StatsView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("StatsNumberHeaderStyle", xaml, StringComparison.Ordinal);
        Assert.Contains("StatsPrimaryCellStyle", xaml, StringComparison.Ordinal);
        Assert.Contains("StatsPagerButtonStyle", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"当前数据\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("PreferredDataSourceLabel", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("PreferredDataSourceDetail", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ShowLocalStatisticsCommand", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ShowCloudStatisticsCommand", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"数据视图\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"连接来源\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("DisplayMemberPath=\"DisplayLabel\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Content=\"本地统计\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Content=\"云端统计\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("AlternatingRowBackground", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void LocalConnectionStatus_ShowsOnlyTheEssentialConnectedState()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "StatsView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("Text=\"本机数据已连接\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"数据后台已连接\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"{Binding BackendSourceLabel}\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"{Binding CloudConnectionNotice}\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Text=\"{Binding CloudAuthorizationNotice}\"", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void UsagePulseMetrics_UseTwoColumnsAndShrinkLongValuesInsteadOfClipping()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "StatsView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("<UniformGrid Columns=\"2\" Margin=\"-5,15,-5,-5\">", xaml, StringComparison.Ordinal);
        Assert.Equal(4, xaml.Split("StretchDirection=\"DownOnly\"", StringSplitOptions.None).Length - 1);
        Assert.Contains("Text=\"{Binding CloudRangeInputTokens}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"{Binding CloudRangeOutputTokens}\"", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void Dashboard_UsesOnlyLocalSub2ApiBackendWithoutObservationOrRemoteFallbacks()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "StatsView.xaml"));
        string xaml = File.ReadAllText(sourcePath);
        Assert.DoesNotContain("IsLocalStatisticsSelected", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("LocalSourceFilters", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("来源同步自连接中心", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ClearLocalAuthorizationCommand", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Content=\"清除授权\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("ShowManualCloudCredentialForm", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("RemotePasswordInput", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("局域网或云端中转地址", xaml, StringComparison.Ordinal);
        Assert.Contains("Text=\"本机数据已连接\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Content=\"一次性授权\"", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void OverviewStatusPill_UsesReadableDarkTextOnItsLightSurface()
    {
        string sourcePath = Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory,
            "..", "..", "..", "..", "..",
            "src", "AiSwitch.Wpf", "Views", "OverviewView.xaml"));
        string xaml = File.ReadAllText(sourcePath);

        Assert.Contains("Text=\"{Binding TelemetryLastUpdated}\"", xaml, StringComparison.Ordinal);
        Assert.Contains("Foreground=\"#FF344054\"", xaml, StringComparison.Ordinal);
        Assert.DoesNotContain("Foreground=\"#E6FFFFFF\"", xaml, StringComparison.Ordinal);
    }

    [Fact]
    public void Calculate_EmptySeries_ProducesAUsablePlotWithoutPoints()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            Array.Empty<UsageLineChartPoint>(),
            new Size(300d, 160d),
            new Thickness(10d, 10d, 10d, 20d));

        Assert.True(result.HasDrawableArea);
        Assert.Equal(10d, result.PlotBounds.Left, 3);
        Assert.Equal(10d, result.PlotBounds.Top, 3);
        Assert.Equal(280d, result.PlotBounds.Width, 3);
        Assert.Equal(130d, result.PlotBounds.Height, 3);
        Assert.Equal(0d, result.MaximumValue, 3);
        Assert.Equal(0d, result.ScaleMaximumValue, 3);
        Assert.Empty(result.Points);
    }

    [Fact]
    public void Calculate_SinglePoint_CentersThePointAndKeepsVisualHeadroom()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            [new UsageLineChartPoint("7/14", 42d, "1 次请求 · 42 Token")],
            new Size(200d, 100d),
            new Thickness(10d, 10d, 10d, 20d));

        UsageLineChartLayoutPoint point = Assert.Single(result.Points);
        Assert.Equal(100d, point.Position.X, 3);
        Assert.Equal(19.13d, point.Position.Y, 2);
        Assert.Equal(1d / 1.15d, point.NormalizedValue, 3);
        Assert.Equal(42d, result.MaximumValue, 3);
        Assert.Equal(48.3d, result.ScaleMaximumValue, 3);
    }

    [Fact]
    public void Calculate_AllZeroSeries_PlacesEveryPointOnTheBaseline()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            [
                new UsageLineChartPoint("7/12", 0d, "0 Token"),
                new UsageLineChartPoint("7/13", 0d, "0 Token"),
                new UsageLineChartPoint("7/14", 0d, "0 Token"),
            ],
            new Size(300d, 160d),
            new Thickness(10d, 10d, 10d, 20d));

        Assert.Equal(0d, result.MaximumValue, 3);
        Assert.All(result.Points, point =>
        {
            Assert.Equal(result.PlotBounds.Bottom, point.Position.Y, 3);
            Assert.Equal(0d, point.NormalizedValue, 3);
        });
    }

    [Fact]
    public void Calculate_MultipleValues_UsesEvenHorizontalSpacingAndRelativeVerticalScale()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            [
                new UsageLineChartPoint("7/12", 0d, "0 Token"),
                new UsageLineChartPoint("7/13", 50d, "50 Token"),
                new UsageLineChartPoint("7/14", 100d, "100 Token"),
            ],
            new Size(300d, 160d),
            new Thickness(10d, 10d, 10d, 20d));

        Assert.Equal(100d, result.MaximumValue, 3);
        Assert.Equal(115d, result.ScaleMaximumValue, 3);
        Assert.Equal(10d, result.Points[0].Position.X, 3);
        Assert.Equal(150d, result.Points[1].Position.X, 3);
        Assert.Equal(290d, result.Points[2].Position.X, 3);
        Assert.Equal(140d, result.Points[0].Position.Y, 3);
        Assert.Equal(83.478d, result.Points[1].Position.Y, 3);
        Assert.Equal(26.957d, result.Points[2].Position.Y, 3);
        Assert.Equal(50d / 115d, result.Points[1].NormalizedValue, 3);
    }

    [Fact]
    public void Calculate_ZeroRenderSize_ReturnsNoUnsafeCoordinates()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            [new UsageLineChartPoint("7/14", 42d, "42 Token")],
            new Size(0d, 0d),
            new Thickness(10d));

        Assert.False(result.HasDrawableArea);
        Assert.Empty(result.Points);
        Assert.Equal(42d, result.MaximumValue, 3);
        Assert.Equal(0d, result.PlotBounds.Width, 3);
        Assert.Equal(0d, result.PlotBounds.Height, 3);
    }

    [Fact]
    public void GetLabelIndices_SevenPoints_ShowsFiveEvenlyDistributedDates()
    {
        IReadOnlyList<int> indices = UsageLineChartLayout.GetLabelIndices(7, 560d);

        Assert.Equal(5, indices.Count);
        Assert.Equal(0, indices[0]);
        Assert.Equal(6, indices[^1]);
        Assert.Equal(indices.Count, indices.Distinct().Count());
    }

    [Fact]
    public void GetLabelIndices_NarrowChart_ReducesLabelsAndKeepsEndpoints()
    {
        IReadOnlyList<int> indices = UsageLineChartLayout.GetLabelIndices(7, 240d);

        Assert.Equal([0, 3, 6], indices);
    }

    [Fact]
    public void GetLabelIndices_ThirtyPoints_ShowsFiveEvenlyDistributedDates()
    {
        IReadOnlyList<int> indices = UsageLineChartLayout.GetLabelIndices(30, 560d);

        Assert.Equal(5, indices.Count);
        Assert.Equal(0, indices[0]);
        Assert.Equal(29, indices[^1]);
        Assert.Equal(indices.Count, indices.Distinct().Count());
    }

    [Fact]
    public void BuildBezierSegments_KeepsControlPointsInsideAdjacentXCoordinates()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [
                new UsageLineChartPoint("7/12", 10d, "10 Token"),
                new UsageLineChartPoint("7/13", 80d, "80 Token"),
                new UsageLineChartPoint("7/14", 30d, "30 Token"),
            ],
            new Size(300d, 160d),
            new Thickness(10d, 10d, 10d, 20d));

        IReadOnlyList<UsageLineChartBezierSegment> segments =
            UsageLineChartGeometry.BuildBezierSegments(layout.Points);

        Assert.Equal(2, segments.Count);
        Assert.All(segments, segment =>
        {
            Assert.InRange(segment.Control1.X, segment.Start.X, segment.End.X);
            Assert.InRange(segment.Control2.X, segment.Start.X, segment.End.X);
            Assert.True(double.IsFinite(segment.Control1.Y));
            Assert.True(double.IsFinite(segment.Control2.Y));
        });
    }

    [Fact]
    public void Calculate_InvalidAndRepeatedValues_NeverProducesUnsafeCoordinates()
    {
        UsageLineChartLayoutResult result = UsageLineChartLayout.Calculate(
            [
                new UsageLineChartPoint("1", double.NaN, "NaN"),
                new UsageLineChartPoint("2", double.PositiveInfinity, "Infinity"),
                new UsageLineChartPoint("3", 25d, "25 Token"),
                new UsageLineChartPoint("4", 25d, "25 Token"),
            ],
            new Size(320d, 180d),
            new Thickness(10d, 10d, 10d, 24d));

        Assert.All(result.Points, point =>
        {
            Assert.True(double.IsFinite(point.Position.X));
            Assert.True(double.IsFinite(point.Position.Y));
            Assert.True(double.IsFinite(point.NormalizedValue));
        });
    }
}
