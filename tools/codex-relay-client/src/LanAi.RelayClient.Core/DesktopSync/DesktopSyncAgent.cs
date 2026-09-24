using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.DesktopSync;

/// <summary>
/// Answers the phone's commands on this computer. Everything that decides whether a
/// command may run is here, locally; the server's own checks are only a second line.
/// </summary>
/// <remarks>
/// <para>
/// Every command passes, in order: the master switch; the pairing being one the user
/// approved on this computer; the command type; the conversation being one the user
/// selected (at most <see cref="MaxSyncedSessions"/>); for a send, the rate limit and
/// the phone's signature. Every command is written to the audit log, refused or not.
/// </para>
/// <para>
/// Sends into a running turn are queued by default. Measured: a message sent mid-turn
/// is folded into that turn, and the model drops what it had not finished of the
/// earlier instruction. The queue sends once the conversation is no longer active.
/// </para>
/// </remarks>
internal sealed class DesktopSyncAgent : IDisposable
{
    public const int MaxSyncedSessions = 5;
    public const int MaxMessageChars = 8000;
    public const int MaxSendsPerMinute = 6;
    public static readonly TimeSpan QueuePumpInterval = TimeSpan.FromSeconds(2);
    public static readonly TimeSpan StatusPollInterval = TimeSpan.FromSeconds(5);

    /// <summary>
    /// How long an unconfirmed send is looked for in the conversation. Measured: the
    /// delegated message is in the rollout 0.6 s after the send returns.
    /// </summary>
    internal TimeSpan ConfirmTimeout { get; init; } = TimeSpan.FromSeconds(5);

    internal static readonly TimeSpan ConfirmPollInterval = TimeSpan.FromMilliseconds(250);

    /// <summary>How long a message waits for a desktop app that is not running before it is dropped.</summary>
    public static readonly TimeSpan DesktopWaitTimeout = TimeSpan.FromMinutes(3);

    /// <summary>Starts are not asked for again within this long of the last one.</summary>
    public static readonly TimeSpan StartRetryInterval = TimeSpan.FromSeconds(60);

    /// <summary>
    /// How long a send waits to hear how a start went before answering the phone. The
    /// refusals (not installed, no group) come back at once; the launch itself does not,
    /// and the server gives the whole command 15 s.
    /// </summary>
    internal TimeSpan StartAnswerWait { get; init; } = TimeSpan.FromSeconds(4);

    private readonly IDesktopAppTools _tools;
    private readonly SessionContentSync _content;
    private readonly Func<string, CodexThreadRecord?> _findThread;
    private readonly IDesktopSyncStateStore _store;
    private readonly SyncAuditLog _audit;
    private readonly SignedSendVerifier _verifier;
    private readonly Func<DateTimeOffset> _clock;
    private readonly Func<CancellationToken, Task<DesktopStartResult>>? _startDesktop;
    private readonly Func<CancellationToken, Task<DesktopSelfCheckResult>>? _selfCheck;
    private Task<DesktopStartResult>? _start;
    private DateTimeOffset _startAt;
    private readonly object _gate = new();
    private readonly Dictionary<long, Queue<DateTimeOffset>> _sendTimes = [];
    private readonly List<QueuedSend> _queue = [];
    private readonly Dictionary<string, Subscription> _subscriptions = new(StringComparer.Ordinal);
    private readonly SemaphoreSlim _pumpGate = new(1, 1);
    private Timer? _pump;
    private DesktopSyncState _state;

    public DesktopSyncAgent(
        IDesktopAppTools tools,
        SessionContentSync content,
        Func<string, CodexThreadRecord?> findThread,
        IDesktopSyncStateStore store,
        SyncAuditLog audit,
        SignedSendVerifier? verifier = null,
        Func<DateTimeOffset>? clock = null,
        Func<CancellationToken, Task<DesktopStartResult>>? startDesktop = null,
        Func<CancellationToken, Task<DesktopSelfCheckResult>>? selfCheck = null)
    {
        _startDesktop = startDesktop;
        _selfCheck = selfCheck;
        _tools = tools;
        _content = content;
        _findThread = findThread;
        _store = store;
        _audit = audit;
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
        _verifier = verifier ?? new SignedSendVerifier(_clock);
        _state = store.Load();
    }

    /// <summary>Raised when the state changes; may be raised on any thread.</summary>
    public event Action? StateChanged;

    public DesktopSyncState State
    {
        get
        {
            lock (_gate)
            {
                return _state;
            }
        }
    }

    public int QueuedCount
    {
        get
        {
            lock (_gate)
            {
                return _queue.Count;
            }
        }
    }

