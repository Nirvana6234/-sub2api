using System.Collections.ObjectModel;
using System.Text.Json;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.WeChatIntent;

namespace LanAi.RelayClient.ViewModels;

public enum JevKeyState
{
    Missing,
    Valid,
    Invalid,
}

/// <summary>What the switch is actually doing, as opposed to what the user asked for (docs §3.2).</summary>
public enum WeChatIntentRunState
{
    Off,
    Running,
    WaitingForWeChat,
    Paused,

    /// <summary>Wanted on, but stopped by a key, quota or reader problem; <see cref="WeChatIntentViewModel.StatusText"/> says which.</summary>
    Stopped,
}

/// <summary>
/// The 「探索」 page's 「微信消息意图判断」 card, and the pipeline behind it: reader events →
/// screen parser → per-conversation transcript → queue → Jev → one card beside each of the other
/// person's messages (docs §3–§6).
/// </summary>
/// <remarks>
/// <para>
/// Every one of their messages on screen gets a card. Each is judged once, on its own, with the
/// conversation up to it as context — the format evaluated in §5.4. A single request covering the
/// whole screen was measured too: as fast and as cheap, but the model then sees the replies that
/// came after each message, and its answers drifted from the one-message ones. So the messages of a
/// screen are sent as separate requests, a few at a time.
/// </para>
/// <para>
/// Every public member runs on the UI thread. The reader's events arrive on its own threads and are
/// posted here through <c>post</c>; Jev calls complete back on the UI thread.
/// </para>
/// <para>
/// Conversation text lives only in <see cref="_transcripts"/> and the cards, in memory. It is dropped
/// when the feature stops or the user signs out, and it reaches the log in no form. The one exception
/// is the debug dump (§4.7), which the developer switches on by environment variable.
/// </para>
/// </remarks>
public sealed partial class WeChatIntentViewModel : ObservableObject, IDisposable
{
    /// <summary>Bump when the consent text changes; everyone is asked again.</summary>
    internal const int ConsentVersion = 2;

    internal const string ConsentText =
        "开启后：\n" +
        "· 客户端会定时截取微信窗口画面，截图只在本机识别文字，识别完就丢，不保存也不上传。\n" +
        "· 当前聊天里对方的每条消息，连同它之前的几条对话，会从本机直接发给第三方模型服务 TypeSafe 做判断，使用你自己的 TypeSafe 账号，共飞服务器接触不到。TypeSafe 是境外服务商，数据保留按它和你的账号协议执行。\n" +
        "· 这些消息是对方发给你的，请只在自己的聊天里使用。\n" +
        "· 随时可以暂停，也可以把某个会话设为不分析。";

    /// <summary>Bump when the relay route's consent text changes.</summary>
    internal const int RelayConsentVersion = 1;

    internal const string RelayConsentText =
        "改用共飞 Jev 分组后：\n" +
        "· 截图仍只在本机识别，不保存也不上传。\n" +
        "· 当前聊天里对方的每条消息，连同它之前的几条对话，会发到共飞服务器，再由服务器转给 TypeSafe 做判断。共飞服务器不保存这些内容。TypeSafe 是境外服务商。\n" +
        "· 每次判断从你的共飞余额扣费，按分组倍率计算。\n" +
        "· 这些消息是对方发给你的，请只在自己的聊天里使用。";

    private static readonly TimeSpan WeChatCheckInterval = TimeSpan.FromSeconds(3);
    private static readonly TimeSpan PauseLength = TimeSpan.FromMinutes(30);
    private const int CardsPerChat = 60;
    private const double CardWidth = 220;
    private const int Concurrency = 4;

    private readonly IWeChatReader? _reader;
    /// <summary>The own-key client; null in a production build (<see cref="ClientOptions.OwnKeyJevRoute"/>).</summary>
    private readonly IJevClient? _jev;
    private readonly IJevClient? _relayJev;
    private readonly Func<string, CancellationToken, Task<JevKeyCheck>> _checkKey;
    private readonly JevApiKeyStore _keys;
    private readonly WeChatIntentPreferenceStore _preferences;
    private readonly WeChatIntentUsageStore _usage;
    private readonly WeChatIntentFeedbackStore _feedback;
    private readonly Func<bool> _isWeChatRunning;
    private readonly Func<bool> _isOwnWindowInFront;
    private readonly Action<Action> _post;
    private readonly Func<DateTimeOffset> _clock;
    private readonly IUiTimer _timer;
    private readonly string? _dumpDirectory;
    private readonly SemaphoreSlim _slots = new(Concurrency);

    private readonly IntentScheduler _scheduler = new();
    private readonly Dictionary<string, ChatTranscript> _transcripts = new(StringComparer.Ordinal);

    /// <summary>Cards made in each conversation, oldest first, pinned beside their messages.</summary>
    private readonly Dictionary<string, List<AnchoredCard>> _anchored = new(StringComparer.Ordinal);

    /// <summary>Cards the user has already marked right or wrong.</summary>
    private readonly HashSet<AnchoredCard> _rated = [];

