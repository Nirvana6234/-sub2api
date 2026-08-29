using System.Net;
using System.Text.Json;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>
/// Binds a response captured verbatim from a running relay.
/// </summary>
/// <remarks>
/// <para>
/// Hand-written JSON in the other tests only proves the client agrees with
/// itself. This one proves it agrees with the server — which is where the real
/// bug was: <c>registration_email_suffix_whitelist</c> was mapped as a delimited
/// string when the server sends a JSON array. Binding failed, the whole settings
/// fetch fell over, and the conservative fallback then hid the registration entry
/// on a server that had registration switched on.
/// </para>
/// <para>
/// Fixture: <c>Fixtures/settings-public.json</c>, captured 2026-07-31 from the
/// local relay (version 0.1.158). Refresh it whenever the server's public
/// settings contract changes.
/// </para>
/// </remarks>
public sealed class LivePublicSettingsContractTests
{
    private static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web);

    private static string FixtureJson =>
        File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "Fixtures", "settings-public.json"));

    [Fact]
    public async Task TheLiveResponseBindsWithoutError()
    {
        // The end-to-end path: envelope unwrapping plus contract binding, exactly
        // as GetPublicSettingsAsync runs it.
        var handler = StubHandler.Raw(HttpStatusCode.OK, FixtureJson);

        PublicSettings settings = await handler.CreateClient().GetPublicSettingsAsync();

        Assert.NotNull(settings);
    }

    [Fact]
    public async Task EveryFieldTheClientDependsOnIsPresentInTheLiveResponse()
    {
        // Guards against silently binding defaults: if the server drops or renames
        // a field, the client would quietly behave as though the feature were off.
        using JsonDocument document = JsonDocument.Parse(FixtureJson);
        JsonElement data = document.RootElement.GetProperty("data");

        string[] required =
        [
            "registration_enabled",
            "email_verify_enabled",
            "registration_email_suffix_whitelist",
            "invitation_code_enabled",
            "promo_code_enabled",
            "password_reset_enabled",
            "totp_enabled",
            "turnstile_enabled",
            "turnstile_site_key",
            "login_agreement_enabled",
            "login_agreement_mode",
            "login_agreement_documents",
            "payment_enabled",
            "backend_mode_enabled",
            "site_name",
            "site_logo",
            "api_base_url",
            "contact_info",
            "balance_low_notify_enabled",
            "balance_low_notify_threshold",
            "balance_low_notify_recharge_url",
            "server_utc_offset",
        ];

        string[] missing = required.Where(name => !data.TryGetProperty(name, out _)).ToArray();

        Assert.True(missing.Length == 0, $"public settings no longer carry: {string.Join(", ", missing)}");

        await Task.CompletedTask;
    }

    [Fact]
    public void TheSuffixWhitelistIsAnArrayNotADelimitedString()
    {
        using JsonDocument document = JsonDocument.Parse(FixtureJson);
        JsonElement whitelist = document.RootElement
            .GetProperty("data")
            .GetProperty("registration_email_suffix_whitelist");

        Assert.Equal(JsonValueKind.Array, whitelist.ValueKind);
    }

    [Fact]
    public void AgreementDocumentsBindAndEmptyBodiesAreRecognised()
    {
        // The live server returns four documents whose markdown is empty. That is
        // valid data, not an error — the UI must skip them, not render blanks.
        PublicSettings settings = BindData();

        Assert.NotEmpty(settings.LoginAgreementDocuments);
        Assert.All(settings.LoginAgreementDocuments, d => Assert.NotEmpty(d.Id));
        Assert.DoesNotContain(settings.LoginAgreementDocuments, d => d.HasContent && string.IsNullOrWhiteSpace(d.ContentMarkdown));
    }

    [Fact]
    public void TurnstileIsOffOnTheCapturedServer()
    {
        // Documents why the client carries no browser-component dependency. If this
        // ever fails after refreshing the fixture, the Turnstile work becomes real:
        // registration and verification-code requests would need a user-solved
        // challenge token, which plain WPF controls cannot produce.
        PublicSettings settings = BindData();

        Assert.False(settings.TurnstileEnabled);
        Assert.True(string.IsNullOrEmpty(settings.TurnstileSiteKey));
    }

    [Fact]
    public void TheServerStatesItsOwnTimezoneOffset()
    {
        // Peak-rate windows are wall-clock times in the server's zone, so without
        // this the client can only show a bare "14:00-18:00" that a user in another
        // zone will read as their own local hours and mis-plan their spending.
        PublicSettings settings = BindData();

        Assert.False(string.IsNullOrWhiteSpace(settings.ServerUtcOffset));
        Assert.Equal("+08:00", settings.ServerUtcOffset);
    }

    private static PublicSettings BindData()
    {
        using JsonDocument document = JsonDocument.Parse(FixtureJson);
        PublicSettings? settings = document.RootElement
            .GetProperty("data")
            .Deserialize<PublicSettings>(Options);

        Assert.NotNull(settings);
        return settings!;
    }
}
