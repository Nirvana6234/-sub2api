using System.Collections.ObjectModel;
using System.Globalization;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.DesktopSync;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>One conversation in the selection list.</summary>
public sealed partial class SyncSessionItem : ObservableObject
{
    internal SyncSessionItem(string threadId, string title, string? cwd, string detail, PermissionMode permission, bool missing)
    {
        ThreadId = threadId;
        Title = title;
        Cwd = cwd;
        Detail = detail;
        Permission = permission;
        IsMissing = missing;
    }

    public string ThreadId { get; }

    public string Title { get; }

    public string? Cwd { get; }

    /// <summary>Status and last activity, e.g. 「空闲 · 3 分钟前」.</summary>
    public string Detail { get; }

    internal PermissionMode Permission { get; }

    /// <summary>Selected earlier but no longer in the desktop app: can only be removed.</summary>
    public bool IsMissing { get; }

    public string PermissionText => Permission switch
    {
        PermissionMode.Auto => "自动 · 越界操作需在电脑上确认",
        PermissionMode.SandboxedNoApproval => "沙箱 · 越界操作会直接失败",
        _ => "完全访问 · 手机指令将直接执行",
    };

    /// <summary>Full access, or unknown (treated the same): the one to warn about.</summary>
    public bool IsFullAccess => Permission is PermissionMode.FullAccess or PermissionMode.Unknown;

    [ObservableProperty]
    private bool isSelected;

    /// <summary>Enabled unless the selection is full and this one is not in it.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(DisabledReason))]
    private bool canToggle = true;

    /// <summary>Why the box is disabled, as a tooltip; null (no tooltip) while it is enabled.</summary>
    public string? DisabledReason => CanToggle ? null : $"最多同步 {DesktopSyncAgent.MaxSyncedSessions} 个会话，请先取消一个";
}

/// <summary>One approved phone.</summary>
public sealed class SyncPhoneItem
{
    internal SyncPhoneItem(long pairingId, string label, DateTimeOffset approvedAt)
    {
        PairingId = pairingId;
        Label = label;
        ApprovedText = $"{approvedAt.ToLocalTime():yyyy-MM-dd HH:mm} 确认";
    }

    public long PairingId { get; }

    public string Label { get; }

    public string ApprovedText { get; }
}

/// <summary>One line of the audit list.</summary>
public sealed class SyncAuditItem
{
    internal SyncAuditItem(SyncAuditEntry entry)
    {
        Text = $"{entry.At.ToLocalTime():MM-dd HH:mm:ss}  {entry.PhoneLabel ?? "手机"}  {CommandName(entry.Command)}  {Outcome(entry.Outcome)}"
            + (entry.Summary is null ? string.Empty : $"：{entry.Summary}");
        IsRefusal = entry.Outcome is not ("ok" or "queued");
    }

    public string Text { get; }

    public bool IsRefusal { get; }

    private static string CommandName(string command) => command switch
    {
        DesktopSyncCommands.ListSessions => "查看列表",
        DesktopSyncCommands.OpenSession => "打开会话",
        DesktopSyncCommands.History => "翻看历史",
        DesktopSyncCommands.Detail => "查看详情",
        DesktopSyncCommands.SendMessage => "发送消息",
        DesktopSyncCommands.Navigate => "在电脑上打开",
        _ => command,
    };

    private static string Outcome(string outcome) => outcome switch
    {
        "ok" => "成功",
        "queued" => "排队中",
        "disabled" => "已拒绝（同步已关闭）",
        "not_approved" => "已拒绝（未确认的手机）",
        "not_selected" => "已拒绝（会话未勾选）",
        "rate_limited" => "已拒绝（太频繁）",
        _ => $"已拒绝（{outcome}）",
    };
}

/// <summary>
/// The 「同步会话」 page: the master switch, which conversations the phone may reach
/// (at most five), which phones are approved, and what they did.
/// </summary>
/// <remarks>
/// Everything the phone can reach is decided here and kept on this computer; the page
/// is the only place those decisions are made. Full-access conversations are marked
/// and need a confirmation to select, because a phone message there runs as the user.
/// </remarks>
public sealed partial class DesktopSyncViewModel : ObservableObject
{
    public const int ListedConversations = 30;

