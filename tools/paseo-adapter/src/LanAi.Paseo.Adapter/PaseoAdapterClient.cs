using System.Buffers;
using System.Collections.Concurrent;
using System.Text.Json;

namespace LanAi.Paseo.Adapter;

/// <summary>Tuning knobs. Defaults are chosen for an interactive desktop consumer.</summary>
public sealed record PaseoAdapterOptions
{
    /// <summary>How long a single operation may take before it is abandoned.</summary>
    public TimeSpan RequestTimeout { get; init; } = TimeSpan.FromSeconds(30);
}

/// <summary>
/// The narrow client: everything a 共飞 consumer is allowed to ask Paseo for.
/// </summary>
/// <remarks>
/// <para>
/// What is absent matters as much as what is present. There is no terminal, no
/// file access, no git, no workspace management — not because they are hidden,
/// but because this type cannot express them and the bridge would refuse them.
/// That is the encapsulation boundary the whole adapter exists to create.
/// </para>
/// <para>
/// This type owns correlation and error classification only. It does not spawn
/// processes, does not know where the bridge came from, and must never learn a
/// UI concept — the moment it does, the second consumer stops being able to
/// reuse it.
/// </para>
/// </remarks>
public sealed class PaseoAdapterClient : IAsyncDisposable
{
    private readonly IPaseoChannel _channel;
    private readonly PaseoAdapterOptions _options;
    private readonly ConcurrentDictionary<string, TaskCompletionSource<ResponseEnvelope>> _pending = new();
    private int _nextRequestId;
    private int _disposed;

    public PaseoAdapterClient(IPaseoChannel channel, PaseoAdapterOptions? options = null)
    {
        _channel = channel ?? throw new ArgumentNullException(nameof(channel));
        _options = options ?? new PaseoAdapterOptions();
        _channel.LineReceived += OnLineReceived;
        _channel.Closed += OnClosed;
    }

    /// <summary>Handshake result, available after <see cref="ConnectAsync"/>. Diagnostics only.</summary>
    public HelloResult? Hello { get; private set; }

    /// <summary>Raised for each batch of timeline events. Never raised for an unsubscribed agent.</summary>
    public event EventHandler<TimelineBatch>? TimelineReceived;

    /// <summary>Raised when an agent needs a human: finished, failed, or waiting on a permission.</summary>
    public event EventHandler<AttentionEvent>? AttentionReceived;

    /// <summary>
    /// Waits for the bridge to attach and performs the version handshake.
    /// </summary>
    /// <exception cref="PaseoAdapterException">
    /// <see cref="PaseoErrorCode.ContractMismatch"/> when the bridge speaks a
    /// different contract major — the case that makes a mixed-version install
    /// fail loudly at startup instead of misbehaving three operations later.
    /// </exception>
    public async Task ConnectAsync(string handshakeToken, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(handshakeToken);
        await _channel.OpenAsync(cancellationToken).ConfigureAwait(false);

        var payload = await SendAsync(
            "hello",
            writer =>
            {
                writer.WriteString("token", handshakeToken);
                writer.WriteString("contract", PaseoContract.Version);
            },
            cancellationToken).ConfigureAwait(false);

        var hello = Deserialize(payload, PaseoAdapterJsonContext.Default.HelloResult);
        if (hello?.Contract is null)
        {
            throw new PaseoAdapterException(
                PaseoErrorCode.Internal,
                "Bridge did not report a contract version");
        }

        // Belt and braces: the bridge already rejects a mismatch, but a consumer
        // that trusts an unchecked reply would keep working against a bridge that
        // silently downgraded.
        if (PaseoContract.Major(hello.Contract) != PaseoContract.Major(PaseoContract.Version))
        {
            throw new PaseoAdapterException(
                PaseoErrorCode.ContractMismatch,
                $"Bridge speaks contract {hello.Contract}, this client speaks {PaseoContract.Version}");
        }

        Hello = hello;
    }

