using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.Workspace.Injection;

namespace LanAi.RelayClient.Services;

/// <summary>What happened when the user pressed 启动 Codex.</summary>
internal enum CodexStartupStatus
{
    /// <summary>Codex is running and pointed at the relay.</summary>
    Ready,

    /// <summary>The official desktop app is not installed (F2 takes over).</summary>
    NotInstalled,

    /// <summary>
    /// Codex is already running, but restarting it is required and needs consent.
    /// </summary>
    NeedsRestartConfirmation,

    /// <summary>The relay refused, or could not be reached.</summary>
    RelayUnavailable,

    /// <summary>Codex was started but never became reachable.</summary>
    CodexUnresponsive,

    /// <summary>Something local failed — writing config, most likely.</summary>
    LocalFailure,
}

internal sealed record CodexStartupResult(CodexStartupStatus Status, string Message);

/// <summary>What the background check found, without changing anything.</summary>
/// <param name="IsInstalled">Whether the official desktop app is present.</param>
/// <param name="IsRunning">Whether it is up right now.</param>
/// <param name="LeaseExpiresAt">When the managed lease lapses; null when there is none.</param>
internal sealed record CodexHealth(bool IsInstalled, bool IsRunning, DateTimeOffset? LeaseExpiresAt);

/// <summary>Runs the sequence that gets Codex talking to the relay.</summary>
/// <remarks>
/// An interface so the dashboard can be tested without the outcome depending on
/// whether the machine running the tests happens to have Codex installed.
/// </remarks>
internal interface ICodexStartup
{
    Task<CodexStartupResult> RunAsync(
        long? groupId,
        string apiBaseUrl,
        bool allowRestart = false,
        CancellationToken cancellationToken = default,
        string? preferredModel = null);

    /// <summary>Reports the current state without starting or writing anything.</summary>
    Task<CodexHealth> CheckAsync(CancellationToken cancellationToken = default);

    /// <summary>Checks only the local installation state without server traffic.</summary>
    Task<bool> CheckInstalledAsync(CancellationToken cancellationToken = default);

    /// <summary>
    /// Extends the lease when it is close to lapsing (F3.2.3).
    /// </summary>
    /// <returns>The new expiry when a renewal happened, otherwise null.</returns>
    Task<DateTimeOffset?> RenewLeaseIfDueAsync(CancellationToken cancellationToken = default);

    /// <summary>Revokes the managed lease and restores the user's Codex files.</summary>
    Task ReleaseAsync(CancellationToken cancellationToken = default);
}

/// <summary>
/// Gets the user from "signed in" to "Codex is billing through the relay" (F3).
/// </summary>
/// <remarks>
/// <para>
/// Order matters and is not arbitrary: the lease is obtained first, the config is
/// written second, Codex is launched last. Launching before the config is in
/// place would start Codex against whatever was there before, and the user would
/// see a working app that is not using their relay balance at all.
/// </para>
/// <para>
/// Nothing here force-closes a running Codex. That would discard whatever turn
/// the user has in flight, so a restart is escalated to them as a question
/// instead (F11.5's reasoning, applied here).
/// </para>
/// </remarks>
internal sealed class CodexStartup : ICodexStartup
{
    /// <summary>Lease length, in days. One day is the lease itself (F3.2.2).</summary>
    private const int LeaseDays = 1;

    /// <summary>
    /// How much of the lease may remain before it is rolled forward.
    /// </summary>
    /// <remarks>
    /// Half the lease. Renewing only at the last moment would mean a machine that
    /// happens to be asleep, offline or shut down over the final hour lets the
    /// authorization lapse — and the user meets a Codex that stopped working for
    /// reasons they cannot see.
    /// </remarks>
    private static readonly TimeSpan RenewWhenRemaining = TimeSpan.FromHours(12);

    /// <summary>The process the official desktop app runs as.</summary>
    private const string CodexProcessName = "ChatGPT";

