namespace LanAi.Workspace.Core;

public static class ConnectionEndpointNormalizer
{
    private static readonly string[] KnownResourceSuffixes =
    [
        "/chat/completions",
        "/count_tokens",
        "/responses",
        "/messages",
        "/models",
    ];

    public static string Normalize(CliKind client, string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return string.Empty;
        }

        string normalized = NormalizeHttpUrl(value);
        return client switch
        {
            CliKind.Codex or CliKind.GrokCli => EnsureVersionSuffix(normalized, "/v1"),
            CliKind.ClaudeCode => RemoveVersionSuffix(normalized, "/v1"),
            CliKind.GeminiCli => RemoveVersionSuffix(
                RemoveVersionSuffix(normalized, "/v1beta"),
                "/v1"),
            _ => normalized,
        };
    }

    public static bool AreEquivalent(CliKind client, string? left, string? right) =>
        string.Equals(
            Normalize(client, left),
            Normalize(client, right),
            StringComparison.OrdinalIgnoreCase);

    private static string NormalizeHttpUrl(string value)
    {
        string raw = value.Trim().Replace('\\', '/').TrimEnd('/');
        if (!Uri.TryCreate(raw, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(uri.Host))
        {
            return StripKnownResourceSuffix(raw);
        }

        var builder = new UriBuilder(uri)
        {
            Query = string.Empty,
            Fragment = string.Empty,
            Path = StripKnownResourceSuffix(uri.AbsolutePath),
        };
        return builder.Uri.GetLeftPart(UriPartial.Path).TrimEnd('/');
    }

    private static string StripKnownResourceSuffix(string value)
    {
        string normalized = value.TrimEnd('/');
        foreach (string suffix in KnownResourceSuffixes)
        {
            if (normalized.EndsWith(suffix, StringComparison.OrdinalIgnoreCase))
            {
                return normalized[..^suffix.Length].TrimEnd('/');
            }
        }

        return normalized;
    }

    private static string EnsureVersionSuffix(string value, string suffix)
    {
        string normalized = value.TrimEnd('/');
        if (normalized.EndsWith("/v1beta", StringComparison.OrdinalIgnoreCase))
        {
            normalized = normalized[..^"/v1beta".Length].TrimEnd('/');
        }

        return normalized.EndsWith(suffix, StringComparison.OrdinalIgnoreCase)
            ? normalized
            : normalized + suffix;
    }

    private static string RemoveVersionSuffix(string value, string suffix)
    {
        string normalized = value.TrimEnd('/');
        return normalized.EndsWith(suffix, StringComparison.OrdinalIgnoreCase)
            ? normalized[..^suffix.Length].TrimEnd('/')
            : normalized;
    }
}
