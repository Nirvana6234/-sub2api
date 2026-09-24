namespace LanAi.RelayClient.DesktopSync;

public enum DesktopStartOutcome
{
    /// <summary>A start is under way (or just finished); queued messages go out once the app answers.</summary>
    Starting,

    /// <summary>ChatGPT is already running and only its pipe is missing: still booting, or a version without app-tools.</summary>
    AlreadyRunning,

    /// <summary>Not started, for the reason given: not installed, no group to bill to, ...</summary>
    Refused,
}

/// <summary>What asking to start the desktop app for a phone came to.</summary>
public sealed record DesktopStartResult(DesktopStartOutcome Outcome, string? Message = null);

/// <summary>
/// What this computer can check about a failed turn without touching ChatGPT.
/// </summary>
public sealed record DesktopSelfCheckResult(bool RelayListening, bool SignedIn, bool ServerReachable)
{
    /// <summary>One sentence for the phone: the first thing found wrong, or that nothing was.</summary>
    public string Summary =>
        !SignedIn ? "电脑上的共飞助手登录已失效，请在电脑上重新登录。"
        : !RelayListening ? "电脑上的本机中转没有在运行：可以点「远程修复 ChatGPT」，或在电脑上点「修复 ChatGPT 启动」。"
        : !ServerReachable ? "电脑连不上共飞服务器，请检查电脑的网络。"
        : "电脑这边一切正常，多半是服务端临时出错，可以重发；仍然失败再试「远程修复 ChatGPT」。";
}

/// <summary>
/// The read-only checks behind the phone's 电脑自检: is the loopback relay answering, is
/// the account still signed in, is the server reachable. Nothing here restarts anything.
/// </summary>
/// <remarks>
/// The relay is not restarted on its own even when it does not answer: its port is
/// written into ChatGPT's config, and a relay back on another port would fail the same
/// way, silently. What fixes that is 修复 ChatGPT 启动, which stops every conversation —
/// a separate, signed command the user asks for from the phone (desktop.repair).
/// </remarks>
/// <param name="relayOrigin">Where the loopback relay listens; null when it has not started, which counts as not listening.</param>
internal sealed class DesktopSelfCheck(
    HttpClient http,
    Func<string?> relayOrigin,
    Uri server,
    Func<CancellationToken, Task<bool>> signedIn)
{
    private static readonly TimeSpan ProbeTimeout = TimeSpan.FromSeconds(4);

    public async Task<DesktopSelfCheckResult> RunAsync(CancellationToken cancellationToken)
    {
        string? origin = relayOrigin();
        Task<bool> relay = origin is null ? Task.FromResult(false) : AnswersAsync(new Uri(origin + "/"), anyStatus: true, cancellationToken);
        Task<bool> reachable = AnswersAsync(new Uri(server, "/health"), anyStatus: false, cancellationToken);
        Task<bool> session = SignedInAsync(cancellationToken);

        await Task.WhenAll(relay, reachable, session).ConfigureAwait(false);
        return new DesktopSelfCheckResult(relay.Result, session.Result, reachable.Result);
    }

    /// <param name="anyStatus">True when any HTTP answer counts: the relay says 404 to a path it does not serve.</param>
    private async Task<bool> AnswersAsync(Uri uri, bool anyStatus, CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(ProbeTimeout);
        try
        {
            using HttpResponseMessage response = await http.GetAsync(uri, HttpCompletionOption.ResponseHeadersRead, timeout.Token)
                .ConfigureAwait(false);
            return anyStatus || response.IsSuccessStatusCode;
        }
        catch (Exception ex) when (ex is HttpRequestException or OperationCanceledException)
        {
            return false;
        }
    }

    private async Task<bool> SignedInAsync(CancellationToken cancellationToken)
    {
        try
        {
            return await signedIn(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is not OutOfMemoryException)
        {
            return false;
        }
    }
}
