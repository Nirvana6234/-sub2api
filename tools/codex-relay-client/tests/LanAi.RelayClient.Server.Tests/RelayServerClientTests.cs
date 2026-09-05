using System.Net;
using System.Text.Json;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

public sealed class RelayServerClientTests
{
    private const string TokenPayload =
        """{"access_token":"at","refresh_token":"rt","expires_in":900,"token_type":"Bearer","user":{"id":7,"email":"a@b.com","username":"ann","balance":12.5}}""";

    [Fact]
    public async Task LoginReturnsTokensWhenTheServerIssuesThem()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, TokenPayload);

        LoginOutcome outcome = await handler.CreateClient().LoginAsync("a@b.com", "pw");

        Assert.False(outcome.RequiresTwoFactor);
        Assert.Equal("at", outcome.Tokens!.AccessToken);
        Assert.Equal("rt", outcome.Tokens.RefreshToken);
        Assert.Equal("ann", outcome.Tokens.User!.DisplayName);
    }

    [Fact]
    public async Task LoginSurfacesTheTwoFactorStepAsAnOutcomeRatherThanAnError()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"requires_2fa":true,"temp_token":"tmp","user_email_masked":"a***@b.com"}""");

        LoginOutcome outcome = await handler.CreateClient().LoginAsync("a@b.com", "pw");

        Assert.True(outcome.RequiresTwoFactor);
        Assert.Equal("tmp", outcome.TempToken);
        Assert.Equal("a***@b.com", outcome.MaskedEmail);
    }

    [Fact]
    public async Task ATwoFactorClaimWithoutATempTokenIsNotTreatedAsAChallenge()
    {
        // Sending the user to a code prompt that can never be completed is worse
        // than failing here, so such a reply must not be read as a challenge —
        // nor may it fall through into a "successful" sign-in carrying no token.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"requires_2fa":true}""");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().LoginAsync("a@b.com", "pw"));

        Assert.Equal(RelayFailure.MalformedResponse, error.Failure);
    }

    [Fact]
    public async Task ATokenReplyWithoutAnAccessTokenIsRejectedOnRegistration()
    {
        // Same defect class as the 2FA case above: every AuthTokens field has a
        // default, so a wrong-shaped payload binds cleanly to an unusable token.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"token_type":"Bearer"}""");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().RegisterAsync(new RegistrationRequest
            {
                Email = "a@b.com",
                Password = "secret1",
            }));

        Assert.Equal(RelayFailure.MalformedResponse, error.Failure);
    }

    [Fact]
    public async Task ATokenReplyWithoutAnAccessTokenIsRejectedOnRefresh()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, """{"access_token":""}""");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().RefreshAsync("rt"));

        Assert.Equal(RelayFailure.MalformedResponse, error.Failure);
    }

    [Fact]
    public async Task AnAccessTokenWithoutARefreshTokenIsAccepted()
    {
        // The server legitimately falls back to issuing an access token alone
        // when pair generation fails, so requiring both would reject a usable
        // session. Such a session just cannot be silently renewed later.
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"access_token":"at","token_type":"Bearer"}""");

        AuthTokens tokens = await handler.CreateClient().RefreshAsync("rt");

        Assert.Equal("at", tokens.AccessToken);
        Assert.Equal(string.Empty, tokens.RefreshToken);
    }

    [Fact]
    public async Task AnUnreachableServerIsReportedAsNetworkNotAsABadPassword()
    {
        // The sign-in acceptance criteria call this out by name: an outage must
        // never be shown to the user as "wrong password".
        var handler = StubHandler.Unreachable();

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().LoginAsync("a@b.com", "pw"));

        Assert.Equal(RelayFailure.NetworkUnreachable, error.Failure);
        Assert.Contains("连不上服务器", error.UserMessage);
    }

    [Fact]
    public async Task RejectedCredentialsOnLoginAreInvalidCredentials()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.Unauthorized, code: 401, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().LoginAsync("a@b.com", "pw"));

        Assert.Equal(RelayFailure.InvalidCredentials, error.Failure);
    }

    [Fact]
    public async Task AnExpiredTokenIsReportedAsAnExpiredSessionNotABadPassword()
    {
        // Regression guard: 401 means different things on a credential-checking
        // endpoint and on an authenticated read. Collapsing them would tell a
        // user whose token simply aged out that their password is wrong.
        var handler = StubHandler.Envelope(HttpStatusCode.Unauthorized, code: 401, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetCurrentUserAsync("stale-token"));

        Assert.Equal(RelayFailure.Unauthenticated, error.Failure);
        Assert.Contains("重新登录", error.UserMessage);
    }

    [Fact]
    public async Task ARefreshRejectionIsAnExpiredSession()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.Unauthorized, code: 401, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().RefreshAsync("expired"));

        Assert.Equal(RelayFailure.Unauthenticated, error.Failure);
    }

    [Fact]
    public async Task APasswordlessAccountIsDistinguishedFromAWrongPassword()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.Unauthorized,
            code: 401,
            dataJson: null,
            reason: "PASSWORD_NOT_SET");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().LoginAsync("a@b.com", "pw"));

        Assert.Equal(RelayFailure.PasswordNotSet, error.Failure);
        Assert.Contains("网页版", error.UserMessage);
        Assert.Equal("PASSWORD_NOT_SET", error.Reason);
    }

    [Fact]
    public async Task RateLimitingIsClassifiedSoCallersCanBackOff()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.TooManyRequests, code: 429, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().SendVerifyCodeAsync("a@b.com", turnstileToken: null));

        Assert.Equal(RelayFailure.RateLimited, error.Failure);
    }

    [Fact]
    public async Task ANonZeroCodeFailsEvenWhenTheStatusIsSuccessful()
    {
        // The envelope's code is authoritative; a 200 carrying code != 0 is a failure.
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 400, dataJson: null);

        await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetPublicSettingsAsync());
    }

    [Fact]
    public async Task ABodyThatIsNotTheEnvelopeIsReportedAsMalformed()
    {
        // A proxy error page or captive portal answering instead of the relay.
        var handler = StubHandler.Raw(HttpStatusCode.BadGateway, "<html>502</html>", "text/html");

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetPublicSettingsAsync());

        Assert.Equal(RelayFailure.MalformedResponse, error.Failure);
        Assert.Equal(502, error.StatusCode);
    }

    [Fact]
    public async Task AMissingDataFieldOnASuccessIsSurfacedNotDefaulted()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, dataJson: null);

        RelayApiException error = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().GetPublicSettingsAsync());

        Assert.Equal(RelayFailure.MalformedResponse, error.Failure);
    }

    [Fact]
    public async Task RegistrationOmitsOptionalFieldsTheOperatorDisabled()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, TokenPayload);

        await handler.CreateClient().RegisterAsync(new RegistrationRequest
        {
            Email = "a@b.com",
            Password = "secret1",
            VerifyCode = "123456",
        });

        Assert.NotNull(handler.LastRequestBody);
        Assert.Contains("\"verify_code\"", handler.LastRequestBody);
        Assert.DoesNotContain("invitation_code", handler.LastRequestBody);
        Assert.DoesNotContain("promo_code", handler.LastRequestBody);
        Assert.DoesNotContain("turnstile_token", handler.LastRequestBody);
    }

    [Fact]
    public async Task AuthenticatedReadsCarryTheBearerToken()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"id":7,"email":"a@b.com","username":"ann"}""");

        await handler.CreateClient().GetCurrentUserAsync("the-token");

        Assert.Equal("Bearer", handler.LastRequest!.Headers.Authorization!.Scheme);
        Assert.Equal("the-token", handler.LastRequest.Headers.Authorization.Parameter);
    }

    [Fact]
    public async Task RequestsLandUnderTheVersionedApiPrefix()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "{}");

        await handler.CreateClient("https://relay.test/").GetPublicSettingsAsync();

        Assert.Equal("https://relay.test/api/v1/settings/public", handler.LastRequest!.RequestUri!.ToString());
    }

    [Fact]
    public void ABaseAddressWithoutATrailingSlashIsRejected()
    {
        // Without the slash, Uri resolution silently drops the last segment and
        // every request would go to the wrong path.
        var http = new HttpClient(StubHandler.Envelope(HttpStatusCode.OK, 0, "{}"))
        {
            BaseAddress = new Uri("https://relay.test/base"),
        };

        Assert.Throws<ArgumentException>(() => new RelayServerClient(http));
    }

    [Fact]
    public async Task CheckoutInfoBindsPaymentMethodsAndLimits()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"methods":{"alipay":{"display_name":"Alipay","single_min":5,"single_max":500,"fee_rate":1.5,"available":true}},"global_min":5,"global_max":500,"balance_disabled":false,"balance_recharge_multiplier":0.14,"recharge_fee_rate":1.5}""");

        PaymentCheckoutInfo info = await handler.CreateClient().GetCheckoutInfoAsync("at");

        Assert.Equal(5m, info.GlobalMin);
        Assert.Equal(500m, info.GlobalMax);
        Assert.Equal(0.14m, info.BalanceRechargeMultiplier);
        Assert.Equal("Alipay", info.Methods["alipay"].DisplayName);
        Assert.Equal(1.5m, info.Methods["alipay"].FeeRate);
        Assert.Equal("Bearer", handler.LastRequest!.Headers.Authorization!.Scheme);
        Assert.Equal("at", handler.LastRequest.Headers.Authorization.Parameter);
        Assert.Equal("GET", handler.LastRequest.Method.Method);
        Assert.EndsWith("/api/v1/payment/checkout-info", handler.LastRequest.RequestUri!.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task BalanceOrderSendsBalanceOrderTypeAndIdempotencyKey()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"order_id":42,"amount":50,"pay_amount":50.75,"fee_rate":1.5,"payment_type":"alipay","qr_code":"data:image/png;base64,qr","out_trade_no":"OUT42","expires_at":"2026-08-05T12:00:00Z"}""");

        PaymentOrderCreateResult result = await handler.CreateClient().CreateBalanceOrderAsync("at", 50m, "alipay");

        Assert.Equal(42, result.OrderId);
        Assert.Equal("OUT42", result.OutTradeNo);
        Assert.Equal("data:image/png;base64,qr", result.QrCode);
        Assert.Equal("Bearer", handler.LastRequest!.Headers.Authorization!.Scheme);
        Assert.False(string.IsNullOrWhiteSpace(handler.LastRequest.Headers.GetValues("Idempotency-Key").Single()));
        Assert.Contains("\"amount\":50", handler.LastRequestBody, StringComparison.Ordinal);
        Assert.Contains("\"payment_type\":\"alipay\"", handler.LastRequestBody, StringComparison.Ordinal);
        Assert.Contains("\"order_type\":\"balance\"", handler.LastRequestBody, StringComparison.Ordinal);
    }

    [Fact]
    public async Task PaymentOrderQueriesAndCancelsById()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"id":42,"amount":50,"pay_amount":50.75,"fee_rate":1.5,"payment_type":"alipay","out_trade_no":"OUT42","status":"COMPLETED","order_type":"balance","expires_at":"2026-08-05T12:00:00Z"}""");

        PaymentOrder order = await handler.CreateClient().GetPaymentOrderAsync("at", 42);

        Assert.Equal(42, order.Id);
        Assert.Equal(PaymentOrderStatus.Completed, order.Status);
        Assert.EndsWith("/api/v1/payment/orders/42", handler.LastRequest!.RequestUri!.ToString(), StringComparison.Ordinal);

        var cancelHandler = StubHandler.Envelope(HttpStatusCode.OK, code: 0, "{}");
        await cancelHandler.CreateClient().CancelPaymentOrderAsync("at", 42);
        Assert.EndsWith("/api/v1/payment/orders/42/cancel", cancelHandler.LastRequest!.RequestUri!.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public async Task VerifyPaymentOrderSendsOutTradeNumber()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"id":42,"out_trade_no":"OUT42","status":"PAID","order_type":"balance","amount":50,"pay_amount":50,"fee_rate":0,"expires_at":"2026-08-05T12:00:00Z"}""");

        PaymentOrder order = await handler.CreateClient().VerifyPaymentOrderAsync("at", "OUT42");

        Assert.Equal(PaymentOrderStatus.Paid, order.Status);
        Assert.Contains("\"out_trade_no\":\"OUT42\"", handler.LastRequestBody, StringComparison.Ordinal);
        Assert.EndsWith("/api/v1/payment/orders/verify", handler.LastRequest!.RequestUri!.ToString(), StringComparison.Ordinal);
    }
}

