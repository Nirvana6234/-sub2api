using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

/// <summary>
/// Every group the account may use, with the account's own rates — fetched once per
/// refresh and left unfiltered.
/// </summary>
/// <remarks>
/// <para>
/// Each tool narrows it to what it can actually use: Codex to OpenAI and the
/// Claude-over-Codex bridge (<c>DashboardViewModel.IsSelectable</c>), the editor
/// plug-ins to Claude groups. Filtering once, for Codex, and deriving everything else
/// from that list is what would hide a Kimi group from a Kimi page: it never survives
/// the Codex filter.
/// </para>
/// <para>
/// The rates call may fail on its own. Without it every group shows its default
/// multiplier, which is still true for anyone without a personal deal; losing the
/// whole list instead would also cost the user the ability to switch.
/// </para>
/// </remarks>
public sealed class GroupCatalog
{
    private static readonly IReadOnlyDictionary<long, double> NoRates = new Dictionary<long, double>();

    private readonly IRelayServerClient _client;
    private readonly RefreshState _refresh;
    private IReadOnlyDictionary<long, double> _rates = NoRates;

    internal GroupCatalog(IRelayServerClient client, RefreshState refresh)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _refresh = refresh ?? throw new ArgumentNullException(nameof(refresh));
    }

    /// <summary>The groups as the server listed them, in its order.</summary>
    public IReadOnlyList<RelayGroup> Groups { get; private set; } = [];

    /// <summary>
    /// Reads the groups and the account's rates. A failure of the group list itself
    /// propagates, for the calling card to grey itself; a failure of the rates alone is
    /// absorbed here.
    /// </summary>
    internal async Task LoadAsync(string token, CancellationToken cancellationToken)
    {
        IReadOnlyList<RelayGroup> groups =
            await _client.GetAvailableGroupsAsync(token, cancellationToken).ConfigureAwait(true);

        IReadOnlyDictionary<long, double> rates;
        try
        {
            rates = await _client.GetUserGroupRatesAsync(token, cancellationToken).ConfigureAwait(true);
        }
        catch (Exception ex) when (_refresh.Observe(ex))
        {
            rates = NoRates;
            ClientLog.Warning("专属倍率取数失败，按分组默认倍率显示", ex);
        }

        Groups = groups;
        _rates = rates;
    }

    /// <summary>A list entry for <paramref name="group"/>, priced at this account's rate.</summary>
    internal GroupItemViewModel CreateItem(RelayGroup group, string? serverUtcOffset) =>
        new(group, GroupRate.Resolve(group, _rates), serverUtcOffset);

    internal static bool IsClaudePlatform(string? platform)
    {
        string p = platform?.ToLowerInvariant() ?? string.Empty;
        return p.Contains("claude") || p.Contains("anthropic");
    }

    /// <summary>Drops the previous account's groups.</summary>
    internal void Reset()
    {
        Groups = [];
        _rates = NoRates;
    }
}
