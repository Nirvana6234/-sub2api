using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>
/// One page of a list endpoint.
/// </summary>
/// <remarks>
/// The relay wraps list results in <c>PaginatedData</c> (<c>response.go:124</c>)
/// rather than returning a bare array, so binding a list endpoint straight to
/// <c>T[]</c> silently yields nothing.
/// </remarks>
public sealed record PagedResult<T>
{
    [JsonConstructor]
    public PagedResult(
        IReadOnlyList<T>? items = null,
        long total = default,
        int page = default,
        int pageSize = default,
        int pages = default)
    {
        Items = items ?? Array.Empty<T>();
        Total = total;
        Page = page;
        PageSize = pageSize;
        Pages = pages;
    }

    [JsonPropertyName("items")]
    public IReadOnlyList<T> Items { get; init; } = Array.Empty<T>();

    [JsonPropertyName("total")]
    public long Total { get; init; }

    [JsonPropertyName("page")]
    public int Page { get; init; }

    [JsonPropertyName("page_size")]
    public int PageSize { get; init; }

    [JsonPropertyName("pages")]
    public int Pages { get; init; }
}

/// <summary>
/// A group the signed-in user may bind a key to.
/// </summary>
/// <remarks>
/// Mirrors <c>dto.Group</c>, mapping only what F5 displays or filters on. The
/// server has already applied subscription and permission filtering in
/// <c>/groups/available</c>; F5.3 forbids the client from widening that set.
/// </remarks>
public sealed record RelayGroup
{
    [JsonConstructor]
    public RelayGroup(
        long id = default,
        string? name = null,
        string? description = null,
        string? platform = null,
        double rateMultiplier = default,
        string? subscriptionType = null,
        bool peakRateEnabled = default,
        string? peakStart = null,
        string? peakEnd = null,
        double peakRateMultiplier = default,
        string? status = null,
        GroupModelAllowlist? modelAllowlist = null)
    {
        Id = id;
        Name = name ?? string.Empty;
        Description = description ?? string.Empty;
        Platform = platform ?? string.Empty;
        RateMultiplier = rateMultiplier;
        SubscriptionType = subscriptionType ?? string.Empty;
        PeakRateEnabled = peakRateEnabled;
        PeakStart = peakStart ?? string.Empty;
        PeakEnd = peakEnd ?? string.Empty;
        PeakRateMultiplier = peakRateMultiplier;
        Status = status ?? string.Empty;
        ModelAllowlist = modelAllowlist ?? new GroupModelAllowlist();
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;

    [JsonPropertyName("description")]
    public string Description { get; init; } = string.Empty;

    /// <summary>Which upstream this group routes to, e.g. <c>openai</c>. Used only to filter (F5.3).</summary>
    [JsonPropertyName("platform")]
    public string Platform { get; init; } = string.Empty;

    /// <summary>The group's default billing multiplier, before any user-specific override.</summary>
    [JsonPropertyName("rate_multiplier")]
    public double RateMultiplier { get; init; }

    /// <summary><c>standard</c> or <c>subscription</c>; gates whether peak rates apply at all.</summary>
    [JsonPropertyName("subscription_type")]
    public string SubscriptionType { get; init; } = string.Empty;

    [JsonPropertyName("peak_rate_enabled")]
    public bool PeakRateEnabled { get; init; }

    /// <summary>Window start as <c>HH:mm</c>, in the server's timezone.</summary>
    [JsonPropertyName("peak_start")]
    public string PeakStart { get; init; } = string.Empty;

    /// <summary>Window end as <c>HH:mm</c>, in the server's timezone. Exclusive.</summary>
    [JsonPropertyName("peak_end")]
    public string PeakEnd { get; init; } = string.Empty;

    [JsonPropertyName("peak_rate_multiplier")]
    public double PeakRateMultiplier { get; init; }

    [JsonPropertyName("status")]
    public string Status { get; init; } = string.Empty;

    /// <summary>
    /// The group's model whitelist. Absent from older servers, which reads as "off" — the
    /// 模型 button stays hidden rather than guessing.
    /// </summary>
    [JsonPropertyName("model_allowlist")]
    public GroupModelAllowlist ModelAllowlist { get; init; } = new();

    /// <summary>Whether this group bills by subscription, which is what enables peak pricing.</summary>
    /// <remarks>
    /// Compared case-sensitively to match the server exactly
    /// (<c>Group.IsSubscriptionType</c> is a plain <c>==</c>). Being more lenient
    /// here would let the client show a peak window for a group the server would
    /// never charge a peak rate on.
    /// </remarks>
    public bool IsSubscription => string.Equals(SubscriptionType, "subscription", StringComparison.Ordinal);
}

/// <summary>The group's model whitelist: when <see cref="Enabled"/>, only <see cref="Models"/> may be requested.</summary>
/// <remarks>Entries may end in <c>*</c> (a prefix wildcard); they are shown as written.</remarks>
public sealed record GroupModelAllowlist
{
    [JsonConstructor]
    public GroupModelAllowlist(bool enabled = default, IReadOnlyList<string>? models = null)
    {
        Enabled = enabled;
        Models = models ?? Array.Empty<string>();
    }

