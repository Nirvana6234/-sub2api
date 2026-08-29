using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.Workspace.Injection.Cdp;

/// <summary>An event pushed by the page, such as <c>Page.frameNavigated</c>.</summary>
public sealed record CdpEvent(string Method, JsonElement Parameters);

/// <summary>
/// The page accepted the command but the evaluated script threw. A script failure is
/// reported in <c>exceptionDetails</c> rather than as a protocol error, so it would
/// otherwise be indistinguishable from success.
/// </summary>
public sealed class CdpScriptException : Exception
{
    public CdpScriptException(string message)
        : base(message)
    {
    }
}

/// <summary>The peer answered a command with an <c>error</c> member.</summary>
public sealed class CdpProtocolException : Exception
{
    public CdpProtocolException(string method, int code, string message)
        : base($"CDP command '{method}' failed ({code}): {message}")
    {
        Method = method;
        Code = code;
    }

    public string Method { get; }

    public int Code { get; }
}

/// <summary>
/// Speaks the Chrome DevTools Protocol over an injectable transport: correlates
/// replies to command ids, surfaces protocol errors, and raises page events.
/// Reads and writes only the target the caller connected to.
/// </summary>
public sealed class CdpConnection : IDisposable
{
    private readonly ICdpTransport _transport;
    private readonly ConcurrentDictionary<int, TaskCompletionSource<JsonElement>> _pending = new();
    private readonly CancellationTokenSource _shutdown = new();
    private int _nextCommandId;
    private Task? _receiveLoop;
    private bool _disposed;

    public CdpConnection(ICdpTransport transport)
    {
        _transport = transport ?? throw new ArgumentNullException(nameof(transport));
    }

    /// <summary>Raised on the receive loop for every event message.</summary>
    public event EventHandler<CdpEvent>? EventReceived;

    public async Task ConnectAsync(Uri endpoint, CancellationToken cancellationToken)
    {
        await _transport.ConnectAsync(endpoint, cancellationToken).ConfigureAwait(false);
        _receiveLoop = Task.Run(ReceiveLoopAsync);
    }

    /// <summary>
    /// Sends a command and waits for its reply. <paramref name="parameters"/> is
    /// serialized as the <c>params</c> member when supplied.
    /// </summary>
    public async Task<JsonElement> InvokeAsync(
        string method,
        JsonNode? parameters,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(method))
        {
            throw new ArgumentException("A CDP method name is required.", nameof(method));
        }

        var id = Interlocked.Increment(ref _nextCommandId);
        var completion = new TaskCompletionSource<JsonElement>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = completion;

        // Built as a JsonObject rather than serialised from a dictionary of object.
        // Reflection-based serialisation of `object` cannot survive trimming — the
        // trimmer has no way to know which types to keep — and the Avalonia client
        // publishes trimmed. JsonObject carries the same JSON with no reflection at
        // all, so the wire format is unchanged and the trim analyser is satisfied.
        var request = new JsonObject
        {
            ["id"] = id,
            ["method"] = method,
        };
        if (parameters is not null)
        {
            request["params"] = parameters;
        }

        try
        {
            await _transport
                .SendAsync(request.ToJsonString(), cancellationToken)
                .ConfigureAwait(false);
        }
        catch
        {
            _pending.TryRemove(id, out _);
            throw;
        }

        using var linked = CancellationTokenSource.CreateLinkedTokenSource(
            cancellationToken,
            _shutdown.Token);
        using var registration = linked.Token.Register(
            static state => ((TaskCompletionSource<JsonElement>)state!).TrySetCanceled(),
            completion);

        try
        {
            return await completion.Task.ConfigureAwait(false);
        }
        finally
        {
            _pending.TryRemove(id, out _);
        }
    }

    /// <summary>
    /// Evaluates an expression in the page and returns the raw result node, throwing
    /// <see cref="CdpScriptException"/> when the script itself threw.
    /// </summary>
    public async Task<JsonElement> EvaluateAsync(
        string expression,
        CancellationToken cancellationToken)
    {
        var result = await InvokeAsync(
                "Runtime.evaluate",
                new JsonObject
                {
                    ["expression"] = expression,
                    ["returnByValue"] = true,
                    ["awaitPromise"] = true,
                },
                cancellationToken)
            .ConfigureAwait(false);

        ThrowIfScriptFailed(result);
        return result;
    }

    private static void ThrowIfScriptFailed(JsonElement result)
    {
        if (result.ValueKind != JsonValueKind.Object
            || !result.TryGetProperty("exceptionDetails", out var details)
            || details.ValueKind != JsonValueKind.Object)
        {
            return;
        }

        var description = details.TryGetProperty("exception", out var exception)
            && exception.TryGetProperty("description", out var descriptionNode)
                ? descriptionNode.GetString()
                : null;
        var text = details.TryGetProperty("text", out var textNode)
            ? textNode.GetString()
            : null;

        throw new CdpScriptException(
            description ?? text ?? "The evaluated script threw an unspecified error.");
    }

    private async Task ReceiveLoopAsync()
    {
        try
        {
            while (!_shutdown.IsCancellationRequested)
            {
                var payload = await _transport
                    .ReceiveAsync(_shutdown.Token)
                    .ConfigureAwait(false);
                if (payload is null)
                {
                    break;
                }

                Dispatch(payload);
            }
        }
        catch (OperationCanceledException)
        {
            // Shutdown requested.
        }
        finally
        {
            FailPending(new IOException("The CDP channel closed."));
        }
    }

    private void Dispatch(string payload)
    {
        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(payload);
        }
        catch (JsonException)
        {
            return;
        }

        using (document)
        {
            var root = document.RootElement;

            if (root.TryGetProperty("id", out var idNode) && idNode.TryGetInt32(out var id))
            {
                if (!_pending.TryRemove(id, out var completion))
                {
                    return;
                }

                if (root.TryGetProperty("error", out var errorNode))
                {
                    var code = errorNode.TryGetProperty("code", out var codeNode)
                        && codeNode.TryGetInt32(out var parsedCode) ? parsedCode : 0;
                    var message = errorNode.TryGetProperty("message", out var messageNode)
                        ? messageNode.GetString() ?? string.Empty
                        : string.Empty;
                    completion.TrySetException(
                        new CdpProtocolException(MethodOf(root), code, message));
                    return;
                }

                var result = root.TryGetProperty("result", out var resultNode)
                    ? resultNode.Clone()
                    : default;
                completion.TrySetResult(result);
                return;
            }

            if (root.TryGetProperty("method", out var methodNode))
            {
                var method = methodNode.GetString();
                if (string.IsNullOrEmpty(method))
                {
                    return;
                }

                var parameters = root.TryGetProperty("params", out var paramsNode)
                    ? paramsNode.Clone()
                    : default;
                EventReceived?.Invoke(this, new CdpEvent(method, parameters));
            }
        }
    }

    private static string MethodOf(JsonElement root)
        => root.TryGetProperty("method", out var node) ? node.GetString() ?? "?" : "?";

    private void FailPending(Exception exception)
    {
        foreach (var id in _pending.Keys)
        {
            if (_pending.TryRemove(id, out var completion))
            {
                completion.TrySetException(exception);
            }
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _shutdown.Cancel();
        _transport.Dispose();
        _shutdown.Dispose();
    }
}