    private readonly IRelayServerClient _relay;
    private readonly RelaySessionManager _session;
    private readonly ManagedKeyNaming _naming;
    private readonly CodexConfigWriter _config;
    private readonly ICodexAppLauncher _launcher;
    private readonly ICodexEnhancementHost _enhancement;
    private readonly SemaphoreSlim _lifecycleGate = new(1, 1);
    private int _releaseRequests;
    private bool _released;

    public CodexStartup(
        IRelayServerClient relay,
        RelaySessionManager session,
        ManagedKeyNaming naming,
        CodexConfigWriter config,
        ICodexAppLauncher launcher,
        ICodexEnhancementHost? enhancement = null)
    {
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _naming = naming ?? throw new ArgumentNullException(nameof(naming));
        _config = config ?? throw new ArgumentNullException(nameof(config));
        _launcher = launcher ?? throw new ArgumentNullException(nameof(launcher));
        _enhancement = enhancement ?? new NullCodexEnhancementHost();
    }

    /// <param name="groupId">The group to bill against, when a key must be issued.</param>
    /// <param name="apiBaseUrl">The relay's OpenAI-compatible endpoint, from the server.</param>
    /// <param name="allowRestart">
    /// Set only after the user has agreed to restarting a running Codex.
    /// </param>
    /// <param name="preferredModel">
    /// An explicit model selected for a Claude/Kiro group. When absent, the
    /// user's existing top-level Codex model setting is preserved.
    /// </param>
    public async Task<CodexStartupResult> RunAsync(
        long? groupId,
        string apiBaseUrl,
        bool allowRestart = false,
        CancellationToken cancellationToken = default,
        string? preferredModel = null)
    {
        if (Volatile.Read(ref _releaseRequests) > 0)
        {
            return ReleaseInProgressResult();
        }

        await _lifecycleGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (Volatile.Read(ref _releaseRequests) > 0)
            {
                return ReleaseInProgressResult();
            }

            _released = false;

            if (!_launcher.IsInstalled)
            {
                // Checked before anything else: issuing a lease for an app that is not
                // there would leave a live credential nobody uses.
                ClientLog.Info("ChatGPT 桌面版未安装");
                return new CodexStartupResult(
                    CodexStartupStatus.NotInstalled,
                    "还没有检测到 ChatGPT 桌面版。");
            }

            if (string.IsNullOrWhiteSpace(apiBaseUrl))
            {
                // Without the server's own endpoint there is nothing correct to write.
                // Guessing one from the sign-in address is how a client ends up
                // pointing at something that merely looks right.
                ClientLog.Warning("服务器未下发 api_base_url，无法写入 ChatGPT 配置");
                return new CodexStartupResult(
                    CodexStartupStatus.RelayUnavailable,
                    "服务器没有提供接口地址，请稍后再试。");
            }

            RelayApiKey key;
            try
            {
                key = await EnsureLeaseAsync(groupId, cancellationToken).ConfigureAwait(true);
            }
            catch (RelayApiException ex)
            {
                ClientLog.Warning("获取授权失败", ex);
                return new CodexStartupResult(CodexStartupStatus.RelayUnavailable, ex.UserMessage);
            }

            if (string.IsNullOrWhiteSpace(key.Key))
            {
                // The list endpoint returns the secret in full, so an empty one means
                // the contract changed. Writing it would produce a config that fails
                // every request with no clue why.
                ClientLog.Warning($"授权 {key.Id} 没有返回密钥内容");
                return new CodexStartupResult(
                    CodexStartupStatus.RelayUnavailable,
                    "服务器没有返回可用的授权内容。");
            }

            try
            {
                _config.Apply(key.Key, apiBaseUrl, preferredModel);
                ClientLog.Info($"已写入 ChatGPT 配置，授权 {key.Id}");
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                ClientLog.Error("写入 ChatGPT 配置失败", ex);
                return new CodexStartupResult(
                    CodexStartupStatus.LocalFailure,
                    "无法写入 ChatGPT 的配置文件，请确认它没有被其他程序占用。");
            }

            CodexLaunchResult launch = await _launcher
                .EnsureDebugPortAsync(new CodexLaunchRequest { AllowTerminateExisting = allowRestart }, cancellationToken)
                .ConfigureAwait(true);

            ClientLog.Info($"拉起 ChatGPT：{launch.Outcome}");

            if (launch.CanAttach)
            {
                bool enhanced = await _enhancement
                    .StartAsync(key.Key, apiBaseUrl, cancellationToken)
                    .ConfigureAwait(true);
                if (!enhanced)
                {
                    ClientLog.Warning("ChatGPT 已启动，状态条与限额检测暂不可用");
                }

                return new CodexStartupResult(CodexStartupStatus.Ready, "ChatGPT 已就绪，可以开始对话了。");
            }

            return launch.Outcome switch
            {
                CodexLaunchOutcome.BlockedByRunningInstance =>
                    new CodexStartupResult(
                        CodexStartupStatus.NeedsRestartConfirmation,
                        "ChatGPT 正在运行，需要重启后才能接入。重启会中断正在进行的对话。"),

                CodexLaunchOutcome.NotInstalled =>
                    new CodexStartupResult(CodexStartupStatus.NotInstalled, "还没有检测到 ChatGPT 桌面版。"),

                _ => new CodexStartupResult(
                    CodexStartupStatus.CodexUnresponsive,
                    "ChatGPT 已启动但没有响应，请稍后重试。"),
            };
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    /// <remarks>
    /// Read-only by construction: the button's state and the tray's status line are
    /// driven from this, and a status check that could start an application or
    /// spend a lease would be a surprising thing to run every minute.
    /// </remarks>
    public async Task<CodexHealth> CheckAsync(CancellationToken cancellationToken = default)
    {
        bool installed = _launcher.IsInstalled;
        bool running = installed && System.Diagnostics.Process.GetProcessesByName(CodexProcessName).Length > 0;

        DateTimeOffset? expiry = null;
        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            IReadOnlyList<RelayApiKey> keys = await _relay.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
            expiry = _naming.FindCurrent(keys)?.ExpiresAt;
        }
        catch (RelayApiException ex) when (ex.Failure == RelayFailure.RateLimited)
        {
            throw;
        }
        catch (Exception ex) when (ex is RelayApiException or OperationCanceledException)
        {
            // Being offline says nothing about whether Codex is installed or up, so
            // those two answers still stand; the lease is simply unknown.
            ClientLog.Warning("检查授权状态失败", ex);
        }

        return new CodexHealth(installed, running, expiry);
    }

