using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using LanAi.RelayClient.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>
/// The local rules every phone command passes. These are what stand between a
/// compromised server and the user's computer, so each rule is pinned by a test that
/// fails when the rule is removed.
/// </summary>
public sealed class DesktopSyncAgentTests : IDisposable
{
    private const long PairingId = 5;
    private const string Thread = "01a0ced9-0000-7000-8000-000000000001";

    private readonly string _dir = Path.Combine(Path.GetTempPath(), $"desktop-sync-{Guid.NewGuid():N}");
    private readonly string _rollout;
    private readonly FakeDesktopTools _tools = new();
    private readonly MemoryStateStore _store = new();
    private readonly SyncAuditLog _audit = new(persist: false);
    private readonly ECDsa _phoneKey = ECDsa.Create(ECCurve.NamedCurves.nistP256);
    private DateTimeOffset _now = DateTimeOffset.UtcNow;
    private readonly DesktopSyncAgent _agent;

    public DesktopSyncAgentTests()
    {
        Directory.CreateDirectory(_dir);
        _rollout = Path.Combine(_dir, "rollout.jsonl");
        File.WriteAllText(_rollout, TurnLines("t1", "你好", "你好！"));

        var record = new CodexThreadRecord(Thread, _rollout, @"C:\Work\p", "测试会话", "gpt-5.5", """{"type":"disabled"}""", "never", false);
        CodexThreadRecord? Find(string id) => id == Thread ? record : null;
        _agent = new DesktopSyncAgent(_tools, new SessionContentSync(Find), Find, _store, _audit, clock: () => _now);

        _agent.SetEnabled(true);
        _agent.Select(Thread, "测试会话");
        _agent.ApprovePhone(PairingId, "iPhone", Convert.ToBase64String(_phoneKey.ExportSubjectPublicKeyInfo()));
    }

    public void Dispose()
    {
        _agent.Dispose();
        _phoneKey.Dispose();
        Directory.Delete(_dir, recursive: true);
    }

    // ---- The gate --------------------------------------------------------------------

    [Fact]
    public async Task NothingRunsWhileTheSwitchIsOff()
    {
        _agent.SetEnabled(false);

        JsonElement answer = await Handle(new { type = "sessions.list" });

        Assert.Equal("disabled", answer.GetProperty("error").GetString());
        Assert.Equal("disabled", _audit.Recent(1)[0].Outcome);
    }

    /// <summary>The server can name any pairing id; only ones approved here count.</summary>
    [Fact]
    public async Task APhoneNotApprovedOnThisComputerIsRefused()
    {
        JsonElement answer = await Handle(new { type = "sessions.list" }, pairingId: 99);

        Assert.Equal("not_approved", answer.GetProperty("error").GetString());
    }

    [Fact]
    public async Task AConversationNotSelectedIsRefused()
    {
        JsonElement answer = await Handle(new { type = "session.open", thread_id = "some-other-thread" });

        Assert.Equal("not_selected", answer.GetProperty("error").GetString());
    }

    [Fact]
    public async Task AnUnknownCommandIsRefused()
    {
        JsonElement answer = await Handle(new { type = "fs.read", thread_id = Thread, path = @"C:\Users\x\.ssh\id_ed25519" });

        Assert.Equal("refused", answer.GetProperty("error").GetString());
    }

    [Fact]
    public void AtMostFiveConversationsCanBeSelected()
    {
        for (int i = 0; i < 10; i++)
        {
            _agent.Select($"extra-{i}", null);
        }

        Assert.Equal(DesktopSyncAgent.MaxSyncedSessions, _agent.State.Sessions.Count);
        Assert.False(_agent.Select("one-more", null));
    }

    // ---- Sending ---------------------------------------------------------------------

    [Fact]
    public async Task ASignedMessageIsSent()
    {
        JsonElement answer = await Handle(Signed("请只回复 OK"));

        Assert.True(answer.GetProperty("ok").GetBoolean());
        Assert.False(answer.GetProperty("queued").GetBoolean());
        Assert.Equal([(Thread, "请只回复 OK")], _tools.Sent);
        Assert.Equal("ok", _audit.Recent(1)[0].Outcome);
    }

