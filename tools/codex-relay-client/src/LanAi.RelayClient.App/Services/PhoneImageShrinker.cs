using Avalonia;
using Avalonia.Media.Imaging;
using LanAi.RelayClient.CodexBinding.DesktopSync;

namespace LanAi.RelayClient.App.Services;

/// <summary>
/// Makes a conversation's image small enough to send to the phone: longest side 1280,
/// at most <see cref="SessionContentSync.MaxImageBytes"/>. Lives in the head because
/// only the head has an image decoder.
/// </summary>
internal static class PhoneImageShrinker
{
    private const int LongestSide = 1280;

    public static (byte[] Bytes, string MediaType)? Shrink(byte[] bytes, string mediaType)
    {
        try
        {
            using var input = new MemoryStream(bytes);
            using var bitmap = new Bitmap(input);
            int longest = Math.Max(bitmap.PixelSize.Width, bitmap.PixelSize.Height);
            if (longest <= LongestSide && bytes.Length <= SessionContentSync.MaxImageBytes)
            {
                return (bytes, mediaType);
            }

            double scale = Math.Min(1.0, (double)LongestSide / longest);
            var size = new PixelSize(
                Math.Max(1, (int)(bitmap.PixelSize.Width * scale)),
                Math.Max(1, (int)(bitmap.PixelSize.Height * scale)));
            using Bitmap scaled = bitmap.CreateScaledBitmap(size);
            using var output = new MemoryStream();
            scaled.Save(output);
            return output.Length <= SessionContentSync.MaxImageBytes ? (output.ToArray(), "image/png") : null;
        }
        catch (Exception ex) when (ex is ArgumentException or InvalidOperationException or IOException or NotSupportedException)
        {
            // Not an image this decoder understands: not sent.
            return null;
        }
    }
}
