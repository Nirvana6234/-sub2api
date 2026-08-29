using System.Collections.Specialized;
using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Media;
using Avalonia.Media.Immutable;

namespace LanAi.RelayClient.Controls;

/// <summary>
/// The 近 7 日消费 chart, drawn with Avalonia's primitives.
/// </summary>
/// <remarks>
/// <para>
/// A rewrite of the WPF control's drawing half, not a namespace swap. The coordinate
/// arithmetic is <b>not</b> rewritten — it lives in <c>src/Shared/UsageLineChartLayout.cs</c>,
/// compiled into both heads from one source, and is covered by
/// <c>UsageLineChartLayoutTests</c>. Only the painting below is new, because the two
/// frameworks genuinely differ here:
/// </para>
/// <list type="bullet">
/// <item><c>StreamGeometryContext</c> exists in both but with different methods —
/// WPF's <c>BezierTo(c1, c2, end, isStroked, isSmoothJoin)</c> against Avalonia's
/// <c>CubicBezierTo(c1, c2, end)</c> plus an explicit <c>EndFigure</c>.</item>
/// <item><c>FormattedText</c> takes different constructor arguments and, more
/// importantly, positions differently — see <see cref="DrawLabels"/>.</item>
/// <item>Avalonia has no <c>Freeze()</c>; immutable brushes and pens are the
/// equivalent, and are used here so the render pass allocates nothing it need not.</item>
/// </list>
/// <para>
/// Bar mode is carried over unused. The dashboard only ever asks for the line form;
/// the property came from the workspace app this control was copied out of, and is
/// kept so the control's contract is unchanged by the port rather than quietly
/// narrowed.
/// </para>
/// </remarks>
public sealed class UsageLineChart : Control
{
    private const double DefaultDesiredWidth = 360d;
    private const double DefaultDesiredHeight = 172d;

    /// <remarks>
    /// A fallback stack rather than one family, and deliberately wider than the WPF
    /// original's "Segoe UI, Microsoft YaHei UI" — neither of those exists on macOS,
    /// where the CJK face is PingFang SC. Getting this wrong does not throw; the
    /// labels simply render in whatever the default face is, with CJK possibly as
    /// boxes.
    /// </remarks>
    private static readonly Typeface LabelTypeface = new(
        FontFamily.Parse("Segoe UI, Microsoft YaHei UI, PingFang SC, Helvetica Neue, Arial"));

    private static readonly IBrush DefaultStrokeBrush = Immutable(0xFF, 0x09, 0x69, 0xDA);
    private static readonly IBrush DefaultFillBrush = Immutable(0x1F, 0x09, 0x69, 0xDA);
    private static readonly IBrush DefaultGridBrush = Immutable(0xFF, 0xE5, 0xE7, 0xEB);
    private static readonly IBrush DefaultLabelBrush = Immutable(0xFF, 0x8E, 0x8E, 0x93);
    private static readonly IBrush TooltipBackgroundBrush = Immutable(0xFA, 0xFF, 0xFF, 0xFF);
    private static readonly IBrush TooltipBorderBrush = Immutable(0xFF, 0xDC, 0xDF, 0xE5);
    private static readonly IBrush TooltipTitleBrush = Immutable(0xFF, 0x1D, 0x1D, 0x1F);
    private static readonly IBrush TooltipDetailBrush = Immutable(0xFF, 0x63, 0x63, 0x6B);
    private static readonly IBrush WhiteBrush = Immutable(0xFF, 0xFF, 0xFF, 0xFF);

    private INotifyCollectionChanged? _observedCollection;
    private bool _isCollectionSubscribed;
    private UsageLineChartLayoutResult _lastLayout = UsageLineChartLayoutResult.Empty;
    private UsageLineChartLayoutPoint? _hoveredPoint;

    public static readonly StyledProperty<IEnumerable<UsageLineChartPoint>?> ItemsSourceProperty =
        AvaloniaProperty.Register<UsageLineChart, IEnumerable<UsageLineChartPoint>?>(nameof(ItemsSource));

    public static readonly StyledProperty<IBrush?> StrokeProperty =
        AvaloniaProperty.Register<UsageLineChart, IBrush?>(nameof(Stroke), DefaultStrokeBrush);

