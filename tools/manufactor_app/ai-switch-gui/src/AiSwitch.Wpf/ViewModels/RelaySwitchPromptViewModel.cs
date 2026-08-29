using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Injection.Sentinel;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// The card offering the relay switch when the official account runs out of allowance.
/// </summary>
/// <remarks>
/// Wording matters here, so it is kept in one place and unit tested. Two points must
/// always be stated, because both drive the user's decision:
/// <list type="bullet">
/// <item>local conversation history and memory survive the switch — they are files
/// under the Codex home directory and switching only rewrites the provider fields;</item>
/// <item>cloud-side features tied to the official account (cloud tasks, remote
/// workspaces) stop working while routed through the relay.</item>
/// </list>
/// </remarks>
// Must be public: WPF data binding cannot reach internal members, and a failed
// binding silently leaves Visibility at its default (Visible) — see
// SignInPromptViewModel for the incident this pattern was learned from.
public sealed partial class RelaySwitchPromptViewModel : ObservableObject
{
    private readonly Func<CancellationToken, Task<RelaySwitchOutcome>> _accept;
    private readonly Action _decline;

    internal RelaySwitchPromptViewModel(
        Func<CancellationToken, Task<RelaySwitchOutcome>> accept,
        Action decline)
    {
        _accept = accept ?? throw new ArgumentNullException(nameof(accept));
        _decline = decline ?? throw new ArgumentNullException(nameof(decline));
    }

    [ObservableProperty]
    private bool isVisible;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanAccept))]
    private bool isBusy;

    public bool CanAccept => !IsBusy;

    [ObservableProperty]
    private string title = string.Empty;

    [ObservableProperty]
    private string message = string.Empty;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasResetHint))]
    private string resetHint = string.Empty;

    public bool HasResetHint => !string.IsNullOrWhiteSpace(ResetHint);

    [ObservableProperty]
    private string acceptLabel = "切换到共飞中转";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasOutcomeMessage))]
    private string? outcomeMessage;

    public bool HasOutcomeMessage => !string.IsNullOrWhiteSpace(OutcomeMessage);

    /// <summary>Shows the card for a prompt raised by the orchestrator.</summary>
    public void Show(RelaySwitchPrompt prompt)
    {
        ArgumentNullException.ThrowIfNull(prompt);

        (Title, Message, AcceptLabel) = Describe(prompt);
        ResetHint = string.IsNullOrWhiteSpace(prompt.Snapshot?.Facts.ResetText)
            ? string.Empty
            : $"官方额度提示：{prompt.Snapshot!.Facts.ResetText}";
        OutcomeMessage = null;
        IsVisible = true;
    }

    internal static (string Title, string Message, string AcceptLabel) Describe(RelaySwitchPrompt prompt)
    {
        const string preserved = "本机聊天记录与记忆会完整保留，可继续当前会话。";
        const string degraded = "切换后与官方账号绑定的云端任务会暂停。";

        return prompt.Reason switch
        {
            RelaySwitchReason.LimitReached => (
                "官方额度已用尽",
                $"官方账号当前无法继续对话。{preserved}{degraded}",
                "切换到共飞中转"),
            RelaySwitchReason.ApproachingLimit => (
                "官方额度接近上限",
                $"官方账号即将达到用量上限，可以先切换避免中断。{preserved}{degraded}",
                "提前切换到共飞中转"),
            RelaySwitchReason.RoutingLost => (
                "中转路由被重置",
                "官方客户端重写了配置，中转路由已失效——这通常发生在重新登录之后。"
                    + $"{preserved}",
                "重新应用共飞中转"),
            _ => ("共飞中转", preserved, "切换到共飞中转"),
        };
    }

    [RelayCommand]
    private async Task AcceptAsync()
    {
        if (IsBusy)
        {
            return;
        }

        IsBusy = true;
        try
        {
            RelaySwitchOutcome outcome = await _accept(CancellationToken.None).ConfigureAwait(true);
            OutcomeMessage = outcome.Summary;

            // Keep a failure on screen: the user has to know the switch did not happen.
            IsVisible = !outcome.Success;
        }
        finally
        {
            IsBusy = false;
        }
    }

    [RelayCommand]
    private void Dismiss()
    {
        _decline();
        IsVisible = false;
    }
}
