using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.Workspace.Injection.Cdp;

namespace LanAi.Workspace.Injection.Sentinel;

/// <summary>How close the official account is to its usage limit.</summary>
public enum CodexLimitLevel
{
    /// <summary>The detector has not reported yet, or the page is not readable.</summary>
    Unknown,

    /// <summary>No limit signal present.</summary>
    Normal,

    /// <summary>
    /// A usage percentage crossed the warning threshold. The limit is a continuous
    /// percentage rather than a boolean, so the switch can be offered before work
    /// actually stops.
    /// </summary>
    Approaching,

    /// <summary>The limit has been hit — a reset surface or its text is present.</summary>
    Reached,
}

/// <summary>Raw facts reported by the in-page detector, before any policy is applied.</summary>
public sealed record CodexLimitFacts
{
    public bool ModalVisible { get; init; }

    public bool BannerVisible { get; init; }

    public bool ReachedText { get; init; }

    public bool UsageLimitsText { get; init; }

    /// <summary>Raw "resets at/in …" text, unparsed — its format is locale dependent.</summary>
    public string? ResetText { get; init; }

    /// <summary>Percentage of the allowance consumed, when the page exposes one.</summary>
    public double? UsedPercent { get; init; }

    public IReadOnlyList<string> Signals { get; init; } = [];

    /// <summary>True when the traversal hit its node cap, so the scan may be partial.</summary>
    public bool Capped { get; init; }

    public int ShadowRoots { get; init; }
}

/// <summary>What the detector's text patterns made of a sample string.</summary>
public sealed record CodexTextVerdict(
    bool Reached,
    bool UsageLimits,
    string? ResetText,
    bool ExcludedAsOtherLimit);

public sealed record CodexLimitSnapshot(
    CodexLimitLevel Level,
    CodexLimitFacts Facts,
    DateTimeOffset ObservedAt)
{
    /// <summary>True when the user should be offered the relay switch.</summary>
    public bool ShouldPromptSwitch => Level is CodexLimitLevel.Reached or CodexLimitLevel.Approaching;
}

public sealed record CodexLimitSentinelOptions
{
    public TimeSpan PollInterval { get; init; } = TimeSpan.FromSeconds(5);

    /// <summary>Percentage at or above which <see cref="CodexLimitLevel.Approaching"/> is reported.</summary>
    public double ApproachingPercent { get; init; } = 85d;

    /// <summary>
    /// Ceiling on a single detector read. Without it, one unanswered CDP command would
    /// stall the poll loop for the lifetime of the connection.
    /// </summary>
    public TimeSpan PollTimeout { get; init; } = TimeSpan.FromSeconds(10);
}

/// <summary>
/// Watches the official app for usage-limit signals and raises an event when the
/// state changes.
/// </summary>
/// <remarks>
/// Network interception cannot serve this purpose: the API calls are issued by the
/// Electron main process, so the page target's Network domain never sees them. The
/// signal is instead read from the renderer, where the app keeps its own limit state
/// and surfaces.
///
/// Transport is a poll rather than a CDP binding: the in-page detector already uses a
/// MutationObserver to keep its state fresh (so transient surfaces are not missed
/// between polls), and polling re-installs the detector for free after a navigation.
///
/// Every failure is swallowed into <see cref="CodexLimitLevel.Unknown"/> — a broken
/// sentinel must never degrade the official app.
/// </remarks>
public sealed class CodexLimitSentinel : IDisposable
{
    private readonly CdpConnection _connection;
    private readonly CodexLimitSentinelOptions _options;
    private readonly CancellationTokenSource _shutdown = new();
    private Task? _loop;
    private bool _disposed;

    public CodexLimitSentinel(CdpConnection connection, CodexLimitSentinelOptions? options = null)
    {
        _connection = connection ?? throw new ArgumentNullException(nameof(connection));
        _options = options ?? new CodexLimitSentinelOptions();
    }

    /// <summary>Raised when the level or a material fact changes.</summary>
    public event EventHandler<CodexLimitSnapshot>? StateChanged;

    public CodexLimitSnapshot? Current { get; private set; }