    /// <summary>The last settled screen, which the cards are placed on.</summary>
    private ChatScreen? _lastScreen;

    private WeChatIntentPreferenceStore.Preferences _prefs;
    private string? _scope;
    private bool _signedIn;
    private string _currentChat = string.Empty;
    private bool _weChatInFront;

    /// <summary>The reader says the window in front is this client's (a card was clicked).</summary>
    private bool _ownInFront;

    /// <summary>The dashboard has delivered the group list at least once since sign-in.</summary>
    private bool _groupsKnown;
    private string? _stopReason;
    private DateTimeOffset _lastWeChatCheck = DateTimeOffset.MinValue;
    private int _busyStreak;

    /// <summary>Bumped whenever everything read is forgotten, so late answers from before are dropped.</summary>
    private int _generation;

    internal WeChatIntentViewModel(
        IWeChatReader? reader,
        IJevClient? jev,
        Func<string, CancellationToken, Task<JevKeyCheck>> checkKey,
        JevApiKeyStore keys,
        WeChatIntentPreferenceStore preferences,
        WeChatIntentUsageStore usage,
        WeChatIntentFeedbackStore feedback,
        Func<bool> isWeChatRunning,
        Action<Action> post,
        UiTimerFactory timerFactory,
        Func<DateTimeOffset>? clock = null,
        Func<bool>? isOwnWindowInFront = null,
        string? dumpDirectory = null,
        IJevClient? relayJev = null)
    {
        _reader = reader;
        _jev = jev;
        _relayJev = relayJev;
        _checkKey = checkKey ?? throw new ArgumentNullException(nameof(checkKey));
        _keys = keys ?? throw new ArgumentNullException(nameof(keys));
        _preferences = preferences ?? throw new ArgumentNullException(nameof(preferences));
        _usage = usage ?? throw new ArgumentNullException(nameof(usage));
        _feedback = feedback ?? throw new ArgumentNullException(nameof(feedback));
        _isWeChatRunning = isWeChatRunning ?? throw new ArgumentNullException(nameof(isWeChatRunning));
        _post = post ?? throw new ArgumentNullException(nameof(post));
        _clock = clock ?? (() => DateTimeOffset.Now);
        _isOwnWindowInFront = isOwnWindowInFront ?? (() => false);
        _dumpDirectory = string.IsNullOrWhiteSpace(dumpDirectory) ? null : dumpDirectory;

        _prefs = _preferences.Load();
        isEnabled = _prefs.Enabled;
        useRelayGroup = jev is null || (_prefs.UseRelayGroup && relayJev is not null);
        foreach (string chat in _prefs.Muted)
        {
            Muted.Add(chat);
            _scheduler.Muted.Add(chat);
        }

        Overlay.IsDumping = _dumpDirectory is not null;
        RefreshToday();

        if (_reader is not null)
        {
            _reader.EventReceived += e => _post(() => OnReaderEvent(e));
            _reader.Failed += message => _post(() => StopWith(message));
        }

        _timer = timerFactory(TimeSpan.FromSeconds(1), OnTick);
        _timer.Start();
        CheckWeChat(force: true);
    }

    public IntentOverlayViewModel Overlay { get; } = new();

    /// <summary>Conversation titles the user excluded.</summary>
    public ObservableCollection<string> Muted { get; } = [];

    /// <summary>Asks the consent question (§3.3). Supplied by the head, which owns a window.</summary>
    public Func<string, Task<bool>>? Confirm { get; set; }

