using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>How much data has passed through context compression, summed since install.</summary>
internal readonly record struct ContextFilterUsage(long TotalBytesBefore, long TotalBytesSaved)
{
    public static readonly ContextFilterUsage Zero = new(0, 0);
}

internal interface IContextFilterUsageStore
{
    ContextFilterUsage Load();

    /// <summary>Adds one request's measurement to the running total and returns the new total.</summary>
    ContextFilterUsage Add(long bytesBefore, long bytesSaved);
}

/// <summary>
/// Persists cumulative context-compression usage in a small file of its own.
/// </summary>
/// <remarks>
/// <para>
/// A running total across launches, not a per-session one: the question this answers
/// is "has this actually been saving me anything", which a counter that resets every
/// time the client starts cannot do.
/// </para>
/// <para>
/// <b>The numbers are an estimate, not a measurement.</b> The bundled filter only ever
/// reports byte counts — see <see cref="LocalPawRelay.DescribeContextFilter"/> in
/// <c>LanAi.RelayClient.Transport</c> — there is no tokenizer on this path. Bytes are
/// converted to tokens with the common ~4-bytes-per-token rule of thumb, which fits
/// the ASCII-heavy code and tool output this filter actually touches; it will drift
/// for content that is not. Treat the figure shown as directional, never as a billing
/// number — <c>TodayTokensText</c> elsewhere on this dashboard is the real one, read
/// from the server's own accounting.
/// </para>
/// </remarks>
internal sealed class ContextFilterUsageStore : IContextFilterUsageStore
{
    /// <summary>
    /// Bytes assumed per token when turning a byte count into the estimate shown on
    /// screen. Kept as one named constant so every release means the same thing by
    /// the number, even though the number itself is not exact.
    /// </summary>
    internal const double BytesPerTokenEstimate = 4.0;

    private readonly string _filePath;
    private readonly object _gate = new();

    public ContextFilterUsageStore(string? filePath = null)
    {
        _filePath = filePath ?? DefaultFilePath();
    }

    internal static string DefaultFilePath() => AppPaths.InData("context-filter-usage.json");

    public ContextFilterUsage Load()
    {
        lock (_gate)
        {
            return LoadUnlocked();
        }
    }

    /// <remarks>
    /// Locked around the full read-modify-write. Forwarded requests can be in flight
    /// concurrently (see <c>LocalPawRelay.ServeAsync</c>), and two turns finishing
    /// close together must not both read the same starting total and overwrite each
    /// other's addition — the second write would silently erase the first.
    /// </remarks>
    public ContextFilterUsage Add(long bytesBefore, long bytesSaved)
    {
        if (bytesBefore <= 0)
        {
            return Load();
        }

        bytesSaved = Math.Clamp(bytesSaved, 0, bytesBefore);

        lock (_gate)
        {
            ContextFilterUsage current = LoadUnlocked();
            var updated = new ContextFilterUsage(
                SaturatingAdd(current.TotalBytesBefore, bytesBefore),
                SaturatingAdd(current.TotalBytesSaved, bytesSaved));
            TrySave(updated);
            return updated;
        }
    }

    /// <summary>
    /// Adds two non-negative counts without wrapping past <see cref="long.MaxValue"/>.
    /// </summary>
    /// <remarks>
    /// This is a running total with no ceiling on how long a client stays installed,
    /// so unlike a one-off calculation it is expected to keep climbing indefinitely.
    /// Plain <c>+</c> on <see langword="long"/> is unchecked in this codebase (no
    /// <c>checked</c> context, no overflow analyzer), so at the actual limit it would
    /// silently wrap to a large negative number rather than throw — a counter that
    /// quietly starts counting down is a worse failure than one that simply stops
    /// growing, because nothing about it looks wrong on screen. Both operands are
    /// guaranteed non-negative by every caller (clamped in <see cref="Add"/>, floored in
    /// <see cref="LoadUnlocked"/>), so wraparound is the only overflow direction
    /// possible and checking the sign of the unchecked sum is enough to catch it.
    /// </remarks>
    private static long SaturatingAdd(long a, long b)
    {
        long sum = unchecked(a + b);
        return sum < 0 ? long.MaxValue : sum;
    }

    private ContextFilterUsage LoadUnlocked()
    {
        if (!File.Exists(_filePath))
        {
            return ContextFilterUsage.Zero;
        }

        try
        {
            Totals? totals = JsonSerializer.Deserialize(
                File.ReadAllBytes(_filePath),
                ClientJsonContext.Default.ContextFilterUsageTotals);
            return totals is null
                ? ContextFilterUsage.Zero
                : new ContextFilterUsage(Math.Max(totals.TotalBytesBefore, 0), Math.Max(totals.TotalBytesSaved, 0));
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            // A damaged file cannot be trusted; starting the total over is better than
            // reporting a number nobody can account for.
            return ContextFilterUsage.Zero;
        }
    }

    private void TrySave(ContextFilterUsage usage)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            var totals = new Totals { TotalBytesBefore = usage.TotalBytesBefore, TotalBytesSaved = usage.TotalBytesSaved };
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(totals, ClientJsonContext.Default.ContextFilterUsageTotals));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Losing a persisted increment is a smaller harm than losing this turn's
            // contribution to the in-memory total the caller still gets back.
        }
    }

    /// <summary>Converts a byte count to the estimated token count shown on screen.</summary>
    internal static long EstimateTokens(long bytes) =>
        bytes <= 0 ? 0 : (long)Math.Round(bytes / BytesPerTokenEstimate, MidpointRounding.AwayFromZero);

    /// <summary>The line shown next to 启用上下文压缩.</summary>
    internal static string Describe(ContextFilterUsage usage)
    {
        if (usage.TotalBytesBefore <= 0)
        {
            return "尚无压缩数据";
        }

        long processedTokens = EstimateTokens(usage.TotalBytesBefore);
        long savedTokens = EstimateTokens(usage.TotalBytesSaved);
        double percent = (double)usage.TotalBytesSaved / usage.TotalBytesBefore * 100;
        return $"累计处理约 {FormatCount(processedTokens)} tokens，节省约 {FormatCount(savedTokens)} tokens（{percent:F1}%）";
    }

    /// <summary>
    /// Renders a count the Chinese way — 万 / 亿 — rather than as a long digit string.
    /// </summary>
    /// <remarks>
    /// This is a running total (see <see cref="Add"/>), so unlike most numbers on this
    /// dashboard it is expected to keep growing for as long as the client stays
    /// installed; a raw "N0" that reads fine at launch turns into an unreadable wall of
    /// digits after months of use. The same instinct already applies to byte counts
    /// elsewhere on this path — see <c>LocalPawRelay.Bytes</c>, which shows KB/MB rather
    /// than a raw byte count — this is that same idea for a token count instead of a
    /// byte count, and for 万/亿 instead of K/M because every other string in this UI is
    /// Chinese.
    /// </remarks>
    internal static string FormatCount(long value)
    {
        const long Wan = 10_000;
        const long Yi = 100_000_000;

        if (value < Wan)
        {
            return value.ToString("N0");
        }

        if (value < Yi)
        {
            return $"{value / (double)Wan:0.##}万";
        }

        return $"{value / (double)Yi:0.##}亿";
    }

    internal sealed record Totals
    {
        public long TotalBytesBefore { get; init; }

        public long TotalBytesSaved { get; init; }
    }
}
