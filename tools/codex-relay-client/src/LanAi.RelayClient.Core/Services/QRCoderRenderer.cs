using QRCoder;

namespace LanAi.RelayClient.Services;

/// <summary>Renders QR codes with QRCoder.</summary>
/// <remarks>
/// <c>PngByteQRCode</c> specifically, not <c>QRCode</c>: the latter draws through
/// <c>System.Drawing.Common</c>, which is Windows-only from .NET 6 onward and would
/// have put a platform dependency in the middle of the payment flow. The PNG encoder
/// is managed code and runs anywhere.
/// </remarks>
public sealed class QRCoderRenderer : IQRCodeRenderer
{
    public byte[] Render(string content)
    {
        if (string.IsNullOrWhiteSpace(content))
        {
            throw new ArgumentException("QR content is required.", nameof(content));
        }

        using var generator = new QRCodeGenerator();
        using QRCodeData data = generator.CreateQrCode(content, QRCodeGenerator.ECCLevel.Q);
        var qr = new PngByteQRCode(data);
        return qr.GetGraphic(8);
    }
}
