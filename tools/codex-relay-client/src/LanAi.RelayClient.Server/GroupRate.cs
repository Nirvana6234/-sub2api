namespace LanAi.RelayClient.Server;

/// <summary>
/// A group's billing multiplier as the user should see it (F5.2).
/// </summary>
/// <remarks>
/// Mirrors what the web panel puts in the group dropdown, because M2's exit
/// criterion is that the two agree number for number.
/// </remarks>
public sealed record GroupRate
{
    /// <summary>The group's own multiplier, ignoring any user-specific deal.</summary>
    public required double DefaultMultiplier { get; init; }

    /// <summary>The user's negotiated multiplier, or null when they have none for this group.</summary>
    public required double? UserMultiplier { get; init; }

    /// <summary>The multiplier actually in force outside peak hours.</summary>
    public double EffectiveMultiplier => UserMultiplier ?? DefaultMultiplier;

    /// <summary>
    /// Whether to render the default struck through next to the effective value.
    /// </summary>
    /// <remarks>
    /// The panel only shows both numbers when they differ; a user-specific rate
    /// that happens to equal the default is shown once, not as a strikethrough
    /// onto an identical value.
    /// </remarks>
    public bool HasUserOverride => UserMultiplier is { } user && user != DefaultMultiplier;

    /// <summary>The peak-hour surcharge window, or null when none applies.</summary>
    public required PeakWindow? Peak { get; init; }

    /// <summary>
    /// Resolves one group's rate against the user's per-group overrides.
    /// </summary>
    /// <param name="group">The group, as returned by <c>/groups/available</c>.</param>
    /// <param name="userRates">
    /// The map from <c>/groups/rates</c>. Absence means "no special deal";
    /// a present value of <c>0</c> means this group is free for this user, which
    /// is a real configuration and must not be confused with absence.
    /// </param>
    public static GroupRate Resolve(RelayGroup group, IReadOnlyDictionary<long, double>? userRates)
    {
        ArgumentNullException.ThrowIfNull(group);

        // TryGetValue, not GetValueOrDefault: the latter turns "no override" into
        // a 0.0 override and reports every group as free.
        double? userRate = null;
        if (userRates is not null && userRates.TryGetValue(group.Id, out double rate))
        {
            userRate = rate;
        }

        return new GroupRate
        {
            DefaultMultiplier = group.RateMultiplier,
            UserMultiplier = userRate,
            Peak = PeakWindow.TryCreate(group),
        };
    }
}

/// <summary>
/// A daily window during which a subscription group bills at a different rate.
/// </summary>
/// <remarks>
/// <para>
/// Only constructed for windows the server would actually honour — see
/// <see cref="TryCreate"/>. Every guard mirrors <c>Group.PeakMultiplierAt</c>;
/// showing a window the server ignores would be worse than showing none.
/// </para>
/// <para>
/// The times are wall-clock in the <em>server's</em> timezone, which is why
/// <see cref="MultiplierAt"/> demands the caller state which instant it means
/// rather than reaching for the local clock.
/// </para>
/// </remarks>
public sealed record PeakWindow
{
    private PeakWindow()
    {
    }

    /// <summary>Window start as <c>HH:mm</c>, server timezone, inclusive.</summary>
    public required string Start { get; init; }

    /// <summary>Window end as <c>HH:mm</c>, server timezone, exclusive.</summary>
    public required string End { get; init; }

    public required double Multiplier { get; init; }

    /// <summary>Window start as minutes past midnight; derived, so never set from outside.</summary>
    private int StartMinutes { get; init; }

    private int EndMinutes { get; init; }

    /// <summary>
    /// Builds a window from a group, or returns null when no peak rate applies.
    /// </summary>
    /// <remarks>
    /// Rejects, in the same order the server does: non-subscription groups (peak
    /// pricing is a subscription-only feature), the disabled flag, blank times,
    /// unparseable times, and any window that does not run forwards. The server
    /// does not support windows crossing midnight — <c>start &gt;= end</c> makes it
    /// fall back to 1.0 rather than wrapping — so neither does this.
    /// </remarks>
    public static PeakWindow? TryCreate(RelayGroup group)
    {
        ArgumentNullException.ThrowIfNull(group);

        if (!group.IsSubscription || !group.PeakRateEnabled)
        {
            return null;
        }

        if (!TryParseMinutes(group.PeakStart, out int start) ||
            !TryParseMinutes(group.PeakEnd, out int end) ||
            start >= end)
        {
            return null;
        }

        return new PeakWindow
        {
            Start = group.PeakStart,
            End = group.PeakEnd,
            Multiplier = group.PeakRateMultiplier,
            StartMinutes = start,
            EndMinutes = end,
        };
    }

    /// <summary>
    /// The surcharge in force at <paramref name="serverLocalTime"/>, or 1.0 outside the window.
    /// </summary>
    /// <param name="serverLocalTime">
    /// A wall-clock time already converted to the server's timezone. Passing the
    /// machine's local time would misreport the window for anyone not sitting in
    /// the server's zone.
    /// </param>
    public double MultiplierAt(TimeOnly serverLocalTime)
    {
        int current = (serverLocalTime.Hour * 60) + serverLocalTime.Minute;
        return current >= StartMinutes && current < EndMinutes ? Multiplier : 1.0;
    }

    /// <summary>Renders the window for display, e.g. <c>14:00-18:00 ×2 (UTC+08:00)</c>.</summary>
    /// <param name="serverUtcOffset">
    /// The server's offset from <c>/settings/public</c>. When absent the timezone
    /// suffix is dropped rather than guessed — an unlabelled window is ambiguous,
    /// but a wrongly labelled one is misleading.
    /// </param>
    public string Format(string? serverUtcOffset)
    {
        string window = $"{Start}-{End} ×{Multiplier:0.##}";
        return string.IsNullOrWhiteSpace(serverUtcOffset) ? window : $"{window} (UTC{serverUtcOffset})";
    }

    /// <summary>
    /// Parses <c>H:mm</c> or <c>HH:mm</c> into minutes past midnight.
    /// </summary>
    /// <remarks>
    /// Hand-written rather than delegating to <see cref="TimeOnly.TryParse(string, out TimeOnly)"/>
    /// so it accepts exactly what the server accepts: the framework parser is
    /// culture-sensitive and would take forms such as <c>2 PM</c> or <c>14:00:00</c>
    /// that <c>parseMinutes</c> rejects.
    /// </remarks>
    internal static bool TryParseMinutes(string? value, out int minutes)
    {
        minutes = 0;

        if (string.IsNullOrEmpty(value))
        {
            return false;
        }

        int colon = value.IndexOf(':', StringComparison.Ordinal);
        if ((colon != 1 && colon != 2) || value.Length - colon - 1 != 2)
        {
            return false;
        }

        int hours = 0;
        for (int i = 0; i < colon; i++)
        {
            int digit = value[i] - '0';
            if (digit is < 0 or > 9)
            {
                return false;
            }

            hours = (hours * 10) + digit;
        }

        int tens = value[colon + 1] - '0';
        int units = value[colon + 2] - '0';
        if (tens is < 0 or > 9 || units is < 0 or > 9)
        {
            return false;
        }

        int mins = (tens * 10) + units;
        if (hours > 23 || mins > 59)
        {
            return false;
        }

        minutes = (hours * 60) + mins;
        return true;
    }
}