    public static readonly StyledProperty<IBrush?> FillProperty =
        AvaloniaProperty.Register<UsageLineChart, IBrush?>(nameof(Fill), DefaultFillBrush);

    public static readonly StyledProperty<IBrush?> PointBrushProperty =
        AvaloniaProperty.Register<UsageLineChart, IBrush?>(nameof(PointBrush), DefaultStrokeBrush);

    public static readonly StyledProperty<IBrush?> GridBrushProperty =
        AvaloniaProperty.Register<UsageLineChart, IBrush?>(nameof(GridBrush), DefaultGridBrush);

    public static readonly StyledProperty<IBrush?> LabelBrushProperty =
        AvaloniaProperty.Register<UsageLineChart, IBrush?>(nameof(LabelBrush), DefaultLabelBrush);

    public static readonly StyledProperty<double> StrokeThicknessProperty =
        AvaloniaProperty.Register<UsageLineChart, double>(nameof(StrokeThickness), 2.35d);

    public static readonly StyledProperty<double> PointRadiusProperty =
        AvaloniaProperty.Register<UsageLineChart, double>(nameof(PointRadius), 3.65d);

    public static readonly StyledProperty<Thickness> ChartPaddingProperty =
        AvaloniaProperty.Register<UsageLineChart, Thickness>(
            nameof(ChartPadding),
            new Thickness(8d, 10d, 8d, 28d));

    public static readonly StyledProperty<bool> IsBarChartProperty =
        AvaloniaProperty.Register<UsageLineChart, bool>(nameof(IsBarChart));

    static UsageLineChart()
    {
        // The Avalonia equivalent of FrameworkPropertyMetadataOptions.AffectsRender.
        // Miss a property here and changing it leaves the old drawing on screen.
        AffectsRender<UsageLineChart>(
            ItemsSourceProperty,
            StrokeProperty,
            FillProperty,
            PointBrushProperty,
            GridBrushProperty,
            LabelBrushProperty,
            StrokeThicknessProperty,
            PointRadiusProperty,
            ChartPaddingProperty,
            IsBarChartProperty);

        ItemsSourceProperty.Changed.AddClassHandler<UsageLineChart>(
            (chart, args) => chart.OnItemsSourceChanged(args.NewValue as INotifyCollectionChanged));
    }

    public UsageLineChart()
    {
        ClipToBounds = true;
        Focusable = false;
        UseLayoutRounding = true;
    }

    /// <summary>
    /// The observable point sequence. When the source implements
    /// <see cref="INotifyCollectionChanged"/>, collection changes redraw the chart.
    /// </summary>
    public IEnumerable<UsageLineChartPoint>? ItemsSource
    {
        get => GetValue(ItemsSourceProperty);
        set => SetValue(ItemsSourceProperty, value);
    }

    public IBrush? Stroke
    {
        get => GetValue(StrokeProperty);
        set => SetValue(StrokeProperty, value);
    }

    public IBrush? Fill
    {
        get => GetValue(FillProperty);
        set => SetValue(FillProperty, value);
    }

    public IBrush? PointBrush
    {
        get => GetValue(PointBrushProperty);
        set => SetValue(PointBrushProperty, value);
    }

    public IBrush? GridBrush
    {
        get => GetValue(GridBrushProperty);
        set => SetValue(GridBrushProperty, value);
    }

    public IBrush? LabelBrush
    {
        get => GetValue(LabelBrushProperty);
        set => SetValue(LabelBrushProperty, value);
    }

    public double StrokeThickness
    {
        get => GetValue(StrokeThicknessProperty);
        set => SetValue(StrokeThicknessProperty, value);
    }

    public double PointRadius
    {
        get => GetValue(PointRadiusProperty);
        set => SetValue(PointRadiusProperty, value);
    }

    public Thickness ChartPadding
    {
        get => GetValue(ChartPaddingProperty);
        set => SetValue(ChartPaddingProperty, value);
    }