    /// <summary>Installs the detector for the current and all future documents.</summary>
    public async Task InstallAsync(CancellationToken cancellationToken)
    {
        await _connection.InvokeAsync("Page.enable", null, cancellationToken).ConfigureAwait(false);
        await _connection.InvokeAsync(
                "Page.addScriptToEvaluateOnNewDocument",
                new JsonObject { ["source"] = LimitDetectorScript.Source },
                cancellationToken)
            .ConfigureAwait(false);
        await _connection.EvaluateAsync(LimitDetectorScript.Source, cancellationToken)
            .ConfigureAwait(false);
    }

    /// <summary>Installs the detector and starts polling it.</summary>
    public async Task StartAsync(CancellationToken cancellationToken)
    {
        await InstallAsync(cancellationToken).ConfigureAwait(false);
        _loop = Task.Run(() => PollLoopAsync(_shutdown.Token), CancellationToken.None);
    }

    /// <summary>
    /// Reads the detector once. Returns an <see cref="CodexLimitLevel.Unknown"/>
    /// snapshot when the page cannot be read.
    /// </summary>
    public async Task<CodexLimitSnapshot> PollOnceAsync(CancellationToken cancellationToken)
    {
        using var budget = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        budget.CancelAfter(_options.PollTimeout);

        string? payload;
        try
        {
            var result = await _connection
                .EvaluateAsync(
                    "(function(){var s=window.__coflySentinel;"
                        + "return s&&s.snapshot?JSON.stringify(s.snapshot()):null;})()",
                    budget.Token)
                .ConfigureAwait(false);
            payload = ReadStringValue(result);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception exception) when (
            exception is CdpProtocolException
                or CdpScriptException
                or IOException
                or OperationCanceledException)
        {
            return Publish(new CodexLimitSnapshot(
                CodexLimitLevel.Unknown,
                new CodexLimitFacts(),
                DateTimeOffset.UtcNow));
        }

        if (string.IsNullOrWhiteSpace(payload) || payload == "null")
        {
            // The detector is gone — most likely the document was replaced before
            // the registered script ran. Re-install and report Unknown for this tick.
            await TryReinstallAsync(cancellationToken).ConfigureAwait(false);
            return Publish(new CodexLimitSnapshot(
                CodexLimitLevel.Unknown,
                new CodexLimitFacts(),
                DateTimeOffset.UtcNow));
        }

        var facts = ParseFacts(payload);
        return Publish(new CodexLimitSnapshot(Classify(facts), facts, DateTimeOffset.UtcNow));
    }

    /// <summary>
    /// Runs the detector's text patterns against a sample string inside the page and
    /// reports what matched.
    /// </summary>
    /// <remarks>
    /// The text patterns are the sentinel's most failure-prone part — they are locale
    /// dependent and must reject look-alike limits such as the workspace invite cap.
    /// They live in JavaScript and so cannot be reached by the C# unit tests; this
    /// entry point lets a live check validate them in the real engine without reading
    /// any page content.
    /// </remarks>
    public async Task<CodexTextVerdict?> MatchTextAsync(string sample, CancellationToken cancellationToken)
    {
        var expression =
            $"(function(){{var s=window.__coflySentinel;"
            + $"return s&&s.matchText?JSON.stringify(s.matchText({JsonSerializer.Serialize(sample)})):null;}})()";

        try
        {
            var result = await _connection.EvaluateAsync(expression, cancellationToken)
                .ConfigureAwait(false);
            var payload = ReadStringValue(result);
            if (string.IsNullOrWhiteSpace(payload) || payload == "null")
            {
                return null;
            }

            using var document = JsonDocument.Parse(payload);
            var root = document.RootElement;
            return new CodexTextVerdict(
                Bool(root, "reached"),
                Bool(root, "usageLimits"),
                String(root, "resetText"),
                Bool(root, "excludedAsOtherLimit"));
        }
        catch (Exception exception) when (
            exception is CdpProtocolException or CdpScriptException or IOException or JsonException)
        {
            return null;
        }
    }

