using Xunit;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>
/// Locks the F5.2 multiplier rules to what the server bills and the web panel shows.
/// </summary>
public sealed class GroupRateTests
{
    private static RelayGroup Group(
        long id = 11,
        double rate = 1.5,
        string subscriptionType = "standard",
        bool peakEnabled = false,
        string peakStart = "",
        string peakEnd = "",
        double peakMultiplier = 1.0) =>
        new()
        {
            Id = id,
            Name = "g",
            RateMultiplier = rate,
            SubscriptionType = subscriptionType,
            PeakRateEnabled = peakEnabled,
            PeakStart = peakStart,
            PeakEnd = peakEnd,
            PeakRateMultiplier = peakMultiplier,
        };

    [Fact]
    public void AGroupWithNoUserRateFallsBackToItsDefault()
    {
        GroupRate rate = GroupRate.Resolve(Group(rate: 1.5), new Dictionary<long, double>());

        Assert.Equal(1.5, rate.EffectiveMultiplier);
        Assert.Null(rate.UserMultiplier);
        Assert.False(rate.HasUserOverride);
    }

    [Fact]
    public void AUserRateOfZeroMeansFreeAndIsNotMistakenForAbsence()
    {
        // The distinction the whole lookup exists for. GetValueOrDefault would make
        // these two cases identical, and would then report every group as free.
        GroupRate free = GroupRate.Resolve(Group(id: 11, rate: 2.0), new Dictionary<long, double> { [11] = 0.0 });
        GroupRate absent = GroupRate.Resolve(Group(id: 11, rate: 2.0), new Dictionary<long, double>());

        Assert.Equal(0.0, free.EffectiveMultiplier);
        Assert.True(free.HasUserOverride);

        Assert.Equal(2.0, absent.EffectiveMultiplier);
        Assert.False(absent.HasUserOverride);
    }

    [Fact]
    public void AUserRateEqualToTheDefaultIsNotShownAsAnOverride()
    {
        // The panel only strikes through the default when the two differ; showing
        // "1.5 struck out, 1.5 in bold" would be noise.
        GroupRate rate = GroupRate.Resolve(Group(rate: 1.5), new Dictionary<long, double> { [11] = 1.5 });

        Assert.False(rate.HasUserOverride);
        Assert.Equal(1.5, rate.EffectiveMultiplier);
    }

    [Fact]
    public void AMissingRatesMapIsTreatedAsNoOverrides()
    {
        // The rates call is allowed to fail on its own (F4.2); the group list must
        // still render at default rates rather than taking the whole card down.
        GroupRate rate = GroupRate.Resolve(Group(rate: 3.0), userRates: null);

        Assert.Equal(3.0, rate.EffectiveMultiplier);
        Assert.False(rate.HasUserOverride);
    }

    [Fact]
    public void PeakPricingIsIgnoredOnStandardGroups()
    {
        // The server returns 1.0 for any non-subscription group before it even
        // looks at the window, so advertising one here would promise a surcharge
        // that is never charged.
        RelayGroup group = Group(
            subscriptionType: "standard",
            peakEnabled: true,
            peakStart: "14:00",
            peakEnd: "18:00",
            peakMultiplier: 2.0);

        Assert.Null(GroupRate.Resolve(group, null).Peak);
    }

    [Fact]
    public void PeakPricingAppliesOnSubscriptionGroups()
    {
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "14:00",
            peakEnd: "18:00",
            peakMultiplier: 2.0);

        PeakWindow peak = Assert.IsType<PeakWindow>(GroupRate.Resolve(group, null).Peak);

        Assert.Equal(1.0, peak.MultiplierAt(new TimeOnly(13, 59)));
        Assert.Equal(2.0, peak.MultiplierAt(new TimeOnly(14, 0)));
        Assert.Equal(2.0, peak.MultiplierAt(new TimeOnly(17, 59)));
        Assert.Equal(1.0, peak.MultiplierAt(new TimeOnly(18, 0)));
    }

    [Fact]
    public void AWindowThatWouldCrossMidnightIsNotAWindow()
    {
        // The server rejects start >= end outright rather than wrapping, and
        // validation forbids configuring one, so wrapping here would invent a
        // surcharge the user is never billed.
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "22:00",
            peakEnd: "06:00",
            peakMultiplier: 2.0);

        Assert.Null(GroupRate.Resolve(group, null).Peak);
    }

    [Fact]
    public void APeakMultiplierOfZeroIsAValidDiscount()
    {
        // Explicitly allowed server-side: peak tokens billed at zero.
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "01:00",
            peakEnd: "02:00",
            peakMultiplier: 0.0);

        PeakWindow peak = Assert.IsType<PeakWindow>(GroupRate.Resolve(group, null).Peak);

        Assert.Equal(0.0, peak.MultiplierAt(new TimeOnly(1, 30)));
    }

    [Theory]
    [InlineData("9:30", 570)]
    [InlineData("09:30", 570)]
    [InlineData("00:00", 0)]
    [InlineData("23:59", 1439)]
    public void AcceptsTheTimeFormsTheServerAccepts(string value, int expected)
    {
        Assert.True(PeakWindow.TryParseMinutes(value, out int minutes));
        Assert.Equal(expected, minutes);
    }

    [Theory]
    [InlineData("24:00")]
    [InlineData("12:60")]
    [InlineData("1400")]
    [InlineData("14:00:00")]
    [InlineData("2 PM")]
    [InlineData("")]
    [InlineData(null)]
    public void RejectsTheTimeFormsTheServerRejects(string? value)
    {
        // Notably 14:00:00 and "2 PM": a culture-aware parser would accept both,
        // and the client would then show a window the server never parsed.
        Assert.False(PeakWindow.TryParseMinutes(value, out _));
    }

    [Fact]
    public void AnUnparseableWindowDegradesToNoPeakRatherThanThrowing()
    {
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "not-a-time",
            peakEnd: "18:00",
            peakMultiplier: 2.0);

        Assert.Null(GroupRate.Resolve(group, null).Peak);
    }

    [Fact]
    public void TheWindowLabelStatesWhichClockItRefersTo()
    {
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "14:00",
            peakEnd: "18:00",
            peakMultiplier: 2.0);

        PeakWindow peak = Assert.IsType<PeakWindow>(GroupRate.Resolve(group, null).Peak);

        Assert.Equal("14:00-18:00 ×2 (UTC+08:00)", peak.Format("+08:00"));
    }

    [Fact]
    public void AnUnknownServerOffsetDropsTheLabelRatherThanGuessing()
    {
        // A window tagged with the wrong timezone is worse than one tagged with
        // none: the user would confidently read the billing window off by hours.
        RelayGroup group = Group(
            subscriptionType: "subscription",
            peakEnabled: true,
            peakStart: "14:00",
            peakEnd: "18:00",
            peakMultiplier: 2.0);

        PeakWindow peak = Assert.IsType<PeakWindow>(GroupRate.Resolve(group, null).Peak);

        Assert.Equal("14:00-18:00 ×2", peak.Format(null));
    }
}