    /// <remarks>Unused by this application — see the note on the class.</remarks>
    public bool IsBarChart
    {
        get => GetValue(IsBarChartProperty);
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

    public override void Render(DrawingContext context)
    {
        base.Render(context);

        UsageLineChartLayoutResult layout = UsageLineChartLayout.Calculate(
            ItemsSource,
            Bounds.Size,
            ChartPadding);
        _lastLayout = layout;
        if (!layout.HasDrawableArea)
        {
            return;
        }

        // A transparent surface keeps hover details available between the dots. In
        // Avalonia this is also what makes the control hit-testable at all: a Control
        // that paints nothing receives no pointer events.
        context.DrawRectangle(Brushes.Transparent, null, new Rect(default, Bounds.Size));

        DrawGrid(context, layout.PlotBounds);
        if (layout.Points.Count == 0)
        {
            return;
        }

        if (IsBarChart)
        {
            DrawBars(context, layout);
        }
        else if (layout.Points.Count > 1)
        {
            context.DrawGeometry(
                CreateAreaBrush(Fill ?? DefaultFillBrush),
                null,
                BuildAreaGeometry(layout.Points, layout.PlotBounds));

            context.DrawGeometry(
                null,
                CreateLinePen(Stroke ?? DefaultStrokeBrush),
                BuildLineGeometry(layout.Points));
        }

        if (!IsBarChart)
        {
            DrawLastPointMarker(context, layout.Points[^1]);
        }

        DrawHoverOverlay(context, layout);
        DrawLabels(context, layout);
    }

    private void DrawBars(DrawingContext context, UsageLineChartLayoutResult layout)
    {
        IBrush brush = Stroke ?? DefaultStrokeBrush;
        double slotWidth = layout.Points.Count > 1
            ? layout.PlotBounds.Width / (layout.Points.Count - 1d)
            : layout.PlotBounds.Width;
        double width = Math.Clamp(slotWidth * 0.58d, 4d, 28d);

        foreach (UsageLineChartLayoutPoint point in layout.Points)
        {
            double ratio = Math.Clamp(point.NormalizedValue, 0d, 1d);
            double height = Math.Max(2d, layout.PlotBounds.Height * ratio);
            var rect = new Rect(
                point.Position.X - (width / 2d),
                layout.PlotBounds.Bottom - height,
                width,
                height);
            context.DrawRectangle(WithOpacity(brush, 0.86d), null, rect, 3d, 3d);
        }
    }

    protected override void OnPointerMoved(PointerEventArgs e)
    {
        base.OnPointerMoved(e);
        UpdateHoveredPoint(e.GetPosition(this));
    }

    protected override void OnPointerExited(PointerEventArgs e)
    {
        base.OnPointerExited(e);
        SetHoveredPoint(null);
    }

    protected override void OnAttachedToVisualTree(VisualTreeAttachmentEventArgs e)
    {
        base.OnAttachedToVisualTree(e);
        AttachCollectionNotifications();
        InvalidateVisual();
    }

    protected override void OnDetachedFromVisualTree(VisualTreeAttachmentEventArgs e)
    {
        base.OnDetachedFromVisualTree(e);
        DetachCollectionNotifications();
    }

    private void OnItemsSourceChanged(INotifyCollectionChanged? incoming)
    {
        DetachCollectionNotifications();
        _observedCollection = incoming;
        AttachCollectionNotifications();
        InvalidateVisual();
    }

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

    private void ItemsSource_CollectionChanged(object? sender, NotifyCollectionChangedEventArgs e) =>
        InvalidateVisual();

    private void DrawGrid(DrawingContext context, Rect plotBounds)
    {
        IBrush brush = GridBrush ?? DefaultGridBrush;
        var guidePen = new ImmutablePen(
            brush.ToImmutable(),
            0.8d,
            new ImmutableDashStyle([2d, 5d], 0d));

        foreach (double ratio in new[] { 1d / 3d, 2d / 3d })
        {
            double y = plotBounds.Top + (plotBounds.Height * ratio);
            context.DrawLine(guidePen, new Point(plotBounds.Left, y), new Point(plotBounds.Right, y));
        }

        var baselinePen = new ImmutablePen(brush.ToImmutable(), 1d);
        context.DrawLine(
            baselinePen,
            new Point(plotBounds.Left, plotBounds.Bottom),
            new Point(plotBounds.Right, plotBounds.Bottom));
    }

    private IPen CreateLinePen(IBrush brush)
    {
        double thickness = double.IsFinite(StrokeThickness) && StrokeThickness > 0d
            ? StrokeThickness
            : 2.35d;

        return new ImmutablePen(
            brush.ToImmutable(),
            thickness,
            dashStyle: null,
            lineCap: PenLineCap.Round,
            lineJoin: PenLineJoin.Round);
    }

    private void DrawLastPointMarker(DrawingContext context, UsageLineChartLayoutPoint point)
    {
        double radius = double.IsFinite(PointRadius) && PointRadius > 0d ? PointRadius : 3.65d;
        IBrush brush = PointBrush ?? DefaultStrokeBrush;

        context.DrawEllipse(WithOpacity(brush, 0.16d), null, point.Position, radius + 4d, radius + 4d);
        context.DrawEllipse(WhiteBrush, null, point.Position, radius + 1.5d, radius + 1.5d);
        context.DrawEllipse(brush, null, point.Position, radius, radius);
    }

    /// <remarks>
    /// <b>The one place the two frameworks disagree about position rather than API.</b>
    /// WPF's <c>FormattedText</c> with <c>TextAlignment.Center</c> treats the origin as
    /// the centre of the text; Avalonia draws from the top-left corner regardless. The
    /// half-width is therefore subtracted explicitly here, which is unambiguous in both
    /// and is why this does not rely on <c>TextAlignment</c> at all. Left as it was,
    /// every date label would sit half a label to the right of its dot.
    /// </remarks>
    private void DrawLabels(DrawingContext context, UsageLineChartLayoutResult layout)
    {
        IReadOnlyList<UsageLineChartLayoutPoint> points = layout.Points;
        IBrush brush = LabelBrush ?? DefaultLabelBrush;
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
                brush);

            double halfTextWidth = Math.Min(text.Width / 2d, layout.PlotBounds.Width / 2d);
            double centre = Math.Clamp(
                points[index].Position.X,
                layout.PlotBounds.Left + halfTextWidth,
                layout.PlotBounds.Right - halfTextWidth);

            context.DrawText(text, new Point(centre - (text.Width / 2d), y));
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
        Cursor = point is null ? Cursor.Default : new Cursor(StandardCursorType.Cross);
        InvalidateVisual();
    }

