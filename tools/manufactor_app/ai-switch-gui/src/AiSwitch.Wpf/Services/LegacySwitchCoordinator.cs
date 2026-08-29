using System.Text.Json;
using AiSwitchGui;
using LanAi.Workspace.Infrastructure;

namespace LanAi.Workspace.Wpf.Services;

internal interface ILegacySwitchCoordinator
{
    LiveStatus ReadLiveStatus();

    ImportedLiveConfig ReadCurrentClientConfig();

    Task<OperationResult> ApplySourceAsync(string profileId, CancellationToken cancellationToken = default);

    Task<OperationResult> ValidateSourceAsync(string profileId, CancellationToken cancellationToken = default);

    Task<OperationResult> ApplyRoutingAsync(CancellationToken cancellationToken = default);

    Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(CancellationToken cancellationToken = default);

    Task<OperationResult> ValidateRoutingAsync(CancellationToken cancellationToken = default);

    Task<OperationResult> RestoreLatestBackupAsync(CancellationToken cancellationToken = default);

    ClaudeGptRoutingStatus ReadClaudeGptRoutingStatus();

    ClaudeGptModelMapping? ReadClaudeGptPreset(string profileId, string targetPlatform);

    Task<IReadOnlyList<string>> GetClaudeGptModelsAsync(
        string profileId,
        string targetPlatform,
        CancellationToken cancellationToken = default);

    Task<OperationResult> EnableClaudeGptRoutingAsync(
        string profileId,
        string targetPlatform,
        ClaudeGptModelMapping mapping,
        CancellationToken cancellationToken = default);

    Task<OperationResult> DisableClaudeGptRoutingAsync(CancellationToken cancellationToken = default);

    CodexClaudeRoutingStatus ReadCodexClaudeRoutingStatus();

    CodexClaudeModelMapping? ReadCodexClaudePreset(string profileId, string targetPlatform);

    Task<IReadOnlyList<string>> GetCodexClaudeModelsAsync(
        string profileId,
        string targetPlatform,
        CancellationToken cancellationToken = default);

    Task<OperationResult> EnableCodexClaudeRoutingAsync(
        string profileId,
        CodexClaudeModelMapping mapping,
        CancellationToken cancellationToken = default);

    Task<OperationResult> DisableCodexClaudeRoutingAsync(CancellationToken cancellationToken = default);

    Task<OperationResult> ResumeLastApplicationStateAsync(CancellationToken cancellationToken = default);

    Task<OperationResult> SaveApplicationStateAsync(CancellationToken cancellationToken = default);

    Task<OperationResult> RestoreApplicationSessionAsync(CancellationToken cancellationToken = default);

    string? GetDashboardUrl(string profileId);
}

/// <summary>
/// Bridges the still-supported legacy source document to the proven client
/// switcher. It deliberately reuses the old ProfileRepository and
/// SwitchService so the WPF shell and the WinForms compatibility tool apply
/// exactly the same configuration rules.
/// </summary>
internal sealed class LegacySwitchCoordinator : ILegacySwitchCoordinator, IDisposable
{
    private readonly ProfileRepository _profiles;
    private readonly SwitchService _switchService;
    private readonly CrossClientRoutingPresetStore _routingPresets;
    private readonly ApplicationResumeStateStore _resumeStateStore;
    private readonly SessionConfigSnapshot _applicationSessionSnapshot;
    private readonly ILocalSub2ApiRoutingService? _localRoutingService;
    private readonly IDisposable? _ownedLocalRoutingService;
    private readonly SemaphoreSlim _operationGate = new(1, 1);
    private ApplicationResumeState _resumeState;
    private bool _applicationSessionRestored;

    public LegacySwitchCoordinator(
        AppDataPaths paths,
        ISub2ApiSessionManager? sessionManager = null,
        Func<string?>? localControlTokenProvider = null)
        : this(
            paths,
            sessionManager is null ? null : new LocalSub2ApiRoutingService(sessionManager, localControlTokenProvider),
            ownsLocalRoutingService: sessionManager is not null)
    {
    }

