using System.IO;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.Workspace.Injection;
using LanAi.RelayClient.Transport;

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
    /// <summary>Whether a bundled context filter exists to be switched at all.</summary>
    bool HasContextFilter => false;

    /// <summary>
    /// Turns context compression on or off, taking effect now rather than at the
    /// next launch.
    /// </summary>
    /// <remarks>
    /// Asynchronous because honouring it while Codex is connected means restarting
    /// the filter process; a checkbox that only takes effect after a relaunch, with
    /// nothing on screen saying so, is the behaviour this replaced.
    /// </remarks>
    Task SetContextFilterEnabledAsync(bool enabled, CancellationToken cancellationToken = default) =>
        Task.CompletedTask;

    /// <summary>
    /// True when Codex is routed through the in-process loopback relay rather than a
    /// managed API key issued by the server.
    /// </summary>
    /// <remarks>
    /// The dashboard has to know: under the loopback relay there is no key whose group
    /// can be edited server-side, so switching groups is a local push
    /// (<see cref="SetActiveGroup"/>) and takes effect at once.
    /// </remarks>
    bool UsesLocalTransport => false;

    /// <summary>Rebinds forwarded traffic to <paramref name="groupId"/>, effective immediately.</summary>
    void SetActiveGroup(long? groupId) { }
    /// <param name="forceNewKey">
    /// Skips reusing an existing, unexpired lease and issues a fresh one instead. The
    /// normal reuse exists so pressing 启动 twice does not litter the key list, but
    /// that same reuse means a key that is live-but-broken in a way <c>IsSpent</c>
    /// cannot see (wrong or missing group, revoked from the panel, ...) gets handed
    /// back forever. Only the manual repair path should set this — a launch that
    /// isn't trying to fix anything has no reason to churn through keys.
    /// </param>
    Task<CodexStartupResult> RunAsync(
        long? groupId,
        string apiBaseUrl,
        bool allowRestart = false,
        CancellationToken cancellationToken = default,
        string? preferredModel = null,
        bool forceNewKey = false);

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
    private readonly ICodexRouteGuardHost _routeGuard;
    private readonly LocalPawRelay? _localRelay;
    private readonly ContextFilterProcess? _contextFilter;
    private bool _contextFilterEnabled = true;
    private readonly SemaphoreSlim _lifecycleGate = new(1, 1);
    private int _releaseRequests;
    private bool _released;

    public CodexStartup(
        IRelayServerClient relay,
        RelaySessionManager session,
        ManagedKeyNaming naming,
        CodexConfigWriter config,
        ICodexAppLauncher launcher,
        ICodexRouteGuardHost? routeGuard = null,
        LocalPawRelay? localRelay = null,
        ContextFilterProcess? contextFilter = null)
    {
        _relay = relay ?? throw new ArgumentNullException(nameof(relay));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _naming = naming ?? throw new ArgumentNullException(nameof(naming));
        _config = config ?? throw new ArgumentNullException(nameof(config));
        _launcher = launcher ?? throw new ArgumentNullException(nameof(launcher));
        _routeGuard = routeGuard ?? new NullCodexRouteGuardHost();
        _localRelay = localRelay;
        _contextFilter = contextFilter;
    }

    public bool HasContextFilter => _contextFilter is not null;

    /// <remarks>
    /// Takes the lifecycle gate: restarting the filter underneath a launch or a
    /// release in flight would move the port Codex was just configured with.
    /// </remarks>
    public async Task SetContextFilterEnabledAsync(bool enabled, CancellationToken cancellationToken = default)
    {
        await _lifecycleGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            _contextFilterEnabled = enabled;
            if (_contextFilter is null)
            {
                return;
            }

            // Only meaningful while a session is up. With Codex not connected there is
            // no process to restart, and the next launch reads the field above.
            if (!_contextFilter.IsRunning)
            {
                return;
            }

            await _contextFilter.ApplyFilterEnabledAsync(enabled, cancellationToken).ConfigureAwait(false);
            ClientLog.Info(enabled ? "已开启上下文压缩" : "已关闭上下文压缩（仍通过本机转发）");
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    public bool UsesLocalTransport => _localRelay is not null;

    public void SetActiveGroup(long? groupId) => _localRelay?.SetGroup(groupId);

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
        string? preferredModel = null,
        bool forceNewKey = false)
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

            if (_localRelay is null && string.IsNullOrWhiteSpace(apiBaseUrl))
            {
                // Without the server's own endpoint there is nothing correct to write.
                // Guessing one from the sign-in address is how a client ends up
                // pointing at something that merely looks right.
                ClientLog.Warning("服务器未下发 api_base_url，无法写入 ChatGPT 配置");
                return new CodexStartupResult(
                    CodexStartupStatus.RelayUnavailable,
                    "服务器没有提供接口地址，请稍后再试。");
            }

            if (_localRelay is not null && groupId is null)
            {
                // The loopback relay refuses every request that carries no group, so
                // launching here would write the config, start Codex, report 就绪 —
                // and then fail every single turn with nothing on screen to explain it.
                ClientLog.Warning("未选择分组，拒绝启动本机转发");
                return new CodexStartupResult(
                    CodexStartupStatus.RelayUnavailable,
                    "请先选择一个分组，再启动 ChatGPT。");
            }

            string codexKey;
            string codexBaseUrl;
            long? selectedGroup = groupId;
            RelayApiKey? managedKey = null;
            if (_localRelay is not null)
            {
                try
                {
                    // Not kept: the relay fetches the session per request. Asked for here
                    // only so a signed-out user is told so now, instead of after Codex has
                    // been restarted and every turn fails.
                    await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
                    await _localRelay.StartAsync(cancellationToken).ConfigureAwait(true);
                    _localRelay.SetGroup(selectedGroup);
                    codexKey = _localRelay.Token;
                    if (_contextFilter is not null)
                    {
                        // In the chain whether or not compression is on. With it off the
                        // filter is a transparent pass-through (measured), and keeping it
                        // there is what lets the switch work without moving the address
                        // Codex was configured with — see ContextFilterProcess.
                        _contextFilter.SetUpstream(_localRelay.BaseAddress!);
                        await _contextFilter.StartAsync(_contextFilterEnabled, cancellationToken).ConfigureAwait(true);
                        codexBaseUrl = _contextFilter.BaseAddress?.GetLeftPart(UriPartial.Authority) + "/v1";
                        ClientLog.Info(_contextFilterEnabled
                            ? "已启动 Context Filter（压缩开启）"
                            : "已启动 Context Filter（压缩关闭，直通）");
                    }
                    else
                    {
                        codexBaseUrl = _localRelay.BaseAddress?.GetLeftPart(UriPartial.Authority) + "/v1";
                    }
                    ClientLog.Info("已启动本机 Paw Relay，Codex 不再使用远程 API Key");
                }
                // Deliberately not a list of expected types. The earlier filter named
                // three and missed two that this path really throws — Win32Exception
                // from Process.Start, and OperationCanceledException from the context
                // filter's own five-second port wait — and anything that escapes here
                // leaves the loopback listener up while it can still reach the user's
                // account session. Caller cancellation is the one thing that is not
                // ours to swallow.
                catch (Exception ex) when (ex is not OperationCanceledException ||
                                           !cancellationToken.IsCancellationRequested)
                {
                    ClientLog.Warning("启动本机 Paw Relay 失败", ex);
                    await StopLocalTransportAsync().ConfigureAwait(false);
                    return new CodexStartupResult(CodexStartupStatus.RelayUnavailable, "本机通信组件启动失败，请重试。");
                }
                catch (OperationCanceledException)
                {
                    await StopLocalTransportAsync().ConfigureAwait(false);
                    throw;
                }
            }
            else
            {
                try { managedKey = await EnsureLeaseAsync(groupId, forceNewKey, cancellationToken).ConfigureAwait(true); }
                catch (RelayApiException ex) { ClientLog.Warning("获取授权失败", ex); return new CodexStartupResult(CodexStartupStatus.RelayUnavailable, ex.UserMessage); }
                if (string.IsNullOrWhiteSpace(managedKey.Key))
                    return new CodexStartupResult(CodexStartupStatus.RelayUnavailable, "服务器没有返回可用的授权内容。");
                codexKey = managedKey.Key;
                codexBaseUrl = apiBaseUrl;
            }

            try
            {
                _config.Apply(codexKey, codexBaseUrl, preferredModel);
                ClientLog.Info(_localRelay is null ? $"已写入 ChatGPT 配置，授权 {managedKey!.Id}" : "已写入 ChatGPT 本机 Relay 配置");
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                ClientLog.Error("写入 ChatGPT 配置失败", ex);
                await StopLocalTransportAsync().ConfigureAwait(false);
                return new CodexStartupResult(
                    CodexStartupStatus.LocalFailure,
                    "无法写入 ChatGPT 的配置文件，请确认它没有被其他程序占用。");
            }

            CodexLaunchResult launch = await _launcher
                .EnsureDebugPortAsync(new CodexLaunchRequest { AllowTerminateExisting = allowRestart }, cancellationToken)
                .ConfigureAwait(true);

            ClientLog.Info($"拉起 ChatGPT：{launch.Outcome}");

            bool debugPortUnavailable = launch.Outcome == CodexLaunchOutcome.DebugPortUnavailable;
            if (launch.CanAttach || debugPortUnavailable)
            {
                if (debugPortUnavailable)
                {
                    // Activation returned a process id, so the app did start — only
                    // its DevTools endpoint never came up in time. Newer builds
                    // increasingly ignore or block --remote-debugging-port, which
                    // used to surface here as "ChatGPT 已启动但没有响应" even though
                    // routing was already applied and the app was perfectly usable.
                    // Nothing depends on that port any more, so this is a note, not
                    // a degraded state.
                    ClientLog.Info("调试端口未打开，按普通启动处理");
                }

                // Runs on every launch path, including the one above where no debug
                // port ever opened. This is the watch that notices an official ChatGPT
                // sign-in rewriting config.toml and dropping [model_providers.gongfei]
                // — the failure the user cannot see, because the client goes on saying
                // 就绪 while the traffic has quietly returned to their ChatGPT plan.
                // An earlier version returned before this call in the no-debug-port
                // branch, which left that guard off entirely on the growing share of
                // machines where the port never opens.
                await _routeGuard
                    .StartAsync(codexKey, codexBaseUrl, cancellationToken)
                    .ConfigureAwait(true);

                return new CodexStartupResult(
                    CodexStartupStatus.Ready,
                    "ChatGPT 已就绪，可以开始对话了。");
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
            if (_localRelay is null)
            {
                string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
                IReadOnlyList<RelayApiKey> keys = await _relay.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
                expiry = _naming.FindCurrent(keys)?.ExpiresAt;
            }
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
        if (_localRelay is not null)
            return null;

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
                    await _routeGuard.StopAsync().ConfigureAwait(false);
                }
                catch (Exception ex)
                {
                    ClientLog.Warning("停止 Codex 路由守护失败", ex);
                }

                bool localReleaseCompleted = false;
                try
                {
                    if (_localRelay is not null)
                    {
                        if (_contextFilter is not null)
                            await _contextFilter.DisposeAsync().ConfigureAwait(false);
                        await _localRelay.StopAsync().ConfigureAwait(false);
                        ClientLog.Info("已停止本机 Paw Relay");
                    }
                    if (_localRelay is null)
                    {
                        string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(false);
                        IReadOnlyList<RelayApiKey> keys = await _relay
                            .ListApiKeysAsync(token, cancellationToken)
                            .ConfigureAwait(false);
                        RelayApiKey? managed = _naming.FindCurrent(keys);
                        IEnumerable<RelayApiKey> keysToDelete = _naming.FindOrphans(keys);
                        if (managed is not null)
                            keysToDelete = keysToDelete.Append(managed);

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

    private async Task StopLocalTransportAsync()
    {
        if (_contextFilter is not null)
            await _contextFilter.DisposeAsync().ConfigureAwait(false);
        if (_localRelay is not null)
            await _localRelay.StopAsync().ConfigureAwait(false);
    }

    /// <summary>
    /// Returns a usable lease, renewing or issuing as needed (F3.2.1 / F3.2.2).
    /// </summary>
    /// <remarks>
    /// An existing lease is reused rather than replaced, so pressing the button
    /// twice does not litter the user's key list. Only a lease that is missing or
    /// already spent leads to a new one — unless <paramref name="forceNewKey"/> says
    /// otherwise: <c>IsSpent</c> only knows about expiry, so a key that is broken in
    /// some other way (no group bound, revoked from the panel) still looks perfectly
    /// reusable to it, and the repair path exists precisely for that case.
    /// </remarks>
    private async Task<RelayApiKey> EnsureLeaseAsync(long? groupId, bool forceNewKey, CancellationToken cancellationToken)
    {
        string token = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);

        IReadOnlyList<RelayApiKey> keys = await _relay.ListApiKeysAsync(token, cancellationToken).ConfigureAwait(true);
        RelayApiKey? existing = _naming.FindCurrent(keys);

        if (!forceNewKey && existing is not null && !IsSpent(existing))
        {
            ClientLog.Info($"复用现有授权 {existing.Id}");
            return existing;
        }

        if (forceNewKey && existing is not null)
        {
            // Best-effort: a failed delete must not block the repair itself, since the
            // new key issued right below is what the user is actually here for. A
            // duplicate left behind this way is harmless — ManagedKeyNaming.FindCurrent
            // picks the one with the later expiry, which the fresh key always has — and
            // it gets swept up as an orphan the next time this installation releases.
            try
            {
                await _relay.DeleteApiKeyAsync(token, existing.Id, cancellationToken).ConfigureAwait(true);
                ClientLog.Info($"修复：已撤销旧授权 {existing.Id}");
            }
            catch (RelayApiException ex)
            {
                ClientLog.Warning($"修复：撤销旧授权 {existing.Id} 失败，继续签发新授权", ex);
            }
        }

        ClientLog.Info(existing is null
            ? "签发新授权"
            : forceNewKey
            ? $"修复：强制签发新授权替换 {existing.Id}"
            : $"授权 {existing.Id} 已过期，重新签发");

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
