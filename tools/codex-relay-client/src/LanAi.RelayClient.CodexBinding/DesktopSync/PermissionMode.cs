using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>How much a conversation may do without asking, as the desktop app shows it.</summary>
public enum PermissionMode
{
    /// <summary>Unreadable or unrecognised. Callers must treat it as <see cref="FullAccess"/>.</summary>
    Unknown,

    /// <summary>No sandbox, never asks: a message from the phone runs as the user.</summary>
    FullAccess,

    /// <summary>Sandboxed and asks before leaving it: a phone-started turn can stall on the desktop.</summary>
    Auto,

    /// <summary>Sandboxed and never asks: steps outside the sandbox just fail.</summary>
    SandboxedNoApproval,
}

public static class PermissionModes
{
    /// <summary>Classifies the two columns the desktop app writes for each conversation.</summary>
    /// <remarks>
    /// Measured values: full access is <c>{"type":"disabled"}</c> with <c>never</c>;
    /// auto is <c>{"type":"managed",…}</c> with <c>on-request</c>. Anything else is
    /// <see cref="PermissionMode.Unknown"/> rather than a guess, because the one wrong
    /// guess that matters is calling a full-access conversation safe.
    /// </remarks>
    public static PermissionMode Classify(string? sandboxPolicy, string? approvalMode)
    {
        string? sandboxType = ReadType(sandboxPolicy);
        return (sandboxType, approvalMode) switch
        {
            ("disabled", "never") => PermissionMode.FullAccess,
            ("managed", "on-request") => PermissionMode.Auto,
            ("managed", "never") => PermissionMode.SandboxedNoApproval,
            _ => PermissionMode.Unknown,
        };
    }

    private static string? ReadType(string? json)
    {
        if (string.IsNullOrWhiteSpace(json))
        {
            return null;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(json);
            return document.RootElement.ValueKind == JsonValueKind.Object &&
                   document.RootElement.TryGetProperty("type", out JsonElement type) &&
                   type.ValueKind == JsonValueKind.String
                ? type.GetString()
                : null;
        }
        catch (JsonException)
        {
            return null;
        }
    }
}