    [JsonPropertyName("enabled")]
    public bool Enabled { get; init; }

    [JsonPropertyName("models")]
    public IReadOnlyList<string> Models { get; init; } = Array.Empty<string>();
}

/// <summary>
/// One model a group's pricing table lists — the group's whitelist, in practice: a model this
/// client shows is one the account pool behind that group actually serves.
/// </summary>
/// <remarks>
/// Deliberately thin. The full <c>PlazaModel</c> the web panel reads also carries pricing
/// (<c>pricing</c>, <c>official_pricing</c>, time-of-day multipliers, ...) — none of which this
/// client's "什么模型能用" tip needs, so only the name is mapped.
/// </remarks>
public sealed record PlazaModel
{
    [JsonConstructor]
    public PlazaModel(string? name = null) => Name = name ?? string.Empty;

    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;
}

/// <summary>One group's entry in the model plaza: its id, and the models it serves.</summary>
public sealed record ModelPlazaGroup
{
    [JsonConstructor]
    public ModelPlazaGroup(long id = default, IReadOnlyList<PlazaModel>? models = null)
    {
        Id = id;
        Models = models ?? Array.Empty<PlazaModel>();
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("models")]
    public IReadOnlyList<PlazaModel> Models { get; init; } = Array.Empty<PlazaModel>();
}

/// <summary>
/// The relay's public, group-centred model price list (<c>GET /model-plaza</c>) — read here
/// only for which models each group serves, the same data the web panel's model plaza page
/// shows. 404s when the operator has not turned the model plaza on; see
/// <c>DashboardViewModel.LoadGroupModelsAsync</c> for how that is told apart from every other
/// reason the request could fail.
/// </summary>
public sealed record ModelPlazaResponse
{
    [JsonConstructor]
    public ModelPlazaResponse(IReadOnlyList<ModelPlazaGroup>? groups = null) =>
        Groups = groups ?? Array.Empty<ModelPlazaGroup>();

    [JsonPropertyName("groups")]
    public IReadOnlyList<ModelPlazaGroup> Groups { get; init; } = Array.Empty<ModelPlazaGroup>();
}

/// <summary>The hidden Playground Chat key's automatic routing configuration.</summary>
public sealed record PawAutoGroupSettings
{
    [JsonConstructor]
    public PawAutoGroupSettings(
        bool autoGroup = default,
        IReadOnlyList<long>? autoGroupIds = null,
        string? autoGroupStrategy = null)
    {
        AutoGroup = autoGroup;
        AutoGroupIds = autoGroupIds ?? Array.Empty<long>();
        AutoGroupStrategy = string.IsNullOrWhiteSpace(autoGroupStrategy) ? "price" : autoGroupStrategy;
    }

    [JsonPropertyName("auto_group")]
    public bool AutoGroup { get; init; }

    [JsonPropertyName("auto_group_ids")]
    public IReadOnlyList<long> AutoGroupIds { get; init; } = Array.Empty<long>();