    // ---- What the user changes on this computer ----------------------------------------

    public void SetEnabled(bool enabled) => Update(s => s with { Enabled = enabled });

    /// <returns>False when the selection is already full.</returns>
    public bool Select(string threadId, string? title)
    {
        bool added = false;
        Update(s =>
        {
            if (s.Sessions.Any(x => x.ThreadId == threadId))
            {
                return s;
            }

            if (s.Sessions.Count >= MaxSyncedSessions)
            {
                return s;
            }

            added = true;
            return s with { Sessions = [.. s.Sessions, new SyncedSession("codex", threadId, title, _clock())] };
        });
        return added;
    }

    /// <summary>Deselecting takes effect at once, including for a phone that has the conversation open.</summary>
    public void Deselect(string threadId)
    {
        Update(s => s with { Sessions = s.Sessions.Where(x => x.ThreadId != threadId).ToList() });
        foreach (Subscription subscription in TakeSubscriptions(s => s.ThreadId == threadId))
        {
            subscription.End("revoked");
        }

        lock (_gate)
        {
            _queue.RemoveAll(q => q.ThreadId == threadId);
        }
    }

    public void ApprovePhone(long pairingId, string phoneLabel, string publicKey) =>
        Update(s => s with
        {
            Phones = [.. s.Phones.Where(p => p.PairingId != pairingId), new ApprovedPhone(pairingId, phoneLabel, publicKey, _clock())],
        });

    public void ForgetPhone(long pairingId)
    {
        Update(s => s with { Phones = s.Phones.Where(p => p.PairingId != pairingId).ToList() });
        foreach (Subscription subscription in TakeSubscriptions(s => s.PairingId == pairingId))
        {
            subscription.End("revoked");
        }
    }

    private void Update(Func<DesktopSyncState, DesktopSyncState> change)
    {
        DesktopSyncState updated;
        lock (_gate)
        {
            updated = change(_state);
            if (ReferenceEquals(updated, _state))
            {
                return;
            }

            _state = updated;
        }

        _store.Save(updated);
        StateChanged?.Invoke();
    }

    // ---- Commands --------------------------------------------------------------------

    /// <summary>Handles one command from the phone; always returns a JSON answer.</summary>
    public async Task<byte[]> HandleAsync(long pairingId, ReadOnlyMemory<byte> body, CancellationToken cancellationToken)
    {
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(body);
        }
        catch (JsonException)
        {
            return SyncJson.Error("bad_request", "指令不是 JSON");
        }

