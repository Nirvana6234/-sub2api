using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Text.RegularExpressions;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using AiSwitchGui;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using LanAi.Workspace.Wpf.Services;
using Microsoft.Win32;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// Mutation layer for the connection center. Existing secrets intentionally
/// never enter this view model: empty password fields mean keep, and a user
/// must explicitly tick clear to delete a stored secret.
/// </summary>
public partial class ConnectionsViewModel
{
    private readonly IConnectionProfileEditor? _editor;
    private readonly ILegacySwitchCoordinator? _switchCoordinator;
    private readonly ConnectionProfileTransferService? _profileTransfer;
    private readonly ILocalGatewayController? _localGatewayController;
    private readonly SemaphoreSlim _mutationGate = new(1, 1);
    private int _claudeGptModelLoadVersion;
    private int _codexClaudeModelLoadVersion;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasSelectedLibrarySource))]
    private ConnectionCardViewModel? selectedLibrarySource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsEditorVisible), nameof(IsNewEditorVisible))]
    private ConnectionEditorViewModel? connectionEditor;

    [ObservableProperty]
    private bool isMutating;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasMutationNotice))]
    private string mutationNotice = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(BackupUpstreamStatusText))]
    private bool isBackupUpstreamEnabled;

    public string BackupUpstreamStatusText => IsBackupUpstreamEnabled ? "已开启" : "已关闭";

    [ObservableProperty]
    private string activeClientStatus = "等待读取官方客户端状态。";

    [ObservableProperty]
    private string activeClientEndpoints = "重新读取后会确认四个客户端是否已准备好。";

    [ObservableProperty]
    private string codexClientStatus = "Codex 待检查";

    [ObservableProperty]
    private string claudeClientStatus = "Claude 待检查";

    [ObservableProperty]
    private string geminiClientStatus = "Gemini 待检查";

    [ObservableProperty]
    private string grokClientStatus = "Grok 待检查";

    [ObservableProperty]
    private ProviderTemplateDefinition? selectedProviderTemplate;

    [ObservableProperty]
    private bool isClaudeGptEditorOpen;

    [ObservableProperty]
    private bool isLoadingClaudeGptModels;

    [ObservableProperty]
    private bool isClaudeGptEnabled;

    [ObservableProperty]
    private string claudeGptStatusText = "未启用";

    [ObservableProperty]
    private string claudeGptSourceName = "本机中转";

    [ObservableProperty]
    private string claudeGptOpusModel = "gpt-5.6-sol";

    [ObservableProperty]
    private string claudeGptSonnetModel = "gpt-5.5";

    [ObservableProperty]
    private string claudeGptHaikuModel = "gpt-5.4-mini";

    [ObservableProperty]
    private string claudeGptMappingStatus = "自动匹配最新模型";

    [ObservableProperty]
    private ConnectionCardViewModel? selectedClaudeGptSource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsClaudeGptTargetGrokSelected), nameof(IsClaudeGptTargetGptSelected))]
    private string selectedClaudeGptTargetPlatform = "GPT";

    [ObservableProperty]
    private bool isCodexClaudeEditorOpen;

    [ObservableProperty]
    private bool isLoadingCodexClaudeModels;

    [ObservableProperty]
    private bool isCodexClaudeEnabled;

    [ObservableProperty]
    private string codexClaudeStatusText = "未启用";

    [ObservableProperty]
    private string codexClaudeSourceName = "本机中转";

    [ObservableProperty]
    private string codexClaudeDefaultModel = "claude-opus-4-8";

    [ObservableProperty]
    private string codexClaudeReviewModel = "claude-sonnet-4-6";

    [ObservableProperty]
    private string codexClaudeReasoningEffort = "高";

    [ObservableProperty]
    private string codexClaudeMappingStatus = "自动配置 Codex 运行策略";

    [ObservableProperty]
    private ConnectionCardViewModel? selectedCodexClaudeSource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsCodexClaudeTargetClaudeSelected), nameof(IsCodexClaudeTargetGrokSelected))]
    private string selectedCodexClaudeTargetPlatform = "Claude";

    internal ConnectionsViewModel(
        Func<Task> refresh,
        IConnectionProfileEditor editor,
        ILegacySwitchCoordinator? switchCoordinator = null,
        ConnectionProfileTransferService? profileTransfer = null,
        ILocalGatewayController? localGatewayController = null)
        : this(refresh)
    {
        _editor = editor ?? throw new ArgumentNullException(nameof(editor));
        _switchCoordinator = switchCoordinator;
        _profileTransfer = profileTransfer;
        _localGatewayController = localGatewayController;
        ProviderTemplates =
        [
            new("openai", "OpenAI 官方", "Codex 官方 API", "https://api.openai.com/v1", null, null),
            new("anthropic", "Anthropic 官方", "Claude Code 官方 API", null, "https://api.anthropic.com", null),
            new("gemini", "Gemini 官方", "Gemini 官方 API", null, null, "https://generativelanguage.googleapis.com", null),
            new("sub2api", "AI 中转", "Codex、Claude、Gemini、Grok 共用一个中转来源", "https://example.com/v1", "https://example.com", "https://example.com", "https://example.com/v1"),
        ];
        SelectedProviderTemplate = ProviderTemplates[^1];
    }

    public IReadOnlyList<ProviderTemplateDefinition> ProviderTemplates { get; } = Array.Empty<ProviderTemplateDefinition>();

    public ObservableCollection<ConnectionCardViewModel> ClaudeGptSources { get; } = [];

    public ObservableCollection<string> ClaudeGptModels { get; } = [];

    public IReadOnlyList<string> ClaudeGptTargetPlatforms { get; } = ["Grok", "GPT"];

    public bool IsClaudeGptTargetGrokSelected =>
        string.Equals(SelectedClaudeGptTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase);

    public bool IsClaudeGptTargetGptSelected =>
        string.Equals(SelectedClaudeGptTargetPlatform, "GPT", StringComparison.OrdinalIgnoreCase);

    public ObservableCollection<ConnectionCardViewModel> CodexClaudeSources { get; } = [];

    public ObservableCollection<string> CodexClaudeModels { get; } = [];

    public bool IsCodexClaudeTargetClaudeSelected =>
        string.Equals(SelectedCodexClaudeTargetPlatform, "Claude", StringComparison.OrdinalIgnoreCase);

    public bool IsCodexClaudeTargetGrokSelected =>
        string.Equals(SelectedCodexClaudeTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase);

    public IReadOnlyList<string> CodexClaudeReasoningEfforts { get; } = ["低", "中", "高", "极高"];

    public bool IsEditorVisible => ConnectionEditor is not null;

    public bool HasSelectedLibrarySource => SelectedLibrarySource is not null;

    /// <summary>Only a newly-created source uses the top-level editor. Existing
    /// sources expand their own card so the user does not lose their place.</summary>
    public bool IsNewEditorVisible => ConnectionEditor?.IsNew == true;

    public bool HasMutationNotice => !string.IsNullOrWhiteSpace(MutationNotice);

    internal void RefreshActiveClientStatus()
    {
        if (_switchCoordinator is null)
        {
            ActiveClientStatus = "暂时无法确认客户端状态";
            ActiveClientEndpoints = "重新打开完整工作台后可再次检查。";
            SetClientStatus("Codex", false, "Claude", false, "Gemini", false, "Grok", false);
            return;
        }

        try
        {
            LiveStatus status = _switchCoordinator.ReadLiveStatus();
            int readyCount = new[] { status.CodexConfigPresent, status.ClaudeConfigPresent, status.GeminiConfigPresent, status.GrokConfigPresent }.Count(value => value);
            ActiveClientStatus = readyCount == 4
                ? $"当前已应用：{Routing.AppliedSummary}"
                : $"当前分流：{Routing.AppliedSummary}；已有 {readyCount} 个客户端完成配置";
            ActiveClientEndpoints = Routing.HasPendingChanges
                ? "下方选择尚未应用，客户端仍使用这里显示的当前分流。"
                : "下方选择与客户端当前分流一致。";
            SetClientStatus(
                Routing.AppliedCodexName, status.CodexConfigPresent,
                Routing.AppliedClaudeName, status.ClaudeConfigPresent,
                Routing.AppliedGeminiName, status.GeminiConfigPresent,
                Routing.AppliedGrokName, status.GrokConfigPresent);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            ActiveClientStatus = "暂时无法读取客户端状态";
            ActiveClientEndpoints = "请点击“重新读取”后再试。";
            SetClientStatus("Codex", false, "Claude", false, "Gemini", false, "Grok", false);
        }
    }

    internal void RefreshClaudeGptStateAndSources()
    {
        ClaudeGptSources.Clear();
        ConnectionCardViewModel? localSource = GetLocalMachineSource();
        if (localSource is not null)
        {
            ClaudeGptSources.Add(localSource);
        }
        SelectedClaudeGptSource = localSource;
        ClaudeGptSourceName = "本机中转";

        if (_switchCoordinator is null)
        {
            IsClaudeGptEnabled = false;
            ClaudeGptStatusText = "暂时无法读取";
            return;
        }

        try
        {
            ClaudeGptRoutingStatus status = _switchCoordinator.ReadClaudeGptRoutingStatus();
            IsClaudeGptEnabled = status.Enabled;
            ClaudeGptStatusText = status.Enabled ? "已启用" : "未启用";
            ClaudeGptMappingStatus = status.Enabled ? $"{status.TargetPlatform} 映射已启用" : "请选择 GPT 或 Grok";
            if (status.Enabled && status.Mapping.IsComplete)
            {
                SelectedClaudeGptTargetPlatform = status.TargetPlatform;
                ClaudeGptOpusModel = status.Mapping.OpusModel;
                ClaudeGptSonnetModel = status.Mapping.SonnetModel;
                ClaudeGptHaikuModel = status.Mapping.HaikuModel;
            }
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            IsClaudeGptEnabled = false;
            ClaudeGptStatusText = "状态读取失败";
            MutationNotice = $"跨客户端路由状态读取失败：{Sanitize(exception.Message)}";
        }
    }

    [RelayCommand]
    private async Task ConfigureClaudeGptAsync()
    {
        if (IsMutating || _switchCoordinator is null)
        {
            return;
        }

        RefreshClaudeGptStateAndSources();
        if (SelectedClaudeGptSource is null)
        {
            MutationNotice = "未找到固定的本机中转配置，请先恢复本机中转后再配置模型路由。";
            return;
        }
        IsCodexClaudeEditorOpen = false;
        IsClaudeGptEditorOpen = true;
        await LoadClaudeGptModelsAsync(SelectedClaudeGptSource).ConfigureAwait(true);
    }

    [RelayCommand]
    private void CancelClaudeGptConfiguration()
    {
        if (!IsMutating)
        {
            IsClaudeGptEditorOpen = false;
        }
    }

    [RelayCommand]
    private async Task EnableClaudeGptAsync()
    {
        var mapping = new ClaudeGptModelMapping
        {
            OpusModel = ClaudeGptOpusModel.Trim(),
            SonnetModel = ClaudeGptSonnetModel.Trim(),
            HaikuModel = ClaudeGptHaikuModel.Trim(),
        };
        if (GetLocalMachineSource() is null || !mapping.IsComplete || IsMutating || _switchCoordinator is null)
        {
            MutationNotice = GetLocalMachineSource() is null
                ? "未找到固定的本机中转配置，请先恢复本机中转后再启用模型路由。"
                : "请完整设置 Claude Opus、Sonnet 和 Haiku 对应的 GPT/Grok 模型。";
            return;
        }

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            IsMutating = true;
            MutationNotice = "正在通过本机中转启用 Claude Code 模型路由…";
            OperationResult result = await _switchCoordinator.EnableClaudeGptRoutingAsync(
                ConnectionProfileIds.LocalMachine,
                SelectedClaudeGptTargetPlatform,
                mapping).ConfigureAwait(true);
            MutationNotice = result.Success ? Sanitize(result.Summary) : $"启用失败：{Sanitize(result.Summary)}";
            if (result.Success)
            {
                IsClaudeGptEditorOpen = false;
                RefreshClaudeGptStateAndSources();
            }
        }
        catch (Exception exception) when (exception is ArgumentException or InvalidOperationException or
                                           IOException or UnauthorizedAccessException or HttpRequestException)
        {
            MutationNotice = $"启用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task DisableClaudeGptAsync()
    {
        if (IsMutating || _switchCoordinator is null)
        {
            return;
        }

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            IsMutating = true;
            MutationNotice = "正在恢复启用前的 Claude Code 配置…";
            OperationResult result = await _switchCoordinator.DisableClaudeGptRoutingAsync().ConfigureAwait(true);
            MutationNotice = result.Success ? Sanitize(result.Summary) : $"停用失败：{Sanitize(result.Summary)}";
            RefreshClaudeGptStateAndSources();
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"停用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    partial void OnSelectedClaudeGptSourceChanged(ConnectionCardViewModel? value)
    {
        ClaudeGptSourceName = "本机中转";
    }

    partial void OnSelectedClaudeGptTargetPlatformChanged(string value)
    {
        if (IsClaudeGptEditorOpen && SelectedClaudeGptSource is not null)
        {
            _ = LoadClaudeGptModelsAsync(SelectedClaudeGptSource);
        }
    }

    [RelayCommand]
    private void SelectClaudeGptTargetPlatform(string? platform)
    {
        string target = string.Equals(platform, "GPT", StringComparison.OrdinalIgnoreCase)
            ? "GPT"
            : "Grok";
        if (!string.Equals(SelectedClaudeGptTargetPlatform, target, StringComparison.OrdinalIgnoreCase))
        {
            SelectedClaudeGptTargetPlatform = target;
        }
    }

    private async Task LoadClaudeGptModelsAsync(ConnectionCardViewModel? source)
    {
        if (source is null || _switchCoordinator is null)
        {
            return;
        }

        int version = Interlocked.Increment(ref _claudeGptModelLoadVersion);
        IsLoadingClaudeGptModels = true;
        try
        {
            IReadOnlyList<string> models = await _switchCoordinator.GetClaudeGptModelsAsync(
                ConnectionProfileIds.LocalMachine,
                SelectedClaudeGptTargetPlatform).ConfigureAwait(true);
            if (version != _claudeGptModelLoadVersion) return;
            ClaudeGptModels.Clear();
            foreach (string model in models)
            {
                ClaudeGptModels.Add(model);
            }
            if (ClaudeGptModels.Count == 0)
            {
                AddClaudeGptFallbackModels();
            }
            ClaudeGptModelMapping? savedMapping = _switchCoordinator.ReadClaudeGptPreset(
                ConnectionProfileIds.LocalMachine,
                SelectedClaudeGptTargetPlatform);
            if (savedMapping?.IsComplete == true)
            {
                ApplyClaudeGptMapping(savedMapping);
            }
            else
            {
                ApplyLatestClaudeGptDefaults();
            }
            MutationNotice = savedMapping?.IsComplete == true
                ? $"已恢复本机中转上次保存的 Claude Code → {SelectedClaudeGptTargetPlatform} 模型映射。"
                : models.Count > 0
                ? $"已从本机中转读取 {models.Count} 个 {SelectedClaudeGptTargetPlatform} 模型，并自动推荐最新的 Opus、Sonnet 和 Haiku 映射。"
                : $"本机中转未返回 {SelectedClaudeGptTargetPlatform} 模型列表，可以直接填写后台配置的模型映射名。";
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or
                                           UnauthorizedAccessException or HttpRequestException or TaskCanceledException)
        {
            if (version != _claudeGptModelLoadVersion) return;
            ClaudeGptModels.Clear();
            AddClaudeGptFallbackModels();
            ClaudeGptModelMapping? savedMapping = _switchCoordinator.ReadClaudeGptPreset(
                ConnectionProfileIds.LocalMachine,
                SelectedClaudeGptTargetPlatform);
            if (savedMapping?.IsComplete == true)
            {
                ApplyClaudeGptMapping(savedMapping);
            }
            else
            {
                ApplyLatestClaudeGptDefaults();
            }
            MutationNotice = $"模型列表暂时无法读取，可手动填写模型名：{Sanitize(exception.Message)}";
        }
        finally
        {
            if (version == _claudeGptModelLoadVersion)
            {
                IsLoadingClaudeGptModels = false;
            }
        }
    }

    internal void RefreshCodexClaudeStateAndSources()
    {
        CodexClaudeSources.Clear();
        ConnectionCardViewModel? localSource = GetLocalMachineSource();
        if (localSource is not null)
        {
            CodexClaudeSources.Add(localSource);
        }
        SelectedCodexClaudeSource = localSource;
        CodexClaudeSourceName = "本机中转";

        if (_switchCoordinator is null)
        {
            IsCodexClaudeEnabled = false;
            CodexClaudeStatusText = "暂时无法读取";
            return;
        }

        try
        {
            CodexClaudeRoutingStatus status = _switchCoordinator.ReadCodexClaudeRoutingStatus();
            IsCodexClaudeEnabled = status.Enabled;
            CodexClaudeStatusText = status.Enabled ? "已启用" : "未启用";
            CodexClaudeMappingStatus = status.Enabled ? "Codex 运行策略已启用" : "自动配置 Claude/Grok";
            if (status.Enabled && status.Mapping.IsComplete)
            {
                SelectedCodexClaudeTargetPlatform = SwitchService.NormalizeCodexClaudeTarget(
                    status.Mapping.TargetPlatform);
                CodexClaudeDefaultModel = status.Mapping.DefaultModel;
                CodexClaudeReviewModel = status.Mapping.ReviewModel;
                CodexClaudeReasoningEffort = ToReasoningEffortLabel(status.Mapping.ReasoningEffort);
            }
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            IsCodexClaudeEnabled = false;
            CodexClaudeStatusText = "状态读取失败";
            MutationNotice = $"跨客户端路由状态读取失败：{Sanitize(exception.Message)}";
        }
    }

    [RelayCommand]
    private async Task ConfigureCodexClaudeAsync()
    {
        if (IsMutating || _switchCoordinator is null)
        {
            return;
        }

        RefreshCodexClaudeStateAndSources();
        if (SelectedCodexClaudeSource is null)
        {
            MutationNotice = "未找到固定的本机中转配置，请先恢复本机中转后再配置模型路由。";
            return;
        }
        IsClaudeGptEditorOpen = false;
        IsCodexClaudeEditorOpen = true;
        await LoadCodexClaudeModelsAsync(SelectedCodexClaudeSource).ConfigureAwait(true);
    }

    [RelayCommand]
    private void CancelCodexClaudeConfiguration()
    {
        if (!IsMutating)
        {
            IsCodexClaudeEditorOpen = false;
        }
    }

    [RelayCommand]
    private async Task EnableCodexClaudeAsync()
    {
        var mapping = new CodexClaudeModelMapping
        {
            TargetPlatform = SwitchService.NormalizeCodexClaudeTarget(SelectedCodexClaudeTargetPlatform),
            DefaultModel = CodexClaudeDefaultModel.Trim(),
            ReviewModel = CodexClaudeReviewModel.Trim(),
            ReasoningEffort = ToReasoningEffortValue(CodexClaudeReasoningEffort),
        };
        if (GetLocalMachineSource() is null || !mapping.IsComplete || IsMutating || _switchCoordinator is null)
        {
            MutationNotice = GetLocalMachineSource() is null
                ? "未找到固定的本机中转配置，请先恢复本机中转后再启用模型路由。"
                : "请完整设置 Codex 默认模型和代码审查模型。";
            return;
        }

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            IsMutating = true;
            MutationNotice = "正在通过本机中转启用 Codex 模型路由…";
            OperationResult result = await _switchCoordinator.EnableCodexClaudeRoutingAsync(
                ConnectionProfileIds.LocalMachine,
                mapping).ConfigureAwait(true);
            MutationNotice = result.Success ? Sanitize(result.Summary) : $"启用失败：{Sanitize(result.Summary)}";
            if (result.Success)
            {
                IsCodexClaudeEditorOpen = false;
                RefreshCodexClaudeStateAndSources();
            }
        }
        catch (Exception exception) when (exception is ArgumentException or InvalidOperationException or
                                           IOException or UnauthorizedAccessException or HttpRequestException)
        {
            MutationNotice = $"启用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task DisableCodexClaudeAsync()
    {
        if (IsMutating || _switchCoordinator is null)
        {
            return;
        }

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            IsMutating = true;
            MutationNotice = "正在恢复启用前的 Codex 配置…";
            OperationResult result = await _switchCoordinator.DisableCodexClaudeRoutingAsync().ConfigureAwait(true);
            MutationNotice = result.Success ? Sanitize(result.Summary) : $"停用失败：{Sanitize(result.Summary)}";
            RefreshCodexClaudeStateAndSources();
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"停用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    partial void OnSelectedCodexClaudeSourceChanged(ConnectionCardViewModel? value)
    {
        CodexClaudeSourceName = "本机中转";
    }

    partial void OnSelectedCodexClaudeTargetPlatformChanged(string value)
    {
        if (IsCodexClaudeEditorOpen && SelectedCodexClaudeSource is not null)
        {
            _ = LoadCodexClaudeModelsAsync(SelectedCodexClaudeSource);
        }
    }

    [RelayCommand]
    private void SelectCodexClaudeTargetPlatform(string? platform)
    {
        string target = SwitchService.NormalizeCodexClaudeTarget(platform);
        if (!string.Equals(SelectedCodexClaudeTargetPlatform, target, StringComparison.OrdinalIgnoreCase))
        {
            SelectedCodexClaudeTargetPlatform = target;
        }
    }

    private async Task LoadCodexClaudeModelsAsync(ConnectionCardViewModel? source)
    {
        if (source is null || _switchCoordinator is null)
        {
            return;
        }

        int version = Interlocked.Increment(ref _codexClaudeModelLoadVersion);
        IsLoadingCodexClaudeModels = true;
        try
        {
            IReadOnlyList<string> models = await _switchCoordinator.GetCodexClaudeModelsAsync(
                ConnectionProfileIds.LocalMachine,
                SelectedCodexClaudeTargetPlatform).ConfigureAwait(true);
            if (version != _codexClaudeModelLoadVersion) return;
            CodexClaudeModels.Clear();
            foreach (string model in models)
            {
                CodexClaudeModels.Add(model);
            }
            if (CodexClaudeModels.Count == 0)
            {
                AddCodexClaudeFallbackModels();
            }
            CodexClaudeModelMapping? savedMapping = _switchCoordinator.ReadCodexClaudePreset(
                ConnectionProfileIds.LocalMachine,
                SelectedCodexClaudeTargetPlatform);
            if (savedMapping?.IsComplete == true)
            {
                ApplyCodexClaudeMapping(savedMapping);
            }
            else
            {
                ApplyLatestCodexClaudeDefaults();
            }
            MutationNotice = savedMapping?.IsComplete == true
                ? "已恢复本机中转上次保存的 Codex → Claude/Grok 运行策略。"
                : models.Count > 0
                ? $"已从本机中转读取 {models.Count} 个 Claude/Grok 模型，并自动推荐默认编码与代码审查模型。"
                : "本机中转未返回 Claude/Grok 模型列表，可以直接填写后台配置的模型名。";
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or
                                           UnauthorizedAccessException or HttpRequestException or TaskCanceledException)
        {
            if (version != _codexClaudeModelLoadVersion) return;
            CodexClaudeModels.Clear();
            AddCodexClaudeFallbackModels();
            CodexClaudeModelMapping? savedMapping = _switchCoordinator.ReadCodexClaudePreset(
                ConnectionProfileIds.LocalMachine,
                SelectedCodexClaudeTargetPlatform);
            if (savedMapping?.IsComplete == true)
            {
                ApplyCodexClaudeMapping(savedMapping);
            }
            else
            {
                ApplyLatestCodexClaudeDefaults();
            }
            MutationNotice = $"模型列表暂时无法读取，可手动填写模型名：{Sanitize(exception.Message)}";
        }
        finally
        {
            if (version == _codexClaudeModelLoadVersion)
            {
                IsLoadingCodexClaudeModels = false;
            }
        }
    }

    private void ApplyLatestClaudeGptDefaults()
    {
        string[] models = ClaudeGptModels.ToArray();
        if (string.Equals(SelectedClaudeGptTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase))
        {
            string grokDefault = models.FirstOrDefault(IsGrokModel) ?? SwitchService.DefaultGrokModel;
            ClaudeGptOpusModel = grokDefault;
            ClaudeGptSonnetModel = grokDefault;
            ClaudeGptHaikuModel = grokDefault;
            ClaudeGptMappingStatus = "已自动推荐 Grok 模型";
            return;
        }

        string? grok = models.FirstOrDefault(IsGrokModel);
        string? premium = models.FirstOrDefault(IsPremiumGptModel) ?? grok;
        string? standard = models.FirstOrDefault(model => !IsPremiumGptModel(model) && !IsLightweightModel(model));
        string? lightweight = models.FirstOrDefault(model => model.Contains("mini", StringComparison.OrdinalIgnoreCase))
                              ?? models.FirstOrDefault(model => model.Contains("flash", StringComparison.OrdinalIgnoreCase))
                              ?? models.FirstOrDefault(model => model.Contains("nano", StringComparison.OrdinalIgnoreCase))
                              ?? models.FirstOrDefault(IsLightweightModel);
        ClaudeGptOpusModel = premium ?? models.FirstOrDefault(model => !IsLightweightModel(model)) ?? "gpt-5.6-sol";
        ClaudeGptSonnetModel = standard ?? models.FirstOrDefault(model => !IsLightweightModel(model) &&
            !string.Equals(model, ClaudeGptOpusModel, StringComparison.OrdinalIgnoreCase)) ?? ClaudeGptOpusModel;
        ClaudeGptHaikuModel = lightweight ?? models.LastOrDefault() ?? ClaudeGptSonnetModel;
        ClaudeGptMappingStatus = "已自动推荐最新模型";
    }

    private void AddClaudeGptFallbackModels()
    {
        if (string.Equals(SelectedClaudeGptTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase))
        {
            ClaudeGptModels.Add(SwitchService.DefaultGrokModel);
            return;
        }

        ClaudeGptModels.Add("gpt-5.6-sol");
        ClaudeGptModels.Add("gpt-5.5");
        ClaudeGptModels.Add("gpt-5.4-mini");
    }

    private ConnectionCardViewModel? GetLocalMachineSource() =>
        Connections.FirstOrDefault(source =>
            source.CanOperate &&
            string.Equals(source.Record.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase));

    private void ApplyClaudeGptMapping(ClaudeGptModelMapping mapping)
    {
        ClaudeGptOpusModel = mapping.OpusModel;
        ClaudeGptSonnetModel = mapping.SonnetModel;
        ClaudeGptHaikuModel = mapping.HaikuModel;
        ClaudeGptMappingStatus = "已恢复此来源的上次设置";
    }

    private void ApplyLatestCodexClaudeDefaults()
    {
        string[] models = CodexClaudeModels.ToArray();
        if (string.Equals(SelectedCodexClaudeTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase))
        {
            string grokDefault = models.FirstOrDefault(IsGrokModel) ?? SwitchService.DefaultGrokModel;
            CodexClaudeDefaultModel = grokDefault;
            CodexClaudeReviewModel = grokDefault;
            CodexClaudeReasoningEffort = "高";
            CodexClaudeMappingStatus = "Grok · 主模型 · Review";
            return;
        }

        CodexClaudeDefaultModel = models.FirstOrDefault(model => model.Contains("opus", StringComparison.OrdinalIgnoreCase))
                                  ?? models.FirstOrDefault()
                                  ?? "claude-opus-4-8";
        CodexClaudeReviewModel = models.FirstOrDefault(model => model.Contains("sonnet", StringComparison.OrdinalIgnoreCase))
                                 ?? models.FirstOrDefault(model => !string.Equals(model, CodexClaudeDefaultModel, StringComparison.OrdinalIgnoreCase))
                                 ?? CodexClaudeDefaultModel;
        CodexClaudeReasoningEffort = "高";
        CodexClaudeMappingStatus = "主模型 · Review · 高推理";
    }

    private void AddCodexClaudeFallbackModels()
    {
        if (string.Equals(SelectedCodexClaudeTargetPlatform, "Grok", StringComparison.OrdinalIgnoreCase))
        {
            CodexClaudeModels.Add(SwitchService.DefaultGrokModel);
            return;
        }

        CodexClaudeModels.Add("claude-opus-4-8");
        CodexClaudeModels.Add("claude-sonnet-4-6");
    }

    private void ApplyCodexClaudeMapping(CodexClaudeModelMapping mapping)
    {
        SelectedCodexClaudeTargetPlatform = SwitchService.NormalizeCodexClaudeTarget(mapping.TargetPlatform);
        CodexClaudeDefaultModel = mapping.DefaultModel;
        CodexClaudeReviewModel = mapping.ReviewModel;
        CodexClaudeReasoningEffort = ToReasoningEffortLabel(mapping.ReasoningEffort);
        CodexClaudeMappingStatus = "已恢复此来源的上次设置";
    }

    private static bool IsPremiumGptModel(string model) =>
        model.Contains("sol", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("pro", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("max", StringComparison.OrdinalIgnoreCase) ||
        IsGrokModel(model);

    private static bool IsLightweightModel(string model) =>
        model.Contains("mini", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("nano", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("flash", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("small", StringComparison.OrdinalIgnoreCase);

    private static bool IsGrokModel(string model) =>
        model.StartsWith("grok-", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("x-ai/grok", StringComparison.OrdinalIgnoreCase) ||
        model.Contains("xai/grok", StringComparison.OrdinalIgnoreCase);

    private static string ToReasoningEffortValue(string label) => label switch
    {
        "低" => "low",
        "中" => "medium",
        "极高" => "xhigh",
        _ => "high",
    };

    private static string ToReasoningEffortLabel(string value) => value.Trim().ToLowerInvariant() switch
    {
        "low" => "低",
        "medium" => "中",
        "xhigh" => "极高",
        _ => "高",
    };

    private static string GetClientBaseUrl(ConnectionProfile profile, CliKind cliKind) =>
        profile.ClientBaseUrls.GetValueOrDefault(cliKind)
        ?? (profile.EnabledClients.Contains(cliKind) ? profile.BaseUrl : string.Empty);

    private void SetClientStatus(
        string codexName,
        bool codexReady,
        string claudeName,
        bool claudeReady,
        string geminiName,
        bool geminiReady,
        string grokName,
        bool grokReady)
    {
        CodexClientStatus = $"{codexName} {(codexReady ? "已配置" : "需配置")}";
        ClaudeClientStatus = $"{claudeName} {(claudeReady ? "已配置" : "需配置")}";
        GeminiClientStatus = $"{geminiName} {(geminiReady ? "已配置" : "需配置")}";
        GrokClientStatus = $"{grokName} {(grokReady ? "已配置" : "需配置")}";
    }

    internal void SetEnteredSecret(CliKind client, string? value)
    {
        if (ConnectionEditor is null)
        {
            return;
        }

        ConnectionEditor.SetEnteredSecret(client, value ?? string.Empty);
    }

    [RelayCommand]
    private void AddConnection()
    {
        if (IsMutating)
        {
            return;
        }

        MutationNotice = string.Empty;
        ClearInlineEditors();
        SelectedLibrarySource = null;
        ConnectionEditor = ConnectionEditorViewModel.CreateNew();
    }

    partial void OnSelectedLibrarySourceChanged(ConnectionCardViewModel? value)
    {
        if (IsMutating)
        {
            return;
        }

        ClearInlineEditors();
        foreach (ConnectionCardViewModel candidate in Connections)
        {
            candidate.IsExpanded = false;
            candidate.IsDeleteConfirmationVisible = false;
        }

        ConnectionEditor = value is null
            ? ConnectionEditor?.IsNew == true ? ConnectionEditor : null
            : ConnectionEditorViewModel.FromExisting(value.Record, value.IsFixed);
        MutationNotice = string.Empty;
    }

    [RelayCommand]
    private void ApplyProviderTemplate()
    {
        if (ConnectionEditor is null || SelectedProviderTemplate is null) return;
        ProviderTemplateDefinition template = SelectedProviderTemplate;
        if (string.IsNullOrWhiteSpace(ConnectionEditor.Name)) ConnectionEditor.Name = template.Name;
        ConnectionEditor.Notes = template.Description;
        ConnectionEditor.CodexBaseUrl = template.CodexBaseUrl ?? string.Empty;
        ConnectionEditor.ClaudeBaseUrl = template.ClaudeBaseUrl ?? string.Empty;
        ConnectionEditor.GeminiBaseUrl = template.GeminiBaseUrl ?? string.Empty;
        ConnectionEditor.GrokBaseUrl = template.GrokBaseUrl ?? string.Empty;
        MutationNotice = "已套用来源模板；密钥仍保持为空，请按需填写。";
    }

    [RelayCommand]
    private async Task ExportProfilesAsync()
    {
        if (_profileTransfer is null || IsMutating) return;
        var dialog = new SaveFileDialog
        {
            Title = "导出连接来源（不含密钥）",
            Filter = "JSON 文件|*.json",
            FileName = $"gongfei-sources-{DateTime.Now:yyyyMMdd}.json",
        };
        if (dialog.ShowDialog() != true) return;
        await RunTransferAsync(async () =>
        {
            await _profileTransfer.ExportSafeAsync(dialog.FileName).ConfigureAwait(true);
            return "来源已导出；文件不包含 API Key、Token 或密码。";
        }).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task ImportProfilesAsync()
    {
        if (_profileTransfer is null || IsMutating) return;
        var dialog = new OpenFileDialog { Title = "导入连接来源", Filter = "JSON 文件|*.json" };
        if (dialog.ShowDialog() != true) return;
        await RunTransferAsync(async () =>
        {
            int count = await _profileTransfer.ImportSafeAsync(dialog.FileName).ConfigureAwait(true);
            await RefreshConnectionsAsync().ConfigureAwait(true);
            return $"已导入 {count} 个远程来源；敏感字段已自动移除。";
        }).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task RestoreProfilesAsync()
    {
        if (_profileTransfer is null || IsMutating) return;
        await RunTransferAsync(async () =>
        {
            bool restored = await _profileTransfer.RestoreLatestAsync().ConfigureAwait(true);
            if (restored) await RefreshConnectionsAsync().ConfigureAwait(true);
            return restored ? "已恢复最近一次来源备份。" : "没有可恢复的来源备份。";
        }).ConfigureAwait(true);
    }

    private async Task RunTransferAsync(Func<Task<string>> operation)
    {
        IsMutating = true;
        try
        {
            MutationNotice = await operation().ConfigureAwait(true);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException)
        {
            MutationNotice = $"来源操作失败：{exception.Message.Replace("\r", " ").Replace("\n", " ")}";
        }
        finally
        {
            IsMutating = false;
        }
    }

    [RelayCommand]
    private void ToggleConnectionDetails(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        bool expand = !card.IsExpanded;
        ClearInlineEditors();
        ConnectionEditor = null;
        foreach (ConnectionCardViewModel candidate in Connections)
        {
            candidate.IsExpanded = false;
            candidate.IsDeleteConfirmationVisible = false;
        }

        card.IsExpanded = expand;
        MutationNotice = string.Empty;
    }

    [RelayCommand]
    private void EditConnection(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        if (!card.CanOperate)
        {
            MutationNotice = "这个旧版遗留本地来源不可编辑；请使用“本机中转”或“局域网中转”两个固定来源。";
            return;
        }

        MutationNotice = string.Empty;
        ClearInlineEditors();
        foreach (ConnectionCardViewModel candidate in Connections)
        {
            candidate.IsExpanded = ReferenceEquals(candidate, card);
            candidate.IsDeleteConfirmationVisible = false;
        }
        card.IsEditing = true;
        string localSub2ApiPath = card.Record.Id == ConnectionProfileIds.LocalMachine
            ? GetConfiguredLocalSub2ApiPath()
            : string.Empty;
        ConnectionEditor = ConnectionEditorViewModel.FromExisting(card.Record, card.IsFixed, localSub2ApiPath);
    }

    [RelayCommand]
    private void SelectLocalSub2ApiPath()
    {
        ConnectionEditorViewModel? editor = ConnectionEditor;
        if (editor is null || !editor.SupportsLocalSub2ApiPath || IsMutating)
        {
            return;
        }

        var dialog = new OpenFolderDialog
        {
            Title = "选择本机中转项目目录",
            Multiselect = false,
        };
        if (Directory.Exists(editor.LocalSub2ApiPath))
        {
            dialog.InitialDirectory = editor.LocalSub2ApiPath;
        }

        if (dialog.ShowDialog() != true)
        {
            return;
        }

        editor.LocalSub2ApiPath = dialog.FolderName;
        MutationNotice = "已选择本机中转项目目录；点击“保存连接”后生效。";
    }

    [RelayCommand]
    private void CancelConnectionEdit()
    {
        if (!IsMutating)
        {
            ClearInlineEditors();
            ConnectionEditor = SelectedLibrarySource is null
                ? null
                : ConnectionEditorViewModel.FromExisting(
                    SelectedLibrarySource.Record,
                    SelectedLibrarySource.IsFixed);
            MutationNotice = string.Empty;
        }
    }

    [RelayCommand]
    private void ImportCurrentClientConfig()
    {
        if (ConnectionEditor is null || IsMutating)
        {
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        try
        {
            ConnectionEditor.ImportCurrentClientConfig(_switchCoordinator.ReadCurrentClientConfig());
            MutationNotice = "已读取当前官方客户端配置；地址已填入，密钥不会显示，保存后才会写入这个来源。";
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            MutationNotice = $"读取当前客户端配置失败：{Sanitize(exception.Message)}";
        }
    }

    [RelayCommand]
    private async Task SaveConnectionAsync()
    {
        ConnectionEditorViewModel? editor = ConnectionEditor;
        if (editor is null || IsMutating)
        {
            return;
        }

        if (!TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        if (string.IsNullOrWhiteSpace(editor.Name) && !editor.IsFixed)
        {
            MutationNotice = "请先填写来源名称。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在安全保存连接…";
            if (editor.SupportsLocalSub2ApiPath && editor.IsLocalSub2ApiPathChanged)
            {
                if (_localGatewayController is null)
                {
                    throw new InvalidOperationException("本机中转控制器尚未初始化，请重新打开工作台。");
                }

                CommandResult pathResult = await _localGatewayController
                    .ConfigureNativeRootAsync(editor.LocalSub2ApiPath, CancellationToken.None)
                    .ConfigureAwait(true);
                if (!pathResult.Success)
                {
                    throw new InvalidOperationException(pathResult.CombinedOutput);
                }
            }

            ConnectionProfileDraft draft = editor.BuildDraft();
            string savedName = draft.Name;
            if (editor.Original is null)
            {
                await connectionEditor.AddAsync(draft);
            }
            else
            {
                await connectionEditor.UpdateAsync(editor.Original.Id, draft);
            }

            ConnectionEditor = null;
            await RefreshAfterMutationAsync(editor.Original is null ? "已新增远程来源。" : "连接配置已保存。");
            SelectedLibrarySource = ExternalSources.FirstOrDefault(source =>
                string.Equals(source.Name, savedName, StringComparison.CurrentCultureIgnoreCase));
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"保存失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    private string GetConfiguredLocalSub2ApiPath()
    {
        string nativeRoot = _localGatewayController?.GetStartupStatus().NativeRoot ?? string.Empty;
        if (string.IsNullOrWhiteSpace(nativeRoot))
        {
            return string.Empty;
        }

        return string.Equals(Path.GetFileName(nativeRoot.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)), "sub2api", StringComparison.OrdinalIgnoreCase)
            ? nativeRoot
            : Path.Combine(nativeRoot, "sub2api");
    }

    [RelayCommand]
    private void RequestDelete(ConnectionCardViewModel? card)
    {
        if (card is null || !card.CanDelete || IsMutating)
        {
            return;
        }

        if (Routing.IsSourceInUse(card.Record.Id))
        {
            MutationNotice = $"“{card.Name}”仍被客户端路由使用。请先在上方把相关客户端切换到其他来源并保存生效。";
            return;
        }

        ClearInlineEditors();
        SelectedLibrarySource = card;
        foreach (ConnectionCardViewModel candidate in Connections)
        {
            candidate.IsExpanded = ReferenceEquals(candidate, card);
            candidate.IsDeleteConfirmationVisible = false;
        }

        card.IsDeleteConfirmationVisible = true;
        MutationNotice = string.Empty;
    }

    [RelayCommand]
    private void CancelDelete(ConnectionCardViewModel? card)
    {
        if (card is not null && !IsMutating)
        {
            card.IsDeleteConfirmationVisible = false;
        }
    }

    [RelayCommand]
    private async Task ConfirmDeleteAsync(ConnectionCardViewModel? card)
    {
        if (card is null || !card.CanDelete || IsMutating)
        {
            return;
        }

        if (!TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        if (Routing.IsSourceInUse(card.Record.Id))
        {
            MutationNotice = $"“{card.Name}”仍被客户端路由使用，不能删除。";
            card.IsDeleteConfirmationVisible = false;
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在删除远程来源…";
            await connectionEditor.DeleteAsync(card.Record.Id);
            card.IsDeleteConfirmationVisible = false;
            SelectedLibrarySource = null;
            ConnectionEditor = null;
            await RefreshAfterMutationAsync("外部来源已删除；项目和历史记录未受影响。");
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"删除失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task SelectConnectionAsync(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        if (!card.CanOperate)
        {
            MutationNotice = "这个旧版遗留本地来源不能作为当前来源。";
            return;
        }

        if (!TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在设为当前来源…";
            ConnectionProfileSelectionGroup group = card.Record.Kind == ConnectionProfileKind.Cloud
                ? ConnectionProfileSelectionGroup.Cloud
                : ConnectionProfileSelectionGroup.Local;
            await connectionEditor.SetSelectedAsync(group, card.Record.Id);
            string groupLabel = group == ConnectionProfileSelectionGroup.Cloud
                ? "云端"
                : "本地/局域网";
            await RefreshAfterMutationAsync(
                $"已将“{card.Name}”设为{groupLabel}当前来源。新的项目对话、非绑定历史续接和高级终端会自动使用它。");
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"选择失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task ApplyConnectionAsync(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        if (!card.CanOperate)
        {
            MutationNotice = "只有外部来源可以加入备用上游池。";
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = card.IsBackupEnabled ? "正在停用备用上游…" : "正在启用备用上游…";
            if (!TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
            {
                return;
            }

            ConnectionProfileRouting current = await connectionEditor.GetRoutingAsync();
            List<string> backupIds = (current.BackupProfileIds ?? []).ToList();
            backupIds.RemoveAll(id => string.Equals(id, card.Record.Id, StringComparison.OrdinalIgnoreCase));
            if (!card.IsBackupEnabled)
            {
                backupIds.Add(card.Record.Id);
            }
            await connectionEditor.SetRoutingAsync(current with
            {
                CodexProfileId = ConnectionProfileIds.LocalMachine,
                ClaudeCodeProfileId = ConnectionProfileIds.LocalMachine,
                GeminiCliProfileId = ConnectionProfileIds.LocalMachine,
                GrokCliProfileId = ConnectionProfileIds.LocalMachine,
                BackupProfileIds = backupIds,
            });
            OperationResult result = await _switchCoordinator.ApplyRoutingAsync();
            if (result.Success)
            {
                MutationNotice = card.IsBackupEnabled
                    ? $"已停用“{card.Name}”备用上游。{Sanitize(result.Summary)}"
                    : $"已将“{card.Name}”加入备用上游末位。{Sanitize(result.Summary)}";
                await RefreshConnectionsAsync();
            }
            else
            {
                MutationNotice = $"应用失败：{Sanitize(result.Summary)}";
            }
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"应用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task ValidateConnectionAsync(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        if (!card.CanOperate)
        {
            MutationNotice = "这个旧版遗留本地来源不能验证。";
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在并行验证来源连通性…";
            OperationResult result = await _switchCoordinator.ValidateSourceAsync(card.Record.Id);
            MutationNotice = result.Success
                ? $"“{card.Name}”验证成功。{Sanitize(result.Summary)}"
                : $"“{card.Name}”验证失败：{Sanitize(result.Summary)}";
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"验证失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private Task MoveBackupUpAsync(ConnectionCardViewModel? card) => MoveBackupAsync(card, -1);

    [RelayCommand]
    private Task MoveBackupDownAsync(ConnectionCardViewModel? card) => MoveBackupAsync(card, 1);

    private Task MoveBackupAsync(ConnectionCardViewModel? card, int offset)
    {
        ConnectionCardViewModel? target = card is null
            ? null
            : BackupConnections.FirstOrDefault(candidate => candidate.BackupOrder == card.BackupOrder + offset);
        return target is null
            ? Task.CompletedTask
            : ReorderBackupAsync(card!, target, insertAfter: offset > 0);
    }

    internal async Task ReorderBackupAsync(
        ConnectionCardViewModel source,
        ConnectionCardViewModel target,
        bool insertAfter)
    {
        if (ReferenceEquals(source, target) || !source.IsBackupEnabled || !target.IsBackupEnabled ||
            IsMutating || _switchCoordinator is null ||
            !TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }
        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            ConnectionProfileRouting current = await connectionEditor.GetRoutingAsync();
            List<string> backupIds = (current.BackupProfileIds ?? []).ToList();
            int sourceIndex = backupIds.FindIndex(id => string.Equals(id, source.Record.Id, StringComparison.OrdinalIgnoreCase));
            int targetIndex = backupIds.FindIndex(id => string.Equals(id, target.Record.Id, StringComparison.OrdinalIgnoreCase));
            if (sourceIndex < 0 || targetIndex < 0)
            {
                return;
            }
            string sourceId = backupIds[sourceIndex];
            backupIds.RemoveAt(sourceIndex);
            targetIndex = backupIds.FindIndex(id => string.Equals(id, target.Record.Id, StringComparison.OrdinalIgnoreCase));
            backupIds.Insert(targetIndex + (insertAfter ? 1 : 0), sourceId);
            await connectionEditor.SetRoutingAsync(current with { BackupProfileIds = backupIds });
            OperationResult result = await _switchCoordinator.ApplyRoutingAsync();
            MutationNotice = result.Success
                ? $"已调整“{source.Name}”的备用顺位。{Sanitize(result.Summary)}"
                : $"顺位更新失败：{Sanitize(result.Summary)}";
            await RefreshConnectionsAsync();
        }
        catch (Exception exception) when (exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"顺位更新失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task ToggleBackupUpstreamAsync()
    {
        if (IsMutating || _switchCoordinator is null ||
            !TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            bool enabled = !IsBackupUpstreamEnabled;
            MutationNotice = enabled ? "正在开启备用上游…" : "正在关闭备用上游…";
            ConnectionProfileRouting current = await connectionEditor.GetRoutingAsync();
            await connectionEditor.SetRoutingAsync(current with { BackupUpstreamEnabled = enabled });
            OperationResult result = await _switchCoordinator.ApplyRoutingAsync();
            if (!result.Success)
            {
                MutationNotice = $"备用上游开关更新失败：{Sanitize(result.Summary)}";
                return;
            }

            IsBackupUpstreamEnabled = enabled;
            MutationNotice = enabled
                ? $"备用上游已开启。个人账号不可调度时将按顺位接续。{Sanitize(result.Summary)}"
                : $"备用上游已关闭，已保留来源和顺位。{Sanitize(result.Summary)}";
            await RefreshConnectionsAsync();
        }
        catch (Exception exception) when (exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"备用上游开关更新失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task RestoreClientBackupAsync()
    {
        if (IsMutating)
        {
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在恢复最近一次官方客户端配置备份…";
            OperationResult result = await _switchCoordinator.RestoreLatestBackupAsync();
            MutationNotice = result.Success
                ? Sanitize(result.Summary)
                : $"恢复失败：{Sanitize(result.Summary)}";
        }
        catch (Exception exception) when (
            exception is InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"恢复失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task OpenConnectionDashboardAsync(ConnectionCardViewModel? card)
    {
        if (card is null || IsMutating)
        {
            return;
        }

        if (!card.CanOperate)
        {
            MutationNotice = "这个旧版遗留本地来源没有可打开的后台入口。";
            return;
        }

        if (IsLocalMachineGateway(card))
        {
            await OpenLocalDashboardAsync(card).ConfigureAwait(true);
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        try
        {
            string? url = _switchCoordinator.GetDashboardUrl(card.Record.Id);
            if (string.IsNullOrWhiteSpace(url) ||
                !Uri.TryCreate(url, UriKind.Absolute, out Uri? uri) ||
                uri.Scheme is not ("http" or "https") ||
                !string.IsNullOrWhiteSpace(uri.UserInfo) ||
                !string.IsNullOrWhiteSpace(uri.Query) ||
                !string.IsNullOrWhiteSpace(uri.Fragment))
            {
                MutationNotice = $"“{card.Name}”尚未配置可打开的后台地址。";
                return;
            }

            Process.Start(new ProcessStartInfo { FileName = uri.AbsoluteUri, UseShellExecute = true });
            MutationNotice = $"已在系统浏览器中打开“{card.Name}”后台。";
        }
        catch (Exception exception) when (exception is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            MutationNotice = $"无法打开后台：{Sanitize(exception.Message)}";
        }
    }

    internal async Task RefreshLocalDashboardActionAsync()
    {
        ConnectionCardViewModel? card = Connections.FirstOrDefault(IsLocalMachineGateway);
        if (card is null)
        {
            return;
        }

        if (_localGatewayController is null)
        {
            SetLocalDashboardUnavailable(card, "未发现本机中转控制入口。请在“编辑”中选择本机中转项目目录。");
            return;
        }

        try
        {
            LocalGatewayStatus status = await _localGatewayController
                .GetStatusAsync(CancellationToken.None)
                .ConfigureAwait(true);
            ApplyLocalDashboardAction(card, status);
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            SetLocalDashboardUnavailable(card, $"无法检查本机中转：{Sanitize(exception.Message)}");
        }
    }

    private async Task OpenLocalDashboardAsync(ConnectionCardViewModel card)
    {
        if (_localGatewayController is null)
        {
            SetLocalDashboardUnavailable(card, "未发现本机中转控制入口。请在“编辑”中选择本机中转项目目录。");
            MutationNotice = card.DashboardActionHint;
            return;
        }

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        try
        {
            IsMutating = true;
            card.SetDashboardAction(false, "正在检查后台…", "正在检查本机中转服务状态。");
            LocalGatewayStatus status = await _localGatewayController
                .GetStatusAsync(CancellationToken.None)
                .ConfigureAwait(true);

            if (!status.WebReachable)
            {
                if (!status.ControlAvailable)
                {
                    SetLocalDashboardUnavailable(card, "未找到可启动的本机中转服务。请在“编辑”中选择本机中转项目目录。");
                    MutationNotice = card.DashboardActionHint;
                    return;
                }

                card.SetDashboardAction(false, "正在启动后台…", "本机中转服务正在启动，完成后会自动打开后台。");
                MutationNotice = "正在启动本机中转服务…";
                CommandResult start = await _localGatewayController
                    .StartAsync(CancellationToken.None)
                    .ConfigureAwait(true);
                if (!start.Success)
                {
                    status = await _localGatewayController.GetStatusAsync(CancellationToken.None).ConfigureAwait(true);
                    ApplyLocalDashboardAction(card, status);
                    MutationNotice = $"启动本机中转失败：{Sanitize(start.CombinedOutput)}";
                    return;
                }

                bool ready = await _localGatewayController
                    .WaitForWebAsync(TimeSpan.FromMinutes(3), CancellationToken.None)
                    .ConfigureAwait(true);
                status = await _localGatewayController.GetStatusAsync(CancellationToken.None).ConfigureAwait(true);
                ApplyLocalDashboardAction(card, status);
                if (!ready || !status.WebReachable)
                {
                    MutationNotice = "本机中转已尝试启动，但后台尚未就绪。请稍后再试或查看中转服务页面的操作日志。";
                    return;
                }
            }

            await _localGatewayController.OpenDashboardAsync(status.WebUrl, CancellationToken.None).ConfigureAwait(true);
            ApplyLocalDashboardAction(card, status);
            MutationNotice = "已在系统浏览器中打开本机后台。";
        }
        catch (Exception exception) when (exception is InvalidOperationException or IOException or UnauthorizedAccessException or System.ComponentModel.Win32Exception)
        {
            MutationNotice = $"无法打开本机后台：{Sanitize(exception.Message)}";
            await RefreshLocalDashboardActionAsync().ConfigureAwait(true);
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    private static bool IsLocalMachineGateway(ConnectionCardViewModel card) =>
        string.Equals(card.Record.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase);

    private static void ApplyLocalDashboardAction(ConnectionCardViewModel card, LocalGatewayStatus status)
    {
        if (status.WebReachable)
        {
            card.SetDashboardAction(true, "打开后台", "本机中转服务已运行，点击后在系统浏览器中打开后台。");
            return;
        }

        if (status.ControlAvailable)
        {
            card.SetDashboardAction(true, "启动并打开后台", "本机中转尚未运行，点击后会启动服务并自动打开后台。");
            return;
        }

        SetLocalDashboardUnavailable(card, "未找到本机中转控制入口。请在“编辑”中选择本机中转项目目录。");
    }

    private static void SetLocalDashboardUnavailable(ConnectionCardViewModel card, string hint) =>
        card.SetDashboardAction(false, "本机后台未配置", hint);

    [RelayCommand]
    private async Task SaveRoutingAsync()
    {
        if (IsMutating)
        {
            return;
        }

        if (!TryGetRouting(out ConnectionProfileRouting? routing) || routing is null ||
            !TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在保存客户端分流组合…";
            await connectionEditor.SetRoutingAsync(routing);
            await SynchronizeUnifiedSelectionAsync(connectionEditor, routing);
            await RefreshAfterMutationAsync("客户端分流组合已保存。各客户端会在下一次“应用分流”时写入官方配置。");
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"保存分流失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task ApplyRoutingAsync()
    {
        if (IsMutating)
        {
            return;
        }

        if (!TryGetRouting(out ConnectionProfileRouting? routing) || routing is null ||
            !TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在保存分流组合并更新本机中转路由…";
            await connectionEditor.SetRoutingAsync(routing);
            await SynchronizeUnifiedSelectionAsync(connectionEditor, routing);
            if (!await DisableModelRoutesBeforeStandardSwitchAsync().ConfigureAwait(true))
            {
                return;
            }
            OperationResult result = await _switchCoordinator.ApplyRoutingAsync();
            await RefreshConnectionsAsync();
            MutationNotice = result.Success
                ? $"本机分流已应用。{Sanitize(result.Summary)}"
                : $"本机分流应用失败：{Sanitize(result.Summary)}";
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"分流应用失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private async Task ValidateRoutingAsync()
    {
        if (IsMutating)
        {
            return;
        }

        if (!TryGetRouting(out ConnectionProfileRouting? routing) || routing is null ||
            !TryGetEditor(out IConnectionProfileEditor? connectionEditor) || connectionEditor is null)
        {
            return;
        }

        if (_switchCoordinator is null)
        {
            MutationNotice = "旧版切换协调器尚未初始化，请重新打开工作台。";
            return;
        }

        await _mutationGate.WaitAsync();
        try
        {
            IsMutating = true;
            MutationNotice = "正在保存分流组合并验证四类客户端…";
            await connectionEditor.SetRoutingAsync(routing);
            OperationResult result = await _switchCoordinator.ValidateRoutingAsync();
            await RefreshConnectionsAsync();
            MutationNotice = result.Success
                ? $"客户端分流验证成功。{Sanitize(result.Summary)}"
                : $"客户端分流验证失败：{Sanitize(result.Summary)}";
        }
        catch (Exception exception) when (
            exception is ArgumentException or InvalidOperationException or IOException or UnauthorizedAccessException)
        {
            MutationNotice = $"分流验证失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsMutating = false;
            _mutationGate.Release();
        }
    }

    private bool TryGetEditor(out IConnectionProfileEditor? editor)
    {
        editor = _editor;
        if (editor is not null)
        {
            return true;
        }

        MutationNotice = "连接编辑器尚未初始化，请重新打开工作台。";
        return false;
    }

    private bool TryGetRouting(out ConnectionProfileRouting? routing)
    {
        if (!Routing.TryBuildRouting(out routing))
        {
            MutationNotice = "请分别为 Codex、Claude Code、Gemini CLI 和 Grok CLI 选择一个有效来源。";
            return false;
        }

        routing = routing! with
        {
            BackupProfileIds = BackupConnections.Any(source => source.IsBackupEnabled)
                ? BackupConnections
                    .Where(source => source.IsBackupEnabled)
                    .OrderBy(source => source.BackupOrder)
                    .Select(source => source.Record.Id)
                    .ToArray()
                : null,
            BackupUpstreamEnabled = IsBackupUpstreamEnabled,
        };

        return true;
    }

    private async Task SynchronizeUnifiedSelectionAsync(
        IConnectionProfileEditor connectionEditor,
        ConnectionProfileRouting routing)
    {
        string? profileId = ConnectionSourceResolver.ResolveUnifiedRoutingProfileId(routing);
        if (string.IsNullOrWhiteSpace(profileId))
        {
            return;
        }

        ConnectionCardViewModel? source = Connections.FirstOrDefault(candidate =>
            string.Equals(candidate.Record.Id, profileId, StringComparison.OrdinalIgnoreCase));
        if (source is null)
        {
            return;
        }

        ConnectionProfileSelectionGroup group = source.Record.Kind == ConnectionProfileKind.Cloud
            ? ConnectionProfileSelectionGroup.Cloud
            : ConnectionProfileSelectionGroup.Local;
        await connectionEditor.SetSelectedAsync(group, source.Record.Id).ConfigureAwait(true);
    }

    private async Task RefreshAfterMutationAsync(string successMessage)
    {
        await RefreshConnectionsAsync();
        MutationNotice = successMessage;
        StatusNotice = successMessage;
    }

    private async Task<bool> DisableModelRoutesBeforeStandardSwitchAsync()
    {
        if (_switchCoordinator is null)
        {
            return true;
        }

        if (IsClaudeGptEnabled)
        {
            OperationResult claudeResult = await _switchCoordinator.DisableClaudeGptRoutingAsync().ConfigureAwait(true);
            if (!claudeResult.Success)
            {
                MutationNotice = $"无法应用普通分流：{Sanitize(claudeResult.Summary)}";
                return false;
            }
            RefreshClaudeGptStateAndSources();
        }

        if (IsCodexClaudeEnabled)
        {
            OperationResult codexResult = await _switchCoordinator.DisableCodexClaudeRoutingAsync().ConfigureAwait(true);
            if (!codexResult.Success)
            {
                MutationNotice = $"无法应用普通分流：{Sanitize(codexResult.Summary)}";
                return false;
            }
            RefreshCodexClaudeStateAndSources();
        }
        return true;
    }

    private void ClearInlineEditors()
    {
        foreach (ConnectionCardViewModel candidate in Connections)
        {
            candidate.IsEditing = false;
        }
    }

    private static string Sanitize(string message)
    {
        if (string.IsNullOrWhiteSpace(message))
        {
            return "发生未知错误。";
        }

        return message.Replace("Secret", "密钥", StringComparison.OrdinalIgnoreCase);
    }
}

public sealed partial class ConnectionCardViewModel
{
    [ObservableProperty]
    private bool dropInsertAfter;

    [ObservableProperty]
    private bool isDeleteConfirmationVisible;

    [ObservableProperty]
    private bool isEditing;

    [ObservableProperty]
    private bool isExpanded;
}

public partial class ConnectionEditorViewModel : ObservableObject
{
    private readonly string _originalLocalSub2ApiPath;

    private ConnectionEditorViewModel(ConnectionProfile? original, bool isFixed, string localSub2ApiPath = "")
    {
        Original = original;
        IsFixed = isFixed;
        Name = original?.Name ?? string.Empty;
        Notes = original?.Notes ?? string.Empty;
        CodexBaseUrl = original?.ClientBaseUrls.GetValueOrDefault(CliKind.Codex)
            ?? (original?.EnabledClients.Contains(CliKind.Codex) == true ? original.BaseUrl : string.Empty);
        ClaudeBaseUrl = original?.ClientBaseUrls.GetValueOrDefault(CliKind.ClaudeCode)
            ?? (original?.EnabledClients.Contains(CliKind.ClaudeCode) == true ? original.BaseUrl : string.Empty);
        GeminiBaseUrl = original?.ClientBaseUrls.GetValueOrDefault(CliKind.GeminiCli)
            ?? (original?.EnabledClients.Contains(CliKind.GeminiCli) == true ? original.BaseUrl : string.Empty);
        GrokBaseUrl = original?.ClientBaseUrls.GetValueOrDefault(CliKind.GrokCli)
            ?? (original?.EnabledClients.Contains(CliKind.GrokCli) == true ? original.BaseUrl : string.Empty);
        DashboardUrl = original?.DashboardUrl ?? string.Empty;
        LocalSub2ApiPath = localSub2ApiPath;
        _originalLocalSub2ApiPath = localSub2ApiPath;
    }

    public ConnectionProfile? Original { get; }

    public bool IsFixed { get; }

    public bool IsNew => Original is null;

    public bool SupportsDashboardAddress => Original?.Kind is ConnectionProfileKind.Local or ConnectionProfileKind.Lan;

    public bool ShowsNotesEditor =>
        !string.Equals(Original?.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase);

    public bool ShowsDashboardAddressEditor => Original?.Kind == ConnectionProfileKind.Lan;

    public bool SupportsLocalSub2ApiPath =>
        Original?.Kind == ConnectionProfileKind.Local &&
        string.Equals(Original.Id, ConnectionProfileIds.LocalMachine, StringComparison.OrdinalIgnoreCase);

    public bool IsLocalSub2ApiPathChanged =>
        SupportsLocalSub2ApiPath &&
        !string.Equals(
            NormalizePath(LocalSub2ApiPath),
            NormalizePath(_originalLocalSub2ApiPath),
            StringComparison.OrdinalIgnoreCase);

    public string LocalGatewayPathDisplay => Regex.Replace(
        LocalSub2ApiPath,
        "sub2api",
        "本机中转",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);

    public string DashboardAddressLabel => Original?.Kind switch
    {
        ConnectionProfileKind.Local => "本机后台地址",
        ConnectionProfileKind.Lan => "局域网后台地址",
        _ => string.Empty,
    };

    public string DashboardAddressToolTip => Original?.Kind switch
    {
        ConnectionProfileKind.Local => "例如 http://127.0.0.1:8080/dashboard",
        ConnectionProfileKind.Lan => "例如 http://192.168.x.x:8080/dashboard",
        _ => string.Empty,
    };

    public string DashboardAddressHint => Original?.Kind switch
    {
        ConnectionProfileKind.Local => "用于在浏览器中打开这台电脑上的管理后台。生产部署与 API 共用同一个 8080 端口。",
        ConnectionProfileKind.Lan => "用于在浏览器中打开另一台电脑的管理后台。生产部署与 API 共用同一个端口。",
        _ => string.Empty,
    };

    public string EditorTitle => IsNew ? "新增远程来源" : "编辑连接来源";

    public string FixedNameHint => IsFixed ? "系统固定来源，名称不可修改。" : "远程来源名称可编辑。";

    public string CodexCredentialHint => GetCredentialHint(CliKind.Codex);

    public string ClaudeCredentialHint => GetCredentialHint(CliKind.ClaudeCode);

    public string GeminiCredentialHint => GetCredentialHint(CliKind.GeminiCli);

    public string GrokCredentialHint => GetCredentialHint(CliKind.GrokCli);

    [ObservableProperty]
    private string name = string.Empty;

    [ObservableProperty]
    private string notes = string.Empty;

    [ObservableProperty]
    private string codexBaseUrl = string.Empty;

    [ObservableProperty]
    private string claudeBaseUrl = string.Empty;

    [ObservableProperty]
    private string geminiBaseUrl = string.Empty;

    [ObservableProperty]
    private string grokBaseUrl = string.Empty;

    [ObservableProperty]
    private string dashboardUrl = string.Empty;

    [ObservableProperty]
    private string localSub2ApiPath = string.Empty;

    partial void OnLocalSub2ApiPathChanged(string value) =>
        OnPropertyChanged(nameof(LocalGatewayPathDisplay));

    [ObservableProperty]
    private string codexSecret = string.Empty;

    [ObservableProperty]
    private string claudeSecret = string.Empty;

    [ObservableProperty]
    private string geminiSecret = string.Empty;

    [ObservableProperty]
    private string grokSecret = string.Empty;

    [ObservableProperty]
    private bool clearCodexSecret;

    [ObservableProperty]
    private bool clearClaudeSecret;

    [ObservableProperty]
    private bool clearGeminiSecret;

    [ObservableProperty]
    private bool clearGrokSecret;

    public static ConnectionEditorViewModel CreateNew() =>
        new(original: null, isFixed: false);

    public static ConnectionEditorViewModel FromExisting(ConnectionProfile original, bool isFixed) =>
        new(original ?? throw new ArgumentNullException(nameof(original)), isFixed);

    public static ConnectionEditorViewModel FromExisting(ConnectionProfile original, bool isFixed, string localSub2ApiPath) =>
        new(original ?? throw new ArgumentNullException(nameof(original)), isFixed, localSub2ApiPath);

    public void SetEnteredSecret(CliKind client, string value)
    {
        switch (client)
        {
            case CliKind.Codex:
                CodexSecret = value;
                break;
            case CliKind.ClaudeCode:
                ClaudeSecret = value;
                break;
            case CliKind.GeminiCli:
                GeminiSecret = value;
                break;
            case CliKind.GrokCli:
                GrokSecret = value;
                break;
        }
    }

    internal void ImportCurrentClientConfig(ImportedLiveConfig current)
    {
        ArgumentNullException.ThrowIfNull(current);

        Import(current.Codex, value =>
        {
            CodexBaseUrl = value.BaseUrl;
            CodexSecret = value.Secret;
            ClearCodexSecret = false;
        });
        Import(current.Claude, value =>
        {
            ClaudeBaseUrl = value.BaseUrl;
            ClaudeSecret = value.Secret;
            ClearClaudeSecret = false;
        });
        Import(current.Gemini, value =>
        {
            GeminiBaseUrl = value.BaseUrl;
            GeminiSecret = value.Secret;
            ClearGeminiSecret = false;
        });
        Import(current.Grok, value =>
        {
            GrokBaseUrl = value.BaseUrl;
            GrokSecret = value.Secret;
            ClearGrokSecret = false;
        });
    }

    public ConnectionProfileDraft BuildDraft() => new(
        IsFixed ? Original!.Name : Name.Trim(),
        Original?.Kind ?? ConnectionProfileKind.Cloud,
        string.IsNullOrWhiteSpace(Notes) ? null : Notes.Trim(),
        new ConnectionClientDraft(CodexBaseUrl.Trim(), ToSecretChange(CodexSecret, ClearCodexSecret)),
        new ConnectionClientDraft(ClaudeBaseUrl.Trim(), ToSecretChange(ClaudeSecret, ClearClaudeSecret)),
        new ConnectionClientDraft(GeminiBaseUrl.Trim(), ToSecretChange(GeminiSecret, ClearGeminiSecret)),
        SupportsDashboardAddress && !string.IsNullOrWhiteSpace(DashboardUrl)
            ? DashboardUrl.Trim()
            : null,
        new ConnectionClientDraft(GrokBaseUrl.Trim(), ToSecretChange(GrokSecret, ClearGrokSecret)));

    private static ConnectionSecretChange ToSecretChange(string typedValue, bool clear)
    {
        if (clear)
        {
            return ConnectionSecretChange.Clear;
        }

        return string.IsNullOrWhiteSpace(typedValue)
            ? ConnectionSecretChange.Keep
            : ConnectionSecretChange.Replace(typedValue);
    }

    private static string NormalizePath(string? path)
    {
        if (string.IsNullOrWhiteSpace(path))
        {
            return string.Empty;
        }

        try
        {
            return Path.GetFullPath(path).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        }
        catch
        {
            return path.Trim().TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        }
    }

    private static void Import(ClientProfile? source, Action<ClientProfile> apply)
    {
        if (source is not null)
        {
            apply(source);
        }
    }

    private string GetCredentialHint(CliKind client)
    {
        if (Original?.ClientCredentialHints.GetValueOrDefault(client) is not { } hint)
        {
            return "未保存密钥";
        }

        return $"已保存：{hint.MaskedPreview} · 指纹 {hint.Fingerprint}";
    }
}

/// <summary>
/// A safe edit model for client routing. It carries profile IDs only;
/// the underlying URLs and credentials stay in profiles.json until the legacy
/// switcher writes the official client configuration.
/// </summary>
public partial class ConnectionRoutingViewModel : ObservableObject
{
    public ObservableCollection<ConnectionCardViewModel> AvailableSources { get; } = [];

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsComplete))]
    [NotifyPropertyChangedFor(nameof(Summary))]
    [NotifyPropertyChangedFor(nameof(HasPendingChanges))]
    private ConnectionCardViewModel? codexSource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsComplete))]
    [NotifyPropertyChangedFor(nameof(Summary))]
    [NotifyPropertyChangedFor(nameof(HasPendingChanges))]
    private ConnectionCardViewModel? claudeCodeSource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsComplete))]
    [NotifyPropertyChangedFor(nameof(Summary))]
    [NotifyPropertyChangedFor(nameof(HasPendingChanges))]
    private ConnectionCardViewModel? geminiCliSource;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsComplete))]
    [NotifyPropertyChangedFor(nameof(Summary))]
    [NotifyPropertyChangedFor(nameof(HasPendingChanges))]
    private ConnectionCardViewModel? grokCliSource;

    private string? _appliedCodexProfileId;
    private string? _appliedClaudeProfileId;
    private string? _appliedGeminiProfileId;
    private string? _appliedGrokProfileId;

    public string AppliedSummary { get; private set; } = "尚未读取已应用分流";

    public string AppliedCodexName { get; private set; } = "Codex";

    public string AppliedClaudeName { get; private set; } = "Claude";

    public string AppliedGeminiName { get; private set; } = "Gemini";

    public string AppliedGrokName { get; private set; } = "Grok";

    public bool HasPendingChanges => IsComplete &&
        (!string.Equals(CodexSource?.Record.Id, _appliedCodexProfileId, StringComparison.OrdinalIgnoreCase) ||
         !string.Equals(ClaudeCodeSource?.Record.Id, _appliedClaudeProfileId, StringComparison.OrdinalIgnoreCase) ||
         !string.Equals(GeminiCliSource?.Record.Id, _appliedGeminiProfileId, StringComparison.OrdinalIgnoreCase) ||
         !string.Equals(GrokCliSource?.Record.Id, _appliedGrokProfileId, StringComparison.OrdinalIgnoreCase));

    public bool IsComplete => CodexSource is not null &&
                              ClaudeCodeSource is not null &&
                              GeminiCliSource is not null &&
                              GrokCliSource is not null;

    public string Summary => IsComplete
        ? $"{(HasPendingChanges ? "待应用" : "当前已应用")}：Codex → {CodexSource!.Name}；Claude Code → {ClaudeCodeSource!.Name}；Gemini CLI → {GeminiCliSource!.Name}；Grok CLI → {GrokCliSource!.Name}"
        : "请分别选择四个官方客户端使用的来源。";

    internal void ApplySnapshot(
        IEnumerable<ConnectionCardViewModel> sources,
        ConnectionProfileRouting? routing)
    {
        ArgumentNullException.ThrowIfNull(sources);

        string? previousCodexId = routing?.CodexProfileId ?? CodexSource?.Record.Id;
        string? previousClaudeId = routing?.ClaudeCodeProfileId ?? ClaudeCodeSource?.Record.Id;
        string? previousGeminiId = routing?.GeminiCliProfileId ?? GeminiCliSource?.Record.Id;
        string? previousGrokId = !string.IsNullOrWhiteSpace(routing?.GrokCliProfileId) ? routing!.GrokCliProfileId : routing?.GeminiCliProfileId ?? GrokCliSource?.Record.Id;

        AvailableSources.Clear();
        foreach (ConnectionCardViewModel source in sources.Where(source =>
                     source.CanOperate && source.Record.Kind == ConnectionProfileKind.Cloud))
        {
            AvailableSources.Add(source);
        }

        ConnectionCardViewModel? fallback = AvailableSources.FirstOrDefault();
        CodexSource = FindSource(previousCodexId) ?? fallback;
        ClaudeCodeSource = FindSource(previousClaudeId) ?? fallback;
        GeminiCliSource = FindSource(previousGeminiId) ?? fallback;
        GrokCliSource = FindSource(previousGrokId) ?? fallback;
        _appliedCodexProfileId = CodexSource?.Record.Id;
        _appliedClaudeProfileId = ClaudeCodeSource?.Record.Id;
        _appliedGeminiProfileId = GeminiCliSource?.Record.Id;
        _appliedGrokProfileId = GrokCliSource?.Record.Id;
        AppliedSummary = IsComplete
            ? $"Codex → {CodexSource!.Name}；Claude Code → {ClaudeCodeSource!.Name}；Gemini CLI → {GeminiCliSource!.Name}；Grok CLI → {GrokCliSource!.Name}"
            : "尚未配置完整分流";
        AppliedCodexName = CodexSource?.Name ?? "Codex";
        AppliedClaudeName = ClaudeCodeSource?.Name ?? "Claude";
        AppliedGeminiName = GeminiCliSource?.Name ?? "Gemini";
        AppliedGrokName = GrokCliSource?.Name ?? "Grok";
        OnPropertyChanged(nameof(IsComplete));
        OnPropertyChanged(nameof(Summary));
        OnPropertyChanged(nameof(AppliedSummary));
        OnPropertyChanged(nameof(AppliedCodexName));
        OnPropertyChanged(nameof(AppliedClaudeName));
        OnPropertyChanged(nameof(AppliedGeminiName));
        OnPropertyChanged(nameof(AppliedGrokName));
        OnPropertyChanged(nameof(HasPendingChanges));
    }

    internal bool TryBuildRouting(out ConnectionProfileRouting? routing)
    {
        if (!IsComplete)
        {
            routing = null;
            return false;
        }

        routing = new ConnectionProfileRouting(
            CodexSource!.Record.Id,
            ClaudeCodeSource!.Record.Id,
            GeminiCliSource!.Record.Id,
            GrokCliSource!.Record.Id);
        return true;
    }

    internal bool IsSourceInUse(string profileId)
    {
        if (string.IsNullOrWhiteSpace(profileId))
        {
            return false;
        }

        return string.Equals(CodexSource?.Record.Id, profileId, StringComparison.OrdinalIgnoreCase) ||
               string.Equals(ClaudeCodeSource?.Record.Id, profileId, StringComparison.OrdinalIgnoreCase) ||
               string.Equals(GeminiCliSource?.Record.Id, profileId, StringComparison.OrdinalIgnoreCase) ||
               string.Equals(GrokCliSource?.Record.Id, profileId, StringComparison.OrdinalIgnoreCase);
    }

    private ConnectionCardViewModel? FindSource(string? id) =>
        string.IsNullOrWhiteSpace(id)
            ? null
            : AvailableSources.FirstOrDefault(source =>
                string.Equals(source.Record.Id, id, StringComparison.OrdinalIgnoreCase));
}







