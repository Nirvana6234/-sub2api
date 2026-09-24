using System.Collections.ObjectModel;
using System.Windows;
using System.Windows.Documents;
using System.Windows.Media;

namespace LanAi.Workspace.Wpf.Controls;

/// <summary>
/// Draws the application's icons from built-in 24 x 24 vector geometry.
/// The control deliberately has no dependency on an installed icon font,
/// an SVG runtime or an external resource file.
/// </summary>
public sealed class AppIcon : FrameworkElement
{
    private const double GeometrySize = 24d;
    private const double DefaultDesiredSize = 20d;

    private static readonly IReadOnlyDictionary<string, IconDefinition> Definitions =
        new ReadOnlyDictionary<string, IconDefinition>(
            new Dictionary<string, IconDefinition>(StringComparer.OrdinalIgnoreCase)
            {
                ["Overview"] = Fill(
                    "M3,3 H10 V10 H3 Z M14,3 H21 V10 H14 Z M3,14 H10 V21 H3 Z M14,14 H21 V21 H14 Z"),
                ["Stats"] = Stroke(
                    "M4,20 V14 M10,20 V4 M16,20 V9 M22,20 V7"),
                ["Accounts"] = Stroke(
                    "M5,5 C5,2.8 19,2.8 19,5 C19,7.2 5,7.2 5,5 Z M5,5 V12 C5,14.2 19,14.2 19,12 V5 M5,12 V19 C5,21.2 19,21.2 19,19 V12"),
                ["Refresh"] = Stroke(
                    "M20,7 V3 L17.2,5.8 A8,8 0 1 0 19.4,15 M4,17 V21 L6.8,18.2"),
                ["Plus"] = Stroke("M12,5 V19 M5,12 H19", 2d),
                ["Play"] = Stroke("M8,5 L19,12 L8,19 Z"),
                ["Edit"] = Stroke("M4,20 H8 L19,9 L15,5 L4,16 Z M13.5,6.5 L17.5,10.5"),
                ["Ban"] = Stroke("M21,12 A9,9 0 1 1 3,12 A9,9 0 1 1 21,12 M5.6,5.6 L18.4,18.4"),
                ["Delete"] = Stroke("M4,7 H20 M9,4 H15 L16,7 M7,7 L8,21 H16 L17,7 M10,11 V17 M14,11 V17"),
                ["More"] = Fill("M3,12 A2,2 0 1 0 7,12 A2,2 0 1 0 3,12 M10,12 A2,2 0 1 0 14,12 A2,2 0 1 0 10,12 M17,12 A2,2 0 1 0 21,12 A2,2 0 1 0 17,12"),
                ["GripVertical"] = Fill(
                    "M7,5 A1.5,1.5 0 1 0 10,5 A1.5,1.5 0 1 0 7,5 " +
                    "M14,5 A1.5,1.5 0 1 0 17,5 A1.5,1.5 0 1 0 14,5 " +
                    "M7,12 A1.5,1.5 0 1 0 10,12 A1.5,1.5 0 1 0 7,12 " +
                    "M14,12 A1.5,1.5 0 1 0 17,12 A1.5,1.5 0 1 0 14,12 " +
                    "M7,19 A1.5,1.5 0 1 0 10,19 A1.5,1.5 0 1 0 7,19 " +
                    "M14,19 A1.5,1.5 0 1 0 17,19 A1.5,1.5 0 1 0 14,19"),
                ["Copy"] = Stroke("M9,9 H20 V20 H9 Z M4,4 H15 V9 M4,4 V15 H9"),
                ["Projects"] = Stroke(
                    "M3,7 H9 L11,9 H21 V19 H3 Z M3,7 V5 H9 L11,7"),
                ["Chat"] = Stroke(
                    "M4,4 H20 V16 H9 L4,20 Z"),
                ["History"] = Stroke(
                    "M4,8 V3 H9 M4.7,4.8 A9,9 0 1 1 3.7,9 M12,7 V12 L15.5,14"),
                ["Connections"] = Stroke(
                    "M7.7,10.7 L10.6,7.7 M13.4,7.7 L16.3,10.7 M7.7,13.3 L10.6,16.3 M13.4,16.3 L16.3,13.3 " +
                    "M8,12 A2,2 0 1 1 4,12 A2,2 0 1 1 8,12 " +
                    "M14,6 A2,2 0 1 1 10,6 A2,2 0 1 1 14,6 " +
                    "M20,12 A2,2 0 1 1 16,12 A2,2 0 1 1 20,12 " +
                    "M14,18 A2,2 0 1 1 10,18 A2,2 0 1 1 14,18"),
                ["Settings"] = Stroke(
                    "M4,7 H9 M15,7 H20 M12,4 V10 " +
                    "M4,12 H14 M20,12 H18 M16,9 V15 " +
                    "M4,17 H6 M12,17 H20 M9,14 V20"),
                ["Extensions"] = Stroke(
                    "M8,3 V8 H3 M16,3 V8 H21 M8,21 V16 H3 M16,21 V16 H21 " +
                    "M8,8 H16 V16 H8 Z"),
                ["ChevronRight"] = Stroke("M9,5 L16,12 L9,19", 2d),
                ["Bell"] = Stroke(
                    "M6,17 L7.5,15.5 V10 A4.5,4.5 0 0 1 16.5,10 V15.5 L18,17 Z M10,20 H14"),
                ["Help"] = Stroke(
                    "M21,12 A9,9 0 1 1 3,12 A9,9 0 1 1 21,12 " +
                    "M9.5,9 A2.5,2.5 0 0 1 14.3,10 C14.3,12 12,12.2 12,14 M12,17 L12.01,17"),
                ["Minimize"] = Stroke("M6,12 H18", 1.7d),
                ["Maximize"] = Stroke("M5,5 H19 V19 H5 Z", 1.6d),
                ["Close"] = Stroke("M6,6 L18,18 M18,6 L6,18", 1.7d),
                ["Computer"] = Stroke("M3,4 H21 V17 H3 Z M8,21 H16 M12,17 V21"),
                ["ArrowRight"] = Stroke("M4,12 H20 M14,6 L20,12 L14,18", 1.9d),
                ["Network"] = Stroke(
                    "M21,12 A9,9 0 1 1 3,12 A9,9 0 1 1 21,12 M3,12 H21 " +
                    "M12,3 C15,6 16,9 16,12 C16,15 15,18 12,21 " +
                    "M12,3 C9,6 8,9 8,12 C8,15 9,18 12,21"),
                ["Info"] = Stroke(
                    "M21,12 A9,9 0 1 1 3,12 A9,9 0 1 1 21,12 M12,10 V17 M12,7 L12.01,7"),
                ["Warning"] = Stroke("M12,3 L22,20 H2 Z M12,9 V14 M12,17 L12.01,17"),
                ["LocalGateway"] = Stroke(
                    "M3,4 H21 V16 H3 Z M8,20 H16 M12,16 V20 M8,11 L12,7 L16,11"),
                ["LanGateway"] = Stroke(
                    "M3,11 H21 V19 H3 Z M8,11 C8,7 16,7 16,11 M7,15 L7.01,15 M11,15 L11.01,15 M15,15 H19"),
                ["CloudGateway"] = Stroke(
                    "M6.5,19 H18 A4,4 0 0 0 18.6,11.1 A6,6 0 0 0 7.1,9.2 A4.8,4.8 0 0 0 6.5,19 Z " +
                    "M9,15 L12,12 L15,15 M12,12 V20"),
                ["Send"] = Fill("M3,4 L22,12 L3,20 L6,13.5 L15,12 L6,10.5 Z"),
            });

