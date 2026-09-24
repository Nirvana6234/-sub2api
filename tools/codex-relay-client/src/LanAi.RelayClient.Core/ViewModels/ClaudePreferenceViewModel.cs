using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// The account's Claude model and thinking level — one server-side setting with two
/// consumers, so exactly one owner.
/// </summary>
/// <remarks>
/// <para>
/// The consumers: Codex, when its own group is a Claude one (the server routes by this
/// preference, F5.4), and Claude Code through the plug-in binding, which is handed the
/// model only — the thinking level is not passed to the editor.
/// </para>
/// <para>
/// Both the Codex page and the Claude page bind this one instance. Two view models each
/// loading and saving it would be the shape of the 09-22 bug, where a poll dragged a
/// value the user had just changed back to a stale copy.
/// </para>
/// </remarks>
public sealed partial class ClaudePreferenceViewModel : ObservableObject
{
    public static IReadOnlyList<string> ClaudeModels { get; } =
        ["claude-sonnet-5", "claude-opus-5"];

    public static IReadOnlyList<string> ClaudeThinkingLevels { get; } =
        ["关闭", "低", "中", "高", "极高"];

    private static readonly string[] _thinkingLevelKeys = ["off", "low", "medium", "high", "max"];

    private const int DefaultClaudeThinkingLevelIndex = 2;

    private readonly IRelayServerClient _client;
    private readonly RelaySessionManager _session;
    private readonly SafeAsyncRunner _safeAsync;

    /// <summary>True while values read from the server are being applied, so they are not saved back.</summary>
    private bool _loading;

    internal ClaudePreferenceViewModel(IRelayServerClient client, RelaySessionManager session, SafeAsyncRunner safeAsync)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _safeAsync = safeAsync ?? throw new ArgumentNullException(nameof(safeAsync));
    }

    /// <summary>
    /// Raised when the model changes in a way a consumer must act on: the user picked one,
    /// or a load from the server finished.
    /// </summary>
    internal event Action? Changed;

    /// <summary>
    /// Whether the preference has been read from the server at least once. Until then the
    /// values are placeholders, and a consumer writing them into the user's files would
    /// only rewrite them a moment later.
    /// </summary>
    internal bool IsLoaded { get; private set; }

    [ObservableProperty]
    private string selectedClaudeModel = "claude-sonnet-5";

    partial void OnSelectedClaudeModelChanged(string value)
    {
        if (_loading)
        {
            return;
        }

        _ = _safeAsync.RunAsync(SaveAsync);
        Changed?.Invoke();
    }

    [ObservableProperty]
    private string selectedClaudeThinkingLevel = ClaudeThinkingLevels[DefaultClaudeThinkingLevelIndex];

    partial void OnSelectedClaudeThinkingLevelChanged(string value) =>
        _ = _loading ? Task.CompletedTask : _safeAsync.RunAsync(SaveAsync);

    internal async Task LoadAsync()
    {
        _loading = true;
        try
        {
            var token = await _session.GetAccessTokenAsync().ConfigureAwait(true);
            var pref = await _client.GetClaudePreferenceAsync(token);
            SelectedClaudeModel = pref.Model;
            var idx = System.Array.IndexOf(_thinkingLevelKeys, pref.ThinkingLevel);
            SelectedClaudeThinkingLevel = idx >= 0
                ? ClaudeThinkingLevels[idx]
                : ClaudeThinkingLevels[DefaultClaudeThinkingLevelIndex];
        }
        catch { /* best-effort */ }
        finally
        {
            _loading = false;
            IsLoaded = true;
        }

        Changed?.Invoke();
    }

    private async Task SaveAsync()
    {
        try
        {
            var token = await _session.GetAccessTokenAsync().ConfigureAwait(true);
            var modelIdx = System.Array.IndexOf(ClaudeModels.ToArray(), SelectedClaudeModel);
            if (modelIdx < 0) modelIdx = 0;
            var levelIdx = System.Array.IndexOf(
                ClaudeThinkingLevels.ToArray(),
                SelectedClaudeThinkingLevel);
            if (levelIdx < 0) levelIdx = 0;
            await _client.SetClaudePreferenceAsync(token, ClaudeModels[modelIdx], _thinkingLevelKeys[levelIdx]);
        }
        catch { /* best-effort */ }
    }

    /// <summary>Forgets that anything was loaded; the next account reads its own.</summary>
    internal void Reset() => IsLoaded = false;
}