    /// <summary>What a compromised server would try: a message it made up, or one it altered.</summary>
    [Fact]
    public async Task AnUnsignedOrForgedMessageIsNeverSent()
    {
        using ECDsa server = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        object unsigned = new { type = "message.send", thread_id = Thread, text = "rm -rf ~", mode = "queue" };
        object forged = Signed("rm -rf ~", key: server);
        object altered = Signed("请只回复 OK") is var original ? Replace(original, "text", "rm -rf ~") : null!;

        foreach (object command in new[] { unsigned, forged, altered })
        {
            JsonElement answer = await Handle(command);
            Assert.False(answer.GetProperty("ok").GetBoolean());
        }

        Assert.Empty(_tools.Sent);
    }

    [Fact]
    public async Task ASignedMessageCannotBeReplayed()
    {
        object command = Signed("请只回复 OK");

        await Handle(command);
        JsonElement second = await Handle(command);

        Assert.Equal("bad_signature", second.GetProperty("error").GetString());
        Assert.Single(_tools.Sent);
    }

    [Fact]
    public async Task AnOldSignatureHasExpired()
    {
        object command = Signed("请只回复 OK", signedAt: _now - TimeSpan.FromMinutes(6));

        JsonElement answer = await Handle(command);

        Assert.Equal("bad_signature", answer.GetProperty("error").GetString());
    }

    /// <summary>A signature for one conversation must not work for another.</summary>
    [Fact]
    public async Task ASignatureIsBoundToItsConversation()
    {
        const string other = "02b0ced9-0000-7000-8000-000000000002";
        _agent.Select(other, null);

        JsonElement answer = await Handle(Replace(Signed("请只回复 OK"), "thread_id", other));

        Assert.Equal("bad_signature", answer.GetProperty("error").GetString());
    }

    [Fact]
    public async Task SendsAreRateLimited()
    {
        for (int i = 0; i < DesktopSyncAgent.MaxSendsPerMinute; i++)
        {
            Assert.True((await Handle(Signed($"第 {i} 条"))).GetProperty("ok").GetBoolean());
        }

        JsonElement answer = await Handle(Signed("再来一条"));

        Assert.Equal("rate_limited", answer.GetProperty("error").GetString());
    }

    /// <summary>
    /// Measured: a message sent mid-turn is folded into the running turn and the model
    /// drops the rest of the earlier instruction. So it waits for the turn to end.
    /// </summary>
    [Fact]
    public async Task AMessageToARunningConversationWaitsForTheTurnToEnd()
    {
        _tools.Status[Thread] = new DesktopThreadStatus("active", []);

        JsonElement answer = await Handle(Signed("接着做下一步"));
        await _agent.PumpAsync();

        Assert.True(answer.GetProperty("queued").GetBoolean());
        Assert.Empty(_tools.Sent);

        _tools.Status[Thread] = new DesktopThreadStatus("idle", []);
        await _agent.PumpAsync();

        Assert.Equal([(Thread, "接着做下一步")], _tools.Sent);
        Assert.Equal(0, _agent.QueuedCount);
    }

    [Fact]
    public async Task InsertingIntoTheRunningTurnIsAnExplicitChoice()
    {
        _tools.Status[Thread] = new DesktopThreadStatus("active", []);

        JsonElement answer = await Handle(Signed("插一句", mode: "insert"));

        Assert.False(answer.GetProperty("queued").GetBoolean());
        Assert.Single(_tools.Sent);
    }

    [Fact]
    public async Task DeselectingDropsWhatWasQueued()
    {
        _tools.Status[Thread] = new DesktopThreadStatus("active", []);
        await Handle(Signed("排队中"));

        _agent.Deselect(Thread);
        _tools.Status[Thread] = new DesktopThreadStatus("idle", []);
        await _agent.PumpAsync();

        Assert.Empty(_tools.Sent);
    }

    // ---- Reading ---------------------------------------------------------------------

    [Fact]
    public async Task TheListShowsOnlySelectedConversationsWithTheirPermission()
    {
        _tools.Threads.Add(new DesktopThread(Thread, "idle", @"C:\Work\p", "测试会话", 5));
        _tools.Threads.Add(new DesktopThread("not-selected", "idle", @"C:\Work\q", "别的会话", 6));

        JsonElement answer = await Handle(new { type = "sessions.list" });

        JsonElement session = Assert.Single(answer.GetProperty("sessions").EnumerateArray());
        Assert.Equal(Thread, session.GetProperty("thread_id").GetString());
        Assert.Equal("full_access", session.GetProperty("permission").GetString());
        Assert.True(answer.GetProperty("desktop_running").GetBoolean());
    }