    /// <remarks>
    /// WPF's <c>BezierTo(c1, c2, end, isStroked, isSmoothJoin)</c> has no Avalonia
    /// counterpart; the equivalent is <c>CubicBezierTo</c> plus an explicit
    /// <c>EndFigure</c>. The stroked/smooth-join flags have no analogue and are simply
    /// absent — they affected nothing here, since the whole figure is stroked.
    /// </remarks>
    private static Geometry BuildLineGeometry(IReadOnlyList<UsageLineChartLayoutPoint> points)
    {
        var geometry = new StreamGeometry();
        using (StreamGeometryContext context = geometry.Open())
        {
            context.BeginFigure(points[0].Position, isFilled: false);
            foreach (UsageLineChartBezierSegment segment in UsageLineChartGeometry.BuildBezierSegments(points))
            {
                context.CubicBezierTo(segment.Control1, segment.Control2, segment.End);
            }

            context.EndFigure(isClosed: false);
        }

        return geometry;
    }

    private static Geometry BuildAreaGeometry(
        IReadOnlyList<UsageLineChartLayoutPoint> points,
        Rect plotBounds)
    {
        var geometry = new StreamGeometry();
        using (StreamGeometryContext context = geometry.Open())
        {
            context.BeginFigure(new Point(points[0].Position.X, plotBounds.Bottom), isFilled: true);
            context.LineTo(points[0].Position);
            foreach (UsageLineChartBezierSegment segment in UsageLineChartGeometry.BuildBezierSegments(points))
            {
                context.CubicBezierTo(segment.Control1, segment.Control2, segment.End);
            }

            context.LineTo(new Point(points[^1].Position.X, plotBounds.Bottom));
            context.EndFigure(isClosed: true);
        }

        return geometry;
    }

