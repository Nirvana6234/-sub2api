using System.Runtime.InteropServices.WindowsRuntime;
using System.Text;
using LanAi.RelayClient.WeChatIntent;
using Windows.Globalization;
using Windows.Graphics.Imaging;
using Windows.Media.Ocr;

namespace LanAi.RelayClient.WeChatReader;

/// <summary>Windows' built-in OCR, Simplified Chinese.</summary>
/// <remarks>
/// Measured at 120–175 ms for a whole 827×961 window on the development machine; reading only
/// the message list is smaller still. Nothing is sent anywhere — the engine is part of Windows.
/// </remarks>
internal sealed class TextRecognizer
{
    private readonly OcrEngine _engine;

    private TextRecognizer(OcrEngine engine) => _engine = engine;

    /// <summary>Null when Windows has no Simplified Chinese recogniser installed.</summary>
    public static TextRecognizer? TryCreate()
    {
        OcrEngine? engine = OcrEngine.TryCreateFromLanguage(new Language("zh-Hans-CN"))
            ?? OcrEngine.TryCreateFromLanguage(new Language("zh-Hans"));
        return engine is null ? null : new TextRecognizer(engine);
    }

    /// <summary>
    /// Recognises the region <paramref name="x"/>,<paramref name="y"/>,<paramref name="w"/>,<paramref name="h"/>
    /// and returns its lines in the window's coordinates, top to bottom.
    /// </summary>
    public async Task<List<ReaderLine>> ReadAsync(Pixels p, int x, int y, int w, int h, double scale)
    {
        x = Math.Clamp(x, 0, p.Width - 1);
        y = Math.Clamp(y, 0, p.Height - 1);
        w = Math.Clamp(w, 1, p.Width - x);
        h = Math.Clamp(h, 1, p.Height - y);

        var crop = new byte[w * h * 4];
        for (int row = 0; row < h; row++)
        {
            Buffer.BlockCopy(p.Bgra, (((y + row) * p.Width) + x) * 4, crop, row * w * 4, w * 4);
        }

        using var bgra = new SoftwareBitmap(BitmapPixelFormat.Bgra8, w, h, BitmapAlphaMode.Premultiplied);
        bgra.CopyFromBuffer(crop.AsBuffer());
        using SoftwareBitmap gray = SoftwareBitmap.Convert(bgra, BitmapPixelFormat.Gray8);
        OcrResult result = await _engine.RecognizeAsync(gray);

        var lines = new List<ReaderLine>();
        foreach (OcrLine line in result.Lines)
        {
            if (line.Words.Count == 0)
            {
                continue;
            }

            int left = (int)line.Words.Min(word => word.BoundingRect.Left);
            int top = (int)line.Words.Min(word => word.BoundingRect.Top);
            int right = (int)Math.Ceiling(line.Words.Max(word => word.BoundingRect.Right));
            int bottom = (int)Math.Ceiling(line.Words.Max(word => word.BoundingRect.Bottom));
            int lx = x + left, ly = y + top, lw = right - left, lh = bottom - top;
            lines.Add(new ReaderLine
            {
                Text = Join(line.Words),
                X = lx,
                Y = ly,
                W = lw,
                H = lh,
                Bg = ChatLayout.SampleBehind(p, lx, ly, lw, lh, scale),
            });
        }

        lines.Sort((a, b) => a.Y != b.Y ? a.Y.CompareTo(b.Y) : a.X.CompareTo(b.X));
        return lines;
    }

    /// <summary>
    /// The engine splits Chinese into one "word" per character or run. Words are joined without
    /// spaces, except between two Latin letters or digits, where the space was real.
    /// </summary>
    private static string Join(IReadOnlyList<OcrWord> words)
    {
        var text = new StringBuilder();
        foreach (OcrWord word in words)
        {
            if (text.Length > 0 && IsLatin(text[^1]) && word.Text.Length > 0 && IsLatin(word.Text[0]))
            {
                text.Append(' ');
            }

            text.Append(word.Text);
        }

        return text.ToString();
    }

    private static bool IsLatin(char c) => c < 128 && char.IsLetterOrDigit(c);
}
