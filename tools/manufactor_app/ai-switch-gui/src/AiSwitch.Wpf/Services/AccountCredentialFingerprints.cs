using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.Workspace.Wpf.Services;

internal static class AccountCredentialFingerprints
{
    private static readonly string[] TokenPropertyNames = ["access_token", "accessToken", "token"];
    private static readonly string[] TokenContainerNames = ["credentials", "tokens"];

    public static bool TryCreateFromJsonObject(JsonObject account, out string fingerprint)
    {
        fingerprint = string.Empty;
        if (!TryFindAccessToken(account, out string? token))
        {
            return false;
        }

        fingerprint = CreateAccessTokenFingerprint(token!);
        return true;
    }

    public static bool TryCreateFromJsonElement(JsonElement account, out string fingerprint)
    {
        fingerprint = string.Empty;
        if (!TryFindAccessToken(account, out string? token))
        {
            return false;
        }

        fingerprint = CreateAccessTokenFingerprint(token!);
        return true;
    }

    public static bool TryFindAccessToken(JsonObject account, out string? token)
    {
        token = null;
        foreach (string propertyName in TokenPropertyNames)
        {
            if (TryGetNonEmptyString(account, propertyName, out token))
            {
                return true;
            }
        }

        foreach (string containerName in TokenContainerNames)
        {
            if (!TryGetObject(account, containerName, out JsonObject? container))
            {
                continue;
            }

            foreach (string propertyName in TokenPropertyNames)
            {
                if (TryGetNonEmptyString(container!, propertyName, out token))
                {
                    return true;
                }
            }
        }

        return false;
    }

    public static bool TryFindAccessToken(JsonElement account, out string? token)
    {
        token = null;
        if (account.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        foreach (string propertyName in TokenPropertyNames)
        {
            if (TryGetNonEmptyString(account, propertyName, out token))
            {
                return true;
            }
        }

        foreach (string containerName in TokenContainerNames)
        {
            if (!TryGetProperty(account, containerName, out JsonElement container) ||
                container.ValueKind != JsonValueKind.Object)
            {
                continue;
            }

            foreach (string propertyName in TokenPropertyNames)
            {
                if (TryGetNonEmptyString(container, propertyName, out token))
                {
                    return true;
                }
            }
        }

        return false;
    }

    private static string CreateAccessTokenFingerprint(string token)
        => Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(token))).ToLowerInvariant();

    private static bool TryGetObject(JsonObject value, string propertyName, out JsonObject? result)
    {
        result = null;
        JsonNode? node = GetProperty(value, propertyName);
        if (node is not JsonObject obj)
        {
            return false;
        }

        result = obj;
        return true;
    }

    internal static string? GetString(JsonObject value, string propertyName)
        => TryGetNonEmptyString(value, propertyName, out string? result) ? result : null;

    private static bool TryGetNonEmptyString(JsonObject value, string propertyName, out string? result)
    {
        result = null;
        JsonNode? node = GetProperty(value, propertyName);
        if (node is null)
        {
            return false;
        }

        string? candidate;
        try
        {
            candidate = node.GetValue<string>();
        }
        catch (InvalidOperationException)
        {
            return false;
        }

        if (string.IsNullOrWhiteSpace(candidate))
        {
            return false;
        }

        result = candidate.Trim();
        return true;
    }

    private static JsonNode? GetProperty(JsonObject value, string propertyName)
    {
        foreach (KeyValuePair<string, JsonNode?> property in value)
        {
            if (string.Equals(property.Key, propertyName, StringComparison.OrdinalIgnoreCase))
            {
                return property.Value;
            }
        }

        return null;
    }

    private static bool TryGetNonEmptyString(JsonElement value, string propertyName, out string? result)
    {
        result = null;
        if (!TryGetProperty(value, propertyName, out JsonElement property) ||
            property.ValueKind != JsonValueKind.String)
        {
            return false;
        }

        string? candidate = property.GetString();
        if (string.IsNullOrWhiteSpace(candidate))
        {
            return false;
        }

        result = candidate.Trim();
        return true;
    }

    private static bool TryGetProperty(JsonElement value, string propertyName, out JsonElement property)
    {
        property = default;
        if (value.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        foreach (JsonProperty candidate in value.EnumerateObject())
        {
            if (string.Equals(candidate.Name, propertyName, StringComparison.OrdinalIgnoreCase))
            {
                property = candidate.Value;
                return true;
            }
        }

        return false;
    }
}