    internal LegacySwitchCoordinator(
        AppDataPaths paths,
        ILocalSub2ApiRoutingService localRoutingService)
        : this(paths, localRoutingService, ownsLocalRoutingService: false)
    {
    }

    private LegacySwitchCoordinator(
        AppDataPaths paths,
        ILocalSub2ApiRoutingService? localRoutingService,
        bool ownsLocalRoutingService)
    {
        ArgumentNullException.ThrowIfNull(paths);

        string root = Path.GetDirectoryName(paths.LegacyProfilesPath)
            ?? throw new InvalidOperationException("无法确定旧版 profiles.json 的目录。");
        var configPaths = new ConfigPaths(root, paths.UserProfile, paths.LocalAppData);
        _profiles = new ProfileRepository(configPaths);
        _profiles.EnsureInitialized();
        _switchService = new SwitchService(configPaths, _profiles);
        _routingPresets = new CrossClientRoutingPresetStore(paths.CrossClientRoutingPresetsPath);
        _resumeStateStore = new ApplicationResumeStateStore(paths.ApplicationResumeStatePath);
        _localRoutingService = localRoutingService;
        _ownedLocalRoutingService = ownsLocalRoutingService
            ? localRoutingService as IDisposable
            : null;
        // Recover all client changes left by an abnormal previous exit before
        // defining what "the state before this launch" means.
        _ = _switchService.RestoreAbandonedApplicationSessionSnapshot();
        _ = _switchService.DisableClaudeGptRouting();
        _ = _switchService.DisableCodexClaudeRouting();
        _applicationSessionSnapshot = _switchService.CreateSessionSnapshot();
        _ = _switchService.PersistApplicationSessionSnapshot(_applicationSessionSnapshot);
        _resumeState = _resumeStateStore.Load() ?? new ApplicationResumeState();
    }

    public LiveStatus ReadLiveStatus()
    {
        ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
        return _switchService.ReadLiveStatus(store);
    }

    public ImportedLiveConfig ReadCurrentClientConfig() => _switchService.ReadCurrentClientConfig();