    private readonly DesktopSyncAgent _agent;
    private readonly DesktopSyncLink _link;
    private readonly IDesktopAppTools _tools;
    private readonly Func<string, CodexThreadRecord?> _findThread;
    private readonly SyncAuditLog _audit;
    private readonly Action<Action> _post;
    private readonly Func<DateTimeOffset> _clock;
    private bool _signedIn;

    internal DesktopSyncViewModel(
        DesktopSyncAgent agent,
        DesktopSyncLink link,
        IDesktopAppTools tools,
        Func<string, CodexThreadRecord?> findThread,
        SyncAuditLog audit,
        Action<Action> post,
        Func<DateTimeOffset>? clock = null)
    {
        _agent = agent;
        _link = link;
        _tools = tools;
        _findThread = findThread;
        _audit = audit;
        _post = post;
        _clock = clock ?? (() => DateTimeOffset.Now);

        _agent.StateChanged += () => _post(ApplyState);
        _link.StateChanged += () => _post(() => OnPropertyChanged(nameof(StatusText)));
        _link.PairingRequested += request => _post(() => _ = HandlePairingRequestAsync(request));
        _audit.Changed += () => _post(RefreshAudit);
        ApplyState();
        RefreshAudit();
    }

    /// <summary>Asks the user something and returns their answer. Set by the head.</summary>
    public Func<string, string, Task<bool>>? Confirm { get; set; }

    public ObservableCollection<SyncSessionItem> Sessions { get; } = [];

    public ObservableCollection<SyncPhoneItem> Phones { get; } = [];

    public ObservableCollection<SyncAuditItem> Audit { get; } = [];

    [ObservableProperty]
    private bool isEnabled;

    [ObservableProperty]
    private string? pairingCode;

    [ObservableProperty]
    private string? pairingHint;

    [ObservableProperty]
    private string? message;

    [ObservableProperty]
    private string desktopStatus = "尚未检测 Codex 桌面版";

