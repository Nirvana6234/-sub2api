using System.ComponentModel;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace AiSwitchGui;

internal sealed class SwitchService
{
    private const int ValidationAttemptCount = 3;
    private const int SecEWrongPrincipal = unchecked((int)0x80090322);
    private static readonly TimeSpan[] ValidationRetryDelays =
    [
        TimeSpan.FromMilliseconds(200),
        TimeSpan.FromMilliseconds(600)
    ];
    public const string DefaultCodexBaseUrl = "https://api.openai.com/v1";
    public const string DefaultClaudeBaseUrl = "https://api.anthropic.com";
    public const string DefaultGeminiBaseUrl = "https://generativelanguage.googleapis.com";
    public const string DefaultGrokBaseUrl = "https://api.x.ai/v1";
    public const string DefaultGeminiModel = "gemini-2.0-flash";
    public const string DefaultGrokModel = "grok-latest";
    private static readonly string[] ClaudeEnvironmentVariables =
    [
        "ANTHROPIC_BASE_URL",
        "ANTHROPIC_AUTH_TOKEN",
        "ANTHROPIC_API_KEY",
        "ANTHROPIC_MODEL",
        "ANTHROPIC_DEFAULT_OPUS_MODEL",
        "ANTHROPIC_DEFAULT_SONNET_MODEL",
        "ANTHROPIC_DEFAULT_HAIKU_MODEL",
        "ANTHROPIC_SMALL_FAST_MODEL"
    ];
    private static readonly byte[] ClaudeGptStateEntropy = SHA256.HashData(
        Encoding.UTF8.GetBytes("LanAi.Workspace/ClaudeGptRouting/v1"));
    private static readonly byte[] CodexClaudeStateEntropy = SHA256.HashData(
        Encoding.UTF8.GetBytes("LanAi.Workspace/CodexClaudeRouting/v1"));
    private static readonly byte[] ApplicationSessionEntropy = SHA256.HashData(
        Encoding.UTF8.GetBytes("LanAi.Workspace/ApplicationClientSession/v1"));
    private static readonly string[] GeminiEnvironmentVariables =
    [
        "GOOGLE_GEMINI_BASE_URL",
        "GEMINI_API_KEY",
        "GOOGLE_API_KEY",
        "GEMINI_MODEL"
    ];
    private static readonly string[] GrokEnvironmentVariables =
    [
        "GROK_MODELS_BASE_URL",
        "XAI_API_KEY",
        "OPENAI_BASE_URL",
        "OPENAI_API_KEY"
    ];
    private static readonly IntPtr HwndBroadcast = new(0xffff);
    private const int WmSettingChange = 0x001A;
    private const int SmtoAbortIfHung = 0x0002;

    private readonly ConfigPaths _paths;
    private readonly HttpClient _httpClient;
    private readonly HttpClient _bridgeHttpClient;
    private readonly ProfileRepository _repository;
    private readonly bool _writeUserEnvironment;
    private ClaudeGptBridgeServer? _claudeGptBridge;

    public SwitchService(ConfigPaths paths, ProfileRepository repository)
        : this(
            paths,
            repository,
            new HttpClient { Timeout = TimeSpan.FromSeconds(30) },
            new HttpClient { Timeout = TimeSpan.FromMinutes(5) },
            writeUserEnvironment: true)
    {
    }

    internal SwitchService(
        ConfigPaths paths,
        ProfileRepository repository,
        HttpClient httpClient,
        bool writeUserEnvironment = true)
        : this(paths, repository, httpClient, httpClient, writeUserEnvironment)
    {
    }

    private SwitchService(
        ConfigPaths paths,
        ProfileRepository repository,
        HttpClient httpClient,
        HttpClient bridgeHttpClient,
        bool writeUserEnvironment)
    {
        _paths = paths;
        _repository = repository;
        _httpClient = httpClient;
        _bridgeHttpClient = bridgeHttpClient;
        _writeUserEnvironment = writeUserEnvironment;
    }

    public ClaudeGptRoutingStatus ReadClaudeGptRoutingStatus()
    {
        ClaudeGptRoutingState? state = LoadClaudeGptRoutingState();
        return state is null
            ? new ClaudeGptRoutingStatus()
            : new ClaudeGptRoutingStatus
            {
                Enabled = IsClaudeGptBridgeActive(),
                SourceId = state.SourceId,
                SourceName = state.SourceName,
                TargetPlatform = state.TargetPlatform,
                Mapping = state.GetMapping(),
            };
    }

    public async Task<IReadOnlyList<string>> GetClaudeGptModelsAsync(
        ClientProfile profile,
        CancellationToken cancellationToken)
    {
        ClientProfile effective = RequireClaudeGptProfile(profile);
        string modelsUrl = BuildModelsUrl(effective.BaseUrl);
        using var request = new HttpRequestMessage(HttpMethod.Get, modelsUrl);
        AddGatewayAuthorization(request, effective.Secret);
        using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException($"读取模型失败：{(int)response.StatusCode} {response.StatusCode}");
        }

