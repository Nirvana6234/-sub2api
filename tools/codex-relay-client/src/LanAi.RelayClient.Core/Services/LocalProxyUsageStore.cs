using System.IO;
using System.Text.Json;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;

namespace LanAi.RelayClient.Services;

/// <summary>Codex's local-proxy use on one account on one (local) day.</summary>
internal sealed record LocalProxyUsageDay
{
    public string Date { get; init; } = string.Empty;

    public long AccountId { get; init; }

    public long Requests { get; init; }

    public long InputTokens { get; init; }

    public long OutputTokens { get; init; }

    public long CachedTokens { get; init; }
}

/// <summary>Sums of local-proxy use over some set of days.</summary>
internal readonly record struct LocalProxyUsageTotals(long Requests, long InputTokens, long OutputTokens, long CachedTokens)
{
    public long TotalTokens => InputTokens + OutputTokens;
}

internal interface ILocalProxyUsageStore
{
    IReadOnlyList<LocalProxyUsageDay> Load();

    void Add(LocalProxyUsage usage);
}

/// <summary>
/// Local-proxy usage, kept on this machine only: this traffic never passes the relay
/// server, so nothing there records it.
/// </summary>
/// <remarks>
/// <para>
/// The official API's own token counts, not an estimate — unlike the compression total in
/// <see cref="ContextFilterUsageStore"/>. Counted per request that reported usage.
/// </para>
/// <para>
/// Same discipline as that store: written atomically through a temporary file, serialized
/// under one lock so concurrent turns cannot lose each other's increments, saturating
/// rather than wrapping, and a damaged file starts over instead of throwing. Only the last
/// <see cref="KeepDays"/> days are kept.
/// </para>
/// </remarks>
internal sealed class LocalProxyUsageStore : ILocalProxyUsageStore
{
    internal const int KeepDays = 31;

    private readonly string _filePath;
    private readonly Func<DateTimeOffset> _clock;
    private readonly object _gate = new();

    public LocalProxyUsageStore(string? filePath = null, Func<DateTimeOffset>? clock = null)
    {
        _filePath = filePath ?? AppPaths.InData("local-proxy-usage.json");
        _clock = clock ?? (() => DateTimeOffset.Now);
    }

    internal static string DateKey(DateTimeOffset moment) => moment.ToString("yyyy-MM-dd", System.Globalization.CultureInfo.InvariantCulture);

    public IReadOnlyList<LocalProxyUsageDay> Load()
    {
        lock (_gate)
        {
            return LoadUnlocked();
        }
    }

    public void Add(LocalProxyUsage usage)
    {
        ArgumentNullException.ThrowIfNull(usage);
        DateTimeOffset now = _clock();
        string today = DateKey(now);
        string oldest = DateKey(now.AddDays(-(KeepDays - 1)));

        lock (_gate)
        {
            List<LocalProxyUsageDay> days = [.. LoadUnlocked().Where(d => string.CompareOrdinal(d.Date, oldest) >= 0)];
            int index = days.FindIndex(d => d.Date == today && d.AccountId == usage.AccountId);
            LocalProxyUsageDay current = index >= 0
                ? days[index]
                : new LocalProxyUsageDay { Date = today, AccountId = usage.AccountId };
            LocalProxyUsageDay updated = current with
            {
                Requests = SaturatingAdd(current.Requests, 1),
                InputTokens = SaturatingAdd(current.InputTokens, Math.Max(usage.InputTokens, 0)),
                OutputTokens = SaturatingAdd(current.OutputTokens, Math.Max(usage.OutputTokens, 0)),
                CachedTokens = SaturatingAdd(current.CachedTokens, Math.Max(usage.CachedTokens, 0)),
            };
            if (index >= 0)
            {
                days[index] = updated;
            }
            else
            {
                days.Add(updated);
            }

            TrySave(days);
        }
    }

    /// <summary>Totals over the days from <paramref name="since"/> on.</summary>
    internal static LocalProxyUsageTotals Sum(IEnumerable<LocalProxyUsageDay> days, string since)
    {
        long requests = 0, input = 0, output = 0, cached = 0;
        foreach (LocalProxyUsageDay day in days)
        {
            if (string.CompareOrdinal(day.Date, since) < 0)
            {
                continue;
            }

            requests = SaturatingAdd(requests, day.Requests);
            input = SaturatingAdd(input, day.InputTokens);
            output = SaturatingAdd(output, day.OutputTokens);
            cached = SaturatingAdd(cached, day.CachedTokens);
        }

        return new LocalProxyUsageTotals(requests, input, output, cached);
    }

    /// <summary>One line for a card: requests and tokens, in 万/亿 when large.</summary>
    internal static string Describe(LocalProxyUsageTotals totals) =>
        totals.Requests == 0
            ? "暂无用量"
            : $"{ContextFilterUsageStore.FormatCount(totals.Requests)} 次请求 · " +
              $"输入 {ContextFilterUsageStore.FormatCount(totals.InputTokens)} · 输出 {ContextFilterUsageStore.FormatCount(totals.OutputTokens)} tokens";

    private static long SaturatingAdd(long a, long b)
    {
        long sum = unchecked(a + b);
        return sum < 0 ? long.MaxValue : sum;
    }

    private List<LocalProxyUsageDay> LoadUnlocked()
    {
        if (!File.Exists(_filePath))
        {
            return [];
        }

        try
        {
            State? state = JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), ClientJsonContext.Default.LocalProxyUsageState);
            return state?.Days?.Where(d => d is not null).ToList() ?? [];
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return [];
        }
    }

    private void TrySave(List<LocalProxyUsageDay> days)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(new State { Days = days }, ClientJsonContext.Default.LocalProxyUsageState));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // A lost increment is a smaller harm than failing the turn that produced it.
        }
    }

    internal sealed record State
    {
        public List<LocalProxyUsageDay> Days { get; init; } = [];
    }
}