    /// <summary>The desktop app is not running: the page offers to start it.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(LaunchDesktopLabel))]
    private bool isDesktopOffline;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(LaunchDesktopLabel))]
    private bool isWaitingForDesktop;

    public string LaunchDesktopLabel => IsWaitingForDesktop ? "正在启动 ChatGPT…" : "启动 ChatGPT";

    public bool HasPairingCode => PairingCode is not null;

    public bool HasMessage => !string.IsNullOrEmpty(Message);

    public bool HasPhones => Phones.Count > 0;

    public string SelectedCountText => $"已选 {_agent.State.Sessions.Count} / {DesktopSyncAgent.MaxSyncedSessions}";

    public string StatusText => !IsEnabled
        ? "未开启"
        : !_signedIn
            ? "未登录"
            : _link.State switch
            {
                DesktopSyncLinkState.Connected => $"已连接服务器 · {DesktopStatus}",
                DesktopSyncLinkState.Connecting => "正在连接服务器…",
                DesktopSyncLinkState.Retrying => $"连接中断，正在重试{(string.IsNullOrEmpty(_link.LastError) ? "" : $"（{_link.LastError}）")}",
                _ => "未连接",
            };

    partial void OnPairingCodeChanged(string? value) => OnPropertyChanged(nameof(HasPairingCode));

    partial void OnMessageChanged(string? value) => OnPropertyChanged(nameof(HasMessage));

    partial void OnDesktopStatusChanged(string value) => OnPropertyChanged(nameof(StatusText));

    partial void OnIsEnabledChanged(bool value)
    {
        if (value != _agent.State.Enabled)
        {
            _agent.SetEnabled(value);
        }

        UpdateLink();
        OnPropertyChanged(nameof(StatusText));
    }

    /// <summary>Follows the account: the link runs only while signed in and switched on.</summary>
    public void SetSignedIn(bool signedIn)
    {
        _signedIn = signedIn;
        UpdateLink();
        OnPropertyChanged(nameof(StatusText));
    }

    private void UpdateLink()
    {
        if (IsEnabled && _signedIn)
        {
            _link.Start();
        }
        else
        {
            _ = _link.StopAsync();
        }
    }

    // ---- Conversations ---------------------------------------------------------------

    /// <summary>Re-reads the desktop app's conversations.</summary>
    public async Task RefreshSessionsAsync(CancellationToken cancellationToken = default)
    {
        IReadOnlyList<DesktopThread> threads;
        try
        {
            threads = await _tools.ListThreadsAsync(ListedConversations, cancellationToken).ConfigureAwait(true);
            DesktopStatus = _tools.Capabilities.CanSend ? "Codex 桌面版已连接" : "Codex 桌面版已连接（当前版本不支持从手机发送，只能查看）";
            IsDesktopOffline = false;
        }
        catch (DesktopAppToolsException ex)
        {
            threads = [];
            IsDesktopOffline = ex.Failure == DesktopAppToolsFailure.Unavailable;
            DesktopStatus = IsDesktopOffline ? "Codex 桌面版未运行" : $"Codex 桌面版出错：{ex.Message}";
        }

        var selected = _agent.State.Sessions.ToDictionary(s => s.ThreadId, StringComparer.Ordinal);
        var items = new List<SyncSessionItem>();

        // Selected but gone from the desktop app: listed first, removable only.
        foreach (SyncedSession stale in selected.Values.Where(s => threads.All(t => t.Id != s.ThreadId) && _findThread(s.ThreadId) is null))
        {
            items.Add(new SyncSessionItem(stale.ThreadId, stale.Title ?? stale.ThreadId, null, "已失效：Codex 里找不到这个会话了", PermissionMode.Unknown, missing: true));
        }

        foreach (DesktopThread thread in threads)
        {
            CodexThreadRecord? record = _findThread(thread.Id);
            items.Add(new SyncSessionItem(
                thread.Id,
                thread.Title ?? "（无标题）",
                record?.Cwd ?? thread.Cwd,
                $"{StatusName(thread.Status)} · {Ago(thread.UpdatedAt)}",
                PermissionModes.Classify(record?.SandboxPolicy, record?.ApprovalMode),
                missing: false));
        }

        // Selected ones that exist but fell outside the latest list still show.
        foreach (SyncedSession older in selected.Values.Where(s => items.All(i => i.ThreadId != s.ThreadId)))
        {
            CodexThreadRecord? record = _findThread(older.ThreadId);
            items.Add(new SyncSessionItem(older.ThreadId, record?.Title ?? older.Title ?? older.ThreadId, record?.Cwd, "较早的会话",
                PermissionModes.Classify(record?.SandboxPolicy, record?.ApprovalMode), missing: false));
        }

        Sessions.Clear();
        foreach (SyncSessionItem item in items)
        {
            Sessions.Add(item);
        }

        ApplyState();
    }

    /// <summary>
    /// After 启动 ChatGPT was asked for elsewhere: re-reads the list until the desktop app
    /// answers, so the page fills in by itself instead of needing 刷新.
    /// </summary>
    /// <remarks>
    /// The start itself runs on the dashboard, which also reports why it could not start
    /// (no group yet, restart declined); running out of time points there.
    /// </remarks>
    public async Task WaitForDesktopAsync(TimeSpan timeout, TimeSpan interval, CancellationToken cancellationToken = default)
    {
        if (IsWaitingForDesktop)
        {
            return;
        }

        IsWaitingForDesktop = true;
        Message = null;
        try
        {
            DateTimeOffset deadline = _clock() + timeout;
            while (true)
            {
                await Task.Delay(interval, cancellationToken).ConfigureAwait(true);
                await RefreshSessionsAsync(cancellationToken).ConfigureAwait(true);
                if (!IsDesktopOffline)
                {
                    return;
                }

                if (_clock() >= deadline)
                {
                    Message = "ChatGPT 还没有打开，可在「仪表盘」查看启动情况。";
                    return;
                }
            }
        }
        finally
        {
            IsWaitingForDesktop = false;
        }
    }

    /// <summary>Selects or deselects; full access needs a confirmation to select.</summary>
    public async Task ToggleAsync(SyncSessionItem item)
    {
        Message = null;
        if (item.IsSelected || item.IsMissing)
        {
            _agent.Deselect(item.ThreadId);
            return;
        }

        if (item.IsFullAccess && Confirm is not null &&
            !await Confirm(
                $"「{item.Title}」是完全访问会话：手机发来的指令会直接在这台电脑上执行，中间没有任何确认。\n\n确定允许手机操作这个会话吗？",
                "允许").ConfigureAwait(true))
        {
            return;
        }

        if (!_agent.Select(item.ThreadId, item.Title))
        {
            Message = $"最多同步 {DesktopSyncAgent.MaxSyncedSessions} 个会话，请先取消一个。";
        }
    }

    // ---- Phones ----------------------------------------------------------------------

    /// <summary>Shows a code for the phone to enter; valid five minutes, replaces any earlier one.</summary>
    public async Task StartPairingAsync(CancellationToken cancellationToken = default)
    {
        Message = null;
        try
        {
            (string code, DateTimeOffset expires) = await _link.StartPairingAsync(cancellationToken).ConfigureAwait(true);
            PairingCode = $"{code[..3]} {code[3..]}";
            PairingHint = $"在手机「电脑」页输入这 6 位数字，{expires.ToLocalTime():HH:mm} 前有效。手机提交后，这里会请你核对指纹并确认。";
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or Server.RelayApiException)
        {
            Message = $"获取配对码失败：{ex.Message}";
        }
    }

    public async Task RevokeAsync(SyncPhoneItem phone)
    {
        Message = null;
        try
        {
            await _link.RevokeAsync(phone.PairingId, CancellationToken.None).ConfigureAwait(true);
        }
        catch (Exception ex) when (ex is HttpRequestException or TaskCanceledException or Server.RelayApiException)
        {
            // Already forgotten on this computer; the server will be told next time.
            Message = $"已在本机移除，但通知服务器失败：{ex.Message}";
        }
    }

    private async Task HandlePairingRequestAsync(PairingRequest request)
    {
        PairingCode = null;
        PairingHint = null;
        bool approved = Confirm is not null && await Confirm(
            $"「{request.PhoneLabel}」请求同步这台电脑的 Codex 会话。\n\n手机上显示的指纹应为：{request.Fingerprint}\n\n两边一致才确认；不一致，说明请求不是你的手机发出的。",
            "确认配对").ConfigureAwait(true);

        if (approved)
        {
            await _link.ApproveAsync(request).ConfigureAwait(true);
            Message = $"已确认「{request.PhoneLabel}」。";
        }
        else
        {
            await _link.RejectAsync(request).ConfigureAwait(true);
            Message = $"已拒绝「{request.PhoneLabel}」。";
        }
    }

    // ---- State ---------------------------------------------------------------------

    private void ApplyState()
    {
        DesktopSyncState state = _agent.State;
        if (IsEnabled != state.Enabled)
        {
            IsEnabled = state.Enabled;
        }

        bool full = state.Sessions.Count >= DesktopSyncAgent.MaxSyncedSessions;
        foreach (SyncSessionItem item in Sessions)
        {
            item.IsSelected = state.Sessions.Any(s => s.ThreadId == item.ThreadId);
            item.CanToggle = item.IsSelected || item.IsMissing || !full;
        }

        Phones.Clear();
        foreach (ApprovedPhone phone in state.Phones)
        {
            Phones.Add(new SyncPhoneItem(phone.PairingId, phone.PhoneLabel, phone.ApprovedAt));
        }

        OnPropertyChanged(nameof(HasPhones));
        OnPropertyChanged(nameof(SelectedCountText));
    }

    private void RefreshAudit()
    {
        Audit.Clear();
        foreach (SyncAuditEntry entry in _audit.Recent(50))
        {
            Audit.Add(new SyncAuditItem(entry));
        }
    }

    private static string StatusName(string status) => status switch
    {
        "active" => "运行中",
        "idle" => "空闲",
        "notLoaded" => "未打开",
        "systemError" => "出错",
        _ => status,
    };

    private string Ago(long unixSeconds)
    {
        if (unixSeconds <= 0)
        {
            return "时间未知";
        }

        TimeSpan age = _clock() - DateTimeOffset.FromUnixTimeSeconds(unixSeconds);
        return age.TotalMinutes < 1 ? "刚刚"
            : age.TotalHours < 1 ? $"{(int)age.TotalMinutes} 分钟前"
            : age.TotalDays < 1 ? $"{(int)age.TotalHours} 小时前"
            : DateTimeOffset.FromUnixTimeSeconds(unixSeconds).ToLocalTime().ToString("MM-dd HH:mm", CultureInfo.InvariantCulture);
    }
}
