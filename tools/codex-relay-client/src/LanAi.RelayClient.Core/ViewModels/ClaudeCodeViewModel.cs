using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// Claude Code and the editor extension built on it — the Claude page.
/// </summary>
/// <remarks>
/// A path of its own, entirely separate from the Codex group: the plug-ins get their own
/// Claude group, chosen here, and their own binding on the relay
/// (<c>LocalPawRelay.SetClaudeGroup</c>). Codex keeps routing through whatever group its
/// own dropdown is on — including a Claude one; F5.4's direct routing is untouched.
/// </remarks>
public sealed partial class ClaudeCodeViewModel : ObservableObject
{
    private readonly ICodexStartup _codex;
    private readonly IGroupPreferenceStore _preferences;
    private readonly IPluginSupportPreferenceStore _pluginSupportPreferences;
    private readonly ClaudePreferenceViewModel _preference;
    private readonly SafeAsyncRunner _safeAsync;

    private bool _applyingKnownPluginSupportState;
    private bool _applyingKnownClaudePluginGroupState;

    /// <summary>The request last applied successfully, so an unchanged input does no file work.</summary>
    private PluginSupportRequest? _lastPluginRequest;

    /// <summary>Set while Claude Code goes straight to Anthropic with one of the user's own accounts.</summary>
    private LocalProxyTarget? _localProxy;

