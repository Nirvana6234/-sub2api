using System.Linq;
using System.Windows;
using LanAi.RelayClient.Controls;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The arithmetic behind the 近 7 日消费 chart.
/// </summary>
/// <remarks>
/// <para>
/// <b>Written before the Avalonia port touches any of it, and this code had no tests
/// at all until now</b> — 816 lines of chart, of which this is the ~185 that decide
/// where every dot lands. The drawing half cannot be covered by a unit test; this half
/// can, and it is the half a port can silently get wrong. A chart that scales its
/// points incorrectly still renders a plausible-looking line.
/// </para>
/// <para>
/// These pin current behaviour rather than assert an ideal. Where a value looks
/// arbitrary — the 15% headroom, the 84px label spacing — the number is copied from
/// the implementation deliberately, so a port that changes it has to change a test and
/// say why.
/// </para>
/// </remarks>
public sealed class UsageLineChartLayoutTests
{
    private static readonly Size Canvas = new(300d, 100d);
    private static readonly Thickness NoPadding = default;

    private static UsageLineChartPoint Point(double value, string label = "d") =>
        new(label, value, "detail");

    [Fact]
    public void NoSourceProducesAnEmptyLayout()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(null, Canvas, NoPadding);

        Assert.Empty(layout.Points);
        Assert.Equal(0d, layout.MaximumValue);
        Assert.True(layout.HasDrawableArea);
    }

    /// <remarks>
    /// A null inside the collection must be dropped rather than throw. The trend comes
    /// from the server, and one bad element should cost that point, not the card.
    /// </remarks>
    [Fact]
    public void NullEntriesAreSkippedRatherThanThrowing()
    {
        UsageLineChartPoint?[] source = [Point(1d), null, Point(3d)];

        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(source!, Canvas, NoPadding);

        Assert.Equal(2, layout.Points.Count);
        Assert.Equal(3d, layout.MaximumValue);
    }

    /// <remarks>
    /// The 15% headroom is why the tallest point does not touch the top edge. Without
    /// it the peak sits exactly on the border and reads as clipped.
    /// </remarks>
    [Fact]
    public void TheScaleLeavesFifteenPercentHeadroomAboveThePeak()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(10d), Point(20d)], Canvas, NoPadding);

        Assert.Equal(20d, layout.MaximumValue);
        Assert.Equal(23d, layout.ScaleMaximumValue, 10);

        // The peak normalises to 20/23, not to 1.
        Assert.Equal(20d / 23d, layout.Points[1].NormalizedValue, 10);
    }

    [Fact]
    public void ASinglePointIsCentredHorizontally()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(5d)], Canvas, NoPadding);

        Assert.Single(layout.Points);
        Assert.Equal(150d, layout.Points[0].Position.X, 10);
    }

    [Fact]
    public void PointsAreSpreadEdgeToEdge()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(2d), Point(3d)], Canvas, NoPadding);

        Assert.Equal(0d, layout.Points[0].Position.X, 10);
        Assert.Equal(150d, layout.Points[1].Position.X, 10);
        Assert.Equal(300d, layout.Points[2].Position.X, 10);
    }

    /// <remarks>
    /// Y grows downward, so the larger value must produce the smaller Y. Getting this
    /// backwards renders a chart that is upside down but otherwise entirely plausible.
    /// </remarks>
    [Fact]
    public void LargerValuesSitHigherOnScreen()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(9d)], Canvas, NoPadding);

        Assert.True(layout.Points[1].Position.Y < layout.Points[0].Position.Y);
        Assert.Equal(Canvas.Height, layout.PlotBounds.Bottom, 10);
    }

    /// <remarks>
    /// Negative and non-finite values are floored to zero rather than rejected. A
    /// refund or a corrupt figure should flatten one point, not blank the card.
    /// </remarks>
    [Theory]
    [InlineData(-5d)]
    [InlineData(double.NaN)]
    [InlineData(double.PositiveInfinity)]
    public void NonPositiveOrNonFiniteValuesAreTreatedAsZero(double value)
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(value)], Canvas, NoPadding);

        Assert.Equal(0d, layout.MaximumValue);
        Assert.Equal(0d, layout.Points[0].NormalizedValue);
    }

    /// <remarks>
    /// Seven days with no usage at all. Every point must land on the baseline instead
    /// of dividing by a zero maximum.
    /// </remarks>
    [Fact]
    public void AllZeroValuesRestOnTheBaselineWithoutDividingByZero()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(0d), Point(0d), Point(0d)], Canvas, NoPadding);

        Assert.Equal(0d, layout.ScaleMaximumValue);
        Assert.All(layout.Points, point => Assert.Equal(0d, point.NormalizedValue));
        Assert.All(layout.Points, point => Assert.Equal(Canvas.Height, point.Position.Y, 10));
    }

    [Fact]
    public void PaddingShrinksThePlotArea()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d)], Canvas, new Thickness(10d, 5d, 20d, 15d));

        Assert.Equal(10d, layout.PlotBounds.Left, 10);
        Assert.Equal(5d, layout.PlotBounds.Top, 10);
        Assert.Equal(270d, layout.PlotBounds.Width, 10);
        Assert.Equal(80d, layout.PlotBounds.Height, 10);
    }

    /// <remarks>
    /// Padding wider than the control must collapse the plot rather than produce a
    /// negative-width rectangle, which downstream drawing would treat as garbage.
    /// </remarks>
    [Fact]
    public void PaddingLargerThanTheControlCollapsesRatherThanGoingNegative()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d)], new Size(40d, 40d), new Thickness(30d, 30d, 30d, 30d));

        Assert.True(layout.PlotBounds.Width >= 0d);
        Assert.True(layout.PlotBounds.Height >= 0d);
        Assert.False(layout.HasDrawableArea);
        Assert.Empty(layout.Points);
    }

    [Fact]
    public void AZeroSizedControlYieldsNoDrawableArea()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d)], new Size(0d, 0d), NoPadding);

        Assert.False(layout.HasDrawableArea);
        Assert.Empty(layout.Points);
    }

    // ---- label thinning ----

    [Theory]
    [InlineData(0)]
    [InlineData(-3)]
    public void NoPointsMeansNoLabels(int count) =>
        Assert.Empty(UsageLineChartLayout.GetLabelIndices(count, 300d));

    [Fact]
    public void OneOrTwoPointsAreAlwaysBothLabelled()
    {
        Assert.Equal([0], UsageLineChartLayout.GetLabelIndices(1, 300d));
        Assert.Equal([0, 1], UsageLineChartLayout.GetLabelIndices(2, 300d));
    }

    /// <remarks>
    /// 84px per label is the implementation's spacing budget. A narrow card must fall
    /// back to first-and-last rather than overlap the text into an unreadable smear.
    /// </remarks>
    [Fact]
    public void ANarrowChartLabelsOnlyTheFirstAndLast()
    {
        Assert.Equal([0, 6], UsageLineChartLayout.GetLabelIndices(7, 50d));
    }

    /// <remarks>
    /// Five is the cap however wide the card gets. The spacing is <i>approximately</i>
    /// even rather than exactly so: the indices come from rounding
    /// <c>i × (n−1) / (desired−1)</c>, and for eight points that yields gaps of
    /// 2, 2, 1, 2. Pinned as it actually behaves, not as it ideally would.
    /// </remarks>
    [Fact]
    public void AWideChartLabelsAtMostFivePoints()
    {
        Assert.Equal([0, 2, 4, 5, 7], UsageLineChartLayout.GetLabelIndices(8, 1000d));
    }

    /// <remarks>
    /// Rounding can land two computed indices on the same point; duplicates must be
    /// dropped so a label is not drawn twice over itself.
    /// </remarks>
    [Fact]
    public void DuplicateIndicesFromRoundingAreRemoved()
    {
        IReadOnlyList<int> indices = UsageLineChartLayout.GetLabelIndices(3, 1000d);

        Assert.Equal(indices.Distinct().Count(), indices.Count);
    }

    // ---- bezier smoothing ----

    [Fact]
    public void FewerThanTwoPointsProduceNoCurve()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d)], Canvas, NoPadding);

        Assert.Empty(UsageLineChartGeometry.BuildBezierSegments(layout.Points));
    }

    /// <remarks>
    /// Control points sit on the same horizontal as their anchors, offset by half the
    /// gap at the default tension. That is what makes the curve ease in and out
    /// without overshooting above the peak — a spline that overshoots would draw
    /// spending that never happened.
    /// </remarks>
    [Fact]
    public void ControlPointsAreHorizontalAndHalfTheGapAtDefaultTension()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(2d)], new Size(100d, 100d), NoPadding);

        UsageLineChartBezierSegment segment = UsageLineChartGeometry
            .BuildBezierSegments(layout.Points)
            .Single();

        Assert.Equal(50d, segment.Control1.X, 10);
        Assert.Equal(50d, segment.Control2.X, 10);
        Assert.Equal(segment.Start.Y, segment.Control1.Y, 10);
        Assert.Equal(segment.End.Y, segment.Control2.Y, 10);
    }

    [Fact]
    public void ZeroTensionCollapsesTheCurveToStraightSegments()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(2d)], new Size(100d, 100d), NoPadding);

        UsageLineChartBezierSegment segment = UsageLineChartGeometry
            .BuildBezierSegments(layout.Points, tension: 0d)
            .Single();

        Assert.Equal(segment.Start.X, segment.Control1.X, 10);
        Assert.Equal(segment.End.X, segment.Control2.X, 10);
    }

    /// <remarks>Out-of-range or non-finite tension must fall back, not produce NaN geometry.</remarks>
    [Theory]
    [InlineData(double.NaN)]
    [InlineData(5d)]
    [InlineData(-1d)]
    public void OutOfRangeTensionIsClampedOrDefaulted(double tension)
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(2d)], new Size(100d, 100d), NoPadding);

        UsageLineChartBezierSegment segment = UsageLineChartGeometry
            .BuildBezierSegments(layout.Points, tension)
            .Single();

        Assert.True(double.IsFinite(segment.Control1.X));
        Assert.True(double.IsFinite(segment.Control2.X));
    }

    [Fact]
    public void SegmentCountIsOneFewerThanThePointCount()
    {
        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            [Point(1d), Point(2d), Point(3d), Point(4d)], Canvas, NoPadding);

        Assert.Equal(3, UsageLineChartGeometry.BuildBezierSegments(layout.Points).Count);
    }
}
