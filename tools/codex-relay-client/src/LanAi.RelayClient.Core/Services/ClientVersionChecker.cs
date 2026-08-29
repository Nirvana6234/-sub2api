using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Services;

public sealed record ClientUpdateInfo(Version Version, Uri DownloadPage, string ReleaseNotes)
{
    public string VersionLabel => $"Ver{Version.Major}.{Version.Minor}";
}

internal sealed class ClientVersionChecker
{
    private readonly HttpClient _http;
    private readonly Version _currentVersion;

    public ClientVersionChecker(HttpClient http, Version currentVersion)
    {
        _http = http ?? throw new ArgumentNullException(nameof(http));
        _currentVersion = currentVersion ?? throw new ArgumentNullException(nameof(currentVersion));
    }

    public async Task<ClientUpdateInfo?> CheckAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            VersionManifest? manifest = await _http
                .GetFromJsonAsync("client-version.json", ClientJsonContext.Default.VersionManifest, cancellationToken)
                .ConfigureAwait(false);
            if (!Version.TryParse(manifest?.Version, out Version? latest) || latest <= _currentVersion ||
                string.IsNullOrWhiteSpace(manifest.DownloadPage) || _http.BaseAddress is null ||
                !Uri.TryCreate(_http.BaseAddress, manifest.DownloadPage, out Uri? downloadPage))
            {
                return null;
            }

            return new ClientUpdateInfo(latest, downloadPage, manifest.ReleaseNotes ?? string.Empty);
        }
        catch (HttpRequestException)
        {
            return null;
        }
        catch (NotSupportedException)
        {
            return null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    internal sealed record VersionManifest(
        [property: JsonPropertyName("version")] string? Version,
        [property: JsonPropertyName("download_page")] string? DownloadPage,
        [property: JsonPropertyName("release_notes")] string? ReleaseNotes);
}