    internal ClaudeCodeViewModel(
        ICodexStartup codex,
        IGroupPreferenceStore preferences,
        IPluginSupportPreferenceStore pluginSupportPreferences,
        ClaudePreferenceViewModel preference,
        SafeAsyncRunner safeAsync)
    {
        _codex = codex ?? throw new ArgumentNullException(nameof(codex));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _pluginSupportPreferences = pluginSupportPreferences ?? throw new ArgumentNullException(nameof(pluginSupportPreferences));
        _preference = preference ?? throw new ArgumentNullException(nameof(preference));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));

        _preference.Changed += RequestSync;
        SetPluginSupportWithoutApplying(_pluginSupportPreferences.Load() ?? false);
    }

    /// <summary>
    /// 接入 Claude Code. Off by default: unlike the Codex group, this writes to files
    /// outside this client (~/.claude/settings.json and the editor's own settings), so the
    /// first launch asks rather than assumes.
    /// </summary>
    [ObservableProperty]
    private bool pluginSupportEnabled;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasPluginSupportStatus))]
    private string pluginSupportStatus = string.Empty;

    public bool HasPluginSupportStatus => !string.IsNullOrEmpty(PluginSupportStatus);

    /// <summary>Whether the last sync left Claude Code pointed at the relay.</summary>
    [ObservableProperty]
    private bool pluginSupportActive;

    /// <summary>Only the loopback relay can serve an editor; greys the box out otherwise.</summary>
    public bool CanConfigurePluginSupport => _codex.UsesLocalTransport;

    /// <summary>The Claude groups the plug-ins may be pointed at — every Claude-platform group.</summary>
    public ObservableCollection<GroupItemViewModel> ClaudePluginGroups { get; } = [];

    public bool HasClaudePluginGroups => ClaudePluginGroups.Count > 0;

    [ObservableProperty]
    private GroupItemViewModel? selectedClaudePluginGroup;

    partial void OnPluginSupportEnabledChanged(bool value)
    {
        if (_applyingKnownPluginSupportState)
        {
            return;
        }

        _pluginSupportPreferences.Save(value);
        RequestSync();
    }

    private void SetPluginSupportWithoutApplying(bool value)
    {
        _applyingKnownPluginSupportState = true;
        try
        {
            PluginSupportEnabled = value;
        }
        finally
        {
            _applyingKnownPluginSupportState = false;
        }
    }

    partial void OnSelectedClaudePluginGroupChanged(GroupItemViewModel? value)
    {
        if (!_applyingKnownClaudePluginGroupState && value is not null)
        {
            _preferences.SaveClaudeGroup(value.Id);
        }

        if (value is not null)
        {
            // Loads the account's Claude model/thinking-level preference; once that
            // completes it raises Changed, which syncs.
            _ = _safeAsync.RunAsync(_preference.LoadAsync);
        }
        else
        {
            RequestSync();
        }
    }

    private void SelectClaudePluginGroupWithoutApplying(GroupItemViewModel? group)
    {
        _applyingKnownClaudePluginGroupState = true;
        try
        {
            SelectedClaudePluginGroup = group;
        }
        finally
        {
            _applyingKnownClaudePluginGroupState = false;
        }
    }

    /// <summary>
    /// Rebuilds the Claude-group candidate list from the catalog and restores the
    /// remembered choice — or, failing that, the only candidate there is, so the picker is
    /// never left empty for an account with just one Claude group.
    /// </summary>
    /// <remarks>
    /// From the catalog, not from the Codex list: which groups Codex can use is a
    /// different question from which the plug-ins can.
    /// </remarks>
    internal void RebuildGroups(GroupCatalog catalog, string? serverUtcOffset)
    {
        ClaudePluginGroups.Clear();
        foreach (RelayGroup group in catalog.Groups.Where(g => GroupCatalog.IsClaudePlatform(g.Platform)))
        {
            ClaudePluginGroups.Add(catalog.CreateItem(group, serverUtcOffset));
        }
        OnPropertyChanged(nameof(HasClaudePluginGroups));

        long? saved = _preferences.LoadClaudeGroup();
        GroupItemViewModel? restored = saved is > 0
            ? ClaudePluginGroups.FirstOrDefault(g => g.Id == saved)
            : null;
        restored ??= ClaudePluginGroups.Count == 1 ? ClaudePluginGroups[0] : null;
        SelectClaudePluginGroupWithoutApplying(restored);
    }

    /// <summary>
    /// Claude Code's local proxy, or null for the relay server. With one set, Claude Code is
    /// ready without a Claude group — the group picker no longer decides where it goes.
    /// </summary>
    internal void SetLocalProxy(LocalProxyTarget? target)
    {
        _localProxy = target;
        OnPropertyChanged(nameof(UsesLocalProxy));
        OnPropertyChanged(nameof(CanChooseGroup));
        RequestSync();
    }

    public bool UsesLocalProxy => _localProxy is not null;

    /// <summary>The Claude group only matters while Claude Code goes through the relay server.</summary>
    public bool CanChooseGroup => _localProxy is null;

    internal void RequestSync()
    {
        if (!_codex.UsesLocalTransport)
        {
            return;
        }

        _ = _safeAsync.RunAsync(SyncPluginSupportAsync);
    }

    internal async Task SyncPluginSupportAsync()
    {
        if (!_codex.UsesLocalTransport)
        {
            return;
        }

        GroupItemViewModel? claudeGroup = SelectedClaudePluginGroup;
        LocalProxyTarget? localProxy = _localProxy;
        bool routed = claudeGroup is not null || localProxy is not null;
        if (routed && !_preference.IsLoaded)
        {
            return;
        }

        var request = new PluginSupportRequest(
            PluginSupportEnabled,
            claudeGroup?.Id,
            claudeGroup?.Name,
            routed ? _preference.SelectedClaudeModel : null,
            localProxy?.AccountId);
        if (request == _lastPluginRequest)
        {
            return;
        }

        PluginSupportResult result = await _codex.SyncPluginSupportAsync(request).ConfigureAwait(true);

        // Only settled outcomes are remembered. A problem or a not-yet-applicable answer must be
        // retried by the next trigger rather than trusted.
        _lastPluginRequest = result.State is PluginSupportState.Off
            or PluginSupportState.NoGroupChosen
            or PluginSupportState.Active
            ? request
            : null;
        PluginSupportStatus = localProxy is not null && result.State == PluginSupportState.Active
            ? $"Claude Code 已接入，正在使用本地代理「{localProxy.Name}」，客户端运行期间可用，退出时自动还原。"
            : DescribePluginSupport(result, hasClaudeGroup: HasClaudePluginGroups);
        PluginSupportActive = result.State == PluginSupportState.Active;
    }

    internal static string DescribePluginSupport(PluginSupportResult result, bool hasClaudeGroup) =>
        result.State switch
        {
            PluginSupportState.Active => "Claude Code 已接入，客户端运行期间可用，退出时自动还原。",
            PluginSupportState.NoGroupChosen => hasClaudeGroup
                ? "请选择一个 Claude 分组以接入 Claude Code。"
                : "该账号没有可用的 Claude 分组。",
            PluginSupportState.Problem => result.Note ?? "Claude Code 接入失败。",
            _ => string.Empty,
        };

    /// <summary>Drops everything belonging to the account that just signed out.</summary>
    /// <remarks>The checkbox itself is this machine's choice, not the account's, so it stays.</remarks>
    internal void Reset()
    {
        _lastPluginRequest = null;
        _localProxy = null;
        OnPropertyChanged(nameof(UsesLocalProxy));
        OnPropertyChanged(nameof(CanChooseGroup));
        PluginSupportStatus = string.Empty;
        PluginSupportActive = false;
        ClaudePluginGroups.Clear();
        OnPropertyChanged(nameof(HasClaudePluginGroups));
        SelectClaudePluginGroupWithoutApplying(null);
    }
}
