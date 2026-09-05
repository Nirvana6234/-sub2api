using System.Net.Http;
using System.Security.Cryptography;
using System.Text;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Fetches an image referenced by an announcement body.</summary>
internal interface IAnnouncementImageLoader
{
    /// <summary>
    /// Loads one image, or returns null when it cannot be shown.
    /// </summary>
    /// <remarks>
    /// Never throws. Every failure — a rejected scheme, a timeout, an oversized
    /// body, a corrupt file — returns null so the caller can fall back to the alt
    /// text. An announcement must never fail to open because of one broken image.
    /// </remarks>
    /// <remarks>
    /// Returns the encoded bytes, not a decoded image. Decoding is the one part of
    /// this that has no cross-platform form — WPF wants a <c>BitmapImage</c>, Avalonia
    /// an <c>Avalonia.Media.Imaging.Bitmap</c> — so each head does it and everything
    /// worth guarding (scheme, size ceiling, timeout, cache) stays here where both
    /// share it.
    /// </remarks>
    Task<byte[]?> LoadAsync(string source, CancellationToken cancellationToken = default);
}

/// <summary>
/// Loads announcement images over the relay's own base address, with a cache.
/// </summary>
/// <remarks>
/// Deliberately not the UI framework's own image-from-URI facility, which would
/// fetch on that framework's stack: no timeout, no size ceiling, no control over
/// which schemes are reachable and no cache we can point at. All four matter here,
/// because the body is written by an operator and fetched by every client that
/// opens it.
/// </remarks>
internal sealed class AnnouncementImageLoader : IAnnouncementImageLoader
{
    /// <summary>Ceiling on a single image, applied before anything is decoded.</summary>
    internal const int MaxImageBytes = 5 * 1024 * 1024;

    /// <summary>How many cached files are kept before the oldest are dropped.</summary>
    private const int MaxCachedFiles = 100;

    private static readonly TimeSpan RequestTimeout = TimeSpan.FromSeconds(15);

    private readonly HttpClient _http;
    private readonly Uri _baseUri;
    private readonly string _cacheDirectory;

    /// <param name="baseAddress">
    /// The relay root. Announcement bodies routinely reference operator-uploaded
    /// images by relative path, so resolving against this is the common case, not
    /// a fallback — handle only absolute URLs and the 客服二维码 in a 公告 shows up
    /// as a broken image.
    /// </param>
    public AnnouncementImageLoader(HttpClient httpClient, string baseAddress, string? cacheDirectory = null)
    {
        _http = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _baseUri = new Uri(baseAddress ?? throw new ArgumentNullException(nameof(baseAddress)));
        _cacheDirectory = cacheDirectory ?? DefaultCacheDirectory();
    }

    internal static string DefaultCacheDirectory() => AppPaths.InData("announcement-images");

    public async Task<byte[]?> LoadAsync(string source, CancellationToken cancellationToken = default)
    {
        try
        {
            if (!TryResolve(source, _baseUri, out Uri? absolute, out byte[]? inlineBytes))
            {
                ClientLog.Warning($"公告图片地址不受支持，已跳过：{Describe(source)}");
                return null;
            }

            byte[]? bytes = inlineBytes ?? await LoadBytesAsync(absolute!, cancellationToken).ConfigureAwait(false);

            if (bytes is not null && !LooksLikeAnImage(bytes))
            {
                ClientLog.Warning($"公告图片不是可识别的图片格式，已跳过：{Describe(source)}");
                return null;
            }

            return bytes;
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            return null;
        }
        catch (Exception exception) when (exception is not (OutOfMemoryException or StackOverflowException))
        {
            ClientLog.Warning($"公告图片加载失败：{Describe(source)}", exception);
            return null;
        }
    }

