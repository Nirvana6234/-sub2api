using System.Collections.Specialized;
using System.Globalization;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Media;

namespace LanAi.Workspace.Wpf.Controls;

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

/// <summary>
/// A compact, native WPF line chart for the local-usage dashboard.  It renders
/// a restrained grid, filled series, line, markers and bottom labels with the
/// WPF drawing primitives already available to the application.
/// </summary>
public sealed class UsageLineChart : FrameworkElement
{
    private const double DefaultDesiredWidth = 360d;
    private const double DefaultDesiredHeight = 172d;

    private static readonly Typeface LabelTypeface = new(
        new FontFamily("Segoe UI, Microsoft YaHei UI"),
        FontStyles.Normal,
        FontWeights.Normal,
        FontStretches.Normal);
    private static readonly Brush DefaultStrokeBrush = CreateBrush(0xFF, 0x09, 0x69, 0xDA);
    private static readonly Brush DefaultFillBrush = CreateBrush(0x1F, 0x09, 0x69, 0xDA);
    private static readonly Brush DefaultGridBrush = CreateBrush(0xFF, 0xE5, 0xE7, 0xEB);
    private static readonly Brush DefaultLabelBrush = CreateBrush(0xFF, 0x8E, 0x8E, 0x93);
    private static readonly Brush TooltipBackgroundBrush = CreateBrush(0xFA, 0xFF, 0xFF, 0xFF);
    private static readonly Brush TooltipBorderBrush = CreateBrush(0xFF, 0xDC, 0xDF, 0xE5);
    private static readonly Brush TooltipTitleBrush = CreateBrush(0xFF, 0x1D, 0x1D, 0x1F);
    private static readonly Brush TooltipDetailBrush = CreateBrush(0xFF, 0x63, 0x63, 0x6B);

    private INotifyCollectionChanged? _observedCollection;
    private bool _isCollectionSubscribed;
    private UsageLineChartLayoutResult _lastLayout = UsageLineChartLayoutResult.Empty;
    private UsageLineChartLayoutPoint? _hoveredPoint;

    public static readonly DependencyProperty ItemsSourceProperty = DependencyProperty.Register(
        nameof(ItemsSource),
        typeof(IEnumerable<UsageLineChartPoint>),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(
            null,
            FrameworkPropertyMetadataOptions.AffectsRender,
            OnItemsSourceChanged));