    public static readonly DependencyProperty KindProperty = DependencyProperty.Register(
        nameof(Kind),
        typeof(string),
        typeof(AppIcon),
        new FrameworkPropertyMetadata(
            string.Empty,
            FrameworkPropertyMetadataOptions.AffectsRender));

    public static readonly DependencyProperty ForegroundProperty = TextElement.ForegroundProperty.AddOwner(
        typeof(AppIcon),
        new FrameworkPropertyMetadata(
            Brushes.Black,
            FrameworkPropertyMetadataOptions.AffectsRender |
            FrameworkPropertyMetadataOptions.Inherits));

    public AppIcon()
    {
        SnapsToDevicePixels = true;
        UseLayoutRounding = true;
        Focusable = false;
    }

    public string Kind
    {
        get => (string)GetValue(KindProperty);
        set => SetValue(KindProperty, value);
    }

    public Brush Foreground
    {
        get => (Brush)GetValue(ForegroundProperty);
        set => SetValue(ForegroundProperty, value);
    }

    internal static bool IsSupported(string? kind) =>
        !string.IsNullOrWhiteSpace(kind) && Definitions.ContainsKey(kind);

    protected override Size MeasureOverride(Size availableSize)
    {
        double width = double.IsInfinity(availableSize.Width)
            ? DefaultDesiredSize
            : Math.Min(DefaultDesiredSize, Math.Max(0d, availableSize.Width));
        double height = double.IsInfinity(availableSize.Height)
            ? DefaultDesiredSize
            : Math.Min(DefaultDesiredSize, Math.Max(0d, availableSize.Height));
        return new Size(width, height);
    }

    protected override void OnRender(DrawingContext drawingContext)
    {
        base.OnRender(drawingContext);

        if (RenderSize.Width <= 0d ||
            RenderSize.Height <= 0d ||
            Foreground is null ||
            !Definitions.TryGetValue(Kind ?? string.Empty, out IconDefinition? definition))
        {
            return;
        }

        double scale = Math.Min(RenderSize.Width, RenderSize.Height) / GeometrySize;
        double x = (RenderSize.Width - (GeometrySize * scale)) / 2d;
        double y = (RenderSize.Height - (GeometrySize * scale)) / 2d;

        drawingContext.PushTransform(new TranslateTransform(x, y));
        drawingContext.PushTransform(new ScaleTransform(scale, scale));
        try
        {
            if (definition.FillGeometry is not null)
            {
                drawingContext.DrawGeometry(Foreground, null, definition.FillGeometry);
            }

            if (definition.StrokeGeometry is not null)
            {
                var pen = new Pen(Foreground, definition.StrokeThickness)
                {
                    StartLineCap = PenLineCap.Round,
                    EndLineCap = PenLineCap.Round,
                    LineJoin = PenLineJoin.Round,
                };
                drawingContext.DrawGeometry(null, pen, definition.StrokeGeometry);
            }
        }
        finally
        {
            drawingContext.Pop();
            drawingContext.Pop();
        }
    }

    private static IconDefinition Fill(string path) =>
        new(CreateGeometry(path), null, 0d);

    private static IconDefinition Stroke(string path, double thickness = 1.8d) =>
        new(null, CreateGeometry(path), thickness);

    private static Geometry CreateGeometry(string path)
    {
        Geometry geometry = Geometry.Parse(path);
        if (geometry.CanFreeze)
        {
            geometry.Freeze();
        }

        return geometry;
    }

    private sealed record IconDefinition(
        Geometry? FillGeometry,
        Geometry? StrokeGeometry,
        double StrokeThickness);
}
