using System.Buffers;
using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>A conversation as <c>list_threads</c> reports it.</summary>
/// <param name="Status"><c>idle</c>, <c>active</c>, <c>notLoaded</c> or <c>systemError</c> as measured; passed through as-is.</param>
/// <param name="UpdatedAt">Unix seconds.</param>
public sealed record DesktopThread(string Id, string Status, string? Cwd, string? Title, long UpdatedAt);

/// <summary>A conversation's live status as <c>read_thread</c> reports it.</summary>
/// <remarks>
/// This is the only place a pending approval shows. The rollout records nothing while
/// a turn waits, and <c>list_threads</c> just says <c>active</c>.
/// </remarks>
public sealed record DesktopThreadStatus(string Type, IReadOnlyList<string> ActiveFlags)
{
    public bool WaitingOnApproval => ActiveFlags.Contains("waitingOnApproval", StringComparer.Ordinal);
}

public enum DesktopAppToolsFailure
{
    /// <summary>The desktop app is not running, or its app-tools pipe could not be found.</summary>
    Unavailable,

    /// <summary>The connected desktop app no longer accepts this call in the shape we send it.</summary>
    Unsupported,

    /// <summary>No conversation exists to name as the caller.</summary>
    NoCaller,

    /// <summary>A JSON-RPC error. The pipe answers <c>-32000</c> for, among other things, an unknown caller.</summary>
    RpcError,

    /// <summary>The tool ran and reported <c>success: false</c>; the message is the tool's own text.</summary>
    ToolFailed,

    /// <summary>The answer did not have the shape this client knows.</summary>
    BadResponse,
}

public sealed class DesktopAppToolsException(DesktopAppToolsFailure failure, string message, Exception? inner = null)
    : Exception(message, inner)
{
    public DesktopAppToolsFailure Failure { get; } = failure;
}

/// <summary>
/// Talks to the Codex desktop app through the pipe its own app-tools MCP server uses.
/// </summary>
/// <remarks>
/// <para>
/// A message sent this way is run by the desktop app's own app-server and appears in
/// its window at once. The app-tools pipe exposes 27 tools; this client wraps four and
/// has no generic call, so what it can do is fixed by its type.
/// </para>
/// <para>
/// Calls are serialised on one connection. A dropped connection is rediscovered once —
/// the pipe is renamed whenever the desktop app restarts — and reads are retried after
/// that. <see cref="SendMessageAsync"/> is not: if the pipe dies after the request went
/// out, the message may already be running, and sending it again would run it twice.
/// </para>
/// </remarks>
public sealed class DesktopAppToolsClient : IAsyncDisposable
{
    private static readonly TimeSpan CallTimeout = TimeSpan.FromSeconds(15);
    private static readonly TimeSpan ProbeTimeout = TimeSpan.FromSeconds(2);

    private readonly IAppToolsTransport _transport;
    private readonly Func<string, string?> _findCaller;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private Stream? _stream;
    private long _nextId;

    /// <param name="findCaller">
    /// Given the target conversation (or empty), the id of another real conversation to
    /// name as the caller. Normally <see cref="CodexStateDatabase.FindCallerThread"/>.
    /// </param>
    public DesktopAppToolsClient(IAppToolsTransport transport, Func<string, string?> findCaller)
    {
        _transport = transport;
        _findCaller = findCaller;
    }

    public AppToolsCapabilities Capabilities { get; private set; } = AppToolsCapabilities.None;

    public string? PipeName { get; private set; }

