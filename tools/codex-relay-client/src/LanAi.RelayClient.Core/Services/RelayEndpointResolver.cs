namespace LanAi.RelayClient.Services;

internal static class RelayEndpointResolver
{
    public static string ResolveApiBaseUrl(string serverAddress, string? advertisedApiBaseUrl)
    {
        if (!Uri.TryCreate(serverAddress, UriKind.Absolute, out Uri? serverUri))
        {
            throw new ArgumentException("Server address must be an absolute URI.", nameof(serverAddress));
        }

        if (Uri.TryCreate(advertisedApiBaseUrl, UriKind.Absolute, out Uri? advertisedUri))
        {
            if (!advertisedUri.IsLoopback || serverUri.IsLoopback)
            {
                return Upgraded(serverUri, advertisedUri).AbsoluteUri.TrimEnd('/');
            }

            var rewritten = new UriBuilder(serverUri)
            {
                Path = advertisedUri.AbsolutePath,
                Query = advertisedUri.Query.TrimStart('?'),
                Fragment = string.Empty,
            };
            return rewritten.Uri.AbsoluteUri.TrimEnd('/');
        }

        return new Uri(serverUri, "v1").AbsoluteUri.TrimEnd('/');
    }

    /// <summary>
    /// Keeps an advertised endpoint from downgrading a secure connection to plaintext.
    /// </summary>
    /// <remarks>
    /// <para>
    /// This value is written into Codex's own configuration and then carries the
    /// relay API key on every request. The client reached the server over https;
    /// a server that advertises an http endpoint would move that key onto a
    /// plaintext connection, and nothing in the UI would say so.
    /// </para>
    /// <para>
    /// Only the scheme is lifted, and only in that direction. A build pointed at
    /// an http server keeps talking http — the guard is about not losing security
    /// the connection already had, not about inventing it.
    /// </para>
    /// </remarks>
    private static Uri Upgraded(Uri serverUri, Uri advertisedUri)
    {
        bool wouldDowngrade =
            serverUri.Scheme == Uri.UriSchemeHttps &&
            advertisedUri.Scheme == Uri.UriSchemeHttp;

        if (!wouldDowngrade)
        {
            return advertisedUri;
        }

        // An explicit :80 would survive the scheme change and produce
        // "https://host:80"; -1 restores the default port for the new scheme.
        return new UriBuilder(advertisedUri)
        {
            Scheme = Uri.UriSchemeHttps,
            Port = advertisedUri.IsDefaultPort ? -1 : advertisedUri.Port,
        }.Uri;
    }
}