    private void DrawHoverOverlay(DrawingContext context, UsageLineChartLayoutResult layout)
    {
        UsageLineChartLayoutPoint? point = _hoveredPoint;
        if (point is null || !layout.Points.Contains(point))
        {
            return;
        }

        IBrush accent = PointBrush ?? DefaultStrokeBrush;
        var guidePen = new ImmutablePen(
            WithOpacity(accent, 0.5d).ToImmutable(),
            1d,
            new ImmutableDashStyle([3d, 4d], 0d));

        context.DrawLine(
            guidePen,
            new Point(point.Position.X, layout.PlotBounds.Top),
            new Point(point.Position.X, layout.PlotBounds.Bottom));

        context.DrawEllipse(WithOpacity(accent, 0.18d), null, point.Position, 8d, 8d);
        context.DrawEllipse(WhiteBrush, null, point.Position, 5d, 5d);
        context.DrawEllipse(accent, null, point.Position, 3.5d, 3.5d);

        DrawTooltipCard(context, layout.PlotBounds, point);
    }

    private static void DrawTooltipCard(
        DrawingContext context,
        Rect plotBounds,
        UsageLineChartLayoutPoint point)
    {
        string title = string.IsNullOrWhiteSpace(point.Source.Label) ? "用量" : point.Source.Label;
        string detail = string.IsNullOrWhiteSpace(point.Source.Detail)
            ? point.Source.Value.ToString("N0", CultureInfo.CurrentCulture)
            : point.Source.Detail;

        FormattedText titleText = BuildTooltipText(title, 11.5d, TooltipTitleBrush);
        FormattedText detailText = BuildTooltipText(detail, 10.5d, TooltipDetailBrush);

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

        context.DrawRectangle(
            TooltipBackgroundBrush,
            new ImmutablePen(TooltipBorderBrush.ToImmutable(), 1d),
            new Rect(x, y, cardWidth, cardHeight),
            9d,
            9d);

        context.DrawText(titleText, new Point(x + horizontalPadding, y + verticalPadding));
        context.DrawText(
            detailText,
            new Point(x + horizontalPadding, y + verticalPadding + titleText.Height + gap));
    }

    private static FormattedText BuildTooltipText(string text, double size, IBrush brush) =>
        new(
            text,
            CultureInfo.CurrentCulture,
            FlowDirection.LeftToRight,
            LabelTypeface,
            size,
            brush)
        {
            MaxTextWidth = 210d,
            Trimming = TextTrimming.CharacterEllipsis,
        };

    /// <remarks>
    /// The area under the line fades to nothing. Avalonia expresses gradient endpoints
    /// as <see cref="RelativePoint"/> rather than a plain point, which is the only
    /// difference from the WPF original.
    /// </remarks>
    private static IBrush CreateAreaBrush(IBrush source)
    {
        Color color = source is ISolidColorBrush solid
            ? solid.Color
            : Color.FromArgb(0x1F, 0x09, 0x69, 0xDA);
        byte topAlpha = color.A == 0 ? (byte)0x1F : color.A;

        return new LinearGradientBrush
        {
            StartPoint = new RelativePoint(0.5d, 0d, RelativeUnit.Relative),
            EndPoint = new RelativePoint(0.5d, 1d, RelativeUnit.Relative),
            GradientStops =
            {
                new GradientStop(Color.FromArgb(topAlpha, color.R, color.G, color.B), 0d),
                new GradientStop(Color.FromArgb(0x00, color.R, color.G, color.B), 1d),
            },
        }.ToImmutable();
    }

    /// <remarks>
    /// WPF cloned the brush and set <c>Opacity</c>. Avalonia's immutable brushes have
    /// no such setter, so the alpha channel is scaled instead. The result is equivalent
    /// for the solid brushes this control is given; anything else is returned
    /// unchanged rather than silently mis-tinted.
    /// </remarks>
    private static IBrush WithOpacity(IBrush source, double opacity)
    {
        if (source is not ISolidColorBrush solid)
        {
            return source;
        }

        Color color = solid.Color;
        var alpha = (byte)Math.Clamp(Math.Round(color.A * Math.Clamp(opacity, 0d, 1d)), 0d, 255d);
        return new ImmutableSolidColorBrush(Color.FromArgb(alpha, color.R, color.G, color.B));
    }

    private static IBrush Immutable(byte alpha, byte red, byte green, byte blue) =>
        new ImmutableSolidColorBrush(Color.FromArgb(alpha, red, green, blue));
}
