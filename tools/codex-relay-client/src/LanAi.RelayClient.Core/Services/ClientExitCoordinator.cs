namespace LanAi.RelayClient.Services;

/// <summary>Coordinates Codex cleanup with the two distinct ways the client ends use.</summary>
internal sealed class ClientExitCoordinator
{
    private readonly ICodexStartup _codex;
    private readonly RelaySessionManager _session;

    public ClientExitCoordinator(ICodexStartup codex, RelaySessionManager session)
    {
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _session = session ?? throw new ArgumentNullException(nameof(session));
    }

    public async Task SignOutAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            await _codex.ReleaseAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            ClientLog.Error("退出登录前释放 Codex 配置失败", ex);
        }
        finally
        {
            await _session.SignOutAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    public async Task ReleaseForExitAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            await _codex.ReleaseAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            ClientLog.Error("退出客户端前释放 Codex 配置失败", ex);
        }
    }
}
