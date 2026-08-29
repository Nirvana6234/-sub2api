namespace LanAi.RelayClient.Server;

/// <summary>
/// Why a relay call failed, in terms the UI can act on.
/// </summary>
/// <remarks>
/// The requirements doc is explicit that every error must carry a usable next
/// step, and that a network outage must never be reported as "wrong password"
/// (F1 acceptance criteria). That distinction only survives if it is made here,
/// at the transport boundary, rather than re-derived from message strings in
/// the view models.
/// </remarks>
public enum RelayFailure
{
    /// <summary>The server could not be reached at all: DNS, TCP, TLS or timeout.</summary>
    NetworkUnreachable,

    /// <summary>Reached the server, but the reply was not a well-formed API envelope.</summary>
    MalformedResponse,

    /// <summary>Email or password rejected.</summary>
    InvalidCredentials,

    /// <summary>
    /// The account exists but has no password set — the documented consequence of
    /// supporting email login only (F1'.5): accounts created through OAuth may
    /// never have had one.
    /// </summary>
    /// <remarks>
    /// Whether the backend distinguishes this from <see cref="InvalidCredentials"/>
    /// is an open verification item in the requirements (risk 20, "M1 实测能否区分").
    /// Until that is settled <see cref="RelayApiException.Reason"/> carries the raw
    /// server reason code so the mapping can be tightened without touching callers.
    /// </remarks>
    PasswordNotSet,

    /// <summary>The caller is not authenticated, or the access token expired.</summary>
    Unauthenticated,

    /// <summary>Authenticated, but not allowed to perform this action.</summary>
    Forbidden,

    /// <summary>The requested resource does not exist.</summary>
    NotFound,

    /// <summary>Rejected by a rate limiter. Callers must back off rather than retry immediately.</summary>
    RateLimited,

    /// <summary>The request was well-formed but rejected on its contents (bad code, weak password, ...).</summary>
    Rejected,

    /// <summary>The server failed while handling an otherwise valid request.</summary>
    ServerError,
}