    public async Task<OperationResult> ApplySourceAsync(
        string profileId,
        CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            (ProfileStore store, TargetMode mode) = SelectSource(profileId);
            IReadOnlyList<string> updatedPlatforms = Array.Empty<string>();
            IReadOnlyList<LocalSub2ApiRoutingIssue> routingIssues = Array.Empty<LocalSub2ApiRoutingIssue>();
            if (_localRoutingService is not null)
            {
                LocalSub2ApiRoutingResult routingResult = await _localRoutingService
                    .ApplySourceAsync(store, profileId, cancellationToken)
                    .ConfigureAwait(false);
                store = routingResult.ClientStore;
                mode = TargetMode.Local;
                updatedPlatforms = routingResult.UpdatedPlatforms;
                routingIssues = routingResult.Issues;
            }

            OperationResult result = await _switchService.SwitchAsync(store, mode, cancellationToken).ConfigureAwait(false);
            if (result.Success)
            {
                _resumeState.BaseRoutingMode = ApplicationBaseRoutingMode.UnifiedSource;
                _resumeState.UnifiedSourceId = profileId;
                SaveResumeState();
                if (_localRoutingService is not null)
                {
                    result.Summary = DescribeLocalRoutingResult(updatedPlatforms, routingIssues, unifiedSource: true);
                }
            }
            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> ValidateSourceAsync(
        string profileId,
        CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            (ProfileStore store, TargetMode mode) = SelectSource(profileId);
            return await _switchService.ValidateProfileAsync(store, mode, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> ApplyRoutingAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
            IReadOnlyList<string> updatedPlatforms = Array.Empty<string>();
            IReadOnlyList<LocalSub2ApiRoutingIssue> routingIssues = Array.Empty<LocalSub2ApiRoutingIssue>();
            TargetMode mode = TargetMode.Mixed;
            if (_localRoutingService is not null)
            {
                LocalSub2ApiRoutingResult routingResult = await _localRoutingService
                    .ApplyRoutingAsync(store, cancellationToken)
                    .ConfigureAwait(false);
                store = routingResult.ClientStore;
                mode = TargetMode.Local;
                updatedPlatforms = routingResult.UpdatedPlatforms;
                routingIssues = routingResult.Issues;
            }

            OperationResult result = await _switchService.SwitchAsync(store, mode, cancellationToken).ConfigureAwait(false);
            if (result.Success)
            {
                _resumeState.BaseRoutingMode = ApplicationBaseRoutingMode.MixedRouting;
                _resumeState.UnifiedSourceId = string.Empty;
                SaveResumeState();
                if (_localRoutingService is not null)
                {
                    result.Summary = DescribeLocalRoutingResult(updatedPlatforms, routingIssues, unifiedSource: false);
                }
            }
            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<IReadOnlySet<string>> GetActiveBackupSourceIdsAsync(
        CancellationToken cancellationToken = default)
    {
        if (_localRoutingService is null)
        {
            return new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        }
        ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
        return await _localRoutingService
            .GetActiveBackupSourceIdsAsync(store, cancellationToken)
            .ConfigureAwait(false);
    }

    public async Task<OperationResult> ValidateRoutingAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
            return await _switchService.ValidateProfileAsync(store, TargetMode.Mixed, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public Task<OperationResult> RestoreLatestBackupAsync(CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return Task.FromResult(_switchService.RestoreLatestBackup());
    }

    public ClaudeGptRoutingStatus ReadClaudeGptRoutingStatus() =>
        _switchService.ReadClaudeGptRoutingStatus();

    public ClaudeGptModelMapping? ReadClaudeGptPreset(string profileId, string targetPlatform) =>
        _routingPresets.ReadClaudeGpt(BuildClaudeGptPresetKey(profileId, targetPlatform));

    public async Task<IReadOnlyList<string>> GetClaudeGptModelsAsync(
        string profileId,
        string targetPlatform,
        CancellationToken cancellationToken = default)
    {
        ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
        List<string> models = [];
        foreach (ClientProfile client in SelectClaudeOpenAiCompatibleProfiles(profile, targetPlatform))
        {
            try
            {
                models.AddRange(await _switchService.GetClaudeGptModelsAsync(client, cancellationToken).ConfigureAwait(false));
            }
            catch (Exception exception) when (exception is InvalidOperationException or HttpRequestException or TaskCanceledException)
            {
            }
        }

        return models
            .Where(model => !string.IsNullOrWhiteSpace(model))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderByDescending(model => model, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    public async Task<OperationResult> EnableClaudeGptRoutingAsync(
        string profileId,
        string targetPlatform,
        ClaudeGptModelMapping mapping,
        CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
            ClientProfile client = SelectClaudeOpenAiCompatibleProfile(profile, targetPlatform);
            OperationResult result = await _switchService.EnableClaudeGptRoutingAsync(
                ProfileSourceIds.LocalMachine,
                $"本机中转 · {SwitchService.NormalizeClaudeGptTarget(targetPlatform)}",
                SwitchService.NormalizeClaudeGptTarget(targetPlatform),
                client,
                mapping,
                cancellationToken,
                validateUpstream: false).ConfigureAwait(false);
            if (result.Success)
            {
                _routingPresets.SaveClaudeGpt(BuildClaudeGptPresetKey(ProfileSourceIds.LocalMachine, targetPlatform), mapping);
                _resumeState.ClaudeGptEnabled = true;
                _resumeState.ClaudeGptSourceId = ProfileSourceIds.LocalMachine;
                _resumeState.ClaudeGptTargetPlatform = SwitchService.NormalizeClaudeGptTarget(targetPlatform);
                _resumeState.ClaudeGptMapping = Clone(mapping);
                SaveResumeState();
            }

            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> DisableClaudeGptRoutingAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            cancellationToken.ThrowIfCancellationRequested();
            OperationResult result = _switchService.DisableClaudeGptRouting();
            if (result.Success)
            {
                _resumeState.ClaudeGptEnabled = false;
                SaveResumeState();
            }
            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public CodexClaudeRoutingStatus ReadCodexClaudeRoutingStatus() =>
        _switchService.ReadCodexClaudeRoutingStatus();

    public CodexClaudeModelMapping? ReadCodexClaudePreset(string profileId, string targetPlatform) =>
        _routingPresets.ReadCodexClaude(profileId, targetPlatform);

    public async Task<IReadOnlyList<string>> GetCodexClaudeModelsAsync(
        string profileId,
        string targetPlatform,
        CancellationToken cancellationToken = default)
    {
        ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
        List<string> models = [];
        foreach (ClientProfile client in SelectCodexCompatibleProfiles(profile, targetPlatform))
        {
            try
            {
                models.AddRange(await _switchService.GetCodexClaudeModelsAsync(client, cancellationToken).ConfigureAwait(false));
            }
            catch (Exception exception) when (exception is InvalidOperationException or HttpRequestException or TaskCanceledException)
            {
            }
        }

        return models
            .Where(model => !string.IsNullOrWhiteSpace(model))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderByDescending(model => model, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    public async Task<OperationResult> EnableCodexClaudeRoutingAsync(
        string profileId,
        CodexClaudeModelMapping mapping,
        CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
            OperationResult result = await _switchService.EnableCodexClaudeRoutingAsync(
                ProfileSourceIds.LocalMachine,
                "本机中转",
                SelectCodexCompatibleProfile(profile, mapping),
                mapping,
                cancellationToken,
                validateUpstream: false).ConfigureAwait(false);
            if (result.Success)
            {
                _routingPresets.SaveCodexClaude(ProfileSourceIds.LocalMachine, mapping);
                _resumeState.CodexClaudeEnabled = true;
                _resumeState.CodexClaudeSourceId = ProfileSourceIds.LocalMachine;
                _resumeState.CodexClaudeMapping = Clone(mapping);
                SaveResumeState();
            }

            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> DisableCodexClaudeRoutingAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            cancellationToken.ThrowIfCancellationRequested();
            OperationResult result = _switchService.DisableCodexClaudeRouting();
            if (result.Success)
            {
                _resumeState.CodexClaudeEnabled = false;
                SaveResumeState();
            }
            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> ResumeLastApplicationStateAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            ApplicationResumeState? persisted = _resumeStateStore.Load();
            var restoredParts = new List<string>();

            if (_localRoutingService is not null)
            {
                ProfileStore localStore = _profiles.LoadProfiles(persistNormalizedDocument: false);
                LocalSub2ApiRoutingResult routingResult = await _localRoutingService
                    .ApplyRoutingAsync(localStore, cancellationToken)
                    .ConfigureAwait(false);
                OperationResult localResult = await _switchService
                    .SwitchAsync(routingResult.ClientStore, TargetMode.Local, cancellationToken)
                    .ConfigureAwait(false);
                if (!localResult.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"同步本机中转调度失败：{localResult.Summary}",
                    };
                }
                restoredParts.Add("本机中转调度");
            }

            if (persisted is null)
            {
                return new OperationResult
                {
                    Success = true,
                    Summary = restoredParts.Count == 0
                        ? "没有需要自动恢复的上次工作状态。"
                        : "已同步本机中转调度。",
                };
            }

            _resumeState = persisted;

            if (_localRoutingService is null && persisted.BaseRoutingMode == ApplicationBaseRoutingMode.UnifiedSource)
            {
                (ProfileStore store, TargetMode mode) = SelectSource(persisted.UnifiedSourceId);
                OperationResult baseResult = await _switchService.SwitchAsync(store, mode, cancellationToken).ConfigureAwait(false);
                if (!baseResult.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"恢复上次来源失败：{baseResult.Summary}",
                    };
                }
                restoredParts.Add("统一来源");
            }
            else if (_localRoutingService is null && persisted.BaseRoutingMode == ApplicationBaseRoutingMode.MixedRouting)
            {
                ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
                TargetMode mode = TargetMode.Mixed;
                OperationResult baseResult = await _switchService.SwitchAsync(store, mode, cancellationToken).ConfigureAwait(false);
                if (!baseResult.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"恢复上次客户端分流失败：{baseResult.Summary}",
                    };
                }
                restoredParts.Add("客户端分流");
            }

            if (persisted.ClaudeGptEnabled)
            {
                ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
                ClientProfile client = SelectClaudeOpenAiCompatibleProfile(
                    profile,
                    persisted.ClaudeGptTargetPlatform);
                OperationResult claudeResult = await _switchService.EnableClaudeGptRoutingAsync(
                    ProfileSourceIds.LocalMachine,
                    $"本机中转 · {SwitchService.NormalizeClaudeGptTarget(persisted.ClaudeGptTargetPlatform)}",
                    SwitchService.NormalizeClaudeGptTarget(persisted.ClaudeGptTargetPlatform),
                    client,
                    persisted.ClaudeGptMapping,
                    cancellationToken,
                    validateUpstream: false).ConfigureAwait(false);
                if (!claudeResult.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"基础来源已恢复，但 Claude Code 模型路由恢复失败：{claudeResult.Summary}",
                    };
                }
                _resumeState.ClaudeGptSourceId = ProfileSourceIds.LocalMachine;
                restoredParts.Add($"Claude Code → {SwitchService.NormalizeClaudeGptTarget(persisted.ClaudeGptTargetPlatform)}");
            }

            if (persisted.CodexClaudeEnabled)
            {
                ProfileDefinition profile = await ResolveLocalRoutingProfileAsync(cancellationToken).ConfigureAwait(false);
                OperationResult codexResult = await _switchService.EnableCodexClaudeRoutingAsync(
                    ProfileSourceIds.LocalMachine,
                    "本机中转",
                    SelectCodexCompatibleProfile(profile, persisted.CodexClaudeMapping),
                    persisted.CodexClaudeMapping,
                    cancellationToken,
                    validateUpstream: false).ConfigureAwait(false);
                if (!codexResult.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"部分状态已恢复，但 Codex 模型路由恢复失败：{codexResult.Summary}",
                    };
                }
                _resumeState.CodexClaudeSourceId = ProfileSourceIds.LocalMachine;
                restoredParts.Add("Codex → Claude/Grok");
            }

            SaveResumeState();

            return new OperationResult
            {
                Success = true,
                Summary = restoredParts.Count == 0
                    ? "上次工作状态没有修改官方客户端配置。"
                    : $"已自动恢复上次工作状态：{string.Join("、", restoredParts)}。",
            };
        }
        catch (Exception exception) when (exception is ArgumentException or InvalidOperationException or
                                           IOException or UnauthorizedAccessException or JsonException)
        {
            return new OperationResult { Success = false, Summary = $"恢复上次工作状态失败：{exception.Message}" };
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> SaveApplicationStateAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            cancellationToken.ThrowIfCancellationRequested();
            ClaudeGptRoutingStatus claudeStatus = _switchService.ReadClaudeGptRoutingStatus();
            if (claudeStatus.Enabled)
            {
                _resumeState.ClaudeGptEnabled = true;
                _resumeState.ClaudeGptSourceId = claudeStatus.SourceId;
                _resumeState.ClaudeGptTargetPlatform = claudeStatus.TargetPlatform;
                _resumeState.ClaudeGptMapping = Clone(claudeStatus.Mapping);
            }

            CodexClaudeRoutingStatus codexStatus = _switchService.ReadCodexClaudeRoutingStatus();
            _resumeState.CodexClaudeEnabled = codexStatus.Enabled;
            if (codexStatus.Enabled)
            {
                _resumeState.CodexClaudeSourceId = codexStatus.SourceId;
                _resumeState.CodexClaudeMapping = Clone(codexStatus.Mapping);
            }

            SaveResumeState();
            return new OperationResult { Success = true, Summary = "退出前工作状态已保存。" };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            return new OperationResult { Success = false, Summary = $"保存退出前工作状态失败：{exception.Message}" };
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OperationResult> RestoreApplicationSessionAsync(CancellationToken cancellationToken = default)
    {
        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_applicationSessionRestored)
            {
                return new OperationResult { Success = true, Summary = "启动前客户端配置已经恢复。" };
            }

            cancellationToken.ThrowIfCancellationRequested();
            OperationResult result = await Task.Run(
                () => _switchService.RestoreApplicationSessionSnapshot(_applicationSessionSnapshot),
                cancellationToken).ConfigureAwait(false);
            if (result.Success)
            {
                _applicationSessionRestored = true;
            }
            return result;
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public string? GetDashboardUrl(string profileId)
    {
        (ProfileStore store, TargetMode mode) = SelectSource(profileId);
        if (string.Equals(profileId, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase))
        {
            ProfileDefinition? lan = store.LocalSources.FirstOrDefault(source =>
                string.Equals(source.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase));
            return string.IsNullOrWhiteSpace(lan?.DashboardUrl)
                ? null
                : lan.DashboardUrl.Trim();
        }

        return _switchService.GetSiteUrl(store, mode);
    }

    private static string DescribeLocalRoutingResult(
        IReadOnlyList<string> updatedPlatforms,
        IReadOnlyList<LocalSub2ApiRoutingIssue> issues,
        bool unifiedSource)
    {
        string applied = updatedPlatforms.Count == 0
            ? "客户端已固定连接本机中转；本机原生调度保持不变。"
            : unifiedSource
                ? $"客户端已固定连接本机中转；已将 {string.Join("、", updatedPlatforms)} 路由切换到所选来源。"
                : $"客户端已固定连接本机中转；已更新 {string.Join("、", updatedPlatforms)} 的本机路由。";
        if (issues.Count == 0)
        {
            return applied;
        }

        string unchanged = string.Join("；", issues.Select(issue =>
            $"{issue.Platform} 保持不变：{issue.Summary.Trim().TrimEnd('。')}"));
        return $"{applied} 未切换项：{unchanged}。";
    }

    internal (ProfileStore Store, TargetMode Mode) SelectSource(string profileId)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(profileId);

        ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
        ProfileDefinition? cloud = store.CloudSources.FirstOrDefault(source =>
            string.Equals(source.Id, profileId, StringComparison.OrdinalIgnoreCase));
        if (cloud is not null)
        {
            store.SelectedCloudSourceId = cloud.Id;
            store.Cloud = cloud;
            return (store, TargetMode.Cloud);
        }

        ProfileDefinition? local = store.LocalSources.FirstOrDefault(source =>
            string.Equals(source.Id, profileId, StringComparison.OrdinalIgnoreCase));
        if (local is not null)
        {
            store.SelectedLocalSourceId = local.Id;
            store.Local = local;
            store.Lan = store.LocalSources.FirstOrDefault(source =>
                string.Equals(source.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase))
                ?? store.Lan;
            return (store, TargetMode.Local);
        }

        throw new InvalidOperationException("该来源已经不存在，无法应用到官方客户端。");
    }

    private ProfileDefinition FindProfile(string profileId)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(profileId);
        ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
        return store.CloudSources.Concat(store.LocalSources).FirstOrDefault(source =>
                   string.Equals(source.Id, profileId, StringComparison.OrdinalIgnoreCase))
               ?? throw new InvalidOperationException("该来源已经不存在，无法配置跨客户端模型路由。");
    }

    private async Task<ProfileDefinition> ResolveLocalRoutingProfileAsync(CancellationToken cancellationToken)
    {
        ProfileStore store = _profiles.LoadProfiles(persistNormalizedDocument: false);
        if (_localRoutingService is not null)
        {
            LocalSub2ApiRoutingResult routingResult = await _localRoutingService
                .ApplyRoutingAsync(store, cancellationToken)
                .ConfigureAwait(false);
            ProfileDefinition effective = routingResult.ClientStore.Local;
            if (string.Equals(effective.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
            {
                return effective;
            }

            store = routingResult.ClientStore;
        }

        return store.LocalSources.FirstOrDefault(source =>
                   string.Equals(source.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase))
               ?? throw new InvalidOperationException("固定的本机中转配置不存在，无法配置跨客户端模型路由。");
    }

    internal static ClientProfile SelectCodexClaudeProfile(ProfileDefinition profile) =>
        !string.IsNullOrWhiteSpace(profile.Claude.BaseUrl) || !string.IsNullOrWhiteSpace(profile.Claude.Secret)
            ? profile.Claude
            : profile.Codex;

    internal static ClientProfile SelectClaudeOpenAiCompatibleProfile(ProfileDefinition profile, string targetPlatform)
    {
        bool wantsGrok = SwitchService.NormalizeClaudeGptTarget(targetPlatform) == "Grok";
        if (wantsGrok)
        {
            return IsConfigured(profile.Grok)
                ? profile.Grok
                : throw new InvalidOperationException("该来源没有配置 Grok 地址和密钥，不能启用 Claude Code 使用 Grok。");
        }

        if (IsConfigured(profile.Codex))
        {
            return profile.Codex;
        }

        throw new InvalidOperationException("该来源没有配置 GPT/Codex 地址和密钥，不能启用 Claude Code 使用 GPT。");
    }

    internal static ClientProfile SelectCodexCompatibleProfile(ProfileDefinition profile, CodexClaudeModelMapping mapping)
    {
        bool wantsGrok = SwitchService.NormalizeCodexClaudeTarget(mapping.TargetPlatform) == "Grok";

        if (wantsGrok && IsFullyConfigured(profile.Grok))
        {
            return profile.Grok;
        }

        if (!wantsGrok && IsFullyConfigured(profile.Claude))
        {
            return profile.Claude;
        }

        if (IsFullyConfigured(profile.Codex))
        {
            return profile.Codex;
        }

        string family = wantsGrok ? "Grok" : "Claude";
        throw new InvalidOperationException($"该来源没有可用于 {family} 模型的完整地址和密钥。");
    }

    private static IEnumerable<ClientProfile> SelectClaudeOpenAiCompatibleProfiles(ProfileDefinition profile, string targetPlatform)
    {
        if (SwitchService.NormalizeClaudeGptTarget(targetPlatform) == "Grok")
        {
            if (IsConfigured(profile.Grok)) yield return profile.Grok;
        }
        else if (IsConfigured(profile.Codex))
        {
            yield return profile.Codex;
        }
    }

    internal static string BuildClaudeGptPresetKey(string profileId, string targetPlatform) =>
        $"{profileId.Trim()}::{SwitchService.NormalizeClaudeGptTarget(targetPlatform)}";

    private static IEnumerable<ClientProfile> SelectCodexCompatibleProfiles(
        ProfileDefinition profile,
        string targetPlatform)
    {
        bool wantsGrok = SwitchService.NormalizeCodexClaudeTarget(targetPlatform) == "Grok";
        ClientProfile target = wantsGrok ? profile.Grok : profile.Claude;
        if (IsConfigured(target))
        {
            yield return target;
        }
        else if (IsConfigured(profile.Codex))
        {
            yield return profile.Codex;
        }
    }

    private static bool IsConfigured(ClientProfile profile) =>
        !string.IsNullOrWhiteSpace(profile.BaseUrl) || !string.IsNullOrWhiteSpace(profile.Secret);

    private static bool IsFullyConfigured(ClientProfile profile) =>
        !string.IsNullOrWhiteSpace(profile.BaseUrl) && !string.IsNullOrWhiteSpace(profile.Secret);

    private static bool IsClaudeModelName(string model) =>
        model.StartsWith("claude-", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("anthropic/claude", StringComparison.OrdinalIgnoreCase);

    private static bool IsGrokModelName(string model) =>
        model.StartsWith("grok-", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("x-ai/grok", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("xai/grok", StringComparison.OrdinalIgnoreCase);

    private void SaveResumeState() => _resumeStateStore.Save(_resumeState);

    public void Dispose()
    {
        _ownedLocalRoutingService?.Dispose();
        _operationGate.Dispose();
    }

    private static ClaudeGptModelMapping Clone(ClaudeGptModelMapping mapping) => new()
    {
        OpusModel = mapping.OpusModel,
        SonnetModel = mapping.SonnetModel,
        HaikuModel = mapping.HaikuModel,
    };

    private static CodexClaudeModelMapping Clone(CodexClaudeModelMapping mapping) => new()
    {
        TargetPlatform = SwitchService.NormalizeCodexClaudeTarget(mapping.TargetPlatform),
        DefaultModel = mapping.DefaultModel,
        ReviewModel = mapping.ReviewModel,
        ReasoningEffort = mapping.ReasoningEffort,
    };
}
