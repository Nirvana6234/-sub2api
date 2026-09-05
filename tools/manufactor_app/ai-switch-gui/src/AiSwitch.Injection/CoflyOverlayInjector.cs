using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Injection.Cdp;

namespace LanAi.Workspace.Injection;

/// <summary>
/// Installs the 共飞 overlay into the official app's page and keeps it alive across
/// navigations.
/// </summary>
/// <remarks>
/// A one-shot <c>Runtime.evaluate</c> is not enough: the overlay disappears whenever
/// the page reloads. The script is registered with
/// <c>Page.addScriptToEvaluateOnNewDocument</c> so future documents carry it, then
/// evaluated once for the document that is already loaded, and re-applied on
/// main-frame navigation.
///
/// The overlay is additive only. Any failure here must leave the official app fully
/// usable, so callers should treat every exception as "run without the overlay".
/// </remarks>
public sealed class CoflyOverlayInjector : IDisposable
{
    private readonly CdpConnection _connection;
    private string? _script;
    private string? _mainFrameId;
    private bool _subscribed;
    private bool _disposed;

    public CoflyOverlayInjector(CdpConnection connection)
    {
        _connection = connection ?? throw new ArgumentNullException(nameof(connection));
    }

    /// <summary>
    /// Registers <paramref name="script"/> for current and future documents.
    /// </summary>
    public async Task InstallAsync(string script, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(script))
        {
            throw new ArgumentException("An overlay script is required.", nameof(script));
        }

        _script = script;

        await _connection.InvokeAsync("Page.enable", null, cancellationToken).ConfigureAwait(false);
        await _connection.InvokeAsync(
                "Page.addScriptToEvaluateOnNewDocument",
                new JsonObject { ["source"] = script },
                cancellationToken)
            .ConfigureAwait(false);

        // The document that is already open predates the registration above.
        await _connection.EvaluateAsync(script, cancellationToken).ConfigureAwait(false);

        if (!_subscribed)
        {
            _connection.EventReceived += OnEventReceived;
            _subscribed = true;
        }
    }

    /// <summary>
    /// Hands a state snapshot to the overlay. The payload is serialized and passed to
    /// <c>window.__cofly.render</c>, which the overlay script defines.
    /// </summary>
    /// <remarks>
    /// Takes a <see cref="JsonNode"/> rather than <c>object</c>. Serialising an
    /// arbitrary object needs reflection over types the trimmer cannot discover, which
    /// the Avalonia client's trimmed publish rejects outright; a node carries the same
    /// payload with none of that. Callers build the shape explicitly, which also makes
    /// the key names the overlay script depends on visible at the call site.
    /// </remarks>
    public Task PushStateAsync(JsonNode state, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(state);
        var json = state.ToJsonString();
        var expression =
            $"(window.__cofly && window.__cofly.render) ? window.__cofly.render({json}) : false";
        return _connection.EvaluateAsync(expression, cancellationToken);
    }

    private void OnEventReceived(object? sender, CdpEvent cdpEvent)
    {
        if (!string.Equals(cdpEvent.Method, "Page.frameNavigated", StringComparison.Ordinal))
        {
            return;
        }

        if (!IsMainFrame(cdpEvent.Parameters))
        {
            return;
        }

        var script = _script;
        if (script is null)
        {
            return;
        }

        // Fire and forget: a failed re-injection must not disturb the page.
        _ = ReapplyAsync(script);
    }

    private async Task ReapplyAsync(string script)
    {
        try
        {
            await _connection.EvaluateAsync(script, CancellationToken.None).ConfigureAwait(false);
        }
        catch (Exception exception) when (
            exception is CdpProtocolException
                or CdpScriptException
                or IOException
                or OperationCanceledException)
        {
            // Overlay stays absent until the next navigation; the app is unaffected.
        }
    }

    /// <summary>
    /// A main-frame navigation has no <c>parentId</c>. The first frame observed is
    /// remembered so later sub-frame events cannot be mistaken for it.
    /// </summary>
    private bool IsMainFrame(JsonElement parameters)
    {
        if (parameters.ValueKind != JsonValueKind.Object
            || !parameters.TryGetProperty("frame", out var frame)
            || frame.ValueKind != JsonValueKind.Object)
        {
            return false;
        }

        if (frame.TryGetProperty("parentId", out var parentId)
            && parentId.ValueKind == JsonValueKind.String
            && !string.IsNullOrEmpty(parentId.GetString()))
        {
            return false;
        }

        var id = frame.TryGetProperty("id", out var idNode) ? idNode.GetString() : null;
        if (string.IsNullOrEmpty(id))
        {
            return false;
        }

        _mainFrameId ??= id;
        return string.Equals(_mainFrameId, id, StringComparison.Ordinal);
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        if (_subscribed)
        {
            _connection.EventReceived -= OnEventReceived;
            _subscribed = false;
        }
    }
}