        using (document)
        {
            JsonElement root = document.RootElement;
            string type = Str(root, "type") ?? string.Empty;
            string? threadId = Str(root, "thread_id");

            (ApprovedPhone? phone, byte[]? refusal) = Admit(pairingId, type, threadId);
            if (refusal is not null)
            {
                return refusal;
            }

            try
            {
                return type switch
                {
                    DesktopSyncCommands.ListSessions => await ListSessionsAsync(cancellationToken).ConfigureAwait(false),
                    DesktopSyncCommands.OpenSession => OpenSession(threadId!, root),
                    DesktopSyncCommands.History => History(threadId!, root),
                    DesktopSyncCommands.Detail => Detail(threadId!, root),
                    DesktopSyncCommands.SendMessage => await SendAsync(phone!, threadId!, root, cancellationToken).ConfigureAwait(false),
                    DesktopSyncCommands.Navigate => await NavigateAsync(phone!, threadId!, cancellationToken).ConfigureAwait(false),
                    DesktopSyncCommands.SelfCheck => await SelfCheckAsync(cancellationToken).ConfigureAwait(false),
                    _ => SyncJson.Error("refused", "不支持的指令"),
                };
            }
            catch (DesktopAppToolsException ex)
            {
                return ex.Failure switch
                {
                    DesktopAppToolsFailure.Unavailable => SyncJson.Error("desktop_unavailable", ex.Message),
                    DesktopAppToolsFailure.Unconfirmed => SyncJson.Error("unconfirmed", "电脑没有回应，这条消息可能已经发出：请先看会话里有没有，再决定是否重发"),
                    _ => SyncJson.Error("desktop_error", ex.Message),
                };
            }
        }
    }

    /// <summary>The local checks every command goes through, in order.</summary>
    private (ApprovedPhone? Phone, byte[]? Refusal) Admit(long pairingId, string type, string? threadId)
    {
        DesktopSyncState state = State;
        ApprovedPhone? phone = state.Phones.FirstOrDefault(p => p.PairingId == pairingId);

        string? refused =
            !state.Enabled ? "disabled"
            : phone is null ? "not_approved"
            : !DesktopSyncCommands.IsKnown(type) ? "refused"
            : type != DesktopSyncCommands.ListSessions && !IsSelected(state, threadId) ? "not_selected"
            : null;

        if (refused is null)
        {
            return (phone, null);
        }

        Audit(pairingId, phone?.PhoneLabel, type, threadId, null, refused);
        return (phone, SyncJson.Error(refused, refused switch
        {
            "disabled" => "电脑上的手机同步已关闭",
            "not_approved" => "这台手机没有在电脑上确认过",
            "not_selected" => "这个会话没有在电脑上勾选同步",
            _ => "不支持的指令",
        }));
    }

    private static bool IsSelected(DesktopSyncState state, string? threadId) =>
        threadId is not null && state.Sessions.Any(s => s.ThreadId == threadId);

    private async Task<byte[]> ListSessionsAsync(CancellationToken cancellationToken)
    {
        IReadOnlyList<SyncedSession> selected = State.Sessions;
        Dictionary<string, DesktopThread> live = new(StringComparer.Ordinal);
        bool desktop = true;
        try
        {
            foreach (DesktopThread thread in await _tools.ListThreadsAsync(50, cancellationToken).ConfigureAwait(false))
            {
                live[thread.Id] = thread;
            }
        }
        catch (DesktopAppToolsException)
        {
            desktop = false;
        }

        return SyncJson.Ok(w =>
        {
            w.WriteBoolean("desktop_running", desktop);
            w.WriteStartArray("sessions");
            foreach (SyncedSession session in selected)
            {
                CodexThreadRecord? record = _findThread(session.ThreadId);
                live.TryGetValue(session.ThreadId, out DesktopThread? thread);
                w.WriteStartObject();
                w.WriteString("thread_id", session.ThreadId);
                w.WriteString("provider", session.Provider);
                SyncJson.WriteNullable(w, "title", thread?.Title ?? record?.Title ?? session.Title);
                SyncJson.WriteNullable(w, "cwd", record?.Cwd ?? thread?.Cwd);
                w.WriteString("status", thread?.Status ?? (record is null ? "missing" : "unknown"));
                w.WriteString("permission", SyncJson.PermissionName(PermissionModes.Classify(record?.SandboxPolicy, record?.ApprovalMode)));
                if (thread is not null)
                {
                    w.WriteNumber("updated_at", thread.UpdatedAt);
                }

                w.WriteEndObject();
            }

            w.WriteEndArray();
        });
    }

    private byte[] OpenSession(string threadId, JsonElement root)
    {
        SessionSnapshot? snapshot = _content.Open(threadId, Int(root, "turns") ?? SessionContentSync.DefaultTurns);
        if (snapshot is null)
        {
            return SyncJson.Error("missing", "找不到这个会话的记录");
        }

        return SyncJson.Ok(w =>
        {
            SyncJson.WriteHeader(w, snapshot.Header);
            SyncJson.WritePage(w, snapshot.Page, FromPhone);
            w.WriteString("cursor", SyncJson.EncodeCursor(snapshot.Cursor));
        });
    }

    private byte[] History(string threadId, JsonElement root)
    {
        string? before = Str(root, "before_turn_id");
        SessionPage? page = before is null ? null : _content.History(threadId, before, Int(root, "turns") ?? SessionContentSync.DefaultTurns);
        return page is null
            ? SyncJson.Error("missing", "找不到这个会话的记录")
            : SyncJson.Ok(w => SyncJson.WritePage(w, page, FromPhone));
    }

    private byte[] Detail(string threadId, JsonElement root)
    {
        DetailPart? part = Str(root, "part") switch
        {
            "output" => DetailPart.Output,
            "diff" => DetailPart.Diff,
            "image" => DetailPart.Image,
            _ => null,
        };
        if (part is null || Str(root, "turn_id") is not string turnId || Str(root, "item_id") is not string itemId)
        {
            return SyncJson.Error("bad_request", "缺少 turn_id / item_id / part");
        }

        int index = root.TryGetProperty("index", out JsonElement i) && i.TryGetInt32(out int n) ? Math.Clamp(n, 0, 50) : 0;
        SessionDetail detail = _content.ReadDetail(threadId, turnId, itemId, part.Value, index);
        if (detail.Outcome != DetailOutcome.Found)
        {
            return SyncJson.Error(detail.Outcome switch
            {
                DetailOutcome.Missing => "missing",
                DetailOutcome.Refused => "refused",
                _ => "not_found",
            }, detail.Outcome == DetailOutcome.Missing ? "文件已不在电脑上" : "无法提供这项内容");
        }

        return SyncJson.Ok(w =>
        {
            SyncJson.WriteNullable(w, "text", detail.Text);
            if (detail.Bytes is not null)
            {
                w.WriteString("media_type", detail.MediaType);
                w.WriteBase64String("data", detail.Bytes);
            }

            w.WriteBoolean("truncated", detail.Truncated);
        });
    }

    private async Task<byte[]> SendAsync(ApprovedPhone phone, string threadId, JsonElement root, CancellationToken cancellationToken)
    {
        string text = Str(root, "text") ?? string.Empty;
        string mode = Str(root, "mode") ?? "queue";
        if (text.Trim().Length == 0 || text.Length > MaxMessageChars || mode is not ("queue" or "insert"))
        {
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, null, "bad_request");
            return SyncJson.Error("bad_request", $"消息为空、超过 {MaxMessageChars} 字，或 mode 不对");
        }

        long timestamp = root.TryGetProperty("ts", out JsonElement ts) && ts.TryGetInt64(out long ms) ? ms : 0;
        string? invalid = _verifier.Verify(phone, threadId, mode, text, timestamp, Str(root, "nonce"), Str(root, "sig"));
        if (invalid is not null)
        {
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, null, $"signature: {invalid}");
            return SyncJson.Error("bad_signature", invalid);
        }

        if (!TakeSendSlot(phone.PairingId))
        {
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, null, "rate_limited");
            return SyncJson.Error("rate_limited", $"每分钟最多发送 {MaxSendsPerMinute} 条");
        }

        if (mode == "queue")
        {
            DesktopThreadStatus status;
            try
            {
                status = await _tools.GetThreadStatusAsync(threadId, cancellationToken).ConfigureAwait(false);
            }
            catch (DesktopAppToolsException ex) when (ex.Failure == DesktopAppToolsFailure.Unavailable)
            {
                return await WaitForDesktopAsync(phone, threadId, text, cancellationToken).ConfigureAwait(false);
            }

            if (status.Type == "active")
            {
                lock (_gate)
                {
                    _queue.Add(new QueuedSend(phone.PairingId, phone.PhoneLabel, threadId, text, _clock()));
                }

                Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, SyncAuditLog.SummaryOf(text), "queued");
                StateChanged?.Invoke();
                return SyncJson.Ok(w => w.WriteBoolean("queued", true));
            }
        }

        try
        {
            await DeliverAsync(threadId, text, cancellationToken).ConfigureAwait(false);
        }
        catch (DesktopAppToolsException ex) when (ex.Failure == DesktopAppToolsFailure.Unavailable)
        {
            // Nothing went out (see DesktopAppToolsFailure.Unavailable). There is no running
            // turn to insert into either, so an insert waits like any other message.
            return await WaitForDesktopAsync(phone, threadId, text, cancellationToken).ConfigureAwait(false);
        }
        catch (DesktopAppToolsException ex)
        {
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, SyncAuditLog.SummaryOf(text), FailureOutcome(ex));
            throw;
        }

        Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, SyncAuditLog.SummaryOf(text), "ok");
        return SyncJson.Ok(w => w.WriteBoolean("queued", false));
    }

    /// <summary>
    /// The desktop app is not answering: the message waits in the queue while ChatGPT is
    /// started, and goes out once it answers — or is dropped after <see cref="DesktopWaitTimeout"/>.
    /// </summary>
    /// <remarks>
    /// Starting is only ever a start: the host checks that ChatGPT is not running and
    /// never confirms a restart, so nothing the user has open is stopped. It never installs.
    /// </remarks>
    private async Task<byte[]> WaitForDesktopAsync(ApprovedPhone phone, string threadId, string text, CancellationToken cancellationToken)
    {
        string summary = SyncAuditLog.SummaryOf(text);
        if (_startDesktop is null)
        {
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, summary, "unavailable");
            return SyncJson.Error("desktop_unavailable", "电脑上的 Codex 桌面版没有运行");
        }

        Task<DesktopStartResult> start = StartDesktop();
        Task finished = await Task.WhenAny(start, Task.Delay(StartAnswerWait, cancellationToken)).ConfigureAwait(false);
        DesktopStartResult? result = finished == start ? await start.ConfigureAwait(false) : null;

        if (result is { Outcome: DesktopStartOutcome.Refused })
        {
            string reason = result.Message ?? "电脑上的 ChatGPT 没有启动";
            Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, summary, $"unavailable: {reason}");

            // Its own code so that the phone shows this reason rather than its fixed text
            // for desktop_unavailable, which would be wrong when ChatGPT is running.
            return SyncJson.Error("desktop_start_refused", reason);
        }

        lock (_gate)
        {
            _queue.Add(new QueuedSend(phone.PairingId, phone.PhoneLabel, threadId, text, _clock(), _clock() + DesktopWaitTimeout));
        }

        Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.SendMessage, threadId, summary, "queued_for_desktop");
        StateChanged?.Invoke();
        return SyncJson.Ok(w =>
        {
            w.WriteBoolean("queued", true);
            w.WriteBoolean("waiting_for_desktop", true);
            w.WriteBoolean("starting_desktop", result?.Outcome != DesktopStartOutcome.AlreadyRunning);
        });
    }

    /// <summary>The start under way, or a new one when the last is old enough or was refused.</summary>
    private Task<DesktopStartResult> StartDesktop()
    {
        lock (_gate)
        {
            bool reuse = _start is not null &&
                         (!_start.IsCompleted ||
                          (_start.Result.Outcome != DesktopStartOutcome.Refused && _clock() - _startAt < StartRetryInterval));
            if (!reuse)
            {
                _startAt = _clock();
                _start = RunStartAsync();
            }

            return _start!;
        }
    }

    private async Task<DesktopStartResult> RunStartAsync()
    {
        DesktopStartResult result;
        try
        {
            result = await _startDesktop!(CancellationToken.None).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is not OutOfMemoryException)
        {
            ClientLog.Warning("为手机启动 ChatGPT 失败", ex);
            result = new DesktopStartResult(DesktopStartOutcome.Refused, "启动电脑上的 ChatGPT 时出错");
        }

        if (result.Outcome == DesktopStartOutcome.Refused)
        {
            // Refused after the phone was already told it is starting: those messages
            // would only wait out their time, so they go now, with the reason.
            List<QueuedSend> dropped;
            lock (_gate)
            {
                dropped = _queue.Where(q => q.ExpiresAt is not null).ToList();
                _queue.RemoveAll(q => q.ExpiresAt is not null);
            }

            foreach (QueuedSend q in dropped)
            {
                Audit(q.PairingId, q.PhoneLabel, DesktopSyncCommands.SendMessage, q.ThreadId, SyncAuditLog.SummaryOf(q.Text), $"unavailable: {result.Message}");
            }

            if (dropped.Count > 0)
            {
                StateChanged?.Invoke();
            }
        }

        return result;
    }

    private async Task<byte[]> SelfCheckAsync(CancellationToken cancellationToken)
    {
        bool desktop = true;
        try
        {
            await _tools.ListThreadsAsync(1, cancellationToken).ConfigureAwait(false);
        }
        catch (DesktopAppToolsException)
        {
            desktop = false;
        }

        DesktopSelfCheckResult? check = _selfCheck is null ? null : await _selfCheck(cancellationToken).ConfigureAwait(false);
        return SyncJson.Ok(w =>
        {
            w.WriteBoolean("desktop_running", desktop);
            if (check is not null)
            {
                w.WriteBoolean("relay_listening", check.RelayListening);
                w.WriteBoolean("signed_in", check.SignedIn);
                w.WriteBoolean("server_reachable", check.ServerReachable);
            }

            bool problem = check is { SignedIn: false } or { RelayListening: false } or { ServerReachable: false };
            w.WriteString("summary",
                problem ? check!.Summary
                : !desktop ? "电脑上的 ChatGPT 没有运行，从手机发消息时会自动启动。"
                : check?.Summary ?? "电脑上的 ChatGPT 在运行。");
        });
    }

    /// <summary>
    /// Sends; when the desktop app took the message and then went quiet, looks for it in
    /// the conversation before calling it lost. Never sends twice.
    /// </summary>
    /// <exception cref="DesktopAppToolsException">
    /// <see cref="DesktopAppToolsFailure.Unconfirmed"/> when it did not show up in time.
    /// It may still, so it is not sent again.
    /// </exception>
    private async Task DeliverAsync(string threadId, string text, CancellationToken cancellationToken)
    {
        SyncCursor? before = _findThread(threadId) is CodexThreadRecord record
            ? new SyncCursor(record.RolloutPath, RolloutFile.LengthOf(record.RolloutPath))
            : null;
        try
        {
            await _tools.SendMessageAsync(threadId, text, cancellationToken).ConfigureAwait(false);
        }
        catch (DesktopAppToolsException ex) when (ex.Failure == DesktopAppToolsFailure.Unconfirmed && before is not null)
        {
            if (!await AppearsAsync(threadId, text, before, cancellationToken).ConfigureAwait(false))
            {
                throw;
            }
        }
    }

    private async Task<bool> AppearsAsync(string threadId, string text, SyncCursor before, CancellationToken cancellationToken)
    {
        string expected = text.Trim();
        DateTimeOffset deadline = DateTimeOffset.UtcNow + ConfirmTimeout;
        while (true)
        {
            SessionDelta? delta = _content.ReadSince(threadId, before);
            if (delta is null || delta.Resync)
            {
                return false;
            }

            if (delta.Items.Any(i => i.Kind == SyncItemKind.User && i.Origin == UserMessageOrigin.Delegated && i.Text?.Trim() == expected))
            {
                return true;
            }

            if (DateTimeOffset.UtcNow >= deadline)
            {
                return false;
            }

            await Task.Delay(ConfirmPollInterval, cancellationToken).ConfigureAwait(false);
        }
    }

    private static string FailureOutcome(DesktopAppToolsException ex) => ex.Failure switch
    {
        DesktopAppToolsFailure.Unavailable => "unavailable",
        DesktopAppToolsFailure.Unconfirmed => "unconfirmed",
        _ => $"failed: {ex.Message}",
    };

    private async Task<byte[]> NavigateAsync(ApprovedPhone phone, string threadId, CancellationToken cancellationToken)
    {
        await _tools.NavigateToAsync(threadId, cancellationToken).ConfigureAwait(false);
        Audit(phone.PairingId, phone.PhoneLabel, DesktopSyncCommands.Navigate, threadId, null, "ok");
        return SyncJson.Ok();
    }

    private bool TakeSendSlot(long pairingId)
    {
        DateTimeOffset now = _clock();
        lock (_gate)
        {
            if (!_sendTimes.TryGetValue(pairingId, out Queue<DateTimeOffset>? times))
            {
                _sendTimes[pairingId] = times = new Queue<DateTimeOffset>();
            }

            while (times.Count > 0 && now - times.Peek() > TimeSpan.FromMinutes(1))
            {
                times.Dequeue();
            }

            if (times.Count >= MaxSendsPerMinute)
            {
                return false;
            }

            times.Enqueue(now);
            return true;
        }
    }

    /// <summary>
    /// A delegated message is shown as "from the phone" when the audit log sent that text.
    /// Matched by text alone: a rollout item carries no time to compare against.
    /// </summary>
    private bool FromPhone(SyncItem item) =>
        item.Origin == UserMessageOrigin.Delegated && item.Text is string text && _audit.WasSentFromPhone(text);

    private void Audit(long pairingId, string? label, string command, string? threadId, string? summary, string outcome) =>
        _audit.Add(new SyncAuditEntry(_clock(), pairingId, label, command, threadId, summary, outcome));

    // ---- The send queue --------------------------------------------------------------

    /// <summary>Starts the queue pump and the status polls of subscriptions.</summary>
    public void Start() => _pump ??= new Timer(_ => _ = PumpAsync(), null, QueuePumpInterval, QueuePumpInterval);

    /// <summary>Sends queued messages whose conversation is no longer running. One pass.</summary>
    internal async Task PumpAsync()
    {
        if (!await _pumpGate.WaitAsync(0).ConfigureAwait(false))
        {
            return;
        }

        try
        {
            List<QueuedSend> pending;
            List<QueuedSend> expired;
            lock (_gate)
            {
                DateTimeOffset now = _clock();
                expired = _queue.Where(q => q.ExpiresAt <= now).ToList();
                _queue.RemoveAll(q => q.ExpiresAt <= now);
                pending = _queue.GroupBy(q => q.ThreadId).Select(g => g.First()).ToList();
            }

            foreach (QueuedSend q in expired)
            {
                Audit(q.PairingId, q.PhoneLabel, DesktopSyncCommands.SendMessage, q.ThreadId, SyncAuditLog.SummaryOf(q.Text), "expired");
            }

            if (expired.Count > 0)
            {
                StateChanged?.Invoke();
            }

            foreach (QueuedSend next in pending)
            {
                try
                {
                    DesktopThreadStatus status = await _tools.GetThreadStatusAsync(next.ThreadId, CancellationToken.None).ConfigureAwait(false);
                    if (status.Type == "active")
                    {
                        continue;
                    }

                    // Taken off before sending, so that nothing else sends it meanwhile.
                    int index;
                    lock (_gate)
                    {
                        index = _queue.IndexOf(next);
                        if (index < 0)
                        {
                            continue;
                        }

                        _queue.RemoveAt(index);
                    }

                    try
                    {
                        await DeliverAsync(next.ThreadId, next.Text, CancellationToken.None).ConfigureAwait(false);
                    }
                    catch (DesktopAppToolsException ex) when (ex.Failure == DesktopAppToolsFailure.Unavailable)
                    {
                        // Not sent: the desktop app went away between the status check and
                        // the send. Back in its place, to go out when the app is back.
                        lock (_gate)
                        {
                            if (IsSelected(_state, next.ThreadId))
                            {
                                _queue.Insert(Math.Min(index, _queue.Count), next);
                            }
                        }

                        continue;
                    }

                    Audit(next.PairingId, next.PhoneLabel, DesktopSyncCommands.SendMessage, next.ThreadId, SyncAuditLog.SummaryOf(next.Text), "ok");
                    StateChanged?.Invoke();
                }
                catch (DesktopAppToolsException ex)
                {
                    // Left queued when the desktop app is merely away; dropped when the send
                    // itself failed or may have gone out (sending again could run it twice).
                    if (ex.Failure != DesktopAppToolsFailure.Unavailable)
                    {
                        lock (_gate)
                        {
                            _queue.Remove(next);
                        }

                        Audit(next.PairingId, next.PhoneLabel, DesktopSyncCommands.SendMessage, next.ThreadId, SyncAuditLog.SummaryOf(next.Text), FailureOutcome(ex));
                        StateChanged?.Invoke();
                    }
                }
            }

            await PollSubscriptionStatusAsync().ConfigureAwait(false);
        }
        finally
        {
            _pumpGate.Release();
        }
    }

    /// <param name="ExpiresAt">Set for a message waiting for the desktop app to come up; a running turn is waited for without limit.</param>
    private sealed record QueuedSend(long PairingId, string PhoneLabel, string ThreadId, string Text, DateTimeOffset QueuedAt, DateTimeOffset? ExpiresAt = null);

    // ---- Following a conversation ----------------------------------------------------

    /// <summary>
    /// Starts streaming a conversation's changes to a phone. <paramref name="emit"/>
    /// receives each event body; it is called from background threads, one at a time.
    /// </summary>
    /// <returns>An error body when refused, otherwise null.</returns>
    public byte[]? Subscribe(long pairingId, string subscriptionId, ReadOnlyMemory<byte> body, Func<byte[], Task> emit)
    {
        string? threadId;
        string? cursorToken;
        try
        {
            using JsonDocument document = JsonDocument.Parse(body);
            threadId = Str(document.RootElement, "thread_id");
            cursorToken = Str(document.RootElement, "cursor");
        }
        catch (JsonException)
        {
            return SyncJson.Error("bad_request", "订阅不是 JSON");
        }

        (_, byte[]? refusal) = Admit(pairingId, DesktopSyncCommands.OpenSession, threadId);
        if (refusal is not null)
        {
            return refusal;
        }

        CodexThreadRecord? record = _findThread(threadId!);
        if (record is null)
        {
            return SyncJson.Error("missing", "找不到这个会话的记录");
        }

        SyncJson.TryDecodeCursor(cursorToken, record.RolloutPath, out SyncCursor cursor);
        var subscription = new Subscription(this, pairingId, threadId!, record.RolloutPath, cursor, emit);
        lock (_gate)
        {
            _subscriptions[subscriptionId] = subscription;
        }

        subscription.Start();
        return null;
    }

    public void Unsubscribe(string subscriptionId)
    {
        foreach (Subscription subscription in TakeSubscriptions((id, _) => id == subscriptionId))
        {
            subscription.Dispose();
        }
    }

    /// <summary>Ends every stream, e.g. when the connection to the server drops.</summary>
    public void UnsubscribeAll()
    {
        foreach (Subscription subscription in TakeSubscriptions(_ => true))
        {
            subscription.Dispose();
        }
    }

    private List<Subscription> TakeSubscriptions(Func<Subscription, bool> match) =>
        TakeSubscriptions((_, s) => match(s));

    private List<Subscription> TakeSubscriptions(Func<string, Subscription, bool> match)
    {
        lock (_gate)
        {
            var taken = _subscriptions.Where(p => match(p.Key, p.Value)).ToList();
            foreach (var pair in taken)
            {
                _subscriptions.Remove(pair.Key);
            }

            return taken.Select(p => p.Value).ToList();
        }
    }

    private async Task PollSubscriptionStatusAsync()
    {
        List<Subscription> open;
        lock (_gate)
        {
            open = _subscriptions.Values.ToList();
        }

        foreach (Subscription subscription in open.Where(s => s.ShouldPollStatus(_clock())))
        {
            try
            {
                DesktopThreadStatus status = await _tools.GetThreadStatusAsync(subscription.ThreadId, CancellationToken.None).ConfigureAwait(false);
                await subscription.ReportStatusAsync(status, _clock()).ConfigureAwait(false);
            }
            catch (DesktopAppToolsException)
            {
                // The next poll tries again.
            }
        }
    }

    /// <summary>One phone following one conversation.</summary>
    private sealed class Subscription : IDisposable
    {
        private readonly DesktopSyncAgent _agent;
        private readonly string _rolloutPath;
        private readonly Func<byte[], Task> _emit;
        private readonly SemaphoreSlim _readGate = new(1, 1);
        private RolloutFollower? _follower;
        private SyncCursor _cursor;
        private string? _lastStatus;
        private DateTimeOffset _lastPoll;
        private bool _turnOpen;
        private int _disposed;

        public Subscription(DesktopSyncAgent agent, long pairingId, string threadId, string rolloutPath, SyncCursor cursor, Func<byte[], Task> emit)
        {
            _agent = agent;
            PairingId = pairingId;
            ThreadId = threadId;
            _rolloutPath = rolloutPath;
            _cursor = cursor;
            _emit = emit;
        }

        public long PairingId { get; }

        public string ThreadId { get; }

        public void Start()
        {
            _follower = new RolloutFollower(_rolloutPath, () => _ = ReadAsync());
            _ = ReadAsync();
        }

        private async Task ReadAsync()
        {
            if (Volatile.Read(ref _disposed) != 0 || !await _readGate.WaitAsync(0).ConfigureAwait(false))
            {
                return;
            }

            try
            {
                SessionDelta? delta = _agent._content.ReadSince(ThreadId, _cursor);
                if (delta is null)
                {
                    return;
                }

                if (delta.Resync)
                {
                    await _emit(SyncJson.Write(w => w.WriteString("type", "resync"))).ConfigureAwait(false);
                    End(null);
                    return;
                }

                _cursor = delta.Cursor;
                if (delta.Items.Count == 0)
                {
                    return;
                }

                foreach (SyncItem item in delta.Items)
                {
                    _turnOpen = item.Kind == SyncItemKind.TurnStarted || (_turnOpen && item.Kind != SyncItemKind.TurnEnded);
                }

                await _emit(SyncJson.Write(w =>
                {
                    w.WriteString("type", "items");
                    SyncJson.WriteItems(w, "items", delta.Items, _agent.FromPhone);
                    w.WriteString("cursor", SyncJson.EncodeCursor(delta.Cursor));
                })).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
            {
                ClientLog.Warning("手机同步读取会话记录失败", ex);
            }
            finally
            {
                _readGate.Release();
            }
        }

        /// <summary>
        /// The desktop app's status is polled only while a turn runs: that is when a
        /// pending approval can appear, and the rollout says nothing about it.
        /// </summary>
        public bool ShouldPollStatus(DateTimeOffset now) =>
            Volatile.Read(ref _disposed) == 0 && _turnOpen && now - _lastPoll >= StatusPollInterval;

        public async Task ReportStatusAsync(DesktopThreadStatus status, DateTimeOffset now)
        {
            _lastPoll = now;
            string summary = status.WaitingOnApproval ? "waiting_on_approval" : status.Type;
            if (summary == _lastStatus)
            {
                return;
            }

            _lastStatus = summary;
            await _emit(SyncJson.Write(w =>
            {
                w.WriteString("type", "status");
                w.WriteString("status", status.Type);
                w.WriteBoolean("waiting_on_approval", status.WaitingOnApproval);
            })).ConfigureAwait(false);
        }

        /// <summary>Tells the phone why the stream ends, then stops.</summary>
        public void End(string? reason)
        {
            if (reason is not null && Volatile.Read(ref _disposed) == 0)
            {
                _ = _emit(SyncJson.Write(w => w.WriteString("type", reason)));
            }

            Dispose();
        }

        public void Dispose()
        {
            if (Interlocked.Exchange(ref _disposed, 1) == 0)
            {
                _follower?.Dispose();
            }
        }
    }

    public void Dispose()
    {
        _pump?.Dispose();
        UnsubscribeAll();
    }

    private static string? Str(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object && element.TryGetProperty(property, out JsonElement value) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private static int? Int(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object && element.TryGetProperty(property, out JsonElement value) && value.TryGetInt32(out int number)
            ? Math.Clamp(number, 1, 50)
            : null;
}
