// Shared source, not a shared assembly.
//
// This file is compiled into each UI head rather than into LanAi.RelayClient.Core,
// because it is written against Point / Rect / Size / Thickness — types that WPF and
// Avalonia both define, with the same names, the same double-based members, and the
// same constructor shapes, in their own namespaces. Core references neither
// framework, so it cannot host the file; duplicating the arithmetic into each head
// would be worse.
//
// The alternative was to invent Core-owned geometry primitives and rewrite every line
// against them. That was rejected deliberately: this code carried no tests at all
// until UsageLineChartLayoutTests was written for it, and rewriting untested
// arithmetic to enable a port means the new tests would pin the rewrite rather than
// the behaviour users have today.
//
// One consequence to be aware of: the tests exercise the copy compiled into the WPF
// head. The Avalonia head compiles the same source against Avalonia's geometry types,
// so a divergence between the two frameworks' semantics — not merely their names —
// would not be caught here. The members used are deliberately kept to the plainest
// ones for that reason.

#if UI_AVALONIA
using Avalonia;
#else
using System.Windows;
#endif

namespace LanAi.RelayClient.Controls;

/// <summary>
/// Pure coordinate calculator used by <see cref="UsageLineChart"/>.  Keeping
/// scaling here makes the visual behavior unit-testable without a WPF window or
/// a third-party chart package.
/// </summary>
internal static class UsageLineChartLayout
{
    private const double VerticalHeadroomRatio = 0.15d;

    internal static UsageLineChartLayoutResult Calculate(
        IEnumerable<UsageLineChartPoint>? source,
        Size renderSize,
        Thickness padding)
    {
        UsageLineChartPoint[] points = NormalizePoints(source);
        Rect plotBounds = CreatePlotBounds(renderSize, padding);
        double maximumValue = points.Select(static point => NormalizeValue(point.Value)).DefaultIfEmpty(0d).Max();
        double scaleMaximumValue = maximumValue <= 0d
            ? 0d
            : maximumValue * (1d + VerticalHeadroomRatio);

        if (plotBounds.Width <= 0d || plotBounds.Height <= 0d || points.Length == 0)
        {
            return new UsageLineChartLayoutResult(
                plotBounds,
                maximumValue,
                scaleMaximumValue,
                Array.Empty<UsageLineChartLayoutPoint>());
        }

        var layoutPoints = new UsageLineChartLayoutPoint[points.Length];
        for (var index = 0; index < points.Length; index++)
        {
            double x = points.Length == 1
                ? plotBounds.Left + (plotBounds.Width / 2d)
                : plotBounds.Left + (plotBounds.Width * index / (points.Length - 1));
            double normalizedValue = scaleMaximumValue <= 0d
                ? 0d
                : Math.Clamp(NormalizeValue(points[index].Value) / scaleMaximumValue, 0d, 1d);
            double y = plotBounds.Bottom - (plotBounds.Height * normalizedValue);
            layoutPoints[index] = new UsageLineChartLayoutPoint(
                points[index],
                new Point(x, y),
                normalizedValue);
        }

        return new UsageLineChartLayoutResult(plotBounds, maximumValue, scaleMaximumValue, layoutPoints);
    }

    internal static IReadOnlyList<int> GetLabelIndices(int pointCount, double plotWidth)
    {
        if (pointCount <= 0)
        {
            return Array.Empty<int>();
        }

        if (pointCount <= 2)
        {
            return Enumerable.Range(0, pointCount).ToArray();
        }

        int widthCapacity = Math.Max(2, (int)Math.Floor(Math.Max(0d, plotWidth) / 84d) + 1);
        int desiredCount = Math.Min(pointCount, Math.Min(5, widthCapacity));
        if (desiredCount <= 2)
        {
            return [0, pointCount - 1];
        }

        return Enumerable.Range(0, desiredCount)
            .Select(index => (int)Math.Round(index * (pointCount - 1d) / (desiredCount - 1d)))
            .Distinct()
            .ToArray();
    }

    private static UsageLineChartPoint[] NormalizePoints(IEnumerable<UsageLineChartPoint>? source)
    {
        if (source is null)
        {
            return Array.Empty<UsageLineChartPoint>();
        }

        var points = new List<UsageLineChartPoint>();
        foreach (UsageLineChartPoint point in source)
        {
            if (point is not null)
            {
                points.Add(point);
            }
        }

        return points.ToArray();
    }

    private static Rect CreatePlotBounds(Size renderSize, Thickness padding)
    {
        double width = NormalizeDimension(renderSize.Width);
        double height = NormalizeDimension(renderSize.Height);
        double left = Math.Min(width, NormalizePadding(padding.Left));
        double right = Math.Min(Math.Max(0d, width - left), NormalizePadding(padding.Right));
        double top = Math.Min(height, NormalizePadding(padding.Top));
        double bottom = Math.Min(Math.Max(0d, height - top), NormalizePadding(padding.Bottom));
        return new Rect(
            left,
            top,
            Math.Max(0d, width - left - right),
            Math.Max(0d, height - top - bottom));
    }

    private static double NormalizeDimension(double value)
        => double.IsFinite(value) && value > 0d ? value : 0d;

    private static double NormalizePadding(double value)
        => double.IsFinite(value) && value > 0d ? value : 0d;

    private static double NormalizeValue(double value)
        => double.IsFinite(value) && value > 0d ? value : 0d;
}

internal sealed record UsageLineChartLayoutPoint(
    UsageLineChartPoint Source,
    Point Position,
    double NormalizedValue);

internal sealed class UsageLineChartLayoutResult
{
    public static UsageLineChartLayoutResult Empty { get; } = new(
        new Rect(0d, 0d, 0d, 0d),
        0d,
        0d,
        Array.Empty<UsageLineChartLayoutPoint>());

    public UsageLineChartLayoutResult(
        Rect plotBounds,
        double maximumValue,
        double scaleMaximumValue,
        IReadOnlyList<UsageLineChartLayoutPoint> points)
    {
        PlotBounds = plotBounds;
        MaximumValue = maximumValue;
        ScaleMaximumValue = scaleMaximumValue;
        Points = points ?? throw new ArgumentNullException(nameof(points));
    }

    public Rect PlotBounds { get; }

    public double MaximumValue { get; }

    public double ScaleMaximumValue { get; }

    public IReadOnlyList<UsageLineChartLayoutPoint> Points { get; }

    public bool HasDrawableArea => PlotBounds.Width > 0d && PlotBounds.Height > 0d;
}

internal sealed record UsageLineChartBezierSegment(
    Point Start,
    Point Control1,
    Point Control2,
    Point End);

internal static class UsageLineChartGeometry
{
    internal static IReadOnlyList<UsageLineChartBezierSegment> BuildBezierSegments(
        IReadOnlyList<UsageLineChartLayoutPoint> points,
        double tension = 0.5d)
    {
        if (points.Count < 2)
        {
            return Array.Empty<UsageLineChartBezierSegment>();
        }

        double normalizedTension = double.IsFinite(tension)
            ? Math.Clamp(tension, 0d, 1d)
            : 0.5d;
        var segments = new UsageLineChartBezierSegment[points.Count - 1];
        for (var index = 0; index < points.Count - 1; index++)
        {
            Point start = points[index].Position;
            Point end = points[index + 1].Position;
            double controlOffset = Math.Max(0d, end.X - start.X) * normalizedTension;
            segments[index] = new UsageLineChartBezierSegment(
                start,
                new Point(start.X + controlOffset, start.Y),
                new Point(end.X - controlOffset, end.Y),
                end);
        }

        return segments;
    }
}