    public static readonly DependencyProperty StrokeProperty = DependencyProperty.Register(
        nameof(Stroke),
        typeof(Brush),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(DefaultStrokeBrush, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty FillProperty = DependencyProperty.Register(
        nameof(Fill),
        typeof(Brush),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(DefaultFillBrush, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty PointBrushProperty = DependencyProperty.Register(
        nameof(PointBrush),
        typeof(Brush),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(DefaultStrokeBrush, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty GridBrushProperty = DependencyProperty.Register(
        nameof(GridBrush),
        typeof(Brush),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(DefaultGridBrush, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty LabelBrushProperty = DependencyProperty.Register(
        nameof(LabelBrush),
        typeof(Brush),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(DefaultLabelBrush, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty StrokeThicknessProperty = DependencyProperty.Register(
        nameof(StrokeThickness),
        typeof(double),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(2.35d, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty PointRadiusProperty = DependencyProperty.Register(
        nameof(PointRadius),
        typeof(double),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(3.65d, FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty ChartPaddingProperty = DependencyProperty.Register(
        nameof(ChartPadding),
        typeof(Thickness),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(
            new Thickness(8d, 10d, 8d, 28d),
            FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty IsBarChartProperty = DependencyProperty.Register(
        nameof(IsBarChart),
        typeof(bool),
        typeof(UsageLineChart),
        new FrameworkPropertyMetadata(false, FrameworkPropertyMetadataOptions.AffectsRender));

    public UsageLineChart()
    {
        ClipToBounds = true;
        Focusable = false;
        SnapsToDevicePixels = true;
        UseLayoutRounding = true;
        Loaded += UsageLineChart_OnLoaded;
        Unloaded += UsageLineChart_OnUnloaded;
    }

    /// <summary>
    /// The observable point sequence.  When the source implements
    /// <see cref="INotifyCollectionChanged"/>, collection changes invalidate
    /// the drawing automatically.
    /// </summary>
    public IEnumerable<UsageLineChartPoint>? ItemsSource
    {
        get => (IEnumerable<UsageLineChartPoint>?)GetValue(ItemsSourceProperty);
        set => SetValue(ItemsSourceProperty, value);
    }

    public Brush Stroke
    {
        get => (Brush)GetValue(StrokeProperty);
        set => SetValue(StrokeProperty, value);
    }

    public Brush Fill
    {
        get => (Brush)GetValue(FillProperty);
        set => SetValue(FillProperty, value);
    }

    public Brush PointBrush
    {
        get => (Brush)GetValue(PointBrushProperty);
        set => SetValue(PointBrushProperty, value);
    }

    public Brush GridBrush
    {
        get => (Brush)GetValue(GridBrushProperty);
        set => SetValue(GridBrushProperty, value);
    }

    public Brush LabelBrush
    {
        get => (Brush)GetValue(LabelBrushProperty);
        set => SetValue(LabelBrushProperty, value);
    }

    public double StrokeThickness
    {
        get => (double)GetValue(StrokeThicknessProperty);
        set => SetValue(StrokeThicknessProperty, value);
    }

    public double PointRadius
    {
        get => (double)GetValue(PointRadiusProperty);
        set => SetValue(PointRadiusProperty, value);
    }

    public Thickness ChartPadding
    {
        get => (Thickness)GetValue(ChartPaddingProperty);
        set => SetValue(ChartPaddingProperty, value);
    }

    public bool IsBarChart
    {
        get => (bool)GetValue(IsBarChartProperty);
        set => SetValue(IsBarChartProperty, value);
    }

    protected override Size MeasureOverride(Size availableSize)
    {
        double width = double.IsInfinity(availableSize.Width)
            ? DefaultDesiredWidth
            : Math.Min(DefaultDesiredWidth, Math.Max(0d, availableSize.Width));
        double height = double.IsInfinity(availableSize.Height)
            ? DefaultDesiredHeight
            : Math.Min(DefaultDesiredHeight, Math.Max(0d, availableSize.Height));
        return new Size(width, height);
    }

    protected override void OnRender(DrawingContext drawingContext)
    {
        base.OnRender(drawingContext);

        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            ItemsSource,
            RenderSize,
            ChartPadding);
        _lastLayout = layout;
        if (!layout.HasDrawableArea)
        {
            return;
        }

        // A transparent surface keeps hover details available between the dots.
        drawingContext.DrawRectangle(Brushes.Transparent, null, new Rect(new Point(), RenderSize));

        DrawGrid(drawingContext, layout.PlotBounds);
        if (layout.Points.Count == 0)
        {
            return;
        }

        if (IsBarChart)
        {
            DrawBars(drawingContext, layout);
        }
        else if (layout.Points.Count > 1)
        {
            Geometry area = BuildAreaGeometry(layout.Points, layout.PlotBounds);
            drawingContext.DrawGeometry(CreateAreaBrush(GetBrush(Fill, DefaultFillBrush)), null, area);

            Geometry line = BuildLineGeometry(layout.Points);
            drawingContext.DrawGeometry(
                null,
                CreateLinePen(GetBrush(Stroke, DefaultStrokeBrush)),
                line);
        }

        if (!IsBarChart)
        {
            DrawLastPointMarker(drawingContext, layout.Points[^1]);
        }
        DrawHoverOverlay(drawingContext, layout);
        DrawLabels(drawingContext, layout);
    }

    private void DrawBars(DrawingContext drawingContext, UsageLineChartLayoutResult layout)
    {
        Brush brush = GetBrush(Stroke, DefaultStrokeBrush);
        double slotWidth = layout.Points.Count > 1
            ? layout.PlotBounds.Width / (layout.Points.Count - 1d)
            : layout.PlotBounds.Width;
        double width = Math.Clamp(slotWidth * 0.58d, 4d, 28d);
        foreach (UsageLineChartLayoutPoint point in layout.Points)
        {
            double ratio = Math.Clamp(point.NormalizedValue, 0d, 1d);
            double height = Math.Max(2d, layout.PlotBounds.Height * ratio);
            var rect = new Rect(
                point.Position.X - width / 2d,
                layout.PlotBounds.Bottom - height,
                width,
                height);
            drawingContext.DrawRoundedRectangle(
                CreateOpacityBrush(brush, 0.86d),
                null,
                rect,
                3d,
                3d);
        }
    }

    protected override void OnMouseMove(MouseEventArgs e)
    {
        base.OnMouseMove(e);
        UpdateHoveredPoint(e.GetPosition(this));
    }

    protected override void OnMouseLeave(MouseEventArgs e)
    {
        base.OnMouseLeave(e);
        SetHoveredPoint(null);
    }

    private static void OnItemsSourceChanged(
        DependencyObject dependencyObject,
        DependencyPropertyChangedEventArgs eventArgs)
    {
        var control = (UsageLineChart)dependencyObject;
        control.DetachCollectionNotifications();
        control._observedCollection = eventArgs.NewValue as INotifyCollectionChanged;
        control.AttachCollectionNotifications();
        control.InvalidateVisual();
    }

    private void UsageLineChart_OnLoaded(object sender, RoutedEventArgs e)
    {
        AttachCollectionNotifications();
        InvalidateVisual();
    }

    private void UsageLineChart_OnUnloaded(object sender, RoutedEventArgs e)
        => DetachCollectionNotifications();

    private void AttachCollectionNotifications()
    {
        if (_isCollectionSubscribed || _observedCollection is null)
        {
            return;
        }

        _observedCollection.CollectionChanged += ItemsSource_CollectionChanged;
        _isCollectionSubscribed = true;
    }

    private void DetachCollectionNotifications()
    {
        if (!_isCollectionSubscribed || _observedCollection is null)
        {
            return;
        }

        _observedCollection.CollectionChanged -= ItemsSource_CollectionChanged;
        _isCollectionSubscribed = false;
    }

    private void ItemsSource_CollectionChanged(object? sender, NotifyCollectionChangedEventArgs e)
        => InvalidateVisual();

    private void DrawGrid(DrawingContext drawingContext, Rect plotBounds)
    {
        Brush brush = GetBrush(GridBrush, DefaultGridBrush);
        var guidePen = new Pen(brush, 0.8d)
        {
            DashStyle = new DashStyle([2d, 5d], 0d),
        };
        guidePen.Freeze();

        foreach (double ratio in new[] { 1d / 3d, 2d / 3d })
        {
            double y = plotBounds.Top + (plotBounds.Height * ratio);
            drawingContext.DrawLine(guidePen, new Point(plotBounds.Left, y), new Point(plotBounds.Right, y));
        }

        var baselinePen = new Pen(brush, 1d);
        baselinePen.Freeze();
        drawingContext.DrawLine(
            baselinePen,
            new Point(plotBounds.Left, plotBounds.Bottom),
            new Point(plotBounds.Right, plotBounds.Bottom));
    }

    private Pen CreateLinePen(Brush brush)
    {
        double thickness = double.IsFinite(StrokeThickness) && StrokeThickness > 0d
            ? StrokeThickness
            : 2.35d;
        return new Pen(brush, thickness)
        {
            StartLineCap = PenLineCap.Round,
            EndLineCap = PenLineCap.Round,
            LineJoin = PenLineJoin.Round,
        };
    }

    private void DrawLastPointMarker(
        DrawingContext drawingContext,
        UsageLineChartLayoutPoint point)
    {
        double radius = double.IsFinite(PointRadius) && PointRadius > 0d
            ? PointRadius
            : 3.65d;
        Brush brush = GetBrush(PointBrush, DefaultStrokeBrush);
        drawingContext.DrawEllipse(CreateOpacityBrush(brush, 0.16d), null, point.Position, radius + 4d, radius + 4d);
        drawingContext.DrawEllipse(Brushes.White, null, point.Position, radius + 1.5d, radius + 1.5d);
        drawingContext.DrawEllipse(brush, null, point.Position, radius, radius);
    }

    private void DrawLabels(DrawingContext drawingContext, UsageLineChartLayoutResult layout)
    {
        IReadOnlyList<UsageLineChartLayoutPoint> points = layout.Points;
        double pixelsPerDip = VisualTreeHelper.GetDpi(this).PixelsPerDip;
        Brush brush = GetBrush(LabelBrush, DefaultLabelBrush);
        double y = layout.PlotBounds.Bottom + 7d;

        foreach (int index in UsageLineChartLayout.GetLabelIndices(points.Count, layout.PlotBounds.Width))
        {
            string label = points[index].Source.Label;
            if (string.IsNullOrWhiteSpace(label))
            {
                continue;
            }

            var text = new FormattedText(
                label,
                CultureInfo.CurrentCulture,
                FlowDirection.LeftToRight,
                LabelTypeface,
                10.8d,
                brush,
                pixelsPerDip)
            {
                TextAlignment = TextAlignment.Center,
            };
            double halfTextWidth = Math.Min(text.Width / 2d, layout.PlotBounds.Width / 2d);
            double x = Math.Clamp(
                points[index].Position.X,
                layout.PlotBounds.Left + halfTextWidth,
                layout.PlotBounds.Right - halfTextWidth);
            drawingContext.DrawText(text, new Point(x, y));
        }
    }

    private void UpdateHoveredPoint(Point pointerPosition)
    {
        if (!_lastLayout.HasDrawableArea ||
            pointerPosition.X < _lastLayout.PlotBounds.Left - 8d ||
            pointerPosition.X > _lastLayout.PlotBounds.Right + 8d ||
            pointerPosition.Y < _lastLayout.PlotBounds.Top - 8d ||
            pointerPosition.Y > _lastLayout.PlotBounds.Bottom + 30d)
        {
            SetHoveredPoint(null);
            return;
        }

        UsageLineChartLayoutPoint? nearest = null;
        double nearestHorizontalDistance = double.MaxValue;

        foreach (UsageLineChartLayoutPoint point in _lastLayout.Points)
        {
            double horizontalDistance = Math.Abs(pointerPosition.X - point.Position.X);
            if (horizontalDistance < nearestHorizontalDistance)
            {
                nearest = point;
                nearestHorizontalDistance = horizontalDistance;
            }
        }

        SetHoveredPoint(nearest);
    }

    private void SetHoveredPoint(UsageLineChartLayoutPoint? point)
    {
        if (ReferenceEquals(_hoveredPoint, point))
        {
            return;
        }

        _hoveredPoint = point;
        Cursor = point is null ? Cursors.Arrow : Cursors.Cross;
        InvalidateVisual();
    }

    private static Geometry BuildLineGeometry(IReadOnlyList<UsageLineChartLayoutPoint> points)
    {
        var geometry = new StreamGeometry();
        using (StreamGeometryContext context = geometry.Open())
        {
            context.BeginFigure(points[0].Position, isFilled: false, isClosed: false);
            foreach (UsageLineChartBezierSegment segment in UsageLineChartGeometry.BuildBezierSegments(points))
            {
                context.BezierTo(
                    segment.Control1,
                    segment.Control2,
                    segment.End,
                    isStroked: true,
                    isSmoothJoin: true);
            }
        }

        geometry.Freeze();
        return geometry;
    }

    private static Geometry BuildAreaGeometry(
        IReadOnlyList<UsageLineChartLayoutPoint> points,
        Rect plotBounds)
    {
        var geometry = new StreamGeometry();
        using (StreamGeometryContext context = geometry.Open())
        {
            context.BeginFigure(
                new Point(points[0].Position.X, plotBounds.Bottom),
                isFilled: true,
                isClosed: true);
            context.LineTo(points[0].Position, isStroked: false, isSmoothJoin: true);
            foreach (UsageLineChartBezierSegment segment in UsageLineChartGeometry.BuildBezierSegments(points))
            {
                context.BezierTo(
                    segment.Control1,
                    segment.Control2,
                    segment.End,
                    isStroked: false,
                    isSmoothJoin: true);
            }

            UsageLineChartLayoutPoint last = points[^1];
            context.LineTo(
                new Point(last.Position.X, plotBounds.Bottom),
                isStroked: true,
                isSmoothJoin: true);
        }

        geometry.Freeze();
        return geometry;
    }

    private void DrawHoverOverlay(DrawingContext drawingContext, UsageLineChartLayoutResult layout)
    {
        UsageLineChartLayoutPoint? point = _hoveredPoint;
        if (point is null || !layout.Points.Contains(point))
        {
            return;
        }

        Brush accent = GetBrush(PointBrush, DefaultStrokeBrush);
        var guidePen = new Pen(CreateOpacityBrush(accent, 0.5d), 1d)
        {
            DashStyle = new DashStyle([3d, 4d], 0d),
        };
        guidePen.Freeze();
        drawingContext.DrawLine(
            guidePen,
            new Point(point.Position.X, layout.PlotBounds.Top),
            new Point(point.Position.X, layout.PlotBounds.Bottom));

        drawingContext.DrawEllipse(CreateOpacityBrush(accent, 0.18d), null, point.Position, 8d, 8d);
        drawingContext.DrawEllipse(Brushes.White, null, point.Position, 5d, 5d);
        drawingContext.DrawEllipse(accent, null, point.Position, 3.5d, 3.5d);
        DrawTooltipCard(drawingContext, layout.PlotBounds, point);
    }

    private void DrawTooltipCard(
        DrawingContext drawingContext,
        Rect plotBounds,
        UsageLineChartLayoutPoint point)
    {
        double pixelsPerDip = VisualTreeHelper.GetDpi(this).PixelsPerDip;
        string title = string.IsNullOrWhiteSpace(point.Source.Label) ? "用量" : point.Source.Label;
        string detail = string.IsNullOrWhiteSpace(point.Source.Detail)
            ? point.Source.Value.ToString("N0", CultureInfo.CurrentCulture)
            : point.Source.Detail;
        var titleText = new FormattedText(
            title,
            CultureInfo.CurrentCulture,
            FlowDirection.LeftToRight,
            LabelTypeface,
            11.5d,
            TooltipTitleBrush,
            pixelsPerDip)
        {
            MaxTextWidth = 210d,
            Trimming = TextTrimming.CharacterEllipsis,
        };
        var detailText = new FormattedText(
            detail,
            CultureInfo.CurrentCulture,
            FlowDirection.LeftToRight,
            LabelTypeface,
            10.5d,
            TooltipDetailBrush,
            pixelsPerDip)
        {
            MaxTextWidth = 210d,
            Trimming = TextTrimming.CharacterEllipsis,
        };

        const double horizontalPadding = 11d;
        const double verticalPadding = 9d;
        const double gap = 3d;
        double cardWidth = Math.Clamp(
            Math.Max(titleText.Width, detailText.Width) + (horizontalPadding * 2d),
            104d,
            232d);
        double cardHeight = titleText.Height + detailText.Height + (verticalPadding * 2d) + gap;
        double x = point.Position.X + 12d;
        if (x + cardWidth > plotBounds.Right)
        {
            x = point.Position.X - cardWidth - 12d;
        }

        x = Math.Clamp(x, plotBounds.Left, Math.Max(plotBounds.Left, plotBounds.Right - cardWidth));
        double y = Math.Clamp(
            point.Position.Y - cardHeight - 14d,
            plotBounds.Top,
            Math.Max(plotBounds.Top, plotBounds.Bottom - cardHeight));
        var card = new Rect(x, y, cardWidth, cardHeight);
        drawingContext.DrawRoundedRectangle(
            TooltipBackgroundBrush,
            new Pen(TooltipBorderBrush, 1d),
            card,
            9d,
            9d);
        drawingContext.DrawText(titleText, new Point(x + horizontalPadding, y + verticalPadding));
        drawingContext.DrawText(
            detailText,
            new Point(x + horizontalPadding, y + verticalPadding + titleText.Height + gap));
    }

    private static Brush CreateAreaBrush(Brush source)
    {
        Color color = source is SolidColorBrush solid
            ? solid.Color
            : Color.FromArgb(0x1F, 0x09, 0x69, 0xDA);
        byte topAlpha = color.A == 0 ? (byte)0x1F : color.A;
        var brush = new LinearGradientBrush(
            Color.FromArgb(topAlpha, color.R, color.G, color.B),
            Color.FromArgb(0x00, color.R, color.G, color.B),
            new Point(0.5d, 0d),
            new Point(0.5d, 1d));
        brush.Freeze();
        return brush;
    }

    private static Brush CreateOpacityBrush(Brush source, double opacity)
    {
        Brush clone = source.Clone();
        clone.Opacity = Math.Clamp(opacity, 0d, 1d);
        clone.Freeze();
        return clone;
    }

    private static Brush GetBrush(Brush? candidate, Brush fallback)
        => candidate ?? fallback;

    private static Brush CreateBrush(byte alpha, byte red, byte green, byte blue)
    {
        var brush = new SolidColorBrush(Color.FromArgb(alpha, red, green, blue));
        brush.Freeze();
        return brush;
    }
}

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