    [Fact]
    public async Task TheListStillAnswersWhenTheDesktopAppIsClosed()
    {
        _tools.Unavailable = true;

        JsonElement answer = await Handle(new { type = "sessions.list" });

        Assert.False(answer.GetProperty("desktop_running").GetBoolean());
        Assert.Equal("unknown", Assert.Single(answer.GetProperty("sessions").EnumerateArray()).GetProperty("status").GetString());
    }

    /// <summary>A message the phone sent is labelled so when the conversation is read back.</summary>
    [Fact]
    public async Task OpeningShowsWhichMessagesCameFromThePhone()
    {
        await Handle(Signed("请只回复 OK"));
        File.AppendAllText(_rollout, DelegatedTurn("t2", "请只回复 OK") + DelegatedTurn("t3", "别的代理发的"));

        JsonElement answer = await Handle(new { type = "session.open", thread_id = Thread });

        string[] origins = answer.GetProperty("items").EnumerateArray()
            .Where(i => i.GetProperty("kind").GetString() == "user")
            .Select(i => i.GetProperty("origin").GetString()!)
            .ToArray();
        Assert.Equal(["desktop", "phone", "delegated"], origins);
        Assert.Matches(@"^\d+\.[0-9a-f]{8}$", answer.GetProperty("cursor").GetString());
    }

    [Fact]
    public async Task AFollowingPhoneReceivesWhatIsAppended()
    {
        JsonElement opened = await Handle(new { type = "session.open", thread_id = Thread });
        var received = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        byte[]? refusal = _agent.Subscribe(PairingId, "s1",
            JsonSerializer.SerializeToUtf8Bytes(new { type = "session.subscribe", thread_id = Thread, cursor = opened.GetProperty("cursor").GetString() }),
            body =>
            {
                using JsonDocument doc = JsonDocument.Parse(body);
                if (doc.RootElement.GetProperty("type").GetString() == "items")
                {
                    received.TrySetResult(doc.RootElement.Clone());
                }

                return Task.CompletedTask;
            });
        Assert.Null(refusal);

        File.AppendAllText(_rollout, TurnLines("t9", "新问题", "新回答"));

        Task finished = await Task.WhenAny(received.Task, Task.Delay(TimeSpan.FromSeconds(5)));
        Assert.Same(received.Task, finished);
        Assert.Contains(received.Task.Result.GetProperty("items").EnumerateArray(), i => i.GetProperty("text").GetString() == "新回答");
    }

    [Fact]
    public async Task DeselectingEndsAPhonesStream()
    {
        var events = new List<string>();
        _agent.Subscribe(PairingId, "s1",
            JsonSerializer.SerializeToUtf8Bytes(new { type = "session.subscribe", thread_id = Thread }),
            body =>
            {
                using JsonDocument doc = JsonDocument.Parse(body);
                lock (events)
                {
                    events.Add(doc.RootElement.GetProperty("type").GetString()!);
                }

                return Task.CompletedTask;
            });

        _agent.Deselect(Thread);
        await Task.Delay(100);

        lock (events)
        {
            Assert.Contains("revoked", events);
        }
    }

    [Fact]
    public void AStreamForAnUnselectedConversationIsRefused()
    {
        byte[]? refusal = _agent.Subscribe(PairingId, "s1",
            JsonSerializer.SerializeToUtf8Bytes(new { type = "session.subscribe", thread_id = "other" }), _ => Task.CompletedTask);

        Assert.NotNull(refusal);
    }

    // ---- Helpers ---------------------------------------------------------------------

    private async Task<JsonElement> Handle(object command, long pairingId = PairingId)
    {
        byte[] answer = await _agent.HandleAsync(pairingId, JsonSerializer.SerializeToUtf8Bytes(command), CancellationToken.None);
        using JsonDocument document = JsonDocument.Parse(answer);
        return document.RootElement.Clone();
    }