    /// <summary>Applies the switch-prompt policy to detector facts.</summary>
    internal CodexLimitLevel Classify(CodexLimitFacts facts)
    {
        if (facts.ModalVisible || facts.ReachedText || facts.UsedPercent >= 100d)
        {
            return CodexLimitLevel.Reached;
        }

        // UNVERIFIED: no real limited state has ever been observed, so it is not known
        // whether the page exposes a usage percentage at all. The detector guesses it
        // from a progressbar's aria-valuenow or an "NN%" label. Until that is confirmed,
        // treat the early-warning path as untested rather than working.
        if (facts.UsedPercent is { } percent && percent >= _options.ApproachingPercent)
        {
            return CodexLimitLevel.Approaching;
        }

        return CodexLimitLevel.Normal;
    }

    internal static CodexLimitFacts ParseFacts(string json)
    {
        try
        {
            using var document = JsonDocument.Parse(json);
            var root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object)
            {
                return new CodexLimitFacts();
            }

            return new CodexLimitFacts
            {
                ModalVisible = Bool(root, "modal"),
                BannerVisible = Bool(root, "banner"),
                ReachedText = Bool(root, "reachedText"),
                UsageLimitsText = Bool(root, "usageLimitsText"),
                ResetText = String(root, "resetText"),
                UsedPercent = Number(root, "percent"),
                Capped = Bool(root, "capped"),
                ShadowRoots = (int)(Number(root, "roots") ?? 0),
                Signals = root.TryGetProperty("signals", out var signals)
                    && signals.ValueKind == JsonValueKind.Array
                        ? signals.EnumerateArray()
                            .Where(item => item.ValueKind == JsonValueKind.String)
                            .Select(item => item.GetString()!)
                            .ToArray()
                        : [],
            };
        }
        catch (JsonException)
        {
            return new CodexLimitFacts();
        }
    }

    private static bool Bool(JsonElement root, string name)
        => root.TryGetProperty(name, out var node)
            && node.ValueKind == JsonValueKind.True;

    private static string? String(JsonElement root, string name)
        => root.TryGetProperty(name, out var node) && node.ValueKind == JsonValueKind.String
            ? node.GetString()
            : null;

    private static double? Number(JsonElement root, string name)
        => root.TryGetProperty(name, out var node)
            && node.ValueKind == JsonValueKind.Number
            && node.TryGetDouble(out var value)
                ? value
                : null;

    private static string? ReadStringValue(JsonElement result)
    {
        if (result.ValueKind != JsonValueKind.Object
            || !result.TryGetProperty("result", out var inner))
        {
            return null;
        }

        return inner.TryGetProperty("value", out var value) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;
    }

    private CodexLimitSnapshot Publish(CodexLimitSnapshot snapshot)
    {
        var previous = Current;
        Current = snapshot;

        if (previous is null || IsMaterialChange(previous, snapshot))
        {
            StateChanged?.Invoke(this, snapshot);
        }

        return snapshot;
    }

    /// <summary>
    /// Suppresses the per-tick noise: only a level change, a crossed percentage, or a
    /// changed reset time is worth telling the user about.
    /// </summary>
    private static bool IsMaterialChange(CodexLimitSnapshot previous, CodexLimitSnapshot current)
    {
        if (previous.Level != current.Level)
        {
            return true;
        }

        if (!string.Equals(previous.Facts.ResetText, current.Facts.ResetText, StringComparison.Ordinal))
        {
            return true;
        }

        var before = previous.Facts.UsedPercent;
        var after = current.Facts.UsedPercent;
        if (before is null != after is null)
        {
            return true;
        }

        return before is not null && after is not null && Math.Abs(before.Value - after.Value) >= 5d;
    }

    private async Task TryReinstallAsync(CancellationToken cancellationToken)
    {
        using var budget = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        budget.CancelAfter(_options.PollTimeout);

        try
        {
            await _connection.EvaluateAsync(LimitDetectorScript.Source, budget.Token)
                .ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception exception) when (
            exception is CdpProtocolException
                or CdpScriptException
                or IOException
                or OperationCanceledException)
        {
            // Next tick tries again.
        }
    }

    private async Task PollLoopAsync(CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                await PollOnceAsync(cancellationToken).ConfigureAwait(false);
                await Task.Delay(_options.PollInterval, cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch
            {
                // The sentinel is strictly additive; keep polling.
                try
                {
                    await Task.Delay(_options.PollInterval, cancellationToken).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
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
        _shutdown.Dispose();
    }
}
