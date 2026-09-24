using System.Diagnostics;
using System.Net;
using System.Net.Http.Headers;
using LanAi.Workspace.Core;

namespace LanAi.Workspace.Wpf.Services;

internal interface IEndpointProbeService
{
    Task<IReadOnlyList<EndpointHealthResult>> ProbeAllAsync(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileRouting? routing,
        ConnectionProfileSelection? selection,
        CancellationToken cancellationToken);
}

internal sealed record EndpointHealthResult(
    CliKind CliKind,
    string ClientLabel,
    string DestinationLabel,
    bool Succeeded,
    int? LatestLatencyMilliseconds,
    string StatusCategory,
    string StatusLabel,
    long ProbeCount24Hours,
    double? SuccessRate24Hours,
    int? P50LatencyMilliseconds,
    int? P95LatencyMilliseconds,
    DateTimeOffset? LastSuccessAt);

/// <summary>
/// Probes the three configured CLI routes from this computer. It never stores
/// endpoints or credentials; persistence receives only a bounded route ID,
/// display label, outcome category, and latency.
/// </summary>
internal sealed class EndpointProbeService : IEndpointProbeService, IDisposable
{
    private readonly IConnectionCredentialProvider _credentialProvider;
    private readonly ILocalTelemetryRepository _telemetryRepository;
    private readonly HttpClient _httpClient;
    private readonly bool _ownsHttpClient;

    public EndpointProbeService(
        IConnectionCredentialProvider credentialProvider,
        ILocalTelemetryRepository telemetryRepository)
        : this(
            credentialProvider,
            telemetryRepository,
            new HttpClient(new HttpClientHandler { UseProxy = false, AllowAutoRedirect = false })
            {
                Timeout = TimeSpan.FromSeconds(8),
            },
            ownsHttpClient: true)
    {
    }

    internal EndpointProbeService(
        IConnectionCredentialProvider credentialProvider,
        ILocalTelemetryRepository telemetryRepository,
        HttpClient httpClient,
        bool ownsHttpClient = false)
    {
        _credentialProvider = credentialProvider ?? throw new ArgumentNullException(nameof(credentialProvider));
        _telemetryRepository = telemetryRepository ?? throw new ArgumentNullException(nameof(telemetryRepository));
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _ownsHttpClient = ownsHttpClient;
    }

    public async Task<IReadOnlyList<EndpointHealthResult>> ProbeAllAsync(
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileRouting? routing,
        ConnectionProfileSelection? selection,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(connections);
        Task<ProbeObservation>[] probes = Enum.GetValues<CliKind>()
            .Select(cli => ProbeOneAsync(
                cli,
                ResolveProfile(cli, connections, routing, selection),
                cancellationToken))
            .ToArray();
        ProbeObservation[] observations = await Task.WhenAll(probes).ConfigureAwait(false);

        IReadOnlyList<LocalNetworkHealthSummary> summaries = await _telemetryRepository
            .GetNetworkHealthSummariesAsync(DateTimeOffset.UtcNow.AddHours(-24), cancellationToken)
            .ConfigureAwait(false);
        var bySource = summaries
            .Where(summary => !string.IsNullOrWhiteSpace(summary.SourceId))
            .ToDictionary(summary => summary.SourceId!, StringComparer.OrdinalIgnoreCase);

        return observations
            .Select(observation => ToResult(
                observation,
                bySource.GetValueOrDefault(SourceId(observation.CliKind))))
            .OrderBy(result => result.CliKind)
            .ToArray();
    }

    public void Dispose()
    {
        if (_ownsHttpClient)
        {
            _httpClient.Dispose();
        }
    }