    /// <summary>Finds the app-tools pipe and reads what it accepts. Safe to call again.</summary>
    public async Task<AppToolsCapabilities> ConnectAsync(CancellationToken cancellationToken)
    {
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await DiscoverAsync(cancellationToken).ConfigureAwait(false);
            return Capabilities;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<IReadOnlyList<DesktopThread>> ListThreadsAsync(int limit, CancellationToken cancellationToken)
    {
        using JsonDocument payload = await CallToolAsync(
            AppToolsContract.ListThreads,
            caps => caps.CanList,
            target: string.Empty,
            writer => writer.WriteNumber("limit", Math.Clamp(limit, 1, 50)),
            retry: true,
            cancellationToken).ConfigureAwait(false);

        var threads = new List<DesktopThread>();
        foreach (string member in new[] { "pinnedThreads", "threads" })
        {
            if (payload.RootElement.TryGetProperty(member, out JsonElement list) && list.ValueKind == JsonValueKind.Array)
            {
                threads.AddRange(list.EnumerateArray().Select(ReadThread).OfType<DesktopThread>());
            }
        }

        return threads;
    }

    public async Task<DesktopThreadStatus> GetThreadStatusAsync(string threadId, CancellationToken cancellationToken)
    {
        using JsonDocument payload = await CallToolAsync(
            AppToolsContract.ReadThread,
            caps => caps.CanReadStatus,
            target: threadId,
            writer =>
            {
                writer.WriteString("threadId", threadId);
                writer.WriteNumber("turnLimit", 1);
                writer.WriteNumber("maxOutputCharsPerItem", 0);
            },
            retry: true,
            cancellationToken).ConfigureAwait(false);

        if (!payload.RootElement.TryGetProperty("thread", out JsonElement thread) ||
            !thread.TryGetProperty("status", out JsonElement status) ||
            status.ValueKind != JsonValueKind.Object ||
            GetString(status, "type") is not string type)
        {
            throw new DesktopAppToolsException(DesktopAppToolsFailure.BadResponse, "read_thread returned no thread status.");
        }

        var flags = new List<string>();
        if (status.TryGetProperty("activeFlags", out JsonElement activeFlags) && activeFlags.ValueKind == JsonValueKind.Array)
        {
            flags.AddRange(activeFlags.EnumerateArray()
                .Where(flag => flag.ValueKind == JsonValueKind.String)
                .Select(flag => flag.GetString()!));
        }

        return new DesktopThreadStatus(type, flags);
    }

    /// <summary>Runs <paramref name="prompt"/> as a new message in the conversation. Never retried.</summary>
    /// <remarks>
    /// If the conversation is mid-turn the message is folded into that turn at its next
    /// step, and the model tends to drop whatever it had not finished (measured). Callers
    /// queue while a turn runs; this method does not second-guess them.
    /// </remarks>
    public async Task SendMessageAsync(string threadId, string prompt, CancellationToken cancellationToken)
    {
        using JsonDocument _ = await CallToolAsync(
            AppToolsContract.SendMessage,
            caps => caps.CanSend,
            target: threadId,
            writer =>
            {
                writer.WriteString("threadId", threadId);
                writer.WriteString("prompt", prompt);
            },
            retry: false,
            cancellationToken).ConfigureAwait(false);
    }

    /// <summary>Brings the conversation to the front of the desktop app's window.</summary>
    public async Task NavigateToAsync(string threadId, CancellationToken cancellationToken)
    {
        using JsonDocument _ = await CallToolAsync(
            AppToolsContract.Navigate,
            caps => caps.CanNavigate,
            target: threadId,
            writer => writer.WriteString("threadId", threadId),
            retry: true,
            cancellationToken).ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        await _gate.WaitAsync().ConfigureAwait(false);
        try
        {
            await DropAsync().ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
            _gate.Dispose();
        }
    }

    private async Task<JsonDocument> CallToolAsync(
        string tool,
        Func<AppToolsCapabilities, bool> allowed,
        string target,
        Action<Utf8JsonWriter> writeArguments,
        bool retry,
        CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(CallTimeout);
        CancellationToken token = timeout.Token;

        await _gate.WaitAsync(token).ConfigureAwait(false);
        try
        {
            if (_stream is null)
            {
                await DiscoverAsync(token).ConfigureAwait(false);
            }

            if (!allowed(Capabilities))
            {
                throw new DesktopAppToolsException(
                    DesktopAppToolsFailure.Unsupported,
                    $"The connected Codex desktop app no longer accepts {tool} in the form this client sends.");
            }

            string caller = _findCaller(target)
                ?? throw new DesktopAppToolsException(
                    DesktopAppToolsFailure.NoCaller,
                    "No other Codex conversation exists to name as the caller.");

            byte[] request = BuildToolCall(tool, caller, writeArguments);
            JsonDocument response;
            try
            {
                response = await RoundTripAsync(request, token).ConfigureAwait(false);
            }
            catch (Exception exception) when (IsBrokenPipe(exception) && retry)
            {
                await DropAsync().ConfigureAwait(false);
                await DiscoverAsync(token).ConfigureAwait(false);
                response = await RoundTripAsync(request, token).ConfigureAwait(false);
            }

            using (response)
            {
                return ReadToolPayload(tool, response.RootElement);
            }
        }
        catch (Exception exception) when (IsBrokenPipe(exception))
        {
            await DropAsync().ConfigureAwait(false);
            throw new DesktopAppToolsException(
                DesktopAppToolsFailure.Unavailable,
                $"Lost the connection to the Codex desktop app during {tool}.",
                exception);
        }
        catch (OperationCanceledException)
        {
            // A cancelled read can leave half a frame in the pipe; the next call on this
            // connection would read it as its own answer.
            await DropAsync().ConfigureAwait(false);
            throw;
        }
        finally
        {
            _gate.Release();
        }
    }

    /// <summary>Must be called holding the gate.</summary>
    private async Task DiscoverAsync(CancellationToken cancellationToken)
    {
        await DropAsync().ConfigureAwait(false);

        foreach (string candidate in _transport.ListCandidates())
        {
            Stream? stream = null;

            // Each candidate gets its own short deadline: a pipe that speaks some other
            // protocol may simply never answer, and must not eat the whole call's time.
            using var probe = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            probe.CancelAfter(ProbeTimeout);
            try
            {
                stream = await _transport.ConnectAsync(candidate, probe.Token).ConfigureAwait(false);
                _stream = stream;
                using JsonDocument response = await RoundTripAsync(BuildToolsList(), probe.Token).ConfigureAwait(false);
                if (response.RootElement.TryGetProperty("result", out JsonElement result))
                {
                    Capabilities = AppToolsContract.Evaluate(result);
                    PipeName = candidate;
                    return;
                }
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                // This candidate did not answer in time. Try the next one.
            }
            catch (Exception exception) when (IsBrokenPipe(exception) || exception is TimeoutException or UnauthorizedAccessException)
            {
                // Not ours, or gone between listing and connecting. Try the next one.
            }

            _stream = null;
            if (stream is not null)
            {
                await stream.DisposeAsync().ConfigureAwait(false);
            }
        }

        Capabilities = AppToolsCapabilities.None;
        PipeName = null;
        throw new DesktopAppToolsException(
            DesktopAppToolsFailure.Unavailable,
            "The Codex desktop app is not running, or none of its pipes answered as app-tools.");
    }

    private async Task<JsonDocument> RoundTripAsync(byte[] request, CancellationToken cancellationToken)
    {
        Stream stream = _stream ?? throw new IOException("Not connected.");
        await AppToolsFraming.WriteFrameAsync(stream, request, cancellationToken).ConfigureAwait(false);
        byte[] response = await AppToolsFraming.ReadFrameAsync(stream, cancellationToken).ConfigureAwait(false);
        return JsonDocument.Parse(response);
    }

    private async Task DropAsync()
    {
        Stream? stream = _stream;
        _stream = null;
        if (stream is not null)
        {
            await stream.DisposeAsync().ConfigureAwait(false);
        }
    }

    private static bool IsBrokenPipe(Exception exception) =>
        exception is IOException or EndOfStreamException or ObjectDisposedException or JsonException or InvalidDataException;

    private static JsonDocument ReadToolPayload(string tool, JsonElement response)
    {
        if (response.TryGetProperty("error", out JsonElement error))
        {
            int code = error.TryGetProperty("code", out JsonElement c) && c.TryGetInt32(out int value) ? value : 0;
            throw new DesktopAppToolsException(
                DesktopAppToolsFailure.RpcError,
                $"{tool} failed ({code}): {GetString(error, "message")}");
        }

        if (!response.TryGetProperty("result", out JsonElement result) ||
            !result.TryGetProperty("contentItems", out JsonElement items) ||
            items.ValueKind != JsonValueKind.Array)
        {
            throw new DesktopAppToolsException(DesktopAppToolsFailure.BadResponse, $"{tool} returned no content.");
        }

        string text = string.Concat(items.EnumerateArray()
            .Where(item => GetString(item, "type") == "inputText")
            .Select(item => GetString(item, "text")));

        bool success = result.TryGetProperty("success", out JsonElement ok) && ok.ValueKind == JsonValueKind.True;
        if (!success)
        {
            throw new DesktopAppToolsException(DesktopAppToolsFailure.ToolFailed, text);
        }

        try
        {
            return JsonDocument.Parse(text);
        }
        catch (JsonException exception)
        {
            throw new DesktopAppToolsException(DesktopAppToolsFailure.BadResponse, $"{tool} returned text that is not JSON.", exception);
        }
    }

    private static DesktopThread? ReadThread(JsonElement thread)
    {
        if (thread.ValueKind != JsonValueKind.Object || GetString(thread, "id") is not string id)
        {
            return null;
        }

        return new DesktopThread(
            id,
            GetString(thread, "status") ?? "unknown",
            GetString(thread, "cwd"),
            GetString(thread, "title"),
            thread.TryGetProperty("updatedAt", out JsonElement updated) && updated.TryGetInt64(out long seconds) ? seconds : 0);
    }

    private static string? GetString(JsonElement element, string property) =>
        element.ValueKind == JsonValueKind.Object &&
        element.TryGetProperty(property, out JsonElement value) &&
        value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;

    private byte[] BuildToolsList() => BuildRequest("tools/list", writer =>
    {
        writer.WriteString("threadStartKind", "all");
    });

    private byte[] BuildToolCall(string tool, string caller, Action<Utf8JsonWriter> writeArguments) =>
        BuildRequest("tools/call", writer =>
        {
            writer.WriteStartObject("arguments");
            writeArguments(writer);
            writer.WriteEndObject();

            // The desktop app's own client makes these up when the executor gives none.
            writer.WriteString("callId", $"cofly-{Guid.NewGuid():N}");
            writer.WriteString("namespace", "codex_app");
            writer.WriteString("threadId", caller);
            writer.WriteString("tool", tool);
            writer.WriteString("turnId", $"cofly-turn-{Guid.NewGuid():N}");
        });

    private byte[] BuildRequest(string method, Action<Utf8JsonWriter> writeParams)
    {
        var buffer = new ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            writer.WriteNumber("id", Interlocked.Increment(ref _nextId));
            writer.WriteString("jsonrpc", "2.0");
            writer.WriteString("method", method);
            writer.WriteStartObject("params");
            writeParams(writer);
            writer.WriteEndObject();
            writer.WriteEndObject();
        }

        return buffer.WrittenSpan.ToArray();
    }
}