    /// <summary>Whether the bytes begin with a signature of a format both heads can draw.</summary>
    /// <remarks>
    /// <para>
    /// This check exists because the decode moved out of this class. The loader used to
    /// return a decoded bitmap, so "not an image" fell out for free — the decode failed
    /// and the caller got null. Now that it returns bytes, that guarantee would have
    /// had to be re-implemented in <i>every</i> head, and the one that forgot would show
    /// a broken picture or throw inside a reader that must always open.
    /// </para>
    /// <para>
    /// A signature check is not a decode and does not prove the file is valid — a
    /// truncated PNG still passes here, and each head keeps its own guard for that. What
    /// it does do is keep the common case in one place, and stop arbitrary
    /// operator-authored bytes from reaching an image decoder at all.
    /// </para>
    /// <para>
    /// The list is the intersection of what WPF and Avalonia can both draw. A format
    /// only one of them supports would render on Windows and silently fail on macOS,
    /// which is worse than refusing it on both.
    /// </para>
    /// </remarks>
    private static bool LooksLikeAnImage(byte[] bytes)
    {
        if (bytes.Length < 12)
        {
            return false;
        }

        // PNG
        if (bytes[0] == 0x89 && bytes[1] == 0x50 && bytes[2] == 0x4E && bytes[3] == 0x47)
        {
            return true;
        }

        // JPEG
        if (bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF)
        {
            return true;
        }

        // GIF87a / GIF89a
        if (bytes[0] == 0x47 && bytes[1] == 0x49 && bytes[2] == 0x46 && bytes[3] == 0x38)
        {
            return true;
        }

        // BMP
        if (bytes[0] == 0x42 && bytes[1] == 0x4D)
        {
            return true;
        }

        // WebP: "RIFF" then a four-byte size then "WEBP".
        if (bytes[0] == 0x52 && bytes[1] == 0x49 && bytes[2] == 0x46 && bytes[3] == 0x46 &&
            bytes[8] == 0x57 && bytes[9] == 0x45 && bytes[10] == 0x42 && bytes[11] == 0x50)
        {
            return true;
        }

        return false;
    }

    /// <summary>
    /// Resolves a markdown image reference to something safe to fetch.
    /// </summary>
    /// <param name="inlineBytes">Set instead of <paramref name="absolute"/> for a data URI.</param>
    /// <remarks>
    /// Only http, https and <c>data:image/*;base64</c> are accepted. <c>file:</c>
    /// in particular is refused: announcement bodies are operator-authored, and a
    /// body must not be able to make a client read its own disk.
    /// </remarks>
    internal static bool TryResolve(string? source, Uri baseUri, out Uri? absolute, out byte[]? inlineBytes)
    {
        absolute = null;
        inlineBytes = null;

        string trimmed = (source ?? string.Empty).Trim();
        if (trimmed.Length == 0)
        {
            return false;
        }

        if (trimmed.StartsWith("data:", StringComparison.OrdinalIgnoreCase))
        {
            return TryDecodeDataUri(trimmed, out inlineBytes);
        }

        if (!Uri.TryCreate(baseUri, trimmed, out Uri? resolved))
        {
            return false;
        }

        if (resolved.Scheme != Uri.UriSchemeHttp && resolved.Scheme != Uri.UriSchemeHttps)
        {
            return false;
        }

        absolute = resolved;
        return true;
    }