    /// <summary>Signs as the phone does: WebCrypto ECDSA P-256, raw r‖s.</summary>
    private Dictionary<string, object> Signed(string text, string mode = "queue", ECDsa? key = null, DateTimeOffset? signedAt = null)
    {
        long ts = (signedAt ?? _now).ToUnixTimeMilliseconds();
        string nonce = Convert.ToBase64String(RandomNumberGenerator.GetBytes(18));
        byte[] message = Encoding.UTF8.GetBytes(SignedSendVerifier.Canonical(PairingId, Thread, mode, text, ts, nonce));
        byte[] sig = (key ?? _phoneKey).SignData(message, HashAlgorithmName.SHA256, DSASignatureFormat.IeeeP1363FixedFieldConcatenation);
        return new Dictionary<string, object>
        {
            ["type"] = "message.send",
            ["thread_id"] = Thread,
            ["text"] = text,
            ["mode"] = mode,
            ["ts"] = ts,
            ["nonce"] = nonce,
            ["sig"] = Convert.ToBase64String(sig),
        };
    }

    private static Dictionary<string, object> Replace(Dictionary<string, object> command, string key, object value) =>
        new(command) { [key] = value };

    private static string Line(string type, string payload) =>
        $$"""{"timestamp":"2026-09-24T00:00:00.000Z","type":"{{type}}","payload":{{payload}}}""" + "\n";

    private static string TurnLines(string turn, string question, string answer) =>
        Line("event_msg", $$"""{"type":"task_started","turn_id":"{{turn}}"}""") +
        Line("event_msg", $$$"""{"type":"item_completed","turn_id":"{{{turn}}}","item":{"type":"UserMessage","id":"u-{{{turn}}}","content":[{"type":"text","text":{{{JsonSerializer.Serialize(question)}}}}]}}""") +
        Line("event_msg", $$$"""{"type":"item_completed","turn_id":"{{{turn}}}","item":{"type":"AgentMessage","id":"a-{{{turn}}}","content":[{"type":"Text","text":{{{JsonSerializer.Serialize(answer)}}}}],"phase":"final_answer"}}""") +
        Line("event_msg", $$"""{"type":"task_complete","turn_id":"{{turn}}","duration_ms":1}""");

    private static string DelegatedTurn(string turn, string text)
    {
        string output = JsonSerializer.Serialize($"<codex_delegation>\n  <source_thread_id>archived</source_thread_id>\n  <input>{text}</input>\n</codex_delegation>");
        return Line("event_msg", $$"""{"type":"task_started","turn_id":"{{turn}}"}""") +
               Line("event_msg", $$$"""{"type":"item_completed","turn_id":"{{{turn}}}","item":{"type":"FunctionCallOutput","id":"f-{{{turn}}}","name":"send_message_to_thread","output":{{{output}}}}}""") +
               Line("event_msg", $$"""{"type":"task_complete","turn_id":"{{turn}}","duration_ms":1}""");
    }

    private sealed class MemoryStateStore : IDesktopSyncStateStore
    {
        private DesktopSyncState _state = DesktopSyncState.Empty;

        public DesktopSyncState Load() => _state;

        public void Save(DesktopSyncState state) => _state = state;
    }

    private sealed class FakeDesktopTools : IDesktopAppTools
    {
        public List<DesktopThread> Threads { get; } = [];

        public Dictionary<string, DesktopThreadStatus> Status { get; } = [];

        public List<(string, string)> Sent { get; } = [];

        public bool Unavailable { get; set; }

        public AppToolsCapabilities Capabilities => new(true, true, true, true);

        public Task<AppToolsCapabilities> ConnectAsync(CancellationToken cancellationToken) => Task.FromResult(Capabilities);

        public Task<IReadOnlyList<DesktopThread>> ListThreadsAsync(int limit, CancellationToken cancellationToken) =>
            Unavailable
                ? throw new DesktopAppToolsException(DesktopAppToolsFailure.Unavailable, "not running")
                : Task.FromResult<IReadOnlyList<DesktopThread>>(Threads);

        public Task<DesktopThreadStatus> GetThreadStatusAsync(string threadId, CancellationToken cancellationToken) =>
            Task.FromResult(Status.TryGetValue(threadId, out DesktopThreadStatus? status) ? status : new DesktopThreadStatus("idle", []));

        public Task SendMessageAsync(string threadId, string prompt, CancellationToken cancellationToken)
        {
            lock (Sent)
            {
                Sent.Add((threadId, prompt));
            }

            return Task.CompletedTask;
        }

        public Task NavigateToAsync(string threadId, CancellationToken cancellationToken) => Task.CompletedTask;
    }
}