    public Task<bool> CheckInstalledAsync(CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return Task.FromResult(_launcher.IsInstalled);
    }

    public async Task<DateTimeOffset?> RenewLeaseIfDueAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            IReadOnlyList<RelayApiKey> keys = await _relay.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
            RelayApiKey? lease = _naming.FindCurrent(keys);

            // Nothing to extend, and nothing is issued here either: creating a lease
            // is what pressing the button does, deliberately, with a group chosen.
            if (lease?.ExpiresAt is not { } expiry)
            {
                return null;
            }

            if (expiry - DateTimeOffset.UtcNow > RenewWhenRemaining)
            {
                return null;
            }

            RelayApiKey renewed = await _relay
                .RenewApiKeyAsync(token, lease.Id, DateTimeOffset.UtcNow.AddDays(LeaseDays), cancellationToken)
                .ConfigureAwait(true);

            ClientLog.Info($"已续签授权 {lease.Id}，新到期 {renewed.ExpiresAt:o}");
            return renewed.ExpiresAt;
        }
        catch (RelayApiException ex) when (ex.Failure == RelayFailure.RateLimited)
        {
            throw;
        }
        catch (Exception ex) when (ex is RelayApiException or OperationCanceledException)
        {
            // A failed renewal is not fatal on its own: the lease still has hours
            // left, and the next poll will try again.
            ClientLog.Warning("续签授权失败，稍后重试", ex);
            return null;
        }
    }

    public async Task ReleaseAsync(CancellationToken cancellationToken = default)
    {
        Interlocked.Increment(ref _releaseRequests);
        try
        {
            await _lifecycleGate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
            try
            {
                if (_released)
                {
                    return;
                }

                try
                {
                    await _enhancement.StopAsync().ConfigureAwait(false);
                }
                catch (Exception ex)
                {
                    ClientLog.Warning("停止 Codex 增强会话失败", ex);
                }

                bool localReleaseCompleted = false;
                try
                {
                    string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(false);
                    IReadOnlyList<RelayApiKey> keys = await _relay
                        .ListApiKeysAsync(token, cancellationToken)
                        .ConfigureAwait(false);
                    RelayApiKey? managed = _naming.FindCurrent(keys);

                    IEnumerable<RelayApiKey> keysToDelete = _naming.FindOrphans(keys);
                    if (managed is not null)
                    {
                        keysToDelete = keysToDelete.Append(managed);
                    }

                    foreach (RelayApiKey key in keysToDelete.DistinctBy(key => key.Id))
                    {
                        try
                        {
                            await _relay.DeleteApiKeyAsync(token, key.Id, cancellationToken).ConfigureAwait(false);
                            ClientLog.Info($"已撤销托管授权 {key.Id}");
                        }
                        catch (RelayApiException ex)
                        {
                            ClientLog.Warning($"撤销托管授权 {key.Id} 失败，将由租约自动过期", ex);
                        }
                    }
                }
                catch (Exception ex) when (ex is RelayApiException or InvalidOperationException or OperationCanceledException)
                {
                    ClientLog.Warning("撤销托管授权失败，将由租约自动过期", ex);
                }
                finally
                {
                    try
                    {
                        if (_config.RestoreOriginalFiles())
                        {
                            ClientLog.Info("已恢复用户原始 Codex 配置");
                        }

                        localReleaseCompleted = true;
                    }
                    catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidDataException)
                    {
                        ClientLog.Error("恢复用户原始 Codex 配置失败", ex);
                    }
                }

                _released = localReleaseCompleted;
            }
            finally
            {
                _lifecycleGate.Release();
            }
        }
        finally
        {
            Interlocked.Decrement(ref _releaseRequests);
        }
    }

    private static CodexStartupResult ReleaseInProgressResult() =>
        new(CodexStartupStatus.LocalFailure, "正在释放 ChatGPT 配置，请稍后再试。");

    /// <summary>
    /// Returns a usable lease, renewing or issuing as needed (F3.2.1 / F3.2.2).
    /// </summary>
    /// <remarks>
    /// An existing lease is reused rather than replaced, so pressing the button
    /// twice does not litter the user's key list. Only a lease that is missing or
    /// already spent leads to a new one.
    /// </remarks>
    private async Task<RelayApiKey> EnsureLeaseAsync(long? groupId, CancellationToken cancellationToken)
    {
        string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);

        IReadOnlyList<RelayApiKey> keys = await _relay.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
        RelayApiKey? existing = _naming.FindCurrent(keys);

        if (existing is not null && !IsSpent(existing))
        {
            ClientLog.Info($"复用现有授权 {existing.Id}");
            return existing;
        }

        ClientLog.Info(existing is null ? "签发新授权" : $"授权 {existing.Id} 已过期，重新签发");

        return await _relay
            .CreateApiKeyAsync(token, _naming.KeyName(), groupId, LeaseDays, cancellationToken)
            .ConfigureAwait(true);
    }

    /// <remarks>
    /// A lease already past its expiry is spent. One with no expiry is also
    /// treated as spent: under F3.2 that is a defect rather than a permanent
    /// grant, and reusing it would keep an unbounded authorization alive.
    /// </remarks>
    private static bool IsSpent(RelayApiKey key) =>
        key.ExpiresAt is not { } expiry || expiry <= DateTimeOffset.UtcNow;
}