    /// <summary>The installed wechat-reader.exe was found. Without it the switch cannot be turned on (§4.6).</summary>
    public bool ReaderAvailable => _reader is not null;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(WeChatText))]
    [NotifyPropertyChangedFor(nameof(CanTurnOn))]
    [NotifyPropertyChangedFor(nameof(DisabledReason))]
    private bool isWeChatRunning;

    public string WeChatText => IsWeChatRunning ? "运行中" : "未运行";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(KeyText))]
    [NotifyPropertyChangedFor(nameof(CanTurnOn))]
    [NotifyPropertyChangedFor(nameof(DisabledReason))]
    [NotifyPropertyChangedFor(nameof(HasKey))]
    [NotifyPropertyChangedFor(nameof(SourceText))]
    private JevKeyState keyState;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(KeyText))]
    [NotifyPropertyChangedFor(nameof(SourceText))]
    private string keyMask = string.Empty;

    public bool HasKey => KeyState != JevKeyState.Missing;

    public string KeyText => KeyState switch
    {
        JevKeyState.Valid => $"已验证 · {KeyMask}",
        JevKeyState.Invalid => $"无效 · {KeyMask}",
        _ => "未填写",
    };

    /// <summary>The key being typed. Cleared once saved; the saved key is never shown again.</summary>
    [ObservableProperty]
    private string keyInput = string.Empty;

    [ObservableProperty]
    private string keyMessage = string.Empty;

    [ObservableProperty]
    private bool isCheckingKey;

    /// <summary>The user's wish, bound to the switch.</summary>
    [ObservableProperty]
    private bool isEnabled;

    /// <summary>
    /// The 共飞 Jev groups this account may use (platform <c>typesafe</c>), from the dashboard's
    /// group list. Empty until the relay offers one — and while it is empty the page shows only the
    /// own-key route, so nothing changes for anyone before the server side is live.
    /// </summary>
    public ObservableCollection<JevGroupOption> RelayGroups { get; } = [];

    public bool HasRelayGroups => RelayGroups.Count > 0 && _relayJev is not null;

    /// <summary>
    /// The own-key card exists: a test build (<see cref="ClientOptions.OwnKeyJevRoute"/>). A
    /// production build judges only through the 共飞 Jev group.
    /// </summary>
    public bool OwnKeyAvailable => _jev is not null;

    /// <summary>Judging through the 共飞 Jev group (true) or with the user's own TypeSafe key (false).</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(UseOwnKey))]
    [NotifyPropertyChangedFor(nameof(CanTurnOn))]
    [NotifyPropertyChangedFor(nameof(DisabledReason))]
    [NotifyPropertyChangedFor(nameof(SourceText))]
    private bool useRelayGroup;

    public bool UseOwnKey => !UseRelayGroup;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanTurnOn))]
    [NotifyPropertyChangedFor(nameof(DisabledReason))]
    [NotifyPropertyChangedFor(nameof(SourceText))]
    private JevGroupOption? selectedRelayGroup;

    /// <summary>One line for the status card: which route judgements take.</summary>
    public string SourceText => UseRelayGroup
        ? SelectedRelayGroup is { } g ? $"共飞分组 · {g.Display}" : "共飞分组 · 暂不可用"
        : $"自己的 key · {KeyText}";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsRunning))]
    private WeChatIntentRunState runState;

    public bool IsRunning => RunState == WeChatIntentRunState.Running;

    [ObservableProperty]
    private string statusText = "未开启";

    [ObservableProperty]
    private string todayText = string.Empty;

    /// <summary>The rail's dot: wanted on, but stopped by something the user must fix.</summary>
    [ObservableProperty]
    private bool needsAttention;

    /// <summary>Whether the switch may be turned on now (§3.2). Turning it off is always allowed.</summary>
    public bool CanTurnOn => ReaderAvailable && IsWeChatRunning && SourceReady;

    /// <summary>The chosen route can take a request: a group is there, or the key is valid.</summary>
    private bool SourceReady => UseRelayGroup ? SelectedRelayGroup is not null : KeyState == JevKeyState.Valid;

    public string DisabledReason =>
        !ReaderAvailable ? "未安装微信读取组件（本地构建时加 -p:IncludeWeChatReader=true）"
        : !SourceReady ? (UseRelayGroup ? (OwnKeyAvailable ? "暂无可用的共飞 Jev 分组（测试包可在下方改用自己的 key）" : "暂无可用的共飞 Jev 分组") : "先填写并验证 TypeSafe API Key")
        : !IsWeChatRunning ? "需要先打开电脑版微信"
        : string.Empty;

    /// <summary>
    /// The dashboard read the group list. Keeps the chosen group when it is still offered, else
    /// takes the first; with none left, the relay route stops until one comes back.
    /// </summary>
    public void SetRelayGroups(IEnumerable<JevGroupOption> groups)
    {
        ArgumentNullException.ThrowIfNull(groups);
        long? chosen = SelectedRelayGroup?.Id ?? _prefs.RelayGroupId;
        RelayGroups.Clear();
        foreach (JevGroupOption group in groups)
        {
            RelayGroups.Add(group);
        }

        SelectedRelayGroup = RelayGroups.FirstOrDefault(g => g.Id == chosen) ?? RelayGroups.FirstOrDefault();
        _groupsKnown = true;
        OnPropertyChanged(nameof(HasRelayGroups));
        UpdateRunState();
    }

    /// <summary>The group for <see cref="PawJevClient"/>, read per request. Null on the own-key route.</summary>
    internal long? CurrentRelayGroupId() => UseRelayGroup ? SelectedRelayGroup?.Id : null;

    /// <summary>
    /// Switches the route. Moving to the relay asks its own consent the first time: the messages
    /// then pass through 共飞's server and are billed to the account (§3.3).
    /// </summary>
    public async Task SetUseRelayGroupAsync(bool relay)
    {
        if (relay == UseRelayGroup)
        {
            return;
        }

        if (!relay && !OwnKeyAvailable)
        {
            return;
        }

        if (relay)
        {
            if (_prefs.RelayConsentVersion < RelayConsentVersion)
            {
                bool agreed = Confirm is not null && await Confirm(RelayConsentText).ConfigureAwait(true);
                if (!agreed)
                {
                    return;
                }

                _prefs = _prefs with { RelayConsentVersion = RelayConsentVersion };
            }
        }

        UseRelayGroup = relay;
        _stopReason = null;
        SaveSource();
        UpdateRunState();
    }

    partial void OnUseRelayGroupChanged(bool value) => RefreshToday();

    partial void OnSelectedRelayGroupChanged(JevGroupOption? value)
    {
        if (value is not null && value.Id != _prefs.RelayGroupId)
        {
            SaveSource();
        }
    }

    private void SaveSource()
    {
        _prefs = _prefs with { UseRelayGroup = UseRelayGroup, RelayGroupId = SelectedRelayGroup?.Id ?? _prefs.RelayGroupId };
        _preferences.Save(_prefs);
    }

    /// <summary>Sign-in changed. The key belongs to the account (§3.4), so it is reloaded; everything read is dropped.</summary>
    public void SetSignedIn(bool signedIn, string? scope)
    {
        _signedIn = signedIn;
        _scope = signedIn ? scope : null;
        string? key = _scope is null || !OwnKeyAvailable ? null : _keys.Load(_scope);
        KeyMask = key is null ? string.Empty : JevApiKeyStore.Mask(key);
        KeyState = key is null ? JevKeyState.Missing : JevKeyState.Valid;
        KeyMessage = string.Empty;
        if (!signedIn)
        {
            ForgetConversations();
            RelayGroups.Clear();
            SelectedRelayGroup = null;
            _groupsKnown = false;
            OnPropertyChanged(nameof(HasRelayGroups));
        }

        _stopReason = null;
        UpdateRunState();
    }

    /// <summary>
    /// The saved key, decrypted for one request (docs §3.4: not kept in memory in the clear).
    /// Null when there is none or it has been found invalid.
    /// </summary>
    internal string? CurrentKey() =>
        _scope is null || !OwnKeyAvailable || KeyState == JevKeyState.Missing ? null : _keys.Load(_scope);

    public async Task SaveKeyAsync()
    {
        string key = KeyInput.Trim();
        if (key.Length == 0 || _scope is null)
        {
            KeyMessage = "请输入 API Key";
            return;
        }

        IsCheckingKey = true;
        KeyMessage = "正在验证…";
        try
        {
            JevKeyCheck check = await _checkKey(key, CancellationToken.None).ConfigureAwait(true);
            switch (check)
            {
                case JevKeyCheck.Valid:
                    _keys.Save(_scope, key);
                    KeyMask = JevApiKeyStore.Mask(key);
                    KeyState = JevKeyState.Valid;
                    KeyInput = string.Empty;
                    KeyMessage = "已保存";
                    _stopReason = null;
                    break;
                case JevKeyCheck.Invalid:
                    KeyMessage = "TypeSafe 不接受这个 key，请检查后重试";
                    break;
                default:
                    KeyMessage = "连不上 TypeSafe，请检查网络或代理后重试";
                    break;
            }
        }
        finally
        {
            IsCheckingKey = false;
        }

        UpdateRunState();
    }

    public void ClearKey()
    {
        if (_scope is not null)
        {
            _keys.Clear(_scope);
        }

        KeyMask = string.Empty;
        KeyState = JevKeyState.Missing;
        KeyMessage = "已清除";
        UpdateRunState();
    }

    /// <summary>The switch. Turning on asks for consent the first time (§3.3).</summary>
    public async Task SetEnabledAsync(bool on)
    {
        if (on)
        {
            if (!CanTurnOn)
            {
                IsEnabled = false;
                StatusText = DisabledReason;
                return;
            }

            if (UseRelayGroup ? _prefs.RelayConsentVersion < RelayConsentVersion : _prefs.ConsentVersion < ConsentVersion)
            {
                bool agreed = Confirm is not null && await Confirm(UseRelayGroup ? RelayConsentText : ConsentText).ConfigureAwait(true);
                if (!agreed)
                {
                    IsEnabled = false;
                    return;
                }

                _prefs = UseRelayGroup
                    ? _prefs with { RelayConsentVersion = RelayConsentVersion }
                    : _prefs with { ConsentVersion = ConsentVersion };
            }
        }

        IsEnabled = on;
        _stopReason = null;
        _scheduler.PausedUntil = null;
        _prefs = _prefs with { Enabled = on };
        _preferences.Save(_prefs);
        if (!on)
        {
            ForgetConversations();
        }

        UpdateRunState();
    }

    public void Pause()
    {
        _scheduler.PausedUntil = _clock() + PauseLength;
        _scheduler.Cancel();
        UpdateRunState();
    }

    public void Resume()
    {
        _scheduler.PausedUntil = null;
        UpdateRunState();
    }

    /// <summary>「本会话不再分析」, from a card's menu.</summary>
    public void MuteCurrentChat()
    {
        if (_currentChat.Length == 0 || Muted.Contains(_currentChat))
        {
            return;
        }

        Muted.Add(_currentChat);
        _scheduler.Muted.Add(_currentChat);
        _scheduler.Cancel();
        SaveMuted();
        Overlay.Status = $"「{_currentChat}」不再分析";
        RefreshInline();
    }

    public void Unmute(string chat)
    {
        if (Muted.Remove(chat))
        {
            _scheduler.Muted.Remove(chat);
            SaveMuted();
        }
    }

    /// <summary>「判断得对 / 不对」 from a card's menu (§10.5).</summary>
    public void GiveFeedback(InlineCardViewModel card, bool correct)
    {
        ArgumentNullException.ThrowIfNull(card);
        if (card.Source is not { } source || !_rated.Add(source))
        {
            return;
        }

        IntentCard judged = source.Card;
        _feedback.Add(new WeChatIntentFeedbackStore.Entry
        {
            At = _clock(),
            Model = judged.Model,
            Intent = judged.Intent,
            Emotion = judged.Emotion,
            Risk = judged.RiskLevel,
            Action = judged.Action,
            Correct = correct,
        });
        RefreshInline();
    }

    public void Dispose()
    {
        _timer.Dispose();
        _reader?.Stop();
    }

    private void OnTick()
    {
        DateTimeOffset now = _clock();
        if (now - _lastWeChatCheck >= WeChatCheckInterval)
        {
            CheckWeChat(force: false);
        }

        if (RunState == WeChatIntentRunState.Paused && !_scheduler.IsPaused(now))
        {
            UpdateRunState();
        }

        if (IsRunning)
        {
            // Reader events only say when WeChat's own state changes. Switching from the client's
            // window to a third app changes nothing there, so the cards would stay on top of that
            // app without this.
            UpdateOverlayVisibility();
            if (_scheduler.Poll(now) is { } due)
            {
                Dispatch(due);
            }
        }
    }

    private void CheckWeChat(bool force)
    {
        _lastWeChatCheck = _clock();
        bool running;
        try
        {
            running = _isWeChatRunning();
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            running = false;
        }

        if (force || running != IsWeChatRunning)
        {
            IsWeChatRunning = running;
            UpdateRunState();
        }
    }

    /// <summary>Decides the real state from the wish and the conditions, and starts or stops the reader to match.</summary>
    private void UpdateRunState()
    {
        WeChatIntentRunState state;
        if (!IsEnabled || !_signedIn)
        {
            state = WeChatIntentRunState.Off;
            StatusText = "未开启";
        }
        else if (UseRelayGroup && !_groupsKnown && _stopReason is null)
        {
            // Just signed in or started: the group list is still on its way. Not a fault.
            state = WeChatIntentRunState.WaitingForWeChat;
            StatusText = "正在读取共飞分组…";
        }
        else if (_stopReason is not null || !SourceReady || !ReaderAvailable)
        {
            state = WeChatIntentRunState.Stopped;
            StatusText = "已暂停：" + (_stopReason ?? DisabledReason);
        }
        else if (!IsWeChatRunning)
        {
            state = WeChatIntentRunState.WaitingForWeChat;
            StatusText = "等待微信启动";
        }
        else if (_scheduler.IsPaused(_clock()))
        {
            state = WeChatIntentRunState.Paused;
            StatusText = $"已暂停，{_scheduler.PausedUntil:HH:mm} 恢复";
        }
        else
        {
            state = WeChatIntentRunState.Running;
            StatusText = "正在分析 · 私聊";
        }

        RunState = state;
        NeedsAttention = state == WeChatIntentRunState.Stopped;
        if (state == WeChatIntentRunState.Running)
        {
            _reader?.Start();
        }
        else
        {
            _reader?.Stop();
            _scheduler.Cancel();
        }

        UpdateOverlayVisibility();
    }

    private void StopWith(string reason)
    {
        _stopReason = reason;
        Overlay.Status = reason;
        UpdateRunState();
    }

    private void OnReaderEvent(ReaderEvent e)
    {
        if (!IsRunning)
        {
            return;
        }

        switch (e.Type)
        {
            case ReaderEvent.Window:
                _weChatInFront = e.State == ReaderEvent.StateForeground;
                if (e.Rect is { } rect)
                {
                    Overlay.WeChatX = rect.X;
                    Overlay.WeChatY = rect.Y;
                    Overlay.WeChatWidth = rect.W;
                    Overlay.WeChatHeight = rect.H;
                }

                Overlay.WeChatScale = e.Scale ?? 1.0;
                Overlay.WeChatImageScale = e.ImageScale is > 0 ? e.ImageScale.Value : 1.0;
                _ownInFront = e.FrontPid == Environment.ProcessId;
                RefreshInline();
                if (e.State == ReaderEvent.StateGone)
                {
                    CheckWeChat(force: true);
                }

                UpdateOverlayVisibility();
                break;
            case ReaderEvent.Scrolling:
                // The bubbles are moving; cards pinned to where they were would point at the wrong
                // messages. They come back on the next settled screen.
                Overlay.InlineCards.Clear();
                _lastScreen = null;
                _scheduler.Cancel();
                break;
            case ReaderEvent.Frame:
                OnFrame(e);
                break;
            case ReaderEvent.Error:
                OnReaderError(e);
                break;
        }
    }

    private void OnReaderError(ReaderEvent e)
    {
        switch (e.Code)
        {
            case ReaderEvent.ErrorOcrLanguageMissing:
                StopWith("Windows 没有安装简体中文文字识别，请在「设置 → 时间和语言 → 语言」里添加中文");
                break;
            case ReaderEvent.ErrorOsUnsupported:
                StopWith(OperatingSystem.IsMacOS() ? "需要 macOS 14 或更高版本" : "需要 Windows 10 2004 或更高版本");
                break;
            case ReaderEvent.ErrorScreenRecordingDenied:
                StopWith("需要「屏幕录制」权限：在「系统设置 → 隐私与安全性 → 屏幕录制」里允许共飞-ChatGPT助手，然后重启助手再打开开关");
                break;
            case ReaderEvent.ErrorNoChatArea:
                Overlay.Status = "未识别到聊天界面";
                Overlay.InlineCards.Clear();
                _lastScreen = null;
                break;
            default:
                Overlay.Status = e.Message ?? "读取微信画面失败";
                break;
        }
    }

    private void OnFrame(ReaderEvent frame)
    {
        // The parser works in image pixels: its thresholds want image pixels per design pixel.
        ChatScreen screen = ChatScreenParser.Parse(frame, Overlay.WeChatScale * Overlay.WeChatImageScale);
        Dump(frame, screen);
        if (screen.Title.Length == 0)
        {
            return;
        }

        // OCR can read the same title a character differently between frames; a new key would look
        // like a conversation seen for the first time.
        string known = _transcripts.Keys.FirstOrDefault(k => ChatTranscript.SameTitle(k, screen.Title)) ?? screen.Title;
        screen = screen with { Title = known };
        if (screen.Title != _currentChat)
        {
            _scheduler.Cancel();
        }

        _currentChat = screen.Title;
        _lastScreen = screen;
        Overlay.Status = screen.IsGroup ? "群聊暂不分析" : Muted.Contains(_currentChat) ? $"「{_currentChat}」不分析" : string.Empty;
        if (screen.IsGroup || Muted.Contains(_currentChat))
        {
            RefreshInline();
            return;
        }

        if (!_transcripts.TryGetValue(screen.Title, out ChatTranscript? transcript))
        {
            transcript = new ChatTranscript();
            _transcripts[screen.Title] = transcript;
        }

        // Kept for the context each judgement carries; which messages get judged no longer depends
        // on what is "new" — every message from them on screen gets a card.
        transcript.Apply(screen);
        RefreshInline();
        _scheduler.OnScreen(screen.Title, WithoutCard(screen), _clock());
    }

    /// <summary>The other person's messages on <paramref name="screen"/> that have no card and none coming.</summary>
    private List<ChatItem> WithoutCard(ChatScreen screen)
    {
        List<AnchoredCard> cards = _anchored.GetValueOrDefault(screen.Title) ?? [];
        var carded = new HashSet<ChatItem>(ReferenceEqualityComparer.Instance);
        foreach (PlacedCard placed in InlinePlacement.Place(cards, screen))
        {
            int index = IndexOf(screen, placed.Bubble);
            if (index >= 0)
            {
                carded.Add(screen.Items[index]);
            }
        }

        return screen.Items
            .Where(i => i.Speaker == ChatSpeaker.Them && !i.Partial && !carded.Contains(i) && !_scheduler.IsInFlight(screen.Title, i))
            .ToList();
    }

    private static int IndexOf(ChatScreen screen, BubbleBounds bubble)
    {
        for (int i = 0; i < screen.Bounds.Count; i++)
        {
            if (screen.Bounds[i] == bubble)
            {
                return i;
            }
        }

        return -1;
    }

    /// <summary>Sends each message of the batch as its own judgement, a few at a time.</summary>
    private void Dispatch(DueBatch batch)
    {
        _transcripts.TryGetValue(batch.Chat, out ChatTranscript? transcript);
        ChatScreen? screen = _lastScreen is { } s && s.Title == batch.Chat ? s : null;
        foreach (ChatItem item in batch.Items)
        {
            IReadOnlyList<ChatItem> context = transcript?.ContextUpTo(item, WeChatIntentQuestions.ContextSize)
                ?? ScreenContext(screen, item);
            _ = JudgeAsync(batch.Chat, item, context, _generation);
        }

        RefreshInline();
    }

    /// <summary>When the transcript cannot place the message (history far back), the screen up to it is the context.</summary>
    private static IReadOnlyList<ChatItem> ScreenContext(ChatScreen? screen, ChatItem item)
    {
        if (screen is null)
        {
            return [item];
        }

        int index = -1;
        for (int i = screen.Items.Count - 1; i >= 0; i--)
        {
            if (ReferenceEquals(screen.Items[i], item))
            {
                index = i;
                break;
            }
        }

        return index < 0
            ? [item]
            : screen.Items.Take(index + 1).Where(i => i.Speaker != ChatSpeaker.Time).TakeLast(WeChatIntentQuestions.ContextSize).ToList();
    }

    private async Task JudgeAsync(string chat, ChatItem item, IReadOnlyList<ChatItem> context, int generation)
    {
        bool succeeded = false;
        await _slots.WaitAsync().ConfigureAwait(true);
        try
        {
            if (generation != _generation || !IsRunning)
            {
                return;
            }

            JevState state = JevRequestBody.StateFor(context, [item]);
            IJevClient? client = UseRelayGroup ? _relayJev : _jev;
            if (client is null)
            {
                return;
            }

            JevOutcome outcome = await client.EvaluateAsync(state, CancellationToken.None).ConfigureAwait(true);
            if (generation != _generation)
            {
                return;
            }

            if (outcome.Response is { } response)
            {
                succeeded = true;
                _busyStreak = 0;
                IntentCard card = IntentCard.From(response, chat, [item], stale: false, _clock());
                if (!_anchored.TryGetValue(chat, out List<AnchoredCard>? cards))
                {
                    cards = [];
                    _anchored[chat] = cards;
                }

                cards.Add(new AnchoredCard(item, card));
                if (cards.Count > CardsPerChat)
                {
                    _rated.Remove(cards[0]);
                    cards.RemoveAt(0);
                }

                if (outcome.FellBack)
                {
                    Overlay.Status = "Jev 模型版本已自动换成最新版";
                }

                _usage.Add(response.Usage?.InputTokens ?? 0);
                RefreshToday();
            }
            else
            {
                ReportFailure(outcome);
            }
        }
        catch (Exception ex) when (ex is not OutOfMemoryException)
        {
            // The type only: the message could quote the request.
            ClientLog.Warning($"意图判断失败：{ex.GetType().Name}");
            Overlay.Status = "分析失败";
        }
        finally
        {
            _slots.Release();
            if (generation == _generation)
            {
                _scheduler.Done(chat, item, succeeded, _clock());
                RefreshInline();
            }
        }
    }

    private void ReportFailure(JevOutcome outcome)
    {
        switch (outcome.Failure)
        {
            case JevFailure.InvalidKey:
                KeyState = JevKeyState.Invalid;
                StopWith("API Key 无效，请更换");
                break;
            case JevFailure.NoKey:
                KeyState = JevKeyState.Missing;
                StopWith("没有 API Key");
                break;
            case JevFailure.Quota:
                StopWith(UseRelayGroup ? "共飞余额不足，充值后再打开开关" : "TypeSafe 账号额度不足或无权限");
                break;
            case JevFailure.SignedOut:
                StopWith("共飞登录已失效，请重新登录");
                break;
            case JevFailure.GroupUnavailable:
                StopWith("共飞 Jev 分组不可用，请换一个分组，或改用自己的 key");
                break;
            case JevFailure.ServiceUnavailable:
                Overlay.Status = "共飞 Jev 分组暂时不可用（未配置价格或没有可用账号），一分钟后重试";
                break;
            case JevFailure.Busy:
                Overlay.Status = ++_busyStreak >= 3 ? "TypeSafe 繁忙，稍后再试" : "有消息没分析成功，一分钟后重试";
                break;
            case JevFailure.Network:
            case JevFailure.Timeout:
                Overlay.Status = "连不上 TypeSafe，请检查网络或代理";
                break;
            default:
                Overlay.Status = $"分析失败（{outcome.Status}）";
                break;
        }
    }

    private void UpdateOverlayVisibility()
    {
        // In front, or our own window is (a click on a card must not hide the cards).
        Overlay.IsVisible = IsRunning && (_weChatInFront || _ownInFront || _isOwnWindowInFront());
    }

    /// <summary>
    /// A card beside each of the other person's messages on the last settled screen: the result
    /// when there is one, 「分析中」 while it is being judged. To the bubble's right when there is
    /// room before the list's edge, else just under it.
    /// </summary>
    private void RefreshInline()
    {
        Overlay.InlineCards.Clear();
        if (_lastScreen is not { } screen || screen.IsGroup || Muted.Contains(screen.Title))
        {
            return;
        }

        // Bubbles are in image pixels; cards are placed in screen units. On Windows the two are the
        // same; on a Retina Mac an image pixel is half a point.
        double imageScale = Overlay.WeChatImageScale > 0 ? Overlay.WeChatImageScale : 1.0;
        double scale = (Overlay.WeChatScale > 0 ? Overlay.WeChatScale : 1.0) * imageScale;
        List<AnchoredCard> cards = _anchored.GetValueOrDefault(screen.Title) ?? [];
        var placedByIndex = new Dictionary<int, PlacedCard>();
        foreach (PlacedCard placed in InlinePlacement.Place(cards, screen))
        {
            placedByIndex[IndexOf(screen, placed.Bubble)] = placed;
        }

        for (int i = 0; i < screen.Items.Count; i++)
        {
            ChatItem item = screen.Items[i];
            if (item.Speaker != ChatSpeaker.Them || i >= screen.Bounds.Count)
            {
                continue;
            }

            (int ix, int iy) = PlaceBeside(screen.Bounds[i], screen.AreaRight, scale);
            int x = (int)Math.Round(ix / imageScale);
            int y = (int)Math.Round(iy / imageScale);
            if (placedByIndex.TryGetValue(i, out PlacedCard? placed))
            {
                Overlay.InlineCards.Add(Present(placed.Card, x, y));
            }
            else if (_scheduler.IsInFlight(screen.Title, item))
            {
                Overlay.InlineCards.Add(new InlineCardViewModel
                {
                    ScreenX = Overlay.WeChatX + x,
                    ScreenY = Overlay.WeChatY + y,
                    Headline = "分析中…",
                    IsPending = true,
                });
            }
        }
    }

    /// <summary>In image pixels; <paramref name="scale"/> is image pixels per design pixel.</summary>
    private static (int X, int Y) PlaceBeside(BubbleBounds bubble, int areaRight, double scale)
    {
        // The text's right edge plus the bubble's own padding, then a gap.
        int gap = (int)Math.Round(20 * scale);
        int width = (int)Math.Round(CardWidth * scale);
        bool roomRight = bubble.Right + gap + width <= areaRight;
        return roomRight
            ? (bubble.Right + gap, bubble.Top - (int)Math.Round(8 * scale))
            : (bubble.Left, bubble.Bottom + (int)Math.Round(14 * scale));
    }

    private InlineCardViewModel Present(AnchoredCard anchored, int x, int y)
    {
        IntentCard card = anchored.Card;
        string intent = card.Intent.Split(" · ")[0];
        string action = card.Action.Split(" · ")[0];
        return new InlineCardViewModel
        {
            ScreenX = Overlay.WeChatX + x,
            ScreenY = Overlay.WeChatY + y,
            Headline = $"{(card.ReplySoon ? "⏱ " : string.Empty)}{intent} · {card.Emotion}{(card.IntentUncertain ? "（不太确定）" : string.Empty)}",
            Advice = $"{card.RiskShort} · 建议：{action}",
            RiskLevel = card.RiskLevel,
            ReplySoon = card.ReplySoon,
            FeedbackGiven = _rated.Contains(anchored),
            Detail = $"意图：{card.Intent}\n情绪：{card.Emotion}\n风险：{card.RiskText}\n建议：{card.Action}\n（右键：判断得对 / 不对 / 本会话不再分析）",
            Source = anchored,
        };
    }

    private void RefreshToday()
    {
        WeChatIntentUsageStore.Totals today = _usage.Today();
        double usd = today.InputTokens * WeChatIntentUsageStore.UsdPerInputToken;
        TodayText = UseRelayGroup
            ? $"今日 {today.Count} 次（共飞分组按次从余额扣费，明细见账户用量）"
            : $"今日 {today.Count} 次 · 约 ${usd:0.0000}（按官方价估算，以 TypeSafe 账单为准）";
    }

    private void SaveMuted()
    {
        _prefs = _prefs with { Muted = [.. Muted] };
        _preferences.Save(_prefs);
    }

    private void ForgetConversations()
    {
        _generation++;
        _transcripts.Clear();
        _anchored.Clear();
        _rated.Clear();
        _lastScreen = null;
        _currentChat = string.Empty;
        _scheduler.Reset();
        Overlay.Clear();
    }

    /// <summary>Developer-only (§4.7): the frame and its parse, one file each. Never on by default.</summary>
    private void Dump(ReaderEvent frame, ChatScreen screen)
    {
        if (_dumpDirectory is null)
        {
            return;
        }

        try
        {
            Directory.CreateDirectory(_dumpDirectory);
            string stem = Path.Combine(_dumpDirectory, $"{_clock():yyyyMMdd-HHmmss-fff}-{frame.Seq}");
            File.WriteAllText(stem + ".frame.json", JsonSerializer.Serialize(frame, ReaderJsonContext.Default.ReaderEvent));
            File.WriteAllLines(stem + ".parsed.txt",
                [$"title={screen.Title} group={screen.IsGroup}", .. screen.Items.Select(i => $"{i.Speaker}{(i.Partial ? "(partial)" : string.Empty)}: {i.Text}")]);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning($"调试导出失败：{ex.GetType().Name}");
        }
    }
}

/// <summary>A 共飞 Jev group the page offers.</summary>
public sealed class JevGroupOption
{
    public JevGroupOption(long id, string name, string rateLabel)
    {
        Id = id;
        Name = name ?? string.Empty;
        RateLabel = rateLabel ?? string.Empty;
    }

    public long Id { get; }

    public string Name { get; }

    /// <summary>The account's rate on the group, as the dashboard shows it (e.g. 「1.0x」).</summary>
    public string RateLabel { get; }

    public string Display => RateLabel.Length > 0 ? $"{Name}（{RateLabel}）" : Name;

    public override string ToString() => Display;
}