    [JsonPropertyName("auto_group_strategy")]
    public string AutoGroupStrategy { get; init; } = "price";
}

/// <summary>
/// The signed-in user's usage totals.
/// </summary>
/// <remarks>
/// Mirrors <c>usagestats.UserDashboardStats</c>. F4 shows only the "today"
/// figures; the cumulative ones are mapped because the same payload carries them
/// and a later card would otherwise need a contract change.
/// </remarks>
public sealed record DashboardStats
{
    [JsonPropertyName("today_requests")]
    public long TodayRequests { get; init; }

    [JsonPropertyName("today_tokens")]
    public long TodayTokens { get; init; }

    /// <summary>What was actually deducted today, as opposed to list-price cost.</summary>
    [JsonPropertyName("today_actual_cost")]
    public double TodayActualCost { get; init; }

    [JsonPropertyName("today_cost")]
    public double TodayCost { get; init; }

    [JsonPropertyName("total_requests")]
    public long TotalRequests { get; init; }

    [JsonPropertyName("total_tokens")]
    public long TotalTokens { get; init; }

    [JsonPropertyName("total_actual_cost")]
    public double TotalActualCost { get; init; }

    [JsonPropertyName("total_api_keys")]
    public long TotalApiKeys { get; init; }

    [JsonPropertyName("active_api_keys")]
    public long ActiveApiKeys { get; init; }
}

/// <summary>
/// An API key as the relay lists it.
/// </summary>
/// <remarks>
/// <para>
/// <c>key</c> is the full plaintext value, not masked (<c>dto/mappers.go:85</c>),
/// so the client never has to cache the secret locally — it can always be read
/// back. F3.2.1 identifies the managed key by <em>name</em>, never by value.
/// </para>
/// <para>
/// <c>expires_at</c> is null for keys that never expire. Under the lease model
/// (F3.2) a managed key with a null expiry is a bug, not a convenience: it is an
/// authorization that outlives the client.
/// </para>
/// </remarks>
public sealed record RelayApiKey
{
    [JsonConstructor]
    public RelayApiKey(
        long id = default,
        string? name = null,
        string? key = null,
        long? groupId = default,
        string? status = null,
        DateTimeOffset? expiresAt = default,
        RelayGroup? group = default)
    {
        Id = id;
        Name = name ?? string.Empty;
        Key = key ?? string.Empty;
        GroupId = groupId;
        Status = status ?? string.Empty;
        ExpiresAt = expiresAt;
        Group = group;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("name")]
    public string Name { get; init; } = string.Empty;

    [JsonPropertyName("key")]
    public string Key { get; init; } = string.Empty;

    [JsonPropertyName("group_id")]
    public long? GroupId { get; init; }

    [JsonPropertyName("status")]
    public string Status { get; init; } = string.Empty;

    /// <summary>When the lease lapses; null means it never does.</summary>
    [JsonPropertyName("expires_at")]
    public DateTimeOffset? ExpiresAt { get; init; }

    /// <summary>The bound group, present because the list endpoint preloads it.</summary>
    [JsonPropertyName("group")]
    public RelayGroup? Group { get; init; }
}

/// <summary>One active subscription's usage against its limits.</summary>
public sealed record SubscriptionSummaryItem
{
    [JsonConstructor]
    public SubscriptionSummaryItem(
        long id = default,
        long groupId = default,
        string? groupName = null,
        string? status = null,
        double dailyUsedUsd = default,
        double dailyLimitUsd = default,
        double weeklyUsedUsd = default,
        double weeklyLimitUsd = default,
        double monthlyUsedUsd = default,
        double monthlyLimitUsd = default)
    {
        Id = id;
        GroupId = groupId;
        GroupName = groupName ?? string.Empty;
        Status = status ?? string.Empty;
        DailyUsedUsd = dailyUsedUsd;
        DailyLimitUsd = dailyLimitUsd;
        WeeklyUsedUsd = weeklyUsedUsd;
        WeeklyLimitUsd = weeklyLimitUsd;
        MonthlyUsedUsd = monthlyUsedUsd;
        MonthlyLimitUsd = monthlyLimitUsd;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("group_id")]
    public long GroupId { get; init; }

