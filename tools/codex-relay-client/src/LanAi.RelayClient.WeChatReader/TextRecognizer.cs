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

    /// <summary>The recogniser's language, for the log.</summary>
    public string LanguageTag => _engine.RecognizerLanguage.LanguageTag;

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
    /// <param name="upscale">1, or 2 to read the region enlarged: a short name the recogniser misses at its size is often read at twice it.</param>
    public async Task<List<ReaderLine>> ReadAsync(Pixels p, int x, int y, int w, int h, double scale, int upscale = 1)
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

        int bw = w, bh = h;
        if (upscale == 2)
        {
            crop = Double(crop, w, h);
            bw = w * 2;
            bh = h * 2;
        }

        using var bgra = new SoftwareBitmap(BitmapPixelFormat.Bgra8, bw, bh, BitmapAlphaMode.Premultiplied);
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

            int k = upscale == 2 ? 2 : 1;
            int left = (int)line.Words.Min(word => word.BoundingRect.Left) / k;
            int top = (int)line.Words.Min(word => word.BoundingRect.Top) / k;
            int right = (int)Math.Ceiling(line.Words.Max(word => word.BoundingRect.Right) / k);
            int bottom = (int)Math.Ceiling(line.Words.Max(word => word.BoundingRect.Bottom) / k);
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

    /// <summary>Twice the size each way, bilinear.</summary>
    private static byte[] Double(byte[] src, int w, int h)
    {
        int dw = w * 2, dh = h * 2;
        var dst = new byte[dw * dh * 4];
        for (int y = 0; y < dh; y++)
        {
            double sy = Math.Clamp(((y + 0.5) / 2) - 0.5, 0, h - 1);
            int y0 = (int)sy, y1 = Math.Min(y0 + 1, h - 1);
            double fy = sy - y0;
            for (int x = 0; x < dw; x++)
            {
                double sx = Math.Clamp(((x + 0.5) / 2) - 0.5, 0, w - 1);
                int x0 = (int)sx, x1 = Math.Min(x0 + 1, w - 1);
                double fx = sx - x0;
                for (int c = 0; c < 4; c++)
                {
                    double top = (src[((y0 * w) + x0) * 4 + c] * (1 - fx)) + (src[((y0 * w) + x1) * 4 + c] * fx);
                    double bottom = (src[((y1 * w) + x0) * 4 + c] * (1 - fx)) + (src[((y1 * w) + x1) * 4 + c] * fx);
                    dst[((y * dw) + x) * 4 + c] = (byte)Math.Round((top * (1 - fy)) + (bottom * fy));
                }
            }
        }

        return dst;
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