    private static bool TryDecodeDataUri(string uri, out byte[]? bytes)
    {
        bytes = null;

        int comma = uri.IndexOf(',');
        if (comma < 0)
        {
            return false;
        }

        string header = uri[5..comma];
        if (!header.StartsWith("image/", StringComparison.OrdinalIgnoreCase) ||
            !header.EndsWith(";base64", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        string payload = uri[(comma + 1)..];

        // Checked before decoding: base64 is 4 characters per 3 bytes, so this
        // rejects an oversized payload without materialising it first.
        if ((long)payload.Length / 4 * 3 > MaxImageBytes)
        {
            return false;
        }

        try
        {
            bytes = Convert.FromBase64String(payload);
            return bytes.Length <= MaxImageBytes;
        }
        catch (FormatException)
        {
            return false;
        }
    }

    private async Task<byte[]?> LoadBytesAsync(Uri absolute, CancellationToken cancellationToken)
    {
        string cachePath = CachePathFor(absolute);
        if (File.Exists(cachePath))
        {
            try
            {
                return await File.ReadAllBytesAsync(cachePath, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                // Fall through to the network; a bad cache entry is not a failure.
            }
        }

        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(RequestTimeout);

        using var request = new HttpRequestMessage(HttpMethod.Get, absolute);
        using HttpResponseMessage response = await _http
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, timeout.Token)
            .ConfigureAwait(false);

        if (!response.IsSuccessStatusCode)
        {
            ClientLog.Warning($"公告图片返回 HTTP {(int)response.StatusCode}：{absolute}");
            return null;
        }

        if (response.Content.Headers.ContentLength is > MaxImageBytes)
        {
            ClientLog.Warning($"公告图片超过大小上限，已跳过：{absolute}");
            return null;
        }

        byte[]? bytes = await ReadCappedAsync(response, timeout.Token).ConfigureAwait(false);
        if (bytes is null)
        {
            ClientLog.Warning($"公告图片超过大小上限，已跳过：{absolute}");
            return null;
        }

        TryCache(cachePath, bytes);
        return bytes;
    }

    /// <summary>Reads the body, giving up rather than buffering past the ceiling.</summary>
    /// <remarks>
    /// A missing or lying Content-Length must not become an unbounded read, so the
    /// cap is enforced against what actually arrives.
    /// </remarks>
    private static async Task<byte[]?> ReadCappedAsync(
        HttpResponseMessage response,
        CancellationToken cancellationToken)
    {
        await using Stream stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);

        using var buffer = new MemoryStream();
        byte[] chunk = new byte[81920];

        while (true)
        {
            int read = await stream.ReadAsync(chunk, cancellationToken).ConfigureAwait(false);
            if (read == 0)
            {
                return buffer.ToArray();
            }

            if (buffer.Length + read > MaxImageBytes)
            {
                return null;
            }

            buffer.Write(chunk, 0, read);
        }
    }

    /// <remarks>
    /// Keyed on the absolute URL rather than the content, because the point of the
    /// cache is to skip the download — there is nothing to hash before it happens.
    /// </remarks>
    private string CachePathFor(Uri absolute)
    {
        byte[] digest = SHA256.HashData(Encoding.UTF8.GetBytes(absolute.AbsoluteUri));
        return Path.Combine(_cacheDirectory, Convert.ToHexString(digest, 0, 16).ToLowerInvariant() + ".img");
    }

    private void TryCache(string cachePath, byte[] bytes)
    {
        try
        {
            Directory.CreateDirectory(_cacheDirectory);

            string temporaryPath = cachePath + ".tmp";
            File.WriteAllBytes(temporaryPath, bytes);
            File.Move(temporaryPath, cachePath, overwrite: true);

            PruneCache();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // The image is already in hand; failing to cache it only costs a repeat
            // download next time.
            ClientLog.Warning("公告图片缓存写入失败", ex);
        }
    }

    private void PruneCache()
    {
        var files = new DirectoryInfo(_cacheDirectory).GetFiles("*.img");
        if (files.Length <= MaxCachedFiles)
        {
            return;
        }

        foreach (FileInfo stale in files
                     .OrderByDescending(file => file.LastWriteTimeUtc)
                     .Skip(MaxCachedFiles))
        {
            try
            {
                stale.Delete();
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                ClientLog.Warning("公告图片缓存清理失败", ex);
            }
        }
    }

    /// <remarks>Truncated so a pathological body cannot flood the log file.</remarks>
    private static string Describe(string? source)
    {
        string text = (source ?? string.Empty).Trim();
        return text.Length <= 120 ? text : text[..120] + "…";
    }
}
