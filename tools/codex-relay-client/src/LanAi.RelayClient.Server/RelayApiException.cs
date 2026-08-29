namespace LanAi.RelayClient.Server;

/// <summary>
/// A relay API call that did not succeed.
/// </summary>
/// <remarks>
/// Carries the server's own <c>reason</c> code alongside the classified
/// <see cref="RelayFailure"/>. Keeping the raw code matters: several mappings in
/// this client are still provisional pending live verification against the
/// production server, and a preserved reason lets those be refined without
/// changing the exception contract.
/// </remarks>
public sealed class RelayApiException : Exception
{
    public RelayApiException(
        RelayFailure failure,
        string message,
        string? reason = null,
        int? statusCode = null,
        Exception? innerException = null)
        : base(message, innerException)
    {
        Failure = failure;
        Reason = reason;
        StatusCode = statusCode;
    }

    public RelayFailure Failure { get; }

    /// <summary>The server's machine-readable reason code, when it supplied one.</summary>
    public string? Reason { get; }

    /// <summary>The HTTP status, absent when the request never reached the server.</summary>
    public int? StatusCode { get; }

    /// <summary>
    /// A message safe to show a non-technical user, with a next step where one exists.
    /// </summary>
    /// <remarks>
    /// Deliberately never includes the raw status code or server text: the
    /// requirements forbid surfacing bare HTTP codes and stack traces (§7).
    /// The technical detail stays in <see cref="Exception.Message"/> for logs.
    /// </remarks>
    public string UserMessage => Failure switch
    {
        RelayFailure.NetworkUnreachable => "连不上服务器，请检查网络后重试。",
        RelayFailure.MalformedResponse => "服务器返回了无法识别的内容，请稍后重试。",
        RelayFailure.InvalidCredentials => "邮箱或密码不正确。",
        RelayFailure.PasswordNotSet => "该账号还没有设置密码，请先在网页版设置密码后再登录。",
        RelayFailure.Unauthenticated => "登录已失效，请重新登录。",
        RelayFailure.Forbidden => "当前账号没有权限执行该操作。",
        RelayFailure.NotFound => "请求的内容不存在，可能已被删除。",
        RelayFailure.RateLimited => "操作过于频繁，请稍后再试。",
        RelayFailure.ServerError => "服务器暂时出了点问题，请稍后重试。",

        // Rejections are the one case where the server's own message is the most
        // useful thing we can say ("邀请码无效", "邮箱后缀不在白名单"), so it is
        // passed through rather than replaced with a generic sentence.
        _ => string.IsNullOrWhiteSpace(Message) ? "请求未被接受，请检查填写的内容。" : Message,
    };
}
