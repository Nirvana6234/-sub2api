using System.Text.Json;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>
/// Locks the values a contract falls back to when the server omits a field.
/// </summary>
/// <remarks>
/// <para>
/// These exist because the move to source-generated serialization silently changed
/// this behaviour once already. Source generation cannot assign <c>init</c>-only
/// properties, so it binds through a constructor — and on that path a property
/// initializer (<c>= string.Empty</c>) never runs, leaving <c>null</c> in a property
/// the type declares as non-nullable. Every contract now carries an explicit
/// <c>[JsonConstructor]</c> with defaulted parameters to keep the old values.
/// </para>
/// <para>
/// Before this file there was exactly one test covering an omitted field
/// (<c>AnAccessTokenWithoutARefreshTokenIsAccepted</c>) against 41 properties that
/// depend on a default. That single test is what caught the regression; the other 40
/// would have reached users as a null in a non-nullable string, or — worse — a null
/// collection that throws on the first iteration.
/// </para>
/// <para>
/// Deserializing <c>{}</c> is deliberate. It is the strongest form of "the server
/// sent nothing", so anything that survives it survives any partial response.
/// </para>
/// </remarks>
public class ContractDefaultsTests
{
    private static T Empty<T>(System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> typeInfo) =>
        JsonSerializer.Deserialize("{}"u8, typeInfo)!;

    [Fact]
    public void OmittedStringsFallBackToEmptyNotNull()
    {
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.AuthTokens).RefreshToken);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.RelayUser).Email);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.RelayGroup).Name);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.RelayApiKey).Key);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.RelayAnnouncement).Title);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.PaymentOrder).OutTradeNo);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.PaymentCheckoutInfo).HelpText);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.UsageTrendPoint).Date);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.ModelUsage).Model);
        Assert.Equal(string.Empty, Empty(RelayJsonContext.Default.LoginAgreementDocument).Title);
    }

    /// <remarks>
    /// A null collection is the worst of these failures: it throws at the first
    /// <c>foreach</c> rather than merely rendering blank.
    /// </remarks>
    [Fact]
    public void OmittedCollectionsFallBackToEmptyNotNull()
    {
        PublicSettings settings = Empty(RelayJsonContext.Default.PublicSettings);
        Assert.NotNull(settings.RegistrationEmailSuffixWhitelist);
        Assert.Empty(settings.RegistrationEmailSuffixWhitelist);
        Assert.NotNull(settings.LoginAgreementDocuments);
        Assert.Empty(settings.LoginAgreementDocuments);

        PaymentCheckoutInfo checkout = Empty(RelayJsonContext.Default.PaymentCheckoutInfo);
        Assert.NotNull(checkout.Methods);
        Assert.Empty(checkout.Methods);
    }

    /// <remarks>
    /// Not every default is the type's zero value, so "fall back to empty" is not a
    /// rule that can be applied blindly — these two carry real values.
    /// </remarks>
    [Fact]
    public void NonZeroDefaultsAreKept()
    {
        Assert.True(Empty(RelayJsonContext.Default.PaymentMethodLimit).Available);

        ClaudePreferenceDto claude = Empty(RelayJsonContext.Default.ClaudePreferenceDto);
        Assert.Equal("claude-sonnet-5", claude.Model);
        Assert.Equal("medium", claude.ThinkingLevel);
    }

    /// <remarks>
    /// Guards the direction the whole migration exists for: a value the server did
    /// send must still win over the default.
    /// </remarks>
    [Fact]
    public void SuppliedValuesStillOverrideDefaults()
    {
        AuthTokens tokens = JsonSerializer.Deserialize(
            """{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":900}""",
            RelayJsonContext.Default.AuthTokens)!;

        Assert.Equal("at", tokens.AccessToken);
        Assert.Equal("rt", tokens.RefreshToken);
        Assert.Equal("Bearer", tokens.TokenType);
        Assert.Equal(900, tokens.ExpiresInSeconds);
    }
}