    private async Task<ProbeObservation> ProbeOneAsync(
        CliKind cli,
        ConnectionProfile? profile,
        CancellationToken cancellationToken)
    {
        string clientLabel = ClientLabel(cli);
        if (profile is null || !TryCreateProbeUri(cli, profile, out Uri? probeUri))
        {
            var missing = new ProbeObservation(cli, clientLabel, "未配置", false, null, "configuration", "未配置可用来源");
            await RecordAsync(missing, cancellationToken).ConfigureAwait(false);
            return missing;
        }

        string? secret = await _credentialProvider
            .GetSecretAsync(profile.Id, cli, cancellationToken)
            .ConfigureAwait(false);

        // Warm-up is intentionally not persisted. It separates DNS/TLS setup
        // from the latency users see after the route is ready.
        await SendProbeAsync(cli, probeUri!, secret, cancellationToken, measure: false).ConfigureAwait(false);
        ProbeMeasurement measurement = await SendProbeAsync(
                cli,
                probeUri!,
                secret,
                cancellationToken,
                measure: true)
            .ConfigureAwait(false);
        secret = null;

        var observation = new ProbeObservation(
            cli,
            clientLabel,
            profile.Name,
            measurement.Succeeded,
            measurement.LatencyMilliseconds,
            measurement.StatusCategory,
            measurement.StatusLabel);
        await RecordAsync(observation, cancellationToken).ConfigureAwait(false);
        return observation;
    }

    private async Task<ProbeMeasurement> SendProbeAsync(
        CliKind cli,
        Uri probeUri,
        string? secret,
        CancellationToken cancellationToken,
        bool measure)
    {
        using HttpRequestMessage request = CreateRequest(cli, probeUri, secret);
        var stopwatch = Stopwatch.StartNew();
        try
        {
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken)
                .ConfigureAwait(false);
            stopwatch.Stop();
            return Classify(response.StatusCode, measure ? ClampLatency(stopwatch.ElapsedMilliseconds) : null);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (TaskCanceledException)
        {
            stopwatch.Stop();
            return new ProbeMeasurement(false, measure ? ClampLatency(stopwatch.ElapsedMilliseconds) : null, "timeout", "连接超时");
        }
        catch (HttpRequestException exception)
        {
            stopwatch.Stop();
            string category = exception.InnerException is System.Net.Sockets.SocketException
                ? "dns-or-connect"
                : "network";
            return new ProbeMeasurement(false, measure ? ClampLatency(stopwatch.ElapsedMilliseconds) : null, category, "网络连接失败");
        }
    }

    private async Task RecordAsync(ProbeObservation observation, CancellationToken cancellationToken)
        => await _telemetryRepository.RecordNetworkProbeAsync(
                new LocalNetworkHealthProbe(
                    DateTimeOffset.UtcNow,
                    SourceId(observation.CliKind),
                    $"{observation.ClientLabel} · {observation.DestinationLabel}",
                    observation.Succeeded,
                    observation.LatencyMilliseconds,
                    observation.StatusCategory),
                cancellationToken)
            .ConfigureAwait(false);

    private static EndpointHealthResult ToResult(
        ProbeObservation observation,
        LocalNetworkHealthSummary? summary)
        => new(
            observation.CliKind,
            observation.ClientLabel,
            observation.DestinationLabel,
            observation.Succeeded,
            observation.LatencyMilliseconds,
            observation.StatusCategory,
            observation.StatusLabel,
            summary?.ProbeCount ?? 1,
            summary?.SuccessRatePercent ?? (observation.Succeeded ? 100d : 0d),
            summary?.P50LatencyMilliseconds,
            summary?.P95LatencyMilliseconds,
            summary?.LastSuccessAt);

    private static ConnectionProfile? ResolveProfile(
        CliKind cli,
        IReadOnlyList<ConnectionProfile> connections,
        ConnectionProfileRouting? routing,
        ConnectionProfileSelection? selection)
    {
        string? id = routing is null
            ? selection?.ActiveProfileId ?? selection?.CloudProfileId ?? selection?.LocalProfileId
            : cli switch
            {
                CliKind.Codex => routing.CodexProfileId,
                CliKind.ClaudeCode => routing.ClaudeCodeProfileId,
                CliKind.GeminiCli => routing.GeminiCliProfileId,
                CliKind.GrokCli => string.IsNullOrWhiteSpace(routing.GrokCliProfileId) ? routing.GeminiCliProfileId : routing.GrokCliProfileId,
                _ => null,
            };
        return connections.FirstOrDefault(profile =>
            string.Equals(profile.Id, id, StringComparison.OrdinalIgnoreCase));
    }