public sealed class PublicSettingsTests
{
    [Fact]
    public void TheConservativeFallbackHidesEverythingOptional()
    {
        // Used when /settings/public cannot be read: show sign-in only rather
        // than guessing which features the operator enabled.
        PublicSettings settings = PublicSettings.Conservative;

        Assert.False(settings.RegistrationEnabled);
        Assert.False(settings.PaymentEnabled);
        Assert.False(settings.TurnstileEnabled);
        Assert.False(settings.PasswordResetEnabled);
        Assert.Empty(settings.RegistrationEmailSuffixWhitelist);
    }

    [Fact]
    public void AnEmptyWhitelistAllowsAnyEmail()
    {
        Assert.True(PublicSettings.Conservative.IsEmailSuffixAllowed("someone@anywhere.dev"));
    }

    [Theory]
    [InlineData("ann@qq.com", true)]
    [InlineData("ann@163.com", true)]
    [InlineData("ANN@QQ.COM", true)]
    [InlineData("ann@gmail.com", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    public void AWhitelistIsMatchedOnSuffixCaseInsensitively(string? email, bool expected)
    {
        var settings = new PublicSettings
        {
            RegistrationEmailSuffixWhitelist = new[] { "@qq.com", " @163.com " },
        };

        Assert.Equal(expected, settings.IsEmailSuffixAllowed(email));
    }

    [Fact]
    public void TheWhitelistBindsFromAJsonArrayNotADelimitedString()
    {
        // Regression guard for a contract bug: the server sends []string here.
        // Binding it as a string throws during deserialization and takes the
        // whole sign-in surface down, since every control depends on these flags.
        const string json =
            """{"registration_enabled":true,"registration_email_suffix_whitelist":["@qq.com","@163.com"]}""";

        PublicSettings? settings = JsonSerializer.Deserialize<PublicSettings>(
            json,
            new JsonSerializerOptions(JsonSerializerDefaults.Web));

        Assert.NotNull(settings);
        Assert.Equal(2, settings!.RegistrationEmailSuffixWhitelist.Count);
        Assert.True(settings.IsEmailSuffixAllowed("ann@qq.com"));
    }
}