    /// <summary>
    /// Daemon reachability and codex readiness.
    /// </summary>
    /// <remarks>
    /// Health deliberately <b>answers</b> when the daemon is down instead of
    /// throwing: callers poll it to discover exactly that, and an exception would
    /// force them to treat a normal state as an error.
    /// </remarks>
    public async Task<HealthSnapshot> GetHealthAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("health", null, cancellationToken).ConfigureAwait(false);
        var health = Deserialize(payload, PaseoAdapterJsonContext.Default.HealthPayload);
        return new HealthSnapshot(
            ParseDaemonState(health?.Daemon),
            health?.Listen,
            health?.DaemonError,
            ParseCodexState(health?.Codex),
            health?.CodexError);
    }

    /// <summary>Agent sessions known to the daemon, newest-first ordering left to the daemon.</summary>
    public async Task<IReadOnlyList<AgentSummary>> ListAgentsAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("agents.list", null, cancellationToken).ConfigureAwait(false);
        var result = Deserialize(payload, PaseoAdapterJsonContext.Default.AgentsListPayload);
        return result?.Agents ?? Array.Empty<AgentSummary>();
    }

    /// <summary>
    /// Directories an agent may be started in, as keys and labels.
    /// </summary>
    /// <remarks>
    /// Paths are not returned and cannot be sent. See <see cref="WorkdirEntry"/>
    /// for why the map lives with the process that owns consent.
    /// </remarks>
    public async Task<IReadOnlyList<WorkdirEntry>> ListWorkdirsAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("workdirs.list", null, cancellationToken).ConfigureAwait(false);
        var result = Deserialize(payload, PaseoAdapterJsonContext.Default.WorkdirsListPayload);
        return result?.Workdirs ?? Array.Empty<WorkdirEntry>();
    }

    /// <summary>Starts a codex session in the directory behind <paramref name="cwdKey"/>.</summary>
    /// <exception cref="PaseoAdapterException">
    /// <see cref="PaseoErrorCode.BadRequest"/> when the key is not registered,
    /// which normally means the consumer and its host disagree about
    /// configuration rather than that the user did something wrong.
    /// </exception>
    public async Task<string> CreateAgentAsync(
        string cwdKey,
        string? model = null,
        string? prompt = null,
        string? title = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(cwdKey);
        var payload = await SendAsync(
            "agents.create",
            writer =>
            {
                writer.WriteString("cwdKey", cwdKey);
                if (!string.IsNullOrEmpty(model)) writer.WriteString("model", model);
                if (!string.IsNullOrEmpty(prompt)) writer.WriteString("prompt", prompt);
                if (!string.IsNullOrEmpty(title)) writer.WriteString("title", title);
            },
            cancellationToken).ConfigureAwait(false);

        var result = Deserialize(payload, PaseoAdapterJsonContext.Default.AgentCreatePayload);
        return result?.AgentId
            ?? throw new PaseoAdapterException(PaseoErrorCode.Internal, "Bridge did not return an agent id");
    }

    /// <summary>Appends a prompt to an existing session. Returns as soon as the daemon accepts it.</summary>
    public async Task SendMessageAsync(string agentId, string text, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(agentId);
        ArgumentException.ThrowIfNullOrWhiteSpace(text);
        await SendAsync(
            "agents.send",
            writer =>
            {
                writer.WriteString("agentId", agentId);
                writer.WriteString("text", text);
            },
            cancellationToken).ConfigureAwait(false);
    }

    /// <summary>Cancels the current turn. The session stays usable.</summary>
    public async Task StopAgentAsync(string agentId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(agentId);
        await SendAsync(
            "agents.stop",
            writer => writer.WriteString("agentId", agentId),
            cancellationToken).ConfigureAwait(false);
    }

    /// <summary>Archives the session. Returns the archive timestamp when the daemon reports one.</summary>
    public async Task<string?> ArchiveAgentAsync(string agentId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(agentId);
        var payload = await SendAsync(
            "agents.archive",
            writer => writer.WriteString("agentId", agentId),
            cancellationToken).ConfigureAwait(false);
        return Deserialize(payload, PaseoAdapterJsonContext.Default.ArchivePayload)?.ArchivedAt;
    }

    /// <summary>
    /// Starts delivering <see cref="TimelineReceived"/> for one agent.
    /// </summary>
    /// <remarks>
    /// The daemon streams only to sessions that asked, and it keeps a small
    /// number of subscriptions per connection; the bridge caps them too and drops
    /// the oldest. Treat a subscription as a view, not as a record: authoritative
    /// history is always a refetch.
    /// </remarks>
    /// <returns>
    /// The agents actually subscribed afterwards. If <paramref name="agentId"/> is
    /// absent from it, the cap evicted something — check before assuming a view is
    /// live.
    /// </returns>
    public async Task<IReadOnlyList<string>> SubscribeTimelineAsync(string agentId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(agentId);
        var payload = await SendAsync(
            "timeline.subscribe",
            writer => writer.WriteString("agentId", agentId),
            cancellationToken).ConfigureAwait(false);
        return Deserialize(payload, PaseoAdapterJsonContext.Default.SubscriptionPayload)?.Subscribed
            ?? Array.Empty<string>();
    }

    /// <summary>Stops delivering timeline events for one agent.</summary>
    /// <returns>The agents still subscribed afterwards.</returns>
    public async Task<IReadOnlyList<string>> UnsubscribeTimelineAsync(string agentId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(agentId);
        var payload = await SendAsync(
            "timeline.unsubscribe",
            writer => writer.WriteString("agentId", agentId),
            cancellationToken).ConfigureAwait(false);
        return Deserialize(payload, PaseoAdapterJsonContext.Default.SubscriptionPayload)?.Subscribed
            ?? Array.Empty<string>();
    }

    /// <summary>
    /// Declares interest in attention events.
    /// </summary>
    /// <remarks>
    /// Attention is never filtered by which timelines are subscribed: an agent
    /// that finished or is blocked must reach the user whether or not anyone is
    /// watching its stream.
    /// </remarks>
    public Task SubscribeNotificationsAsync(CancellationToken cancellationToken = default) =>
        SendAsync("notifications.subscribe", null, cancellationToken);

    /// <summary>Current remote-access state. Safe to poll; changes nothing.</summary>
    public async Task<RelayStatus> GetRelayStatusAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("relay.status", null, cancellationToken).ConfigureAwait(false);
        var status = Deserialize(payload, PaseoAdapterJsonContext.Default.RelayStatusPayload);
        return new RelayStatus(
            status?.Enabled ?? false,
            status?.Endpoint,
            status?.UseTls ?? false,
            status?.ServerId);
    }

    /// <summary>
    /// Turns remote access on and returns a pairing offer.
    /// </summary>
    /// <remarks>
    /// <para>
    /// This is the one call in the adapter that changes who can reach the machine,
    /// so it belongs behind an explicit, informed user action — not behind a
    /// settings toggle that reads like "sync". The returned
    /// <see cref="RelayPairing.Url"/> is a credential; see its documentation.
    /// </para>
    /// <para>
    /// The bridge refuses this operation unless its host granted the relay
    /// operations at spawn time, so a consumer embedding the adapter cannot turn
    /// remote access on behind the user's back.
    /// </para>
    /// </remarks>
    /// <exception cref="PaseoAdapterException">
    /// <see cref="PaseoErrorCode.Unauthorized"/> when the host did not grant relay
    /// operations.
    /// </exception>
    public async Task<RelayPairing> PairRelayAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("relay.pair", null, cancellationToken).ConfigureAwait(false);
        var pairing = Deserialize(payload, PaseoAdapterJsonContext.Default.RelayPairPayload);
        return pairing?.Url is { Length: > 0 } url
            ? new RelayPairing(pairing.Enabled ?? true, url)
            : throw new PaseoAdapterException(PaseoErrorCode.Internal, "The daemon returned no pairing URL");
    }

    /// <summary>
    /// Turns remote access off.
    /// </summary>
    /// <returns>
    /// <c>false</c> always, for "was the offer revoked" — disabling stops the
    /// daemon dialling out but leaves an already-issued offer usable the moment it
    /// is turned back on. Real revocation means replacing the Paseo home so the
    /// server id changes. Surfaced as a return value rather than a comment because
    /// "disable" reads like "revoke" and is not.
    /// </returns>
    public async Task<bool> DisableRelayAsync(CancellationToken cancellationToken = default)
    {
        var payload = await SendAsync("relay.disable", null, cancellationToken).ConfigureAwait(false);
        var result = Deserialize(payload, PaseoAdapterJsonContext.Default.RelayDisablePayload);
        return result?.OfferRevoked ?? false;
    }

    private static DaemonState ParseDaemonState(string? value) => value switch
    {
        "running" => DaemonState.Running,
        "unauthorized" => DaemonState.Unauthorized,
        // Unknown values (a newer bridge) degrade to Down: "not usable" is the
        // safe reading, and it keeps an older consumer honest instead of
        // optimistic.
        _ => DaemonState.Down,
    };

    private static CodexState ParseCodexState(string? value) => value switch
    {
        "ready" => CodexState.Ready,
        "missing" => CodexState.Missing,
        _ => CodexState.Unknown,
    };

    private static T? Deserialize<T>(JsonElement? payload, System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> typeInfo)
        where T : class
    {
        if (payload is null || payload.Value.ValueKind is JsonValueKind.Null or JsonValueKind.Undefined)
        {
            return null;
        }

        try
        {
            return payload.Value.Deserialize(typeInfo);
        }
        catch (JsonException ex)
        {
            throw new PaseoAdapterException(
                PaseoErrorCode.Internal,
                "Bridge returned a payload this client could not read",
                ex.Message);
        }
    }

    private async Task<JsonElement?> SendAsync(
        string op,
        Action<Utf8JsonWriter>? writeArgs,
        CancellationToken cancellationToken)
    {
        ObjectDisposedException.ThrowIf(Volatile.Read(ref _disposed) == 1, this);

        var id = Interlocked.Increment(ref _nextRequestId).ToString(System.Globalization.CultureInfo.InvariantCulture);
        var completion = new TaskCompletionSource<ResponseEnvelope>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = completion;

        try
        {
            await _channel.SendLineAsync(BuildRequest(id, op, writeArgs), cancellationToken).ConfigureAwait(false);

            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(_options.RequestTimeout);
            await using var registration = timeout.Token.Register(
                static state =>
                {
                    var tcs = (TaskCompletionSource<ResponseEnvelope>)state!;
                    tcs.TrySetException(new PaseoAdapterException(
                        PaseoErrorCode.TransportDown,
                        "The bridge did not answer in time"));
                },
                completion).ConfigureAwait(false);

            var envelope = await completion.Task.ConfigureAwait(false);
            if (!envelope.Ok)
            {
                throw new PaseoAdapterException(
                    PaseoAdapterException.ParseCode(envelope.Error?.Code),
                    envelope.Error?.Message ?? "The bridge reported a failure",
                    envelope.Error?.Detail);
            }

            return envelope.Data;
        }
        finally
        {
            _pending.TryRemove(id, out _);
        }
    }

    private static string BuildRequest(string id, string op, Action<Utf8JsonWriter>? writeArgs)
    {
        var buffer = new ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            writer.WriteString("t", "req");
            writer.WriteString("id", id);
            writer.WriteString("op", op);
            if (writeArgs is not null)
            {
                writer.WritePropertyName("args");
                writer.WriteStartObject();
                writeArgs(writer);
                writer.WriteEndObject();
            }

            writer.WriteEndObject();
        }

        return System.Text.Encoding.UTF8.GetString(buffer.WrittenSpan);
    }

    private void OnLineReceived(object? sender, string line)
    {
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(line);
        }
        catch (JsonException)
        {
            // A malformed line cannot be correlated with anything, so there is no
            // request to fail. Dropping it keeps the channel usable; the pending
            // request's own timeout still applies.
            return;
        }

        using (document)
        {
            var root = document.RootElement;
            if (!root.TryGetProperty("t", out var kind))
            {
                return;
            }

            var frameKind = kind.GetString();
            if (frameKind == "evt")
            {
                DispatchEvent(root);
                return;
            }

            if (frameKind != "res")
            {
                // A frame kind this client has never heard of is forward
                // compatibility, not corruption: a newer bridge may emit one.
                return;
            }

            if (!root.TryGetProperty("id", out var idElement) || idElement.GetString() is not { } id)
            {
                return;
            }

            if (!_pending.TryGetValue(id, out var completion))
            {
                return;
            }

            var ok = root.TryGetProperty("ok", out var okElement) && okElement.ValueKind == JsonValueKind.True;
            JsonElement? data = root.TryGetProperty("data", out var dataElement) ? dataElement.Clone() : null;
            ContractErrorPayload? error = null;
            if (!ok && root.TryGetProperty("error", out var errorElement))
            {
                error = errorElement.Deserialize(PaseoAdapterJsonContext.Default.ContractErrorPayload);
            }

            completion.TrySetResult(new ResponseEnvelope(ok, data, error));
        }
    }

    private void DispatchEvent(JsonElement root)
    {
        if (!root.TryGetProperty("topic", out var topicElement) || topicElement.GetString() is not { } topic)
        {
            return;
        }

        if (!root.TryGetProperty("data", out var data))
        {
            return;
        }

        switch (topic)
        {
            case "timeline":
            {
                var batch = data.Deserialize(PaseoAdapterJsonContext.Default.TimelineBatchPayload);
                if (batch?.AgentId is not { } agentId) return;
                var events = (batch.Events ?? Array.Empty<TimelineEventPayload>())
                    .Select(item => new TimelineEvent(
                        item.AgentId ?? agentId,
                        ParseTimelineKind(item.Kind),
                        item.Text,
                        item.ToolName,
                        item.Seq,
                        item.At,
                        item.Raw ?? string.Empty))
                    .ToArray();
                TimelineReceived?.Invoke(this, new TimelineBatch(agentId, events, batch.Dropped ?? 0));
                return;
            }

            case "attention":
            {
                var payload = data.Deserialize(PaseoAdapterJsonContext.Default.AttentionEventPayload);
                if (payload?.AgentId is not { } agentId) return;
                AttentionReceived?.Invoke(this, new AttentionEvent(
                    agentId,
                    ParseAttentionReason(payload.Reason),
                    payload.At,
                    payload.ShouldNotify ?? true,
                    payload.Title,
                    payload.Body));
                return;
            }

            default:
                // Unknown topic from a newer bridge: ignore rather than fail.
                return;
        }
    }

    private static TimelineEventKind ParseTimelineKind(string? value) => value switch
    {
        "user" => TimelineEventKind.User,
        "assistant" => TimelineEventKind.Assistant,
        "reasoning" => TimelineEventKind.Reasoning,
        "tool" => TimelineEventKind.Tool,
        "error" => TimelineEventKind.Error,
        "turn_started" => TimelineEventKind.TurnStarted,
        "turn_completed" => TimelineEventKind.TurnCompleted,
        "turn_failed" => TimelineEventKind.TurnFailed,
        _ => TimelineEventKind.Other,
    };

    private static AttentionReason ParseAttentionReason(string? value) => value switch
    {
        "error" => AttentionReason.Error,
        "permission" => AttentionReason.Permission,
        // Anything else, including an unknown reason, reads as "the turn ended":
        // the least alarming interpretation, and the consumer still surfaces the
        // agent as needing a look.
        _ => AttentionReason.Finished,
    };

    private void OnClosed(object? sender, EventArgs e)
    {
        // No response can arrive after the channel is gone, so every in-flight
        // request must fail now rather than wait out its timeout.
        foreach (var entry in _pending)
        {
            entry.Value.TrySetException(new PaseoAdapterException(
                PaseoErrorCode.TransportDown,
                "The bridge connection closed"));
        }

        _pending.Clear();
    }

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposed, 1) == 1)
        {
            return;
        }

        _channel.LineReceived -= OnLineReceived;
        _channel.Closed -= OnClosed;
        OnClosed(this, EventArgs.Empty);
        await _channel.DisposeAsync().ConfigureAwait(false);
    }

    private readonly record struct ResponseEnvelope(bool Ok, JsonElement? Data, ContractErrorPayload? Error);
}
