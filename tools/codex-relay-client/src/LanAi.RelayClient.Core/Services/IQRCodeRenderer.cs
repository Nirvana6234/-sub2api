namespace LanAi.RelayClient.Services;

/// <summary>Turns payment-order content into a QR code image.</summary>
/// <remarks>
/// <para>
/// Returns PNG bytes rather than a framework image type. The WPF version handed back a
/// <c>BitmapSource</c>, which was the single line of <c>System.Windows</c> in the
/// entire payment view model and the only reason that view model could not move to
/// this project.
/// </para>
/// <para>
/// Nothing is lost by the change: <c>PngByteQRCode</c> produces bytes and the WPF
/// implementation was wrapping them anyway. Each head now does its own two-line
/// conversion, and the shared code stops knowing which UI framework it is under.
/// </para>
/// </remarks>
public interface IQRCodeRenderer
{
    /// <summary>Renders <paramref name="content"/> as a PNG.</summary>
    byte[] Render(string content);
}