        await using Stream stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using JsonDocument document = await JsonDocument.ParseAsync(stream, cancellationToken: cancellationToken).ConfigureAwait(false);
        IEnumerable<string> models = ReadModelNames(document.RootElement)
            .Where(IsOpenAiOrGrokModelName)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderByDescending(name => name, StringComparer.OrdinalIgnoreCase);
        return models.ToArray();
    }

    public async Task<OperationResult> EnableClaudeGptRoutingAsync(
        string sourceId,
        string sourceName,
        string targetPlatform,
        ClientProfile profile,
        ClaudeGptModelMapping mapping,
        CancellationToken cancellationToken,
        bool validateUpstream = true)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(sourceId);
        ArgumentException.ThrowIfNullOrWhiteSpace(sourceName);
        ArgumentNullException.ThrowIfNull(mapping);
        if (!mapping.IsComplete)
        {
            throw new InvalidOperationException("请完整设置 Opus、Sonnet 和 Haiku 三项 GPT/Grok 模型映射。");
        }
        ClientProfile effective = RequireClaudeGptProfile(profile);
        var normalizedMapping = new ClaudeGptModelMapping
        {
            OpusModel = mapping.OpusModel.Trim(),
            SonnetModel = mapping.SonnetModel.Trim(),
            HaikuModel = mapping.HaikuModel.Trim(),
        };

        OperationResult preflight = validateUpstream
            ? await PreflightClaudeGptRoutingAsync(effective, normalizedMapping, cancellationToken).ConfigureAwait(false)
            : new OperationResult { Success = true, Summary = string.Empty };
        if (!preflight.Success)
        {
            return preflight;
        }

        ClaudeGptRoutingState? persistedState = LoadClaudeGptRoutingState();
        ClaudeGptRoutingState rollbackState = CaptureClaudeGptRoutingState(
            persistedState?.SourceId ?? sourceId,
            persistedState?.SourceName ?? sourceName,
            persistedState?.TargetPlatform ?? targetPlatform,
            persistedState?.GetMapping() ?? normalizedMapping);
        ClaudeGptRoutingState originalState = persistedState is null
            ? CaptureClaudeGptRoutingState(sourceId, sourceName, targetPlatform, normalizedMapping)
            : persistedState.WithSelection(sourceId, sourceName, targetPlatform, normalizedMapping);

        try
        {
            SaveClaudeGptRoutingState(originalState);
            WriteClaudeGptConfiguration(effective, normalizedMapping, sourceId, sourceName, targetPlatform);
            return new OperationResult
            {
                Success = true,
                Summary = $"Claude Code 已通过“{sourceName.Trim()}”启用 {NormalizeClaudeGptTarget(targetPlatform)} 模型映射。{preflight.Summary}原配置已加密备份。"
            };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException)
        {
            StopClaudeGptBridge();
            RestoreClaudeGptSnapshot(rollbackState);
            if (persistedState is null)
            {
                TryDeleteClaudeGptRoutingState();
            }
            else
            {
                SaveClaudeGptRoutingState(persistedState);
            }

            return new OperationResult
            {
                Success = false,
                Summary = $"Claude Code 配置写入失败：{exception.Message}"
            };
        }
    }

    public Task<OperationResult> EnableClaudeGptRoutingAsync(
        string sourceId,
        string sourceName,
        ClientProfile profile,
        ClaudeGptModelMapping mapping,
        CancellationToken cancellationToken) =>
        EnableClaudeGptRoutingAsync(
            sourceId,
            sourceName,
            mapping.DistinctModels().Any(IsGrokModelName) ? "Grok" : "GPT",
            profile,
            mapping,
            cancellationToken);

    public OperationResult DisableClaudeGptRouting()
    {
        ClaudeGptRoutingState? state = LoadClaudeGptRoutingState();
        if (state is null)
        {
            StopClaudeGptBridge();
            return new OperationResult { Success = true, Summary = "Claude Code 当前未启用 GPT/Grok 路由。" };
        }

        try
        {
            StopClaudeGptBridge();
            RestoreClaudeGptSnapshot(state);
            if (!TryDeleteClaudeGptRoutingState())
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = "原 Claude Code 配置已恢复，但加密状态文件未能删除；请重新打开工作台后再次停用。"
                };
            }

            return new OperationResult { Success = true, Summary = "已停用 GPT/Grok 路由并恢复原 Claude Code 配置。" };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException)
        {
            return new OperationResult { Success = false, Summary = $"恢复 Claude Code 原配置失败：{exception.Message}" };
        }
    }

    public CodexClaudeRoutingStatus ReadCodexClaudeRoutingStatus()
    {
        CodexClaudeRoutingState? state = LoadProtectedSnapshot<CodexClaudeRoutingState>(
            _paths.CodexClaudeRoutingStatePath,
            CodexClaudeStateEntropy);
        return state is null || state.Version != 1
            ? new CodexClaudeRoutingStatus()
            : new CodexClaudeRoutingStatus
            {
                Enabled = true,
                SourceId = state.SourceId,
                SourceName = state.SourceName,
                Mapping = state.GetMapping(),
            };
    }

    public async Task<IReadOnlyList<string>> GetCodexClaudeModelsAsync(
        ClientProfile profile,
        CancellationToken cancellationToken)
    {
        ClientProfile effective = RequireCodexClaudeProfile(profile);
        string modelsUrl = BuildModelsUrl(effective.BaseUrl);
        using var request = new HttpRequestMessage(HttpMethod.Get, modelsUrl);
        AddGatewayAuthorization(request, effective.Secret);
        using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException($"读取模型失败：{(int)response.StatusCode} {response.StatusCode}");
        }

        await using Stream stream = await response.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using JsonDocument document = await JsonDocument.ParseAsync(stream, cancellationToken: cancellationToken).ConfigureAwait(false);
        return ReadModelNames(document.RootElement)
            .Where(IsClaudeOrGrokModelName)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderByDescending(name => name, StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    public async Task<OperationResult> EnableCodexClaudeRoutingAsync(
        string sourceId,
        string sourceName,
        ClientProfile profile,
        CodexClaudeModelMapping mapping,
        CancellationToken cancellationToken,
        bool validateUpstream = true)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(sourceId);
        ArgumentException.ThrowIfNullOrWhiteSpace(sourceName);
        ArgumentNullException.ThrowIfNull(mapping);
        if (!mapping.IsComplete)
        {
            throw new InvalidOperationException("请完整设置 Codex 默认模型和代码审查模型。");
        }

        ClientProfile effective = RequireCodexClaudeProfile(profile);
        var normalizedMapping = new CodexClaudeModelMapping
        {
            TargetPlatform = NormalizeCodexClaudeTarget(mapping.TargetPlatform),
            DefaultModel = mapping.DefaultModel.Trim(),
            ReviewModel = mapping.ReviewModel.Trim(),
            ReasoningEffort = mapping.ReasoningEffort.Trim().ToLowerInvariant(),
        };
        OperationResult preflight = validateUpstream
            ? await PreflightCodexClaudeRoutingAsync(
                effective,
                normalizedMapping,
                cancellationToken).ConfigureAwait(false)
            : new OperationResult { Success = true, Summary = string.Empty };
        if (!preflight.Success)
        {
            return preflight;
        }

        CodexClaudeRoutingState? persistedState = LoadProtectedSnapshot<CodexClaudeRoutingState>(
            _paths.CodexClaudeRoutingStatePath,
            CodexClaudeStateEntropy);
        if (persistedState?.Version != 1)
        {
            persistedState = null;
        }
        CodexClaudeRoutingState rollbackState = CaptureCodexClaudeRoutingState(
            persistedState?.SourceId ?? sourceId,
            persistedState?.SourceName ?? sourceName,
            persistedState?.GetMapping() ?? normalizedMapping);
        CodexClaudeRoutingState originalState = persistedState is null
            ? CaptureCodexClaudeRoutingState(sourceId, sourceName, normalizedMapping)
            : persistedState.WithSelection(sourceId, sourceName, normalizedMapping);

        try
        {
            SaveProtectedSnapshot(_paths.CodexClaudeRoutingStatePath, originalState, CodexClaudeStateEntropy);
            WriteCodexClaudeConfiguration(effective, normalizedMapping);
            return new OperationResult
            {
                Success = true,
                Summary = $"Codex 已通过“{sourceName.Trim()}”启用 {normalizedMapping.TargetPlatform} 模型路由。{preflight.Summary}原配置已加密备份。"
            };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException)
        {
            RestoreCodexClaudeSnapshot(rollbackState);
            if (persistedState is null)
            {
                TryDeleteCodexClaudeRoutingState();
            }
            else
            {
                SaveProtectedSnapshot(_paths.CodexClaudeRoutingStatePath, persistedState, CodexClaudeStateEntropy);
            }

            return new OperationResult
            {
                Success = false,
                Summary = $"Codex 配置写入失败：{exception.Message}"
            };
        }
    }

    public OperationResult DisableCodexClaudeRouting()
    {
        CodexClaudeRoutingState? state = LoadProtectedSnapshot<CodexClaudeRoutingState>(
            _paths.CodexClaudeRoutingStatePath,
            CodexClaudeStateEntropy);
        if (state is null || state.Version != 1)
        {
            return new OperationResult { Success = true, Summary = "Codex 当前未启用 Claude/Grok 路由。" };
        }

        try
        {
            RestoreCodexClaudeSnapshot(state);
            if (!TryDeleteCodexClaudeRoutingState())
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = "原 Codex 配置已恢复，但加密状态文件未能删除；请重新打开工作台后再次停用。"
                };
            }
            return new OperationResult { Success = true, Summary = "已停用 Claude/Grok 路由并恢复原 Codex 配置。" };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException)
        {
            return new OperationResult { Success = false, Summary = $"恢复 Codex 原配置失败：{exception.Message}" };
        }
    }

    public LiveStatus ReadLiveStatus(ProfileStore store)
    {
        var codexProfile = ReadCodexClientProfile();
        var claudeProfile = ReadClaudeClientProfile();
        var geminiProfile = ReadGeminiClientProfile();
        var status = new LiveStatus
        {
            CodexConfigPresent = File.Exists(_paths.CodexConfigPath) && File.Exists(_paths.CodexAuthPath),
            ClaudeConfigPresent = File.Exists(_paths.ClaudeSettingsPath),
            GeminiConfigPresent = File.Exists(_paths.GeminiSettingsPath) || GeminiEnvironmentVariables.Any(name =>
                !string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.User))),
            CodexBaseUrl = codexProfile?.BaseUrl ?? "<missing>",
            ClaudeBaseUrl = claudeProfile?.BaseUrl ?? "<missing>",
            GeminiBaseUrl = geminiProfile?.BaseUrl ?? "<missing>",
            GrokBaseUrl = ReadGrokClientProfile()?.BaseUrl ?? "<missing>",
            MixedCodexSource = store.Mixed.CodexSource,
            MixedClaudeSource = store.Mixed.ClaudeSource,
            MixedGeminiSource = store.Mixed.GeminiSource,
            MixedGrokSource = store.Mixed.GrokSource,
            MixedCodexSourceId = store.Mixed.CodexSourceId,
            MixedClaudeSourceId = store.Mixed.ClaudeSourceId,
            MixedGeminiSourceId = store.Mixed.GeminiSourceId,
            MixedGrokSourceId = store.Mixed.GrokSourceId
        };

        PopulateStatusClassification(store, status);
        return status;
    }

    public ImportedLiveConfig ReadCurrentClientConfig()
    {
        return new ImportedLiveConfig
        {
            Codex = ReadCodexClientProfile(),
            Claude = ReadClaudeClientProfile(),
            Gemini = ReadGeminiClientProfile(),
            Grok = ReadGrokClientProfile()
        };
    }

    public SessionConfigSnapshot CreateSessionSnapshot()
    {
        var snapshot = new SessionConfigSnapshot();
        snapshot.Files.Add(ReadSnapshotFile(_paths.CodexConfigPath));
        snapshot.Files.Add(ReadSnapshotFile(_paths.CodexAuthPath));
        snapshot.Files.Add(ReadSnapshotFile(_paths.ClaudeSettingsPath));
        snapshot.Files.Add(ReadSnapshotFile(_paths.GeminiSettingsPath));
        snapshot.Files.Add(ReadSnapshotFile(_paths.GrokConfigPath));
        snapshot.Files.Add(ReadSnapshotFile(_paths.VsCodeUserSettingsPath));
        foreach (var name in ClaudeEnvironmentVariables)
        {
            snapshot.EnvironmentVariables.Add(ReadEnvironmentVariableSnapshot(name));
        }

        foreach (var name in GeminiEnvironmentVariables)
        {
            snapshot.EnvironmentVariables.Add(ReadEnvironmentVariableSnapshot(name));
        }

        return snapshot;
    }

    public void RestoreSessionSnapshot(SessionConfigSnapshot snapshot)
    {
        foreach (var file in snapshot.Files)
        {
            if (file.Existed)
            {
                JsonFile.WriteText(file.Path, file.Content);
                continue;
            }

            if (File.Exists(file.Path))
            {
                File.Delete(file.Path);
            }
        }

        var anyUserEnvChanged = false;
        foreach (var variable in snapshot.EnvironmentVariables)
        {
            var targetValue = variable.Existed ? variable.Value : null;

            // 进程级恢复很便宜，也不会广播，直接做。
            Environment.SetEnvironmentVariable(
                variable.Name,
                targetValue,
                EnvironmentVariableTarget.Process);

            // 写 User 级环境变量会触发系统级 WM_SETTINGCHANGE 广播，
            // 关闭时若叠加多个变量很容易让 UI 卡死。仅在值确实变化时才写，
            // 没变化（绝大多数情况）就跳过，关闭即可秒退。
            var currentUserValue = Environment.GetEnvironmentVariable(
                variable.Name,
                EnvironmentVariableTarget.User);
            if (!string.Equals(currentUserValue, targetValue, StringComparison.Ordinal))
            {
                Environment.SetEnvironmentVariable(
                    variable.Name,
                    targetValue,
                    EnvironmentVariableTarget.User);
                anyUserEnvChanged = true;
            }
        }

        if (anyUserEnvChanged)
        {
            BroadcastEnvironmentChange();
        }
    }

    public static void SetUserEnvironmentVariable(string name, string? value)
    {
        if (string.Equals(name, ClaudeEnvironmentVariables[0], StringComparison.OrdinalIgnoreCase))
        {
            value = null;
        }

        Environment.SetEnvironmentVariable(
            name,
            string.IsNullOrWhiteSpace(value) ? null : value.Trim(),
            EnvironmentVariableTarget.Process);
        Environment.SetEnvironmentVariable(
            name,
            string.IsNullOrWhiteSpace(value) ? null : value.Trim(),
            EnvironmentVariableTarget.User);
        BroadcastEnvironmentChange();
    }

    public string? GetSiteUrl(ProfileStore store, TargetMode mode)
    {
        var profile = ResolveProfile(store, mode);
        var candidate = !string.IsNullOrWhiteSpace(profile.Claude.BaseUrl)
            ? profile.Claude.BaseUrl
            : !string.IsNullOrWhiteSpace(profile.Gemini.BaseUrl)
                ? profile.Gemini.BaseUrl
                : profile.Codex.BaseUrl;

        if (string.IsNullOrWhiteSpace(candidate) || !Uri.TryCreate(candidate, UriKind.Absolute, out var uri))
        {
            return null;
        }

        return new UriBuilder(uri.Scheme, uri.Host, uri.Port).Uri.ToString().TrimEnd('/');
    }

    public Task<OperationResult> SaveOnlyAsync(ProfileStore store, CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        return Task.FromResult(new OperationResult { Success = true, Summary = "配置已保存到 profiles.json，当前生效配置未切换。" });
    }

    public Task<OperationResult> SwitchAsync(ProfileStore store, TargetMode mode, CancellationToken cancellationToken)
    {
        // 文件备份、用户环境变量和系统通知都可能被杀毒软件、注册表或某个
        // 无响应的桌面程序拖慢。调用方是 WinForms UI，必须完整放到线程池中，
        // 否则按钮会表现为“整个窗口卡死”。
        return Task.Run(async () =>
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (LoadClaudeGptRoutingState() is not null)
            {
                OperationResult disabled = DisableClaudeGptRouting();
                if (!disabled.Success)
                {
                    return new OperationResult
                    {
                        Success = false,
                        Summary = $"无法切换普通来源：{disabled.Summary}"
                    };
                }
            }
            var profile = ResolveProfile(store, mode);
            ValidateProfile(profile.Name, profile);
            string grokModel = await ResolveGrokModelAsync(profile.Grok, cancellationToken).ConfigureAwait(false);

            var backupFolder = BackupCurrentFiles();
            WriteCodexFiles(profile.Codex);
            WriteClaudeFile(profile.Claude);
            WriteGeminiConfig(profile.Gemini);
            WriteGrokConfig(profile.Grok, grokModel);
            _repository.TrimOldBackups();

            return new OperationResult
            {
                Success = true,
                Summary = $"已切换到 {profile.Name}，当前备份 {backupFolder}。需要连通性检查时请点击“测试当前模式”。"
            };
        }, cancellationToken);
    }

    public async Task<OperationResult> ValidateProfileAsync(ProfileStore store, TargetMode mode, CancellationToken cancellationToken)
    {
        var profile = ResolveProfile(store, mode);
        ValidateProfile(profile.Name, profile);

        var result = new OperationResult();
        var validationDetails = await ValidateClientsAsync(profile, cancellationToken);
        result.Details.AddRange(validationDetails);
        result.Success = result.Details.All(x => x.Success);
        result.Summary = result.Success
            ? $"{profile.Name} 验证成功，当前主配置未切换。"
            : $"{profile.Name} 验证失败：{string.Join("；", result.Details.Where(detail => !detail.Success).Select(detail => $"{detail.Name} {detail.Message}"))}。当前主配置未切换。";
        return result;
    }

    public OperationResult RestoreLatestBackup()
    {
        if (LoadClaudeGptRoutingState() is not null)
        {
            OperationResult disabled = DisableClaudeGptRouting();
            if (!disabled.Success)
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = $"无法恢复客户端备份：{disabled.Summary}"
                };
            }
        }

        var latest = new[] { _paths.BackupRoot, _paths.FallbackBackupRoot }
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .Where(Directory.Exists)
            .SelectMany(path => new DirectoryInfo(path).GetDirectories())
            .OrderByDescending(x => x.Name)
            .FirstOrDefault();
        if (latest is null)
        {
            return new OperationResult { Success = false, Summary = "没有找到可恢复的备份。" };
        }

        RestoreFile(latest.FullName, "codex-config.toml", _paths.CodexConfigPath);
        RestoreFile(latest.FullName, "codex-auth.json", _paths.CodexAuthPath);
        RestoreFile(latest.FullName, "claude-settings.json", _paths.ClaudeSettingsPath);
        RestoreFile(latest.FullName, "gemini-settings.json", _paths.GeminiSettingsPath);
        RestoreFile(latest.FullName, "grok-config.toml", _paths.GrokConfigPath);
        RestoreFile(latest.FullName, "vscode-settings.json", _paths.VsCodeUserSettingsPath);

        return new OperationResult { Success = true, Summary = $"已恢复最近备份 {latest.FullName}" };
    }

    private static void ValidateProfile(string name, ProfileDefinition profile)
    {
        // A blank field deliberately means "leave the current client setting
        // unchanged".  This lets a source configure only the clients it owns.
        _ = name;
        _ = profile;
    }

    private static ProfileDefinition ResolveProfile(ProfileStore store, TargetMode mode)
    {
        return mode switch
        {
            TargetMode.Cloud => store.Cloud,
            TargetMode.Local => store.Local,
            TargetMode.Mixed => new ProfileDefinition
            {
                Name = "来源指定",
                Notes = "Codex/Claude 使用不同来源。",
                Codex = CloneClient(ResolveClient(store, store.Mixed.CodexSourceId, isCodex: true)),
                Claude = CloneClient(ResolveClient(store, store.Mixed.ClaudeSourceId, isCodex: false)),
                Gemini = CloneClient(ResolveGeminiClient(store, store.Mixed.GeminiSourceId)),
                Grok = CloneClient(ResolveGrokClient(store, store.Mixed.GrokSourceId))
            },
            _ => throw new ArgumentOutOfRangeException(nameof(mode), mode, null)
        };
    }

    private static SessionFileSnapshot ReadSnapshotFile(string path)
    {
        return new SessionFileSnapshot
        {
            Path = path,
            Existed = File.Exists(path),
            Content = File.Exists(path) ? JsonFile.ReadText(path) : string.Empty
        };
    }

    private static SessionEnvironmentVariableSnapshot ReadEnvironmentVariableSnapshot(string name)
    {
        var value = Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.User);
        return new SessionEnvironmentVariableSnapshot
        {
            Name = name,
            Existed = value is not null,
            Value = value ?? string.Empty
        };
    }

    private static void BroadcastEnvironmentChange()
    {
        // WM_SETTINGCHANGE 不需要等待接收方完成处理。以前使用
        // SendMessageTimeout(HWND_BROADCAST, ..., 5000)，桌面上有无响应窗口时
        // 会让一次切换等待很久。SendNotifyMessage 保留通知语义，但立即返回。
        _ = SendNotifyMessage(HwndBroadcast, WmSettingChange, UIntPtr.Zero, "Environment");
    }

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
    private static extern bool SendNotifyMessage(
        IntPtr hWnd,
        int msg,
        UIntPtr wParam,
        string lParam);

    private static ClientProfile ResolveClient(ProfileStore store, ClientSourceMode sourceMode, bool isCodex)
    {
        var profile = sourceMode == ClientSourceMode.Cloud ? store.Cloud : store.Local;
        return isCodex ? profile.Codex : profile.Claude;
    }

    private static ClientProfile ResolveClient(ProfileStore store, string sourceId, bool isCodex)
    {
        var profile = ResolveProfileSource(store, sourceId);
        return isCodex ? profile.Codex : profile.Claude;
    }

    private static ClientProfile ResolveGeminiClient(ProfileStore store, string sourceId)
    {
        return ResolveProfileSource(store, sourceId).Gemini;
    }

    private static ClientProfile ResolveGrokClient(ProfileStore store, string sourceId)
    {
        return ResolveProfileSource(store, sourceId).Grok;
    }

    private static ProfileDefinition ResolveProfileSource(ProfileStore store, string sourceId)
    {
        var cloudSource = store.CloudSources.FirstOrDefault(x => string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase));
        if (cloudSource is not null) return cloudSource;

        return store.LocalSources.FirstOrDefault(x => string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase))
               ?? store.Local;
    }

    private static ClientProfile CloneClient(ClientProfile profile)
    {
        return new ClientProfile { BaseUrl = profile.BaseUrl, Secret = profile.Secret };
    }

    private string BackupCurrentFiles()
    {
        try
        {
            return BackupCurrentFiles(_paths.BackupRoot);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return BackupCurrentFiles(_paths.FallbackBackupRoot);
        }
    }

    private string BackupCurrentFiles(string backupRoot)
    {
        Directory.CreateDirectory(backupRoot);
        var folder = Path.Combine(backupRoot, DateTime.Now.ToString("yyyyMMdd-HHmmss"));
        Directory.CreateDirectory(folder);

        CopyIfExists(_paths.CodexConfigPath, Path.Combine(folder, "codex-config.toml"));
        CopyIfExists(_paths.CodexAuthPath, Path.Combine(folder, "codex-auth.json"));
        CopyIfExists(_paths.ClaudeSettingsPath, Path.Combine(folder, "claude-settings.json"));
        CopyIfExists(_paths.GeminiSettingsPath, Path.Combine(folder, "gemini-settings.json"));
        CopyIfExists(_paths.GrokConfigPath, Path.Combine(folder, "grok-config.toml"));
        CopyIfExists(_paths.VsCodeUserSettingsPath, Path.Combine(folder, "vscode-settings.json"));
        return folder;
    }

    private static void CopyIfExists(string source, string destination)
    {
        var directory = Path.GetDirectoryName(destination);
        if (!string.IsNullOrWhiteSpace(directory))
        {
            Directory.CreateDirectory(directory);
        }

        if (File.Exists(source))
        {
            File.Copy(source, destination, overwrite: true);
        }
    }

    private static void RestoreFile(string folder, string backupName, string targetPath)
    {
        var source = Path.Combine(folder, backupName);
        if (!File.Exists(source))
        {
            return;
        }

        JsonFile.WriteText(targetPath, JsonFile.ReadText(source));
    }

    private void WriteCodexFiles(ClientProfile profile)
    {
        var baseUrl = profile.BaseUrl.Trim();
        var secret = profile.Secret.Trim();
        if (string.IsNullOrWhiteSpace(baseUrl) && string.IsNullOrWhiteSpace(secret)) return;

        if (!string.IsNullOrWhiteSpace(baseUrl))
        {
            JsonFile.WriteText(_paths.CodexConfigPath, BuildCodexConfigToml(profile));
        }

        if (!string.IsNullOrWhiteSpace(secret))
        {
            var auth = new Dictionary<string, string>();
            if (File.Exists(_paths.CodexAuthPath))
            {
                try { auth = JsonSerializer.Deserialize<Dictionary<string, string>>(JsonFile.ReadText(_paths.CodexAuthPath)) ?? auth; } catch { }
            }
            auth["OPENAI_API_KEY"] = secret;
            JsonFile.Write(_paths.CodexAuthPath, auth);
        }
    }

    private string BuildCodexConfigToml(ClientProfile profile)
    {
        IReadOnlyDictionary<string, string> current = ReadCurrentCodexTopLevelSettings();
        string model = ReadCodexStringSetting(current, "model", "gpt-5.6-sol");
        string reviewModel = ReadCodexStringSetting(current, "review_model", model);
        string reasoningEffort = ReadCodexStringSetting(current, "model_reasoning_effort", "high");
        int contextWindow = ReadCodexPositiveIntSetting(current, "model_context_window", 1_000_000);
        int autoCompactTokenLimit = ReadCodexPositiveIntSetting(
            current,
            "model_auto_compact_token_limit",
            Math.Min(900_000, contextWindow));

        return BuildCodexConfigToml(
            profile,
            model,
            reviewModel,
            reasoningEffort,
            contextWindow,
            Math.Min(autoCompactTokenLimit, contextWindow));
    }

    private string BuildCodexConfigToml(
        ClientProfile profile,
        string model,
        string reviewModel,
        string reasoningEffort,
        int contextWindow,
        int autoCompactTokenLimit)
    {
        var managed = string.Join(
            Environment.NewLine,
            [
                "model_provider = \"sub2api\"",
                $"model = \"{EscapeTomlValue(model)}\"",
                $"review_model = \"{EscapeTomlValue(reviewModel)}\"",
                $"model_reasoning_effort = \"{EscapeTomlValue(reasoningEffort)}\"",
                "disable_response_storage = true",
                "network_access = \"enabled\"",
                "windows_wsl_setup_acknowledged = true",
                $"model_context_window = {contextWindow}",
                $"model_auto_compact_token_limit = {autoCompactTokenLimit}",
                string.Empty,
                "[model_providers.sub2api]",
                "name = \"sub2api\"",
                $"base_url = \"{EscapeTomlValue(NormalizeOpenAiApiBaseUrl(profile.BaseUrl))}\"",
                "wire_api = \"responses\"",
                "requires_openai_auth = true"
            ]);

        var preserved = ReadPreservedCodexSections();
        return string.IsNullOrWhiteSpace(preserved)
            ? managed + Environment.NewLine
            : managed + Environment.NewLine + Environment.NewLine + preserved.Trim() + Environment.NewLine;
    }

    private static string EscapeTomlValue(string value) =>
        value.Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal);

    private IReadOnlyDictionary<string, string> ReadCurrentCodexTopLevelSettings()
    {
        var settings = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (!File.Exists(_paths.CodexConfigPath))
        {
            return settings;
        }

        foreach (string line in JsonFile.ReadText(_paths.CodexConfigPath)
                     .Replace("\r\n", "\n", StringComparison.Ordinal)
                     .Replace('\r', '\n')
                     .Split('\n'))
        {
            string trimmed = line.Trim();
            if (trimmed.StartsWith("[", StringComparison.Ordinal))
            {
                break;
            }

            int separator = trimmed.IndexOf('=');
            if (separator <= 0)
            {
                continue;
            }

            string key = trimmed[..separator].Trim();
            string value = trimmed[(separator + 1)..].Trim();
            if (value.Length >= 2 && value[0] == '"' && value[^1] == '"')
            {
                value = value[1..^1]
                    .Replace("\\\"", "\"", StringComparison.Ordinal)
                    .Replace("\\\\", "\\", StringComparison.Ordinal);
            }

            settings[key] = value;
        }

        return settings;
    }

    private static string ReadCodexStringSetting(
        IReadOnlyDictionary<string, string> settings,
        string key,
        string fallback) =>
        settings.TryGetValue(key, out string? value) && !string.IsNullOrWhiteSpace(value)
            ? value.Trim()
            : fallback;

    private static int ReadCodexPositiveIntSetting(
        IReadOnlyDictionary<string, string> settings,
        string key,
        int fallback) =>
        settings.TryGetValue(key, out string? value) && int.TryParse(value, out int parsed) && parsed > 0
            ? parsed
            : fallback;

    private string ReadPreservedCodexSections()
    {
        if (!File.Exists(_paths.CodexConfigPath))
        {
            return string.Empty;
        }

        var lines = JsonFile.ReadText(_paths.CodexConfigPath)
            .Replace("\r\n", "\n", StringComparison.Ordinal)
            .Replace('\r', '\n')
            .Split('\n');

        var preserved = new List<string>();
        var inPreservedSection = false;

        foreach (var line in lines)
        {
            var trimmed = line.Trim();
            if (trimmed.StartsWith("[", StringComparison.Ordinal))
            {
                inPreservedSection = !trimmed.StartsWith("[model_providers.sub2api]", StringComparison.OrdinalIgnoreCase);
            }

            if (inPreservedSection)
            {
                preserved.Add(line);
            }
        }

        while (preserved.Count > 0 && string.IsNullOrWhiteSpace(preserved[0]))
        {
            preserved.RemoveAt(0);
        }

        while (preserved.Count > 0 && string.IsNullOrWhiteSpace(preserved[^1]))
        {
            preserved.RemoveAt(preserved.Count - 1);
        }

        return string.Join(Environment.NewLine, preserved);
    }

    private void WriteClaudeFile(ClientProfile profile)
    {
        var baseUrl = string.IsNullOrWhiteSpace(profile.BaseUrl)
            ? string.Empty
            : NormalizeGatewayRoot(profile.BaseUrl);
        var secret = profile.Secret.Trim();
        if (string.IsNullOrWhiteSpace(baseUrl) && string.IsNullOrWhiteSpace(secret)) return;

        var settings = ReadJsonObjectOrEmpty(_paths.ClaudeSettingsPath);
        if (settings["env"] is not JsonObject env)
        {
            env = new JsonObject();
            settings["env"] = env;
        }
        if (!string.IsNullOrWhiteSpace(baseUrl)) env["ANTHROPIC_BASE_URL"] = baseUrl;
        if (!string.IsNullOrWhiteSpace(secret)) env["ANTHROPIC_AUTH_TOKEN"] = secret;
        env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0";
        env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1";
        WriteJsonObject(_paths.ClaudeSettingsPath, settings);
    }

    private async Task<OperationResult> PreflightClaudeGptRoutingAsync(
        ClientProfile profile,
        ClaudeGptModelMapping mapping,
        CancellationToken cancellationToken)
    {
        IReadOnlyList<string> models = mapping.DistinctModels();
        bool modelListVerified = false;
        try
        {
            IReadOnlyList<string> availableModels = await GetClaudeGptModelsAsync(profile, cancellationToken)
                .ConfigureAwait(false);
            modelListVerified = true;
            string? missingModel = models.FirstOrDefault(model =>
                !availableModels.Contains(model, StringComparer.OrdinalIgnoreCase));
            if (missingModel is not null)
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = $"模型 {missingModel} 不在当前来源返回的 GPT/Grok 模型列表中。未修改 Claude Code 配置。"
                };
            }
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or
                                           InvalidOperationException or JsonException)
        {
            // Some compatible gateways do not expose /v1/models. In that case
            // the token-count bridge remains the authoritative preflight.
        }

        OperationResult[] results = await Task.WhenAll(models.Select(model =>
            PreflightClaudeGptModelAsync(profile, model, modelListVerified, cancellationToken))).ConfigureAwait(false);
        OperationResult? failed = results.FirstOrDefault(result => !result.Success);
        if (failed is not null)
        {
            return failed;
        }

        return new OperationResult
        {
            Success = true,
            Summary = $"{models.Count} 个 GPT/Grok 模型的 Responses 上游预检成功，本地 Claude Code 协议桥可启用。"
        };
    }

    private async Task<OperationResult> PreflightClaudeGptModelAsync(
        ClientProfile profile,
        string model,
        bool modelListVerified,
        CancellationToken cancellationToken)
    {
        try
        {
            string root = NormalizeOpenAiApiBaseUrl(profile.BaseUrl);
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{root}/responses");
            AddGatewayAuthorization(request, profile.Secret);
            request.Content = new StringContent(
                JsonSerializer.Serialize(new
                {
                    model,
                    input = "Reply with OK.",
                    max_output_tokens = 16,
                    stream = false,
                }),
                Encoding.UTF8,
                "application/json");

            using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
            if (response.IsSuccessStatusCode)
            {
                return new OperationResult { Success = true, Summary = $"{model} 预检成功。" };
            }

            string responseBody = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
            string reason = ReadGatewayErrorMessage(responseBody);
            return new OperationResult
            {
                Success = false,
                Summary = $"模型 {model} 的 Responses 上游预检失败：{(int)response.StatusCode} {response.StatusCode}{(string.IsNullOrWhiteSpace(reason) ? string.Empty : $"，{reason}")}。未修改 Claude Code 配置。"
            };
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or InvalidOperationException)
        {
            if (modelListVerified && exception is TaskCanceledException)
            {
                return new OperationResult
                {
                    Success = true,
                    Summary = $"模型 {model} 已在当前来源模型列表中确认；Responses 短预检超时，已按可用模型继续写入。"
                };
            }

            return new OperationResult
            {
                Success = false,
                Summary = $"模型 {model} 的 Responses 上游预检失败：{exception.Message}。未修改 Claude Code 配置。"
            };
        }
    }

    private async Task<OperationResult> PreflightCodexClaudeRoutingAsync(
        ClientProfile profile,
        CodexClaudeModelMapping mapping,
        CancellationToken cancellationToken)
    {
        IReadOnlyList<string> models = mapping.DistinctModels();
        bool modelListVerified = false;
        try
        {
            IReadOnlyList<string> availableModels = await GetCodexClaudeModelsAsync(profile, cancellationToken)
                .ConfigureAwait(false);
            string? missingModel = models.FirstOrDefault(model =>
                !availableModels.Contains(model, StringComparer.OrdinalIgnoreCase));
            if (missingModel is not null)
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = $"模型 {missingModel} 不在当前来源返回的 Claude/Grok 模型列表中。未修改 Codex 配置。"
                };
            }

            modelListVerified = true;
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or
                                           InvalidOperationException or JsonException)
        {
            // A real Responses request below remains authoritative when a
            // compatible gateway does not expose /v1/models.
        }

        OperationResult[] results = await Task.WhenAll(models.Select(model =>
            PreflightCodexClaudeModelAsync(
                profile,
                model,
                mapping.ReasoningEffort,
                modelListVerified,
                cancellationToken))).ConfigureAwait(false);
        OperationResult? failed = results.FirstOrDefault(result => !result.Success);
        OperationResult? warning = results.FirstOrDefault(result =>
            result.Success &&
            result.Summary.Contains("短预检超时", StringComparison.OrdinalIgnoreCase));
        return failed ?? new OperationResult
        {
            Success = true,
            Summary = warning is not null
                ? $"{models.Count} 个 Claude/Grok 模型已通过模型列表确认；部分 Responses 短预检超时，已继续启用。"
                : $"{models.Count} 个 Claude/Grok 模型的 Responses 兼容预检均成功。"
        };
    }

    private async Task<OperationResult> PreflightCodexClaudeModelAsync(
        ClientProfile profile,
        string model,
        string reasoningEffort,
        bool modelListVerified,
        CancellationToken cancellationToken)
    {
        try
        {
            string root = NormalizeGatewayRoot(profile.BaseUrl);
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{root}/v1/responses");
            AddGatewayAuthorization(request, profile.Secret);
            request.Content = new StringContent(
                JsonSerializer.Serialize(new
                {
                    model,
                    input = "Reply with OK.",
                    max_output_tokens = 16,
                    reasoning = new { effort = reasoningEffort },
                    stream = false,
                }),
                Encoding.UTF8,
                "application/json");
            using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
            if (response.IsSuccessStatusCode)
            {
                return new OperationResult { Success = true, Summary = $"{model} 预检成功。" };
            }

            string body = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
            string reason = ReadGatewayErrorMessage(body);
            return new OperationResult
            {
                Success = false,
                Summary = $"模型 {model} 的 Responses 预检失败：{(int)response.StatusCode} {response.StatusCode}{(string.IsNullOrWhiteSpace(reason) ? string.Empty : $"，{reason}")}。未修改 Codex 配置。"
            };
        }
        catch (Exception exception) when (exception is HttpRequestException or TaskCanceledException or InvalidOperationException)
        {
            if (modelListVerified && exception is TaskCanceledException)
            {
                return new OperationResult
                {
                    Success = true,
                    Summary = $"模型 {model} 已在当前来源模型列表中确认；Responses 短预检超时，已按可用模型继续写入。"
                };
            }

            return new OperationResult
            {
                Success = false,
                Summary = $"模型 {model} 的 Responses 预检失败：{exception.Message}。未修改 Codex 配置。"
            };
        }
    }

    private void WriteCodexClaudeConfiguration(ClientProfile profile, CodexClaudeModelMapping mapping)
    {
        JsonFile.WriteText(
            _paths.CodexConfigPath,
            BuildCodexConfigToml(
                profile,
                mapping.DefaultModel,
                mapping.ReviewModel,
                mapping.ReasoningEffort,
                200_000,
                180_000));

        var auth = new Dictionary<string, string>();
        if (File.Exists(_paths.CodexAuthPath))
        {
            try
            {
                auth = JsonSerializer.Deserialize<Dictionary<string, string>>(JsonFile.ReadText(_paths.CodexAuthPath)) ?? auth;
            }
            catch (JsonException)
            {
            }
        }
        auth["OPENAI_API_KEY"] = profile.Secret.Trim();
        JsonFile.Write(_paths.CodexAuthPath, auth);
    }

    private CodexClaudeRoutingState CaptureCodexClaudeRoutingState(
        string sourceId,
        string sourceName,
        CodexClaudeModelMapping mapping) =>
        new()
        {
            Version = 1,
            SourceId = sourceId.Trim(),
            SourceName = sourceName.Trim(),
            TargetPlatform = NormalizeCodexClaudeTarget(mapping.TargetPlatform),
            DefaultModel = mapping.DefaultModel.Trim(),
            ReviewModel = mapping.ReviewModel.Trim(),
            ReasoningEffort = mapping.ReasoningEffort.Trim().ToLowerInvariant(),
            ConfigExisted = File.Exists(_paths.CodexConfigPath),
            ConfigBytes = File.Exists(_paths.CodexConfigPath) ? File.ReadAllBytes(_paths.CodexConfigPath) : [],
            AuthExisted = File.Exists(_paths.CodexAuthPath),
            AuthBytes = File.Exists(_paths.CodexAuthPath) ? File.ReadAllBytes(_paths.CodexAuthPath) : [],
        };

    private void RestoreCodexClaudeSnapshot(CodexClaudeRoutingState state)
    {
        RestoreFileBytes(_paths.CodexConfigPath, state.ConfigExisted, state.ConfigBytes);
        RestoreFileBytes(_paths.CodexAuthPath, state.AuthExisted, state.AuthBytes);
    }

    private static void RestoreFileBytes(string path, bool existed, byte[] bytes)
    {
        if (existed)
        {
            string? directory = Path.GetDirectoryName(path);
            if (!string.IsNullOrWhiteSpace(directory))
            {
                Directory.CreateDirectory(directory);
            }
            File.WriteAllBytes(path, bytes);
        }
        else if (File.Exists(path))
        {
            File.Delete(path);
        }
    }

    private bool TryDeleteCodexClaudeRoutingState()
    {
        try
        {
            if (File.Exists(_paths.CodexClaudeRoutingStatePath))
            {
                File.Delete(_paths.CodexClaudeRoutingStatePath);
            }
            return true;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    public OperationResult RestoreApplicationSessionSnapshot(SessionConfigSnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);
        try
        {
            RestoreSessionSnapshot(snapshot);
            if (!TryDeleteClaudeGptRoutingState())
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = "客户端配置已恢复，但 Claude GPT 临时状态文件未能删除。"
                };
            }
            if (!TryDeleteCodexClaudeRoutingState())
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = "客户端配置已恢复，但 Codex Claude 临时状态文件未能删除。"
                };
            }
            if (!TryDeleteApplicationSessionSnapshot())
            {
                return new OperationResult
                {
                    Success = false,
                    Summary = "客户端配置已恢复，但应用启动快照未能删除。"
                };
            }

            return new OperationResult
            {
                Success = true,
                Summary = "已恢复工作台启动前的 Codex、Claude Code、Gemini CLI 和 Grok CLI 配置。"
            };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException)
        {
            return new OperationResult
            {
                Success = false,
                Summary = $"恢复工作台启动前配置失败：{exception.Message}"
            };
        }
    }

    public OperationResult PersistApplicationSessionSnapshot(SessionConfigSnapshot snapshot)
    {
        ArgumentNullException.ThrowIfNull(snapshot);
        try
        {
            SaveProtectedSnapshot(_paths.ApplicationSessionStatePath, snapshot, ApplicationSessionEntropy);
            return new OperationResult { Success = true, Summary = "应用启动快照已加密保存。" };
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or InvalidOperationException or
                                           PlatformNotSupportedException)
        {
            return new OperationResult { Success = false, Summary = $"保存应用启动快照失败：{exception.Message}" };
        }
    }

    public OperationResult RestoreAbandonedApplicationSessionSnapshot()
    {
        SessionConfigSnapshot? snapshot = LoadProtectedSnapshot<SessionConfigSnapshot>(
            _paths.ApplicationSessionStatePath,
            ApplicationSessionEntropy);
        if (snapshot is null)
        {
            return new OperationResult { Success = true, Summary = "没有待恢复的异常退出快照。" };
        }

        return RestoreApplicationSessionSnapshot(snapshot);
    }

    private static T? LoadProtectedSnapshot<T>(string path, byte[] entropy) where T : class
    {
        if (!OperatingSystem.IsWindows() || !File.Exists(path))
        {
            return null;
        }

        byte[]? protectedBytes = null;
        byte[]? plainBytes = null;
        try
        {
            protectedBytes = File.ReadAllBytes(path);
            plainBytes = ProtectedData.Unprotect(protectedBytes, entropy, DataProtectionScope.CurrentUser);
            return JsonSerializer.Deserialize<T>(plainBytes);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or PlatformNotSupportedException)
        {
            return null;
        }
        finally
        {
            if (protectedBytes is not null) CryptographicOperations.ZeroMemory(protectedBytes);
            if (plainBytes is not null) CryptographicOperations.ZeroMemory(plainBytes);
        }
    }

    private static void SaveProtectedSnapshot<T>(string path, T snapshot, byte[] entropy)
    {
        if (!OperatingSystem.IsWindows())
        {
            throw new PlatformNotSupportedException("安全快照需要 Windows DPAPI。");
        }

        byte[]? plainBytes = null;
        byte[]? protectedBytes = null;
        string? temporaryPath = null;
        try
        {
            plainBytes = JsonSerializer.SerializeToUtf8Bytes(snapshot);
            protectedBytes = ProtectedData.Protect(plainBytes, entropy, DataProtectionScope.CurrentUser);
            string directory = Path.GetDirectoryName(path)
                ?? throw new InvalidOperationException("无法确定安全快照目录。");
            Directory.CreateDirectory(directory);
            temporaryPath = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllBytes(temporaryPath, protectedBytes);
            File.Move(temporaryPath, path, overwrite: true);
            temporaryPath = null;
        }
        finally
        {
            if (plainBytes is not null) CryptographicOperations.ZeroMemory(plainBytes);
            if (protectedBytes is not null) CryptographicOperations.ZeroMemory(protectedBytes);
            if (!string.IsNullOrWhiteSpace(temporaryPath) && File.Exists(temporaryPath))
            {
                try { File.Delete(temporaryPath); } catch (IOException) { } catch (UnauthorizedAccessException) { }
            }
        }
    }

    private bool TryDeleteApplicationSessionSnapshot()
    {
        try
        {
            if (File.Exists(_paths.ApplicationSessionStatePath))
            {
                File.Delete(_paths.ApplicationSessionStatePath);
            }
            return true;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    private static bool IsUnsupportedTokenCountingResponse(string reason)
    {
        string normalized = reason.Trim().ToLowerInvariant();
        return normalized.Contains("upstream request failed", StringComparison.Ordinal) ||
               IsInvalidTokenCountingUrlResponse(normalized) ||
               normalized.Contains("token counting is not supported", StringComparison.Ordinal) ||
               normalized.Contains("input_tokens", StringComparison.Ordinal) &&
               (normalized.Contains("not found", StringComparison.Ordinal) ||
                normalized.Contains("not supported", StringComparison.Ordinal));
    }

    private static bool IsInvalidTokenCountingUrlResponse(string reason)
    {
        string normalized = reason.Trim().ToLowerInvariant();
        return normalized.Contains("invalid url", StringComparison.Ordinal) &&
               normalized.Contains("messages/count_tokens", StringComparison.Ordinal);
    }

    private void WriteClaudeGptConfiguration(
        ClientProfile profile,
        ClaudeGptModelMapping mapping,
        string sourceId,
        string sourceName,
        string targetPlatform)
    {
        StopClaudeGptBridge();
        ClaudeGptBridgeServer bridge = ClaudeGptBridgeServer.Start(profile, mapping, _bridgeHttpClient);
        _claudeGptBridge = bridge;
        string bridgeUrl = bridge.BaseUrl;
        string bridgeToken = bridge.AuthToken;
        var settings = ReadJsonObjectOrEmpty(_paths.ClaudeSettingsPath);
        if (settings["env"] is not JsonObject env)
        {
            env = new JsonObject();
            settings["env"] = env;
        }

        var values = new Dictionary<string, string>(StringComparer.Ordinal)
        {
            ["ANTHROPIC_BASE_URL"] = bridgeUrl,
            ["ANTHROPIC_AUTH_TOKEN"] = bridgeToken,
            ["ANTHROPIC_DEFAULT_OPUS_MODEL"] = mapping.OpusModel,
            ["ANTHROPIC_DEFAULT_SONNET_MODEL"] = mapping.SonnetModel,
            ["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = mapping.HaikuModel,
            ["ANTHROPIC_SMALL_FAST_MODEL"] = mapping.HaikuModel,
        };
        env.Remove("ANTHROPIC_MODEL");
        env.Remove("ANTHROPIC_API_KEY");
        foreach ((string name, string value) in values)
        {
            env[name] = value;
        }

        env["CLAUDE_CODE_ATTRIBUTION_HEADER"] = "0";
        env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1";
        settings["_gongfei_claude_gpt"] = new JsonObject
        {
            ["enabled"] = true,
            ["source_id"] = sourceId.Trim(),
            ["source_name"] = sourceName.Trim(),
            ["target_platform"] = NormalizeClaudeGptTarget(targetPlatform),
            ["bridge"] = new JsonObject
            {
                ["mode"] = "anthropic-messages-to-openai-responses",
                ["url"] = bridgeUrl,
                ["upstream"] = NormalizeOpenAiApiBaseUrl(profile.BaseUrl),
            },
            ["mapping"] = new JsonObject
            {
                ["opus"] = mapping.OpusModel,
                ["sonnet"] = mapping.SonnetModel,
                ["haiku"] = mapping.HaikuModel,
            },
        };
        WriteJsonObject(_paths.ClaudeSettingsPath, settings);

        bool environmentChanged = false;
        if (_writeUserEnvironment)
        {
            environmentChanged |= SetUserEnvironmentVariableRaw("ANTHROPIC_MODEL", null);
            environmentChanged |= SetUserEnvironmentVariableRaw("ANTHROPIC_API_KEY", null);
            foreach ((string name, string value) in values)
            {
                environmentChanged |= SetUserEnvironmentVariableRaw(name, value);
            }
        }
        if (environmentChanged)
        {
            BroadcastEnvironmentChange();
        }
    }

    private void StopClaudeGptBridge()
    {
        _claudeGptBridge?.Dispose();
        _claudeGptBridge = null;
    }

    private bool IsClaudeGptBridgeActive()
    {
        ClaudeGptBridgeServer? bridge = _claudeGptBridge;
        if (bridge is null || !bridge.IsRunning)
        {
            return false;
        }

        try
        {
            JsonObject settings = ReadJsonObjectOrEmpty(_paths.ClaudeSettingsPath);
            if (settings["env"] is not JsonObject env)
            {
                return false;
            }

            return string.Equals(
                       env["ANTHROPIC_BASE_URL"]?.GetValue<string>(),
                       bridge.BaseUrl,
                       StringComparison.OrdinalIgnoreCase) &&
                   string.Equals(
                       env["ANTHROPIC_AUTH_TOKEN"]?.GetValue<string>(),
                       bridge.AuthToken,
                       StringComparison.Ordinal);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or JsonException or InvalidOperationException)
        {
            return false;
        }
    }

    private ClaudeGptRoutingState CaptureClaudeGptRoutingState(
        string sourceId,
        string sourceName,
        string targetPlatform,
        ClaudeGptModelMapping mapping)
    {
        var state = new ClaudeGptRoutingState
        {
            Version = 2,
            SourceId = sourceId.Trim(),
            SourceName = sourceName.Trim(),
            TargetPlatform = NormalizeClaudeGptTarget(targetPlatform),
            OpusModel = mapping.OpusModel.Trim(),
            SonnetModel = mapping.SonnetModel.Trim(),
            HaikuModel = mapping.HaikuModel.Trim(),
            SettingsExisted = File.Exists(_paths.ClaudeSettingsPath),
            SettingsBytes = File.Exists(_paths.ClaudeSettingsPath)
                ? File.ReadAllBytes(_paths.ClaudeSettingsPath)
                : [],
        };
        foreach (string name in ClaudeEnvironmentVariables)
        {
            string? value = Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.User);
            state.EnvironmentVariables.Add(new ClaudeGptEnvironmentSnapshot
            {
                Name = name,
                Existed = value is not null,
                Value = value ?? string.Empty,
            });
        }
        return state;
    }

    private void RestoreClaudeGptSnapshot(ClaudeGptRoutingState state)
    {
        if (state.SettingsExisted)
        {
            string? directory = Path.GetDirectoryName(_paths.ClaudeSettingsPath);
            if (!string.IsNullOrWhiteSpace(directory))
            {
                Directory.CreateDirectory(directory);
            }
            File.WriteAllBytes(_paths.ClaudeSettingsPath, state.SettingsBytes);
        }
        else if (File.Exists(_paths.ClaudeSettingsPath))
        {
            File.Delete(_paths.ClaudeSettingsPath);
        }

        bool environmentChanged = false;
        if (_writeUserEnvironment)
        {
            foreach (ClaudeGptEnvironmentSnapshot variable in state.EnvironmentVariables)
            {
                string? value = variable.Existed ? variable.Value : null;
                Environment.SetEnvironmentVariable(variable.Name, value, EnvironmentVariableTarget.Process);
                string? current = Environment.GetEnvironmentVariable(variable.Name, EnvironmentVariableTarget.User);
                if (!string.Equals(current, value, StringComparison.Ordinal))
                {
                    Environment.SetEnvironmentVariable(variable.Name, value, EnvironmentVariableTarget.User);
                    environmentChanged = true;
                }
            }
        }
        if (environmentChanged)
        {
            BroadcastEnvironmentChange();
        }
    }

    private ClaudeGptRoutingState? LoadClaudeGptRoutingState()
    {
        if (!OperatingSystem.IsWindows() || !File.Exists(_paths.ClaudeGptRoutingStatePath))
        {
            return null;
        }

        byte[]? protectedBytes = null;
        byte[]? plainBytes = null;
        try
        {
            protectedBytes = File.ReadAllBytes(_paths.ClaudeGptRoutingStatePath);
            plainBytes = ProtectedData.Unprotect(
                protectedBytes,
                ClaudeGptStateEntropy,
                DataProtectionScope.CurrentUser);
            ClaudeGptRoutingState? state = JsonSerializer.Deserialize<ClaudeGptRoutingState>(plainBytes);
            return state?.Version is 1 or 2 ? state : null;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or
                                           CryptographicException or JsonException or PlatformNotSupportedException)
        {
            return null;
        }
        finally
        {
            if (protectedBytes is not null) CryptographicOperations.ZeroMemory(protectedBytes);
            if (plainBytes is not null) CryptographicOperations.ZeroMemory(plainBytes);
        }
    }

    private void SaveClaudeGptRoutingState(ClaudeGptRoutingState state)
    {
        if (!OperatingSystem.IsWindows())
        {
            throw new PlatformNotSupportedException("Claude GPT 路由的安全备份需要 Windows DPAPI。");
        }

        byte[]? plainBytes = null;
        byte[]? protectedBytes = null;
        string? temporaryPath = null;
        try
        {
            plainBytes = JsonSerializer.SerializeToUtf8Bytes(state);
            protectedBytes = ProtectedData.Protect(
                plainBytes,
                ClaudeGptStateEntropy,
                DataProtectionScope.CurrentUser);
            string directory = Path.GetDirectoryName(_paths.ClaudeGptRoutingStatePath)
                ?? throw new InvalidOperationException("无法确定 Claude GPT 路由状态目录。");
            Directory.CreateDirectory(directory);
            temporaryPath = _paths.ClaudeGptRoutingStatePath + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllBytes(temporaryPath, protectedBytes);
            File.Move(temporaryPath, _paths.ClaudeGptRoutingStatePath, overwrite: true);
            temporaryPath = null;
        }
        finally
        {
            if (plainBytes is not null) CryptographicOperations.ZeroMemory(plainBytes);
            if (protectedBytes is not null) CryptographicOperations.ZeroMemory(protectedBytes);
            if (!string.IsNullOrWhiteSpace(temporaryPath) && File.Exists(temporaryPath))
            {
                try { File.Delete(temporaryPath); } catch (IOException) { } catch (UnauthorizedAccessException) { }
            }
        }
    }

    private bool TryDeleteClaudeGptRoutingState()
    {
        try
        {
            if (File.Exists(_paths.ClaudeGptRoutingStatePath))
            {
                File.Delete(_paths.ClaudeGptRoutingStatePath);
            }
            return true;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            return false;
        }
    }

    internal static string NormalizeGatewayRoot(string baseUrl)
    {
        string normalized = NormalizeHttpApiBaseUrl(baseUrl);
        foreach (string endpoint in new[] { "/chat/completions", "/count_tokens", "/responses", "/messages", "/models" })
        {
            if (normalized.EndsWith(endpoint, StringComparison.OrdinalIgnoreCase))
            {
                normalized = normalized[..^endpoint.Length].TrimEnd('/');
                break;
            }
        }

        if (normalized.EndsWith("/v1beta", StringComparison.OrdinalIgnoreCase))
        {
            return normalized[..^"/v1beta".Length].TrimEnd('/');
        }

        return normalized.EndsWith("/v1", StringComparison.OrdinalIgnoreCase)
            ? normalized[..^"/v1".Length].TrimEnd('/')
            : normalized;
    }

    internal static string NormalizeOpenAiApiBaseUrl(string baseUrl)
    {
        return $"{NormalizeGatewayRoot(baseUrl)}/v1";
    }

    private static string BuildModelsUrl(string baseUrl)
    {
        return $"{NormalizeOpenAiApiBaseUrl(baseUrl)}/models";
    }

    private static string NormalizeHttpApiBaseUrl(string baseUrl)
    {
        string normalized = baseUrl.Trim().Replace('\\', '/').TrimEnd('/');
        if (!Uri.TryCreate(normalized, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(uri.Host))
        {
            return normalized;
        }

        var builder = new UriBuilder(uri)
        {
            Query = string.Empty,
            Fragment = string.Empty,
        };
        return builder.Uri.GetLeftPart(UriPartial.Path).TrimEnd('/');
    }

    private static ClientProfile RequireClaudeGptProfile(ClientProfile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);
        if (string.IsNullOrWhiteSpace(profile.BaseUrl))
        {
            throw new InvalidOperationException("所选来源没有配置 GPT/Grok 服务地址。");
        }
        string normalizedBaseUrl = NormalizeOpenAiApiBaseUrl(profile.BaseUrl);
        if (!Uri.TryCreate(normalizedBaseUrl, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https"))
        {
            throw new InvalidOperationException("所选来源的 GPT/Grok 服务地址无效。");
        }
        if (string.IsNullOrWhiteSpace(profile.Secret))
        {
            throw new InvalidOperationException("所选来源没有保存 GPT/Grok 密钥。");
        }
        return new ClientProfile { BaseUrl = normalizedBaseUrl, Secret = profile.Secret.Trim() };
    }

    private static ClientProfile RequireCodexClaudeProfile(ClientProfile profile)
    {
        ArgumentNullException.ThrowIfNull(profile);
        if (string.IsNullOrWhiteSpace(profile.BaseUrl))
        {
            throw new InvalidOperationException("所选来源没有配置 Sub2API 服务地址。");
        }
        string normalizedBaseUrl = NormalizeOpenAiApiBaseUrl(profile.BaseUrl);
        if (!Uri.TryCreate(normalizedBaseUrl, UriKind.Absolute, out Uri? uri) ||
            uri.Scheme is not ("http" or "https"))
        {
            throw new InvalidOperationException("所选来源的 Sub2API 服务地址无效。");
        }
        if (string.IsNullOrWhiteSpace(profile.Secret))
        {
            throw new InvalidOperationException("所选来源没有保存 API 密钥。");
        }
        return new ClientProfile { BaseUrl = normalizedBaseUrl, Secret = profile.Secret.Trim() };
    }

    private static void AddGatewayAuthorization(HttpRequestMessage request, string secret)
    {
        string key = secret.Trim();
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", key);
        request.Headers.TryAddWithoutValidation("x-api-key", key);
    }

    private static IEnumerable<string> ReadModelNames(JsonElement root)
    {
        if (root.ValueKind == JsonValueKind.Object && root.TryGetProperty("data", out JsonElement data))
        {
            foreach (JsonElement item in data.EnumerateArray())
            {
                if (item.TryGetProperty("id", out JsonElement id) && id.GetString() is { Length: > 0 } value)
                {
                    yield return value;
                }
            }
        }
        if (root.ValueKind == JsonValueKind.Object && root.TryGetProperty("models", out JsonElement models))
        {
            foreach (JsonElement item in models.EnumerateArray())
            {
                if (item.TryGetProperty("name", out JsonElement name) && name.GetString() is { Length: > 0 } value)
                {
                    yield return value.StartsWith("models/", StringComparison.OrdinalIgnoreCase) ? value[7..] : value;
                }
            }
        }
    }

    private static bool IsOpenAiOrGrokModelName(string name) =>
        IsTextGenerationModelName(name) &&
        (name.StartsWith("gpt-", StringComparison.OrdinalIgnoreCase) ||
         name.StartsWith("o1", StringComparison.OrdinalIgnoreCase) ||
         name.StartsWith("o3", StringComparison.OrdinalIgnoreCase) ||
         name.StartsWith("o4", StringComparison.OrdinalIgnoreCase) ||
         name.Contains("codex", StringComparison.OrdinalIgnoreCase) ||
         IsGrokModelName(name));

    private static bool IsTextGenerationModelName(string name) =>
        !name.Contains("image", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("audio", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("realtime", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("transcribe", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("embedding", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("moderation", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("auto-review", StringComparison.OrdinalIgnoreCase) &&
        !name.Contains("tts", StringComparison.OrdinalIgnoreCase);

    private static bool IsClaudeOrGrokModelName(string name) =>
        name.StartsWith("claude-", StringComparison.OrdinalIgnoreCase) ||
        name.Contains("anthropic/claude", StringComparison.OrdinalIgnoreCase) ||
        IsGrokModelName(name);

    private static bool IsGrokModelName(string name) =>
        name.StartsWith("grok-", StringComparison.OrdinalIgnoreCase) ||
        name.Contains("x-ai/grok", StringComparison.OrdinalIgnoreCase) ||
        name.Contains("xai/grok", StringComparison.OrdinalIgnoreCase);

    internal static string NormalizeClaudeGptTarget(string? targetPlatform)
    {
        string value = (targetPlatform ?? string.Empty).Trim();
        return value.Equals("Grok", StringComparison.OrdinalIgnoreCase) ||
               value.Equals("grok", StringComparison.OrdinalIgnoreCase)
            ? "Grok"
            : "GPT";
    }

    internal static string NormalizeCodexClaudeTarget(string? targetPlatform)
    {
        string value = (targetPlatform ?? string.Empty).Trim();
        return value.Equals("Grok", StringComparison.OrdinalIgnoreCase)
            ? "Grok"
            : "Claude";
    }

    private static string ReadGatewayErrorMessage(string body)
    {
        if (string.IsNullOrWhiteSpace(body)) return string.Empty;
        try
        {
            using JsonDocument document = JsonDocument.Parse(body);
            JsonElement root = document.RootElement;
            if (root.TryGetProperty("error", out JsonElement error))
            {
                if (error.ValueKind == JsonValueKind.Object &&
                    error.TryGetProperty("message", out JsonElement nestedMessage))
                {
                    return LimitMessage(nestedMessage.GetString());
                }
                if (error.ValueKind == JsonValueKind.String) return LimitMessage(error.GetString());
            }
            if (root.TryGetProperty("message", out JsonElement message)) return LimitMessage(message.GetString());
        }
        catch (JsonException)
        {
        }
        return LimitMessage(body);
    }

    private static string LimitMessage(string? value)
    {
        string normalized = (value ?? string.Empty).Replace("\r", " ").Replace("\n", " ").Trim();
        return normalized.Length <= 300 ? normalized : normalized[..300];
    }

    private sealed class ClaudeGptRoutingState
    {
        public int Version { get; set; }
        public string SourceId { get; set; } = string.Empty;
        public string SourceName { get; set; } = string.Empty;
        public string TargetPlatform { get; set; } = "GPT";
        // Version 1 used one model for all Claude roles. Keep this field so an
        // already-enabled installation can migrate without losing its backup.
        public string Model { get; set; } = string.Empty;
        public string OpusModel { get; set; } = string.Empty;
        public string SonnetModel { get; set; } = string.Empty;
        public string HaikuModel { get; set; } = string.Empty;
        public bool SettingsExisted { get; set; }
        public byte[] SettingsBytes { get; set; } = [];
        public List<ClaudeGptEnvironmentSnapshot> EnvironmentVariables { get; set; } = [];

        public ClaudeGptModelMapping GetMapping()
        {
            string fallback = Model.Trim();
            return new ClaudeGptModelMapping
            {
                OpusModel = string.IsNullOrWhiteSpace(OpusModel) ? fallback : OpusModel,
                SonnetModel = string.IsNullOrWhiteSpace(SonnetModel) ? fallback : SonnetModel,
                HaikuModel = string.IsNullOrWhiteSpace(HaikuModel) ? fallback : HaikuModel,
            };
        }

        public ClaudeGptRoutingState WithSelection(
            string sourceId,
            string sourceName,
            string targetPlatform,
            ClaudeGptModelMapping mapping) =>
            new()
            {
                Version = 2,
                SourceId = sourceId.Trim(),
                SourceName = sourceName.Trim(),
                TargetPlatform = NormalizeClaudeGptTarget(targetPlatform),
                OpusModel = mapping.OpusModel.Trim(),
                SonnetModel = mapping.SonnetModel.Trim(),
                HaikuModel = mapping.HaikuModel.Trim(),
                SettingsExisted = SettingsExisted,
                SettingsBytes = SettingsBytes.ToArray(),
                EnvironmentVariables = EnvironmentVariables.Select(variable => new ClaudeGptEnvironmentSnapshot
                {
                    Name = variable.Name,
                    Existed = variable.Existed,
                    Value = variable.Value,
                }).ToList(),
            };
    }

    private sealed class CodexClaudeRoutingState
    {
        public int Version { get; set; }
        public string SourceId { get; set; } = string.Empty;
        public string SourceName { get; set; } = string.Empty;
        public string TargetPlatform { get; set; } = "Claude";
        public string DefaultModel { get; set; } = string.Empty;
        public string ReviewModel { get; set; } = string.Empty;
        public string ReasoningEffort { get; set; } = "high";
        public bool ConfigExisted { get; set; }
        public byte[] ConfigBytes { get; set; } = [];
        public bool AuthExisted { get; set; }
        public byte[] AuthBytes { get; set; } = [];

        public CodexClaudeModelMapping GetMapping() => new()
        {
            TargetPlatform = NormalizeCodexClaudeTarget(TargetPlatform),
            DefaultModel = DefaultModel,
            ReviewModel = ReviewModel,
            ReasoningEffort = string.IsNullOrWhiteSpace(ReasoningEffort) ? "high" : ReasoningEffort,
        };

        public CodexClaudeRoutingState WithSelection(
            string sourceId,
            string sourceName,
            CodexClaudeModelMapping mapping) =>
            new()
            {
                Version = 1,
                SourceId = sourceId.Trim(),
                SourceName = sourceName.Trim(),
                TargetPlatform = NormalizeCodexClaudeTarget(mapping.TargetPlatform),
                DefaultModel = mapping.DefaultModel.Trim(),
                ReviewModel = mapping.ReviewModel.Trim(),
                ReasoningEffort = mapping.ReasoningEffort.Trim().ToLowerInvariant(),
                ConfigExisted = ConfigExisted,
                ConfigBytes = ConfigBytes.ToArray(),
                AuthExisted = AuthExisted,
                AuthBytes = AuthBytes.ToArray(),
            };
    }

    private sealed class ClaudeGptEnvironmentSnapshot
    {
        public string Name { get; set; } = string.Empty;
        public bool Existed { get; set; }
        public string Value { get; set; } = string.Empty;
    }

    private void WriteGeminiConfig(ClientProfile profile)
    {
        var baseUrl = string.IsNullOrWhiteSpace(profile.BaseUrl)
            ? string.Empty
            : NormalizeGatewayRoot(profile.BaseUrl);
        var secret = profile.Secret.Trim();
        if (string.IsNullOrWhiteSpace(baseUrl) && string.IsNullOrWhiteSpace(secret)) return;

        var geminiSettings = ReadJsonObjectOrEmpty(_paths.GeminiSettingsPath);
        if (!string.IsNullOrWhiteSpace(baseUrl)) geminiSettings["api_base"] = baseUrl;
        geminiSettings["_ai_switch_gui"] = true;
        WriteJsonObject(_paths.GeminiSettingsPath, geminiSettings);

        var environmentChanged = false;
        if (!string.IsNullOrWhiteSpace(baseUrl)) environmentChanged |= SetUserEnvironmentVariableRaw("GOOGLE_GEMINI_BASE_URL", baseUrl);
        if (!string.IsNullOrWhiteSpace(secret))
        {
            environmentChanged |= SetUserEnvironmentVariableRaw("GEMINI_API_KEY", secret);
            environmentChanged |= SetUserEnvironmentVariableRaw("GOOGLE_API_KEY", secret);
        }
        UpdateVsCodeTerminalEnvironment(baseUrl, secret);
        if (environmentChanged)
        {
            BroadcastEnvironmentChange();
        }
    }

    private async Task<string> ResolveGrokModelAsync(
        ClientProfile profile,
        CancellationToken cancellationToken)
    {
        string fallback = ReadConfiguredGrokModel() ?? DefaultGrokModel;
        if (string.IsNullOrWhiteSpace(profile.BaseUrl) || string.IsNullOrWhiteSpace(profile.Secret))
        {
            return fallback;
        }

        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Get, BuildModelsUrl(profile.BaseUrl));
            AddGatewayAuthorization(request, profile.Secret);
            using HttpResponseMessage response = await _httpClient
                .SendAsync(request, cancellationToken)
                .ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                return fallback;
            }

            await using Stream stream = await response.Content
                .ReadAsStreamAsync(cancellationToken)
                .ConfigureAwait(false);
            using JsonDocument document = await JsonDocument
                .ParseAsync(stream, cancellationToken: cancellationToken)
                .ConfigureAwait(false);
            return SelectLatestGrokModel(ReadModelNames(document.RootElement)) ?? fallback;
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            return fallback;
        }
        catch (Exception exception) when (exception is HttpRequestException or JsonException or InvalidOperationException)
        {
            return fallback;
        }
    }

    internal static string? SelectLatestGrokModel(IEnumerable<string> models)
    {
        string[] candidates = models
            .Where(IsChatGrokModelName)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
        if (candidates.FirstOrDefault(model =>
                model.Equals("grok-latest", StringComparison.OrdinalIgnoreCase)) is { } latestAlias)
        {
            return latestAlias;
        }

        string? versionedLatest = candidates
            .Where(model => model.EndsWith("-latest", StringComparison.OrdinalIgnoreCase))
            .OrderByDescending(ReadGrokVersion)
            .ThenByDescending(model => model, StringComparer.OrdinalIgnoreCase)
            .FirstOrDefault();
        if (versionedLatest is not null)
        {
            return versionedLatest;
        }

        string? neutralVersion = candidates
            .Where(IsNeutralVersionedGrokModel)
            .OrderByDescending(ReadGrokVersion)
            .ThenByDescending(model => model, StringComparer.OrdinalIgnoreCase)
            .FirstOrDefault();
        return neutralVersion ?? candidates
            .OrderByDescending(ReadGrokVersion)
            .ThenByDescending(model => model, StringComparer.OrdinalIgnoreCase)
            .FirstOrDefault();
    }

    private string? ReadConfiguredGrokModel()
    {
        if (!File.Exists(_paths.GrokConfigPath))
        {
            return null;
        }

        try
        {
            string? model = ReadTomlStringValue(JsonFile.ReadText(_paths.GrokConfigPath), "default");
            return model is not null && IsChatGrokModelName(model) ? model : null;
        }
        catch (IOException)
        {
            return null;
        }
        catch (UnauthorizedAccessException)
        {
            return null;
        }
    }

    private static bool IsChatGrokModelName(string model) =>
        IsGrokModelName(model) &&
        !model.Contains("build", StringComparison.OrdinalIgnoreCase) &&
        !model.Contains("composer", StringComparison.OrdinalIgnoreCase) &&
        !model.Contains("imagine", StringComparison.OrdinalIgnoreCase) &&
        !model.Contains("image", StringComparison.OrdinalIgnoreCase) &&
        !model.Contains("video", StringComparison.OrdinalIgnoreCase);

    private static bool IsNeutralVersionedGrokModel(string model)
    {
        if (!model.StartsWith("grok-", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        string version = model["grok-".Length..];
        return Version.TryParse(version, out _);
    }

    private static Version ReadGrokVersion(string model)
    {
        if (!model.StartsWith("grok-", StringComparison.OrdinalIgnoreCase))
        {
            return new Version(0, 0);
        }

        string versionPart = model["grok-".Length..].Split('-', 2)[0];
        if (!versionPart.Contains('.', StringComparison.Ordinal))
        {
            versionPart += ".0";
        }
        return Version.TryParse(versionPart, out Version? version)
            ? version
            : new Version(0, 0);
    }

    private void WriteGrokConfig(ClientProfile profile, string model)
    {
        var baseUrl = string.IsNullOrWhiteSpace(profile.BaseUrl)
            ? string.Empty
            : NormalizeOpenAiApiBaseUrl(profile.BaseUrl);
        var secret = profile.Secret.Trim();
        if (string.IsNullOrWhiteSpace(baseUrl) && string.IsNullOrWhiteSpace(secret)) return;

        string effectiveBaseUrl = string.IsNullOrWhiteSpace(baseUrl) ? DefaultGrokBaseUrl : baseUrl;
        string directory = Path.GetDirectoryName(_paths.GrokConfigPath) ?? throw new InvalidOperationException("无法确定 Grok 配置目录。");
        Directory.CreateDirectory(directory);
        var config = new StringBuilder();
        config.AppendLine("[endpoints]");
        config.AppendLine($"models_base_url = \"{EscapeTomlValue(effectiveBaseUrl)}\"");
        config.AppendLine();
        config.AppendLine("[models]");
        config.AppendLine($"default = \"{EscapeTomlValue(model)}\"");
        config.AppendLine();
        config.AppendLine($"[model.{EscapeTomlValue(model)}]");
        config.AppendLine("name = \"Grok\"");
        config.AppendLine($"model = \"{EscapeTomlValue(model)}\"");
        config.AppendLine($"base_url = \"{EscapeTomlValue(effectiveBaseUrl)}\"");
        config.AppendLine("api_backend = \"chat_completions\"");
        if (!string.IsNullOrWhiteSpace(secret))
        {
            config.AppendLine($"api_key = \"{EscapeTomlValue(secret)}\"");
        }
        config.AppendLine("# managed_by = \"gongfei-ai-workbench\"");
        JsonFile.WriteText(_paths.GrokConfigPath, config.ToString());

        var environmentChanged = false;
        environmentChanged |= SetUserEnvironmentVariableRaw("GROK_MODELS_BASE_URL", effectiveBaseUrl);
        environmentChanged |= SetUserEnvironmentVariableRaw("OPENAI_BASE_URL", effectiveBaseUrl);
        if (!string.IsNullOrWhiteSpace(secret))
        {
            environmentChanged |= SetUserEnvironmentVariableRaw("XAI_API_KEY", secret);
            environmentChanged |= SetUserEnvironmentVariableRaw("OPENAI_API_KEY", secret);
        }
        if (environmentChanged)
        {
            BroadcastEnvironmentChange();
        }
    }

    private void UpdateVsCodeTerminalEnvironment(string baseUrl, string secret)
    {
        var settings = ReadJsonObjectOrEmpty(_paths.VsCodeUserSettingsPath);
        if (settings["terminal.integrated.env.windows"] is not JsonObject env)
        {
            env = new JsonObject();
            settings["terminal.integrated.env.windows"] = env;
        }

        if (!string.IsNullOrWhiteSpace(baseUrl)) env["GOOGLE_GEMINI_BASE_URL"] = baseUrl;
        // VS Code settings are global, routinely synced, and readable by every
        // extension. Remove legacy key material instead of persisting it here.
        env.Remove("GEMINI_API_KEY");
        env.Remove("GOOGLE_API_KEY");
        settings["geminicodeassist.enable"] = true;
        WriteJsonObject(_paths.VsCodeUserSettingsPath, settings);
    }

    private static JsonObject ReadJsonObjectOrEmpty(string path)
    {
        if (!File.Exists(path))
        {
            return new JsonObject();
        }

        try
        {
            var options = new JsonDocumentOptions
            {
                AllowTrailingCommas = true,
                CommentHandling = JsonCommentHandling.Skip
            };
            return JsonNode.Parse(JsonFile.ReadText(path), documentOptions: options) as JsonObject ?? new JsonObject();
        }
        catch
        {
            return new JsonObject();
        }
    }

    private static void WriteJsonObject(string path, JsonObject value)
    {
        JsonFile.WriteText(path, value.ToJsonString(new JsonSerializerOptions { WriteIndented = true }) + Environment.NewLine);
    }

    private static bool SetUserEnvironmentVariableRaw(string name, string? value)
    {
        var normalized = string.IsNullOrWhiteSpace(value) ? null : value.Trim();
        string? currentProcessValue = Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.Process);
        Environment.SetEnvironmentVariable(name, normalized, EnvironmentVariableTarget.Process);
        var existing = Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.User);
        // Secrets in the user environment are exposed to every process of the
        // account and can be copied by profile-sync tooling. Child CLI processes
        // inherit the process-scoped value while this app is running.
        if (existing is not null)
        {
            Environment.SetEnvironmentVariable(name, null, EnvironmentVariableTarget.User);
        }

        return !string.Equals(currentProcessValue, normalized, StringComparison.Ordinal) || existing is not null;
    }

    private async Task<ValidationDetail[]> ValidateClientsAsync(ProfileDefinition profile, CancellationToken cancellationToken)
    {
        // Keep validation semantics identical to application semantics.  A
        // completely blank client section deliberately means "do not change
        // this official client", so it must not turn a valid routing change
        // into a failed HTTP request to an empty address.
        var codexTask = ValidateCodexAsync(profile.Codex, ReadCodexClientProfile(), cancellationToken);
        var claudeTask = ValidateClaudeAsync(profile.Claude, ReadClaudeClientProfile(), cancellationToken);
        var geminiTask = ValidateGeminiAsync(profile.Gemini, ReadGeminiClientProfile(), cancellationToken);
        return await Task.WhenAll(codexTask, claudeTask, geminiTask);
    }

    private static bool LeavesClientUnchanged(ClientProfile profile) =>
        string.IsNullOrWhiteSpace(profile.BaseUrl) && string.IsNullOrWhiteSpace(profile.Secret);

    private static ClientProfile MergeForValidation(ClientProfile profile, ClientProfile? current) => new()
    {
        BaseUrl = string.IsNullOrWhiteSpace(profile.BaseUrl) ? current?.BaseUrl ?? string.Empty : profile.BaseUrl,
        Secret = string.IsNullOrWhiteSpace(profile.Secret) ? current?.Secret ?? string.Empty : profile.Secret,
    };

    private async Task<ValidationDetail> ValidateCodexAsync(
        ClientProfile profile,
        ClientProfile? current,
        CancellationToken cancellationToken)
    {
        if (LeavesClientUnchanged(profile))
        {
            return new ValidationDetail { Name = "Codex", Success = true, Message = "留空，维持当前客户端配置" };
        }

        ClientProfile effective = MergeForValidation(profile, current);
        if (string.IsNullOrWhiteSpace(effective.BaseUrl) || string.IsNullOrWhiteSpace(effective.Secret))
        {
            return new ValidationDetail { Name = "Codex", Success = false, Message = "要验证此次变更，需要填写服务地址和密钥，或先在官方客户端完成现有配置" };
        }

        var baseUrl = NormalizeOpenAiApiBaseUrl(effective.BaseUrl);
        try
        {
            using HttpResponseMessage response = await SendCodexModelsRequestAsync(baseUrl, effective.Secret, cancellationToken);
            if (response.IsSuccessStatusCode)
            {
                return new ValidationDetail { Name = "Codex", Success = true, Message = $"GET /models => {(int)response.StatusCode} {response.StatusCode}" };
            }

            return new ValidationDetail { Name = "Codex", Success = false, Message = $"GET /models => {(int)response.StatusCode} {response.StatusCode}" };
        }
        catch (Exception ex)
        {
            return new ValidationDetail { Name = "Codex", Success = false, Message = $"GET /models 失败: {DescribeValidationException(ex)}" };
        }
    }

    private async Task<HttpResponseMessage> SendCodexModelsRequestAsync(
        string baseUrl,
        string secret,
        CancellationToken cancellationToken)
    {
        return await SendValidationRequestAsync(
            () =>
            {
                var request = new HttpRequestMessage(HttpMethod.Get, $"{baseUrl.TrimEnd('/')}/models");
                request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", secret.Trim());
                return request;
            },
            cancellationToken).ConfigureAwait(false);
    }

    private Task<HttpResponseMessage> SendOpenAiCompatibleModelsRequestAsync(
        string baseUrl,
        string secret,
        CancellationToken cancellationToken)
    {
        string apiBaseUrl = NormalizeOpenAiApiBaseUrl(baseUrl);
        return SendCodexModelsRequestAsync(apiBaseUrl, secret, cancellationToken);
    }

    private async Task<ValidationDetail> ValidateClaudeAsync(
        ClientProfile profile,
        ClientProfile? current,
        CancellationToken cancellationToken)
    {
        if (LeavesClientUnchanged(profile))
        {
            return new ValidationDetail { Name = "Claude Code", Success = true, Message = "留空，维持当前客户端配置" };
        }

        ClientProfile effective = MergeForValidation(profile, current);
        if (string.IsNullOrWhiteSpace(effective.BaseUrl) || string.IsNullOrWhiteSpace(effective.Secret))
        {
            return new ValidationDetail { Name = "Claude Code", Success = false, Message = "要验证此次变更，需要填写服务地址和密钥，或先在官方客户端完成现有配置" };
        }

        string root = NormalizeGatewayRoot(effective.BaseUrl);
        try
        {
            using HttpResponseMessage response = await SendValidationRequestAsync(
                () =>
                {
                    var request = new HttpRequestMessage(HttpMethod.Get, $"{root}/v1/models");
                    request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", effective.Secret.Trim());
                    request.Headers.Add("x-api-key", effective.Secret.Trim());
                    return request;
                },
                cancellationToken).ConfigureAwait(false);
            return new ValidationDetail { Name = "Claude Code", Success = response.IsSuccessStatusCode, Message = $"GET /v1/models => {(int)response.StatusCode} {response.StatusCode}" };
        }
        catch (Exception ex)
        {
            return new ValidationDetail { Name = "Claude Code", Success = false, Message = $"GET /v1/models 失败: {DescribeValidationException(ex)}" };
        }
    }

    private async Task<ValidationDetail> ValidateGeminiAsync(
        ClientProfile profile,
        ClientProfile? current,
        CancellationToken cancellationToken)
    {
        if (LeavesClientUnchanged(profile))
        {
            return new ValidationDetail { Name = "Gemini CLI", Success = true, Message = "留空，维持当前客户端配置" };
        }

        ClientProfile effective = MergeForValidation(profile, current);
        if (string.IsNullOrWhiteSpace(effective.BaseUrl) || string.IsNullOrWhiteSpace(effective.Secret))
        {
            return new ValidationDetail { Name = "Gemini CLI", Success = false, Message = "要验证此次变更，需要填写服务地址和密钥，或先在官方客户端完成现有配置" };
        }

        var key = effective.Secret.Trim();
        string root = NormalizeGatewayRoot(effective.BaseUrl);
        try
        {
            using HttpResponseMessage response = await SendValidationRequestAsync(
                () =>
                {
                    var request = new HttpRequestMessage(HttpMethod.Get, $"{root}/v1beta/models?key={Uri.EscapeDataString(key)}");
                    request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", key);
                    request.Headers.Add("x-goog-api-key", key);
                    return request;
                },
                cancellationToken).ConfigureAwait(false);
            if (response.IsSuccessStatusCode)
            {
                return new ValidationDetail { Name = "Gemini CLI", Success = true, Message = $"GET /v1beta/models => {(int)response.StatusCode} {response.StatusCode}" };
            }

            // Sub2API and other OpenAI-compatible gateways expose Gemini
            // through /v1/models instead of Google's native v1beta route.
            // Treat a successful compatibility probe as valid rather than
            // rejecting an otherwise working mixed routing configuration.
            using HttpResponseMessage compatibleResponse = await SendOpenAiCompatibleModelsRequestAsync(
                effective.BaseUrl,
                key,
                cancellationToken);
            if (compatibleResponse.IsSuccessStatusCode)
            {
                return new ValidationDetail
                {
                    Name = "Gemini CLI",
                    Success = true,
                    Message = $"中转兼容接口 GET /v1/models => {(int)compatibleResponse.StatusCode} {compatibleResponse.StatusCode}"
                };
            }

            return new ValidationDetail
            {
                Name = "Gemini CLI",
                Success = false,
                Message = $"GET /v1beta/models => {(int)response.StatusCode} {response.StatusCode}；兼容接口 GET /v1/models => {(int)compatibleResponse.StatusCode} {compatibleResponse.StatusCode}"
            };
        }
        catch (Exception ex)
        {
            return new ValidationDetail { Name = "Gemini CLI", Success = false, Message = $"GET /v1beta/models 失败: {DescribeValidationException(ex)}" };
        }
    }

    private async Task<HttpResponseMessage> SendValidationRequestAsync(
        Func<HttpRequestMessage> requestFactory,
        CancellationToken cancellationToken)
    {
        for (int attempt = 1; attempt <= ValidationAttemptCount; attempt++)
        {
            try
            {
                using HttpRequestMessage request = requestFactory();
                return await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
            }
            catch (Exception exception) when (
                attempt < ValidationAttemptCount &&
                IsTransientValidationException(exception, cancellationToken))
            {
                await Task.Delay(ValidationRetryDelays[attempt - 1], cancellationToken).ConfigureAwait(false);
            }
        }

        throw new InvalidOperationException("连接验证重试流程提前结束。");
    }

    internal static bool IsTransientValidationException(Exception exception, CancellationToken cancellationToken)
    {
        if (cancellationToken.IsCancellationRequested || FindException<AuthenticationException>(exception) is not null)
        {
            return false;
        }

        if (exception is TaskCanceledException)
        {
            return true;
        }

        if (FindException<SocketException>(exception) is not null)
        {
            return true;
        }

        return exception is HttpRequestException requestException && requestException.HttpRequestError is
            HttpRequestError.NameResolutionError or
            HttpRequestError.ConnectionError or
            HttpRequestError.HttpProtocolError or
            HttpRequestError.ProxyTunnelError or
            HttpRequestError.InvalidResponse or
            HttpRequestError.ResponseEnded;
    }

    internal static string DescribeValidationException(Exception exception)
    {
        if (HasCertificateNameMismatch(exception))
        {
            return "TLS 证书域名不匹配：服务器证书不包含当前服务地址，请检查该域名的 DNS、反向代理和证书配置";
        }

        if (FindException<AuthenticationException>(exception) is not null ||
            exception is HttpRequestException { HttpRequestError: HttpRequestError.SecureConnectionError })
        {
            return $"TLS 证书或安全连接验证失败：{GetInnermostMessage(exception)}";
        }

        if (exception is TaskCanceledException)
        {
            return "连接超时，已自动重试 2 次";
        }

        if (FindException<SocketException>(exception) is not null || exception is HttpRequestException)
        {
            return $"网络连接失败，已自动重试 2 次：{GetInnermostMessage(exception)}";
        }

        return GetInnermostMessage(exception);
    }

    private static bool HasCertificateNameMismatch(Exception exception)
    {
        for (Exception? current = exception; current is not null; current = current.InnerException)
        {
            if (current.HResult == SecEWrongPrincipal ||
                current is Win32Exception { NativeErrorCode: SecEWrongPrincipal })
            {
                return true;
            }

            string message = current.Message;
            if (message.Contains("RemoteCertificateNameMismatch", StringComparison.OrdinalIgnoreCase) ||
                message.Contains("certificate name mismatch", StringComparison.OrdinalIgnoreCase) ||
                message.Contains("target principal name is incorrect", StringComparison.OrdinalIgnoreCase) ||
                message.Contains("目标主要名称不正确", StringComparison.Ordinal))
            {
                return true;
            }
        }

        return false;
    }

    private static TException? FindException<TException>(Exception exception)
        where TException : Exception
    {
        for (Exception? current = exception; current is not null; current = current.InnerException)
        {
            if (current is TException match)
            {
                return match;
            }
        }

        return null;
    }

    private static string GetInnermostMessage(Exception exception)
    {
        Exception current = exception;
        while (current.InnerException is not null)
        {
            current = current.InnerException;
        }

        return string.IsNullOrWhiteSpace(current.Message) ? exception.Message : current.Message;
    }

    private async Task<ValidationDetail> ValidateGrokAsync(
        ClientProfile profile,
        ClientProfile? current,
        CancellationToken cancellationToken)
    {
        if (LeavesClientUnchanged(profile))
        {
            return new ValidationDetail { Name = "Grok CLI", Success = true, Message = "留空，维持当前客户端配置" };
        }

        ClientProfile effective = MergeForValidation(profile, current);
        if (string.IsNullOrWhiteSpace(effective.BaseUrl) || string.IsNullOrWhiteSpace(effective.Secret))
        {
            return new ValidationDetail { Name = "Grok CLI", Success = false, Message = "要验证此次变更，需要填写服务地址和密钥，或先在 Grok CLI 完成现有配置" };
        }

        try
        {
            using HttpResponseMessage response = await SendOpenAiCompatibleModelsRequestAsync(
                effective.BaseUrl,
                effective.Secret,
                cancellationToken).ConfigureAwait(false);
            return new ValidationDetail
            {
                Name = "Grok CLI",
                Success = response.IsSuccessStatusCode,
                Message = $"GET /v1/models => {(int)response.StatusCode} {response.StatusCode}"
            };
        }
        catch (Exception ex)
        {
            return new ValidationDetail { Name = "Grok CLI", Success = false, Message = $"GET /v1/models 失败: {DescribeValidationException(ex)}" };
        }
    }
    private ClientProfile? ReadCodexClientProfile()
    {
        if (!File.Exists(_paths.CodexConfigPath) || !File.Exists(_paths.CodexAuthPath)) return null;
        var baseUrl = ReadCodexBaseUrl();
        if (string.IsNullOrWhiteSpace(baseUrl)) baseUrl = DefaultCodexBaseUrl;

        try
        {
            using var document = JsonDocument.Parse(JsonFile.ReadText(_paths.CodexAuthPath));
            var secret = document.RootElement.GetProperty("OPENAI_API_KEY").GetString();
            if (string.IsNullOrWhiteSpace(secret)) return null;
            return new ClientProfile { BaseUrl = baseUrl, Secret = secret };
        }
        catch
        {
            return null;
        }
    }

    private ClientProfile? ReadClaudeClientProfile()
    {
        var envBaseUrl = Environment.GetEnvironmentVariable("ANTHROPIC_BASE_URL", EnvironmentVariableTarget.User);
        var envToken = Environment.GetEnvironmentVariable("ANTHROPIC_AUTH_TOKEN", EnvironmentVariableTarget.User) ??
                       Environment.GetEnvironmentVariable("ANTHROPIC_API_KEY", EnvironmentVariableTarget.User);

        if (!string.IsNullOrWhiteSpace(envBaseUrl) || !string.IsNullOrWhiteSpace(envToken))
        {
            return new ClientProfile
            {
                BaseUrl = string.IsNullOrWhiteSpace(envBaseUrl) ? DefaultClaudeBaseUrl : envBaseUrl.Trim(),
                Secret = envToken?.Trim() ?? string.Empty
            };
        }

        if (!File.Exists(_paths.ClaudeSettingsPath)) return null;
        try
        {
            using var document = JsonDocument.Parse(JsonFile.ReadText(_paths.ClaudeSettingsPath));
            var env = document.RootElement.GetProperty("env");
            var baseUrl = env.TryGetProperty("ANTHROPIC_BASE_URL", out var baseUrlElement)
                ? baseUrlElement.GetString()
                : DefaultClaudeBaseUrl;
            var secret = env.TryGetProperty("ANTHROPIC_AUTH_TOKEN", out var tokenElement)
                ? tokenElement.GetString()
                : null;
            if (string.IsNullOrWhiteSpace(baseUrl)) baseUrl = DefaultClaudeBaseUrl;
            if (string.IsNullOrWhiteSpace(secret)) return null;
            return new ClientProfile { BaseUrl = baseUrl, Secret = secret };
        }
        catch
        {
            return null;
        }
    }

    private ClientProfile? ReadGeminiClientProfile()
    {
        var envBaseUrl = Environment.GetEnvironmentVariable("GOOGLE_GEMINI_BASE_URL", EnvironmentVariableTarget.User);
        var envToken = Environment.GetEnvironmentVariable("GEMINI_API_KEY", EnvironmentVariableTarget.User) ??
                       Environment.GetEnvironmentVariable("GOOGLE_API_KEY", EnvironmentVariableTarget.User);
        var settingsBaseUrl = ReadGeminiBaseUrl();

        if (!string.IsNullOrWhiteSpace(envBaseUrl) || !string.IsNullOrWhiteSpace(envToken) || !string.IsNullOrWhiteSpace(settingsBaseUrl))
        {
            var baseUrl = !string.IsNullOrWhiteSpace(envBaseUrl)
                ? envBaseUrl.Trim()
                : !string.IsNullOrWhiteSpace(settingsBaseUrl)
                    ? settingsBaseUrl.Trim()
                    : DefaultGeminiBaseUrl;
            if (string.IsNullOrWhiteSpace(envToken)) return null;
            return new ClientProfile { BaseUrl = baseUrl, Secret = envToken.Trim() };
        }

        return null;
    }

    private ClientProfile? ReadGrokClientProfile()
    {
        var envBaseUrl = Environment.GetEnvironmentVariable("GROK_MODELS_BASE_URL", EnvironmentVariableTarget.User) ??
                         Environment.GetEnvironmentVariable("OPENAI_BASE_URL", EnvironmentVariableTarget.User);
        var envToken = Environment.GetEnvironmentVariable("XAI_API_KEY", EnvironmentVariableTarget.User) ??
                       Environment.GetEnvironmentVariable("OPENAI_API_KEY", EnvironmentVariableTarget.User);

        if (!string.IsNullOrWhiteSpace(envBaseUrl) || !string.IsNullOrWhiteSpace(envToken))
        {
            return new ClientProfile
            {
                BaseUrl = string.IsNullOrWhiteSpace(envBaseUrl) ? DefaultGrokBaseUrl : envBaseUrl.Trim(),
                Secret = envToken?.Trim() ?? string.Empty
            };
        }

        if (!File.Exists(_paths.GrokConfigPath)) return null;
        try
        {
            string content = JsonFile.ReadText(_paths.GrokConfigPath);
            string? baseUrl = ReadTomlStringValue(content, "base_url")
                ?? ReadTomlStringValue(content, "models_base_url")
                ?? DefaultGrokBaseUrl;
            string? secret = ReadTomlStringValue(content, "api_key");
            if (string.IsNullOrWhiteSpace(secret)) return null;
            return new ClientProfile { BaseUrl = baseUrl, Secret = secret };
        }
        catch
        {
            return null;
        }
    }
    private string? ReadCodexBaseUrl()
    {
        if (!File.Exists(_paths.CodexConfigPath)) return null;
        var content = JsonFile.ReadText(_paths.CodexConfigPath);
        const string marker = "base_url = \"";
        var index = content.IndexOf(marker, StringComparison.OrdinalIgnoreCase);
        if (index < 0) return null;
        var start = index + marker.Length;
        var end = content.IndexOf('"', start);
        return end > start ? content[start..end] : null;
    }

    private static string? ReadTomlStringValue(string content, string key)
    {
        string marker = key + " = \"";
        int index = content.IndexOf(marker, StringComparison.OrdinalIgnoreCase);
        if (index < 0) return null;
        int start = index + marker.Length;
        int end = content.IndexOf('"', start);
        return end > start ? content[start..end] : null;
    }
    private string? ReadClaudeBaseUrl() => ReadClaudeClientProfile()?.BaseUrl;

    private string? ReadGeminiBaseUrl()
    {
        if (!File.Exists(_paths.GeminiSettingsPath)) return null;
        try
        {
            var settings = ReadJsonObjectOrEmpty(_paths.GeminiSettingsPath);
            return settings.TryGetPropertyValue("api_base", out var baseUrlNode)
                ? baseUrlNode?.GetValue<string>()
                : null;
        }
        catch
        {
            return null;
        }
    }

    private static void PopulateStatusClassification(ProfileStore store, LiveStatus status)
    {
        var effectiveCodexBaseUrl = status.CodexBaseUrl;
        var effectiveClaudeBaseUrl = status.ClaudeBaseUrl;
        var effectiveGeminiBaseUrl = status.GeminiBaseUrl;

        status.CodexMatchedProfile = MatchProfileName(
            store,
            effectiveCodexBaseUrl,
            source => source.Codex.BaseUrl,
            NormalizeOpenAiApiBaseUrl);
        status.ClaudeMatchedProfile = MatchProfileName(
            store,
            effectiveClaudeBaseUrl,
            source => source.Claude.BaseUrl,
            NormalizeGatewayRoot);
        status.GeminiMatchedProfile = MatchProfileName(
            store,
            effectiveGeminiBaseUrl,
            source => source.Gemini.BaseUrl,
            NormalizeGatewayRoot);

        var codexMissing = string.Equals(status.CodexBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase);
        var claudeMissing = string.Equals(status.ClaudeBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase);
        var geminiMissing = string.Equals(status.GeminiBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase);
        if (codexMissing || claudeMissing || geminiMissing ||
            !status.CodexConfigPresent || !status.ClaudeConfigPresent || !status.GeminiConfigPresent)
        {
            status.Kind = LiveStatusKind.Missing;
            status.ActiveTarget = "配置缺失";
            status.Summary = BuildMissingSummary(status);
            status.HealthText = "状态：配置缺失";
            return;
        }

        var matchedCloudSource = store.CloudSources.FirstOrDefault(source =>
            string.Equals(status.CodexMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase) &&
            string.Equals(status.ClaudeMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase) &&
            string.Equals(status.GeminiMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase));
        if (matchedCloudSource is not null)
        {
            status.Kind = LiveStatusKind.Cloud;
            status.ActiveTarget = matchedCloudSource.Name;
            status.Summary = $"Codex / Claude / Gemini 均匹配 {matchedCloudSource.Name}。";
            status.HealthText = $"状态：{matchedCloudSource.Name}";
            return;
        }

        var matchedLocalSource = store.LocalSources.FirstOrDefault(source =>
            string.Equals(status.CodexMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase) &&
            string.Equals(status.ClaudeMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase) &&
            string.Equals(status.GeminiMatchedProfile, source.Name, StringComparison.OrdinalIgnoreCase));
        if (matchedLocalSource is not null)
        {
            status.Kind = LiveStatusKind.Local;
            status.ActiveTarget = matchedLocalSource.Name;
            status.Summary = $"Codex / Claude / Gemini 均匹配 {matchedLocalSource.Name}。";
            status.HealthText = $"状态：{matchedLocalSource.Name}";
            return;
        }

        if (AreEquivalent(effectiveCodexBaseUrl, ResolveClient(store, store.Mixed.CodexSourceId, true).BaseUrl, NormalizeOpenAiApiBaseUrl) &&
            AreEquivalent(effectiveClaudeBaseUrl, ResolveClient(store, store.Mixed.ClaudeSourceId, false).BaseUrl, NormalizeGatewayRoot) &&
            AreEquivalent(effectiveGeminiBaseUrl, ResolveGeminiClient(store, store.Mixed.GeminiSourceId).BaseUrl, NormalizeGatewayRoot))
        {
            status.Kind = LiveStatusKind.Mixed;
            status.ActiveTarget = "混合模式";
            status.Summary = $"Codex: {ResolveProfileSource(store, store.Mixed.CodexSourceId).Name}; Claude: {ResolveProfileSource(store, store.Mixed.ClaudeSourceId).Name}; Gemini: {ResolveProfileSource(store, store.Mixed.GeminiSourceId).Name}.";
            status.HealthText = "状态：混合";
            return;
        }

        status.Kind = LiveStatusKind.Custom;
        status.ActiveTarget = "自定义/未知";
        status.Summary = "当前配置未匹配任何已保存 profile。";
        status.HealthText = "状态：未知";
    }

    private static string? MatchProfileName(
        ProfileStore store,
        string baseUrl,
        Func<ProfileDefinition, string> baseUrlSelector,
        Func<string, string> normalize)
    {
        if (string.Equals(baseUrl, "<missing>", StringComparison.OrdinalIgnoreCase)) return null;

        foreach (var source in store.CloudSources)
        {
            if (AreEquivalent(baseUrl, baseUrlSelector(source), normalize)) return source.Name;
        }

        foreach (var source in store.LocalSources)
        {
            if (AreEquivalent(baseUrl, baseUrlSelector(source), normalize)) return source.Name;
        }

        return null;
    }

    private static bool AreEquivalent(string left, string right, Func<string, string> normalize) =>
        !string.IsNullOrWhiteSpace(left) &&
        !string.IsNullOrWhiteSpace(right) &&
        string.Equals(normalize(left), normalize(right), StringComparison.OrdinalIgnoreCase);

    private static string BuildMissingSummary(LiveStatus status)
    {
        var issues = new List<string>();
        if (!status.CodexConfigPresent) issues.Add("Codex 配置缺失");
        if (!status.ClaudeConfigPresent) issues.Add("Claude 配置缺失");
        if (!status.GeminiConfigPresent) issues.Add("Gemini 配置缺失");
        if (string.Equals(status.CodexBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase)) issues.Add("Codex base_url 缺失");
        if (string.Equals(status.ClaudeBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase)) issues.Add("Claude base_url 缺失");
        if (string.Equals(status.GeminiBaseUrl, "<missing>", StringComparison.OrdinalIgnoreCase)) issues.Add("Gemini base_url 缺失");
        return issues.Count == 0 ? "配置不完整，请检查主配置。" : string.Join("; ", issues);
    }
}