    private static bool TryCreateProbeUri(CliKind cli, ConnectionProfile profile, out Uri? uri)
    {
        uri = null;
        string raw = ConnectionEndpointNormalizer.Normalize(
            cli,
            profile.ClientBaseUrls.GetValueOrDefault(cli, profile.BaseUrl));
        if (!Uri.TryCreate(raw, UriKind.Absolute, out Uri? baseUri) ||
            baseUri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(baseUri.Host) ||
            !string.IsNullOrWhiteSpace(baseUri.UserInfo) ||
            (string.Equals(baseUri.Scheme, Uri.UriSchemeHttp, StringComparison.OrdinalIgnoreCase) &&
             !IsLoopbackAddress(baseUri.Host)))
        {
            return false;
        }

        var builder = new UriBuilder(baseUri) { Query = string.Empty, Fragment = string.Empty };
        string path = builder.Path.TrimEnd('/');
        builder.Path = cli switch
        {
            CliKind.Codex when path.EndsWith("/v1", StringComparison.OrdinalIgnoreCase) => path + "/models",
            CliKind.Codex => path + "/v1/models",
            CliKind.GrokCli when path.EndsWith("/v1", StringComparison.OrdinalIgnoreCase) => path + "/models",
            CliKind.GrokCli => path + "/v1/models",
            CliKind.ClaudeCode when path.EndsWith("/v1", StringComparison.OrdinalIgnoreCase) => path + "/models",
            CliKind.ClaudeCode => path + "/v1/models",
            CliKind.GeminiCli when path.EndsWith("/v1beta", StringComparison.OrdinalIgnoreCase) => path + "/models",
            CliKind.GeminiCli => path + "/v1beta/models",
            _ => path,
        };
        uri = builder.Uri;
        return true;
    }

    private static bool IsLoopbackAddress(string host)
        => IPAddress.TryParse(host.Trim('[', ']'), out IPAddress? address) && IPAddress.IsLoopback(address);

    private static HttpRequestMessage CreateRequest(CliKind cli, Uri uri, string? secret)
    {
        var request = new HttpRequestMessage(HttpMethod.Get, uri);
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
        if (!string.IsNullOrWhiteSpace(secret))
        {
            switch (cli)
            {
                case CliKind.Codex:
                case CliKind.GrokCli:
                    request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", secret);
                    break;
                case CliKind.ClaudeCode:
                    request.Headers.TryAddWithoutValidation("x-api-key", secret);
                    request.Headers.TryAddWithoutValidation("anthropic-version", "2023-06-01");
                    break;
                case CliKind.GeminiCli:
                    request.Headers.TryAddWithoutValidation("x-goog-api-key", secret);
                    break;
            }
        }

        return request;
    }

    private static ProbeMeasurement Classify(HttpStatusCode statusCode, int? latency)
    {
        int code = (int)statusCode;
        if (code is >= 200 and < 300)
        {
            return new ProbeMeasurement(true, latency, "ok", "连接正常");
        }

        return statusCode switch
        {
            HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden =>
                new ProbeMeasurement(false, latency, "authentication", "密钥或权限异常"),
            HttpStatusCode.TooManyRequests =>
                new ProbeMeasurement(false, latency, "rate-limited", "当前受到限流"),
            HttpStatusCode.NotFound =>
                new ProbeMeasurement(false, latency, "route", "接口路径不兼容"),
            _ when code >= 500 =>
                new ProbeMeasurement(false, latency, "upstream", "上游服务异常"),
            _ => new ProbeMeasurement(false, latency, "http", $"HTTP {code}"),
        };
    }

    private static int ClampLatency(long elapsedMilliseconds)
        => (int)Math.Clamp(elapsedMilliseconds, 0, int.MaxValue);

    private static string SourceId(CliKind cli) => $"route-{cli}";

    private static string ClientLabel(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude",
        CliKind.GeminiCli => "Gemini",
        CliKind.GrokCli => "Grok",
        _ => cli.ToString(),
    };

    private sealed record ProbeObservation(
        CliKind CliKind,
        string ClientLabel,
        string DestinationLabel,
        bool Succeeded,
        int? LatencyMilliseconds,
        string StatusCategory,
        string StatusLabel);

    private sealed record ProbeMeasurement(
        bool Succeeded,
        int? LatencyMilliseconds,
        string StatusCategory,
        string StatusLabel);
}


