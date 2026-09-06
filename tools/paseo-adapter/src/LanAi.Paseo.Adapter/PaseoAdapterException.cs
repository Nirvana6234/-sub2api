namespace LanAi.Paseo.Adapter;

/// <summary>
/// Every failure this library reports, carrying the one thing the caller has to
/// branch on: <see cref="Code"/>.
/// </summary>
/// <remarks>
/// The codes exist because the right user-facing behaviour differs per case:
/// <c>CodexMissing</c> is an install/login flow, <c>DaemonDown</c> is a restart
/// flow, <c>PermissionRequired</c> is a prompt, and <c>TransportDown</c> is a
/// silent reconnect. A consumer that catches this type and prints
/// <see cref="Exception.Message"/> for all of them has thrown away the reason
/// the adapter classifies at all.
/// </remarks>
public sealed class PaseoAdapterException : Exception
{
    public PaseoAdapterException(PaseoErrorCode code, string message, string? detail = null)
        : base(message)
    {
        Code = code;
        Detail = detail;
    }

    public PaseoErrorCode Code { get; }

    /// <summary>Diagnostic text for logs. Never render verbatim to end users.</summary>
    public string? Detail { get; }

    internal static PaseoErrorCode ParseCode(string? wireCode) => wireCode switch
    {
        "CONTRACT_MISMATCH" => PaseoErrorCode.ContractMismatch,
        "UNAUTHORIZED" => PaseoErrorCode.Unauthorized,
        "DAEMON_DOWN" => PaseoErrorCode.DaemonDown,
        "CODEX_MISSING" => PaseoErrorCode.CodexMissing,
        "PERMISSION_REQUIRED" => PaseoErrorCode.PermissionRequired,
        "UNKNOWN_OP" => PaseoErrorCode.UnknownOperation,
        "BAD_REQUEST" => PaseoErrorCode.BadRequest,
        // An unrecognised code is version skew, not a transport problem. Internal
        // keeps it visible in logs without pretending we understood it.
        _ => PaseoErrorCode.Internal,
    };
}
