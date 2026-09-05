using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class AnnouncementImageLoaderTests
{
    private static readonly Uri Relay = new("https://relay.test/");

    /// <remarks>
    /// The common case, not an edge case: operator-uploaded images are referenced
    /// by relative path, so a loader that only understood absolute URLs would show
    /// every uploaded 客服二维码 as a broken image.
    /// </remarks>
    [Theory]
    [InlineData("/uploads/qr.png", "https://relay.test/uploads/qr.png")]
    [InlineData("uploads/qr.png", "https://relay.test/uploads/qr.png")]
    [InlineData("https://cdn.example.com/a.png", "https://cdn.example.com/a.png")]
    [InlineData("http://cdn.example.com/a.png", "http://cdn.example.com/a.png")]
    public void ReferencesResolveAgainstTheRelayBaseAddress(string source, string expected)
    {
        Assert.True(AnnouncementImageLoader.TryResolve(source, Relay, out Uri? absolute, out byte[]? inline));

        Assert.Equal(expected, absolute!.AbsoluteUri);
        Assert.Null(inline);
    }

    /// <remarks>
    /// Announcement bodies are operator-authored. A body must not be able to make
    /// every client that opens it read its own disk or reach a non-web scheme.
    /// </remarks>
    [Theory]
    [InlineData("file:///C:/Windows/win.ini")]
    [InlineData("ftp://example.com/a.png")]
    [InlineData("javascript:alert(1)")]
    [InlineData("")]
    [InlineData("   ")]
    public void UnsupportedSchemesAreRefused(string source)
    {
        Assert.False(AnnouncementImageLoader.TryResolve(source, Relay, out Uri? absolute, out byte[]? inline));

        Assert.Null(absolute);
        Assert.Null(inline);
    }

    [Fact]
    public void InlineBase64ImagesAreDecodedWithoutATrip()
    {
        byte[] payload = [1, 2, 3, 4];
        string uri = "data:image/png;base64," + Convert.ToBase64String(payload);

        Assert.True(AnnouncementImageLoader.TryResolve(uri, Relay, out Uri? absolute, out byte[]? inline));

        Assert.Null(absolute);
        Assert.Equal(payload, inline);
    }

    [Theory]
    [InlineData("data:text/html;base64,PGgxPmhpPC9oMT4=")]
    [InlineData("data:image/png,notbase64")]
    [InlineData("data:image/png;base64,!!!not base64!!!")]
    [InlineData("data:image/png;base64")]
    public void OnlyBase64ImageDataUrisAreAccepted(string uri)
    {
        Assert.False(AnnouncementImageLoader.TryResolve(uri, Relay, out _, out byte[]? inline));

        Assert.Null(inline);
    }

    [Fact]
    public void AnOversizedInlineImageIsRejectedBeforeItIsDecoded()
    {
        // Sized past the ceiling in base64 characters, so the check has to happen
        // on the encoded length rather than after materialising the bytes.
        string payload = new('A', (AnnouncementImageLoader.MaxImageBytes / 3 * 4) + 8);

        Assert.False(AnnouncementImageLoader.TryResolve(
            "data:image/png;base64," + payload,
            Relay,
            out _,
            out byte[]? inline));

        Assert.Null(inline);
    }

    [Fact]
    public async Task ABodyThatIsNotAnImageFailsToNullRatherThanThrowing()
    {
        using var http = new System.Net.Http.HttpClient { BaseAddress = Relay };
        var loader = new AnnouncementImageLoader(http, Relay.AbsoluteUri, TestCacheDirectory());

        // Valid base64, valid data URI, but nothing any codec can decode. An
        // announcement must still open.
        Assert.Null(await loader.LoadAsync("data:image/png;base64," + Convert.ToBase64String([1, 2, 3, 4])));
    }

    /// <remarks>
    /// The counterpart of the test above. Rejecting everything would also make that one
    /// pass, so this pins that real images still get through — one signature per format
    /// both heads can draw.
    /// </remarks>
    [Theory]
    [InlineData(new byte[] { 0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0 })]
    [InlineData(new byte[] { 0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0 })]
    [InlineData(new byte[] { 0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0, 0, 0, 0, 0, 0 })]
    [InlineData(new byte[] { 0x42, 0x4D, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0 })]
    [InlineData(new byte[] { 0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50 })]
    public async Task RecognisedImageSignaturesAreLoaded(byte[] body)
    {
        using var http = new System.Net.Http.HttpClient { BaseAddress = Relay };
        var loader = new AnnouncementImageLoader(http, Relay.AbsoluteUri, TestCacheDirectory());

        byte[]? loaded = await loader.LoadAsync(
            "data:image/png;base64," + Convert.ToBase64String(body));

        Assert.NotNull(loaded);
        Assert.Equal(body, loaded);
    }

    /// <remarks>
    /// A body too short to carry any signature must be refused rather than indexed
    /// past. The check reads twelve bytes for the WebP case.
    /// </remarks>
    [Fact]
    public async Task ABodyTooShortToCarryASignatureIsRefused()
    {
        using var http = new System.Net.Http.HttpClient { BaseAddress = Relay };
        var loader = new AnnouncementImageLoader(http, Relay.AbsoluteUri, TestCacheDirectory());

        Assert.Null(await loader.LoadAsync(
            "data:image/png;base64," + Convert.ToBase64String([0x89, 0x50, 0x4E])));
    }

    [Fact]
    public async Task ARefusedSchemeFailsToNullRatherThanThrowing()
    {
        using var http = new System.Net.Http.HttpClient { BaseAddress = Relay };
        var loader = new AnnouncementImageLoader(http, Relay.AbsoluteUri, TestCacheDirectory());

        Assert.Null(await loader.LoadAsync("file:///C:/Windows/win.ini"));
    }

    private static string TestCacheDirectory() => System.IO.Path.Combine(
        System.IO.Path.GetTempPath(),
        "lanai-announce-img-" + Guid.NewGuid().ToString("N"));
}
