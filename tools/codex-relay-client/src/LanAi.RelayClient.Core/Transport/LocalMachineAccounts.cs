using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// The official sign-ins already on this machine — Codex's ChatGPT login and Claude Code's
/// Claude login — as local-proxy accounts beside the user's accounts on the relay.
/// </summary>
/// <remarks>
/// They take reserved negative ids: relay account ids are always positive, so everything
/// keyed by account id (the saved choice, outcomes, usage, refused encrypted history) keeps
/// the two kinds apart without a second key.
/// </remarks>
internal static class LocalMachineAccounts
{
    public const long CodexId = -1;

    public const long ClaudeId = -2;

    public static bool IsLocal(long accountId) => accountId < 0;

    public static long IdFor(LocalProxyKind kind) => kind == LocalProxyKind.ClaudeCode ? ClaudeId : CodexId;
}

/// <summary>A local-machine account could not give a token; <see cref="Exception.Message"/> is for the user.</summary>
internal sealed class LocalProxyCredentialException(string userMessage, Exception? innerException = null)
    : Exception(userMessage, innerException)
{
    public string UserMessage => Message;
}

internal enum LocalMachineAccountState
{
    SignedIn,
    NotSignedIn,
    Unsupported,
}

/// <param name="DisplayName">Who is signed in, e.g. 本机 ChatGPT 登录（a@b.com · Plus）.</param>
/// <param name="Detail">What to do about it, when not signed in or not supported.</param>
internal sealed record LocalMachineAccountStatus(LocalMachineAccountState State, string DisplayName, string Detail)
{
    public bool IsUsable => State == LocalMachineAccountState.SignedIn;
}

/// <summary>One official sign-in on this machine, as a credential source for the local proxy.</summary>
internal interface ILocalMachineAccount : ILocalProxyCredentialSource
{
    LocalProxyKind Kind { get; }

    /// <summary>Looks for the sign-in without refreshing anything. Never throws.</summary>
    LocalMachineAccountStatus Probe();
}

/// <summary>Reads a JWT's claims. Nothing is verified: these are the user's own tokens, read for labels and expiry.</summary>
internal static class JwtPayload
{
    public static JsonObject? Read(string? jwt)
    {
        if (string.IsNullOrWhiteSpace(jwt))
        {
            return null;
        }

        string[] parts = jwt.Split('.');
        if (parts.Length < 2)
        {
            return null;
        }

        try
        {
            string payload = parts[1].Replace('-', '+').Replace('_', '/');
            payload = payload.PadRight(payload.Length + ((4 - (payload.Length % 4)) % 4), '=');
            return JsonNode.Parse(Encoding.UTF8.GetString(Convert.FromBase64String(payload))) as JsonObject;
        }
        catch (Exception ex) when (ex is FormatException or JsonException or ArgumentException)
        {
            return null;
        }
    }

    public static DateTimeOffset? ExpiresAt(string? jwt) =>
        Read(jwt)?["exp"] is JsonValue exp && exp.TryGetValue(out long seconds)
            ? DateTimeOffset.FromUnixTimeSeconds(seconds)
            : null;

    public static string String(JsonObject? obj, string name) =>
        obj?[name] is JsonValue value && value.TryGetValue(out string? text) ? text ?? string.Empty : string.Empty;

    public static bool Bool(JsonObject? obj, string name) =>
        obj?[name] is JsonValue value && value.TryGetValue(out bool flag) && flag;
}