    [JsonPropertyName("group_name")]
    public string GroupName { get; init; } = string.Empty;

    [JsonPropertyName("status")]
    public string Status { get; init; } = string.Empty;

    [JsonPropertyName("daily_used_usd")]
    public double DailyUsedUsd { get; init; }

    [JsonPropertyName("daily_limit_usd")]
    public double DailyLimitUsd { get; init; }

    [JsonPropertyName("weekly_used_usd")]
    public double WeeklyUsedUsd { get; init; }

    [JsonPropertyName("weekly_limit_usd")]
    public double WeeklyLimitUsd { get; init; }

    [JsonPropertyName("monthly_used_usd")]
    public double MonthlyUsedUsd { get; init; }

    [JsonPropertyName("monthly_limit_usd")]
    public double MonthlyLimitUsd { get; init; }
}

/// <summary>订阅摘要端点返回的数据对象。</summary>
internal sealed record SubscriptionSummaryResponse
{
    [JsonPropertyName("subscriptions")]
    public SubscriptionSummaryItem[]? Subscriptions { get; init; }
}

/// <summary>One day (or hour) of usage, for the trend chart.</summary>
/// <remarks>
/// Mirrors <c>usagestats.TrendDataPoint</c>. Only the fields the novice chart
/// plots are mapped; the token breakdown stays out because showing five
/// near-identical lines is what makes a dashboard unreadable.
/// </remarks>
public sealed record UsageTrendPoint
{
    [JsonConstructor]
    public UsageTrendPoint(
        string? date = null,
        long requests = default,
        long totalTokens = default,
        double actualCost = default)
    {
        Date = date ?? string.Empty;
        Requests = requests;
        TotalTokens = totalTokens;
        ActualCost = actualCost;
    }

    [JsonPropertyName("date")]
    public string Date { get; init; } = string.Empty;

    [JsonPropertyName("requests")]
    public long Requests { get; init; }

    [JsonPropertyName("total_tokens")]
    public long TotalTokens { get; init; }

    /// <summary>What was actually deducted, which is what the user is charged.</summary>
    [JsonPropertyName("actual_cost")]
    public double ActualCost { get; init; }
}

/// <summary>The trend section of a usage snapshot.</summary>
internal sealed record UsageSnapshot
{
    [JsonPropertyName("trend")]
    public UsageTrendPoint[]? Trend { get; init; }
}

/// <summary>Usage attributed to one model.</summary>
public sealed record ModelUsage
{
    [JsonConstructor]
    public ModelUsage(
        string? model = null,
        long requests = default,
        long totalTokens = default,
        double actualCost = default)
    {
        Model = model ?? string.Empty;
        Requests = requests;
        TotalTokens = totalTokens;
        ActualCost = actualCost;
    }

    [JsonPropertyName("model")]
    public string Model { get; init; } = string.Empty;

    [JsonPropertyName("requests")]
    public long Requests { get; init; }

    [JsonPropertyName("total_tokens")]
    public long TotalTokens { get; init; }

    [JsonPropertyName("actual_cost")]
    public double ActualCost { get; init; }
}

/// <summary>The models section of the usage page.</summary>
internal sealed record ModelUsageResponse
{
    [JsonPropertyName("models")]
    public ModelUsage[]? Models { get; init; }
}

/// <summary>User preferred Claude model and thinking level.</summary>
public sealed record ClaudePreferenceDto
{
    [JsonConstructor]
    public ClaudePreferenceDto(
        string model = "claude-sonnet-5",
        string thinkingLevel = "medium")
    {
        Model = model;
        ThinkingLevel = thinkingLevel;
    }

    [JsonPropertyName("model")]
    public string Model { get; init; } = "claude-sonnet-5";

    [JsonPropertyName("thinking_level")]
    public string ThinkingLevel { get; init; } = "medium";
}
