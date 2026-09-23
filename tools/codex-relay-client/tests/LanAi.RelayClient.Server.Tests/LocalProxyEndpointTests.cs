using System.Net;
using Xunit;

namespace LanAi.RelayClient.Server.Tests;

/// <summary>The two endpoints behind the local proxy page.</summary>
public sealed class LocalProxyEndpointTests
{
    [Fact]
    public async Task ContributionListingBindsTheFieldsThePageShowsAndClassifiesEachAccount()
    {
        // Shaped like the server's dto.Account after RedactCredentials: secrets are gone,
        // plan_type survives as a label.
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """
            {"items":[
              {"id":1,"name":"我的 Plus","platform":"openai","type":"oauth","status":"active","error_message":"","credentials":{"plan_type":"plus"},"credentials_status":{"has_access_token":true}},
              {"id":2,"name":"Max","platform":"anthropic","type":"oauth","status":"active","credentials":{}},
              {"id":3,"name":"长期 token","platform":"anthropic","type":"setup-token","status":"active"},
              {"id":4,"name":"key","platform":"openai","type":"apikey","status":"error","error_message":"401"},
              {"id":5,"name":"影子","platform":"openai","type":"oauth","status":"active","parent_account_id":1},
              {"id":6,"name":"Gemini","platform":"gemini","type":"oauth","status":"active"}
            ],"total":6,"page":1,"limit":100,"wallet":null,"income_rates":null}
            """);

        IReadOnlyList<ContributionAccount> accounts = await handler.CreateClient().ListContributionAccountsAsync("at");

        Assert.Equal(6, accounts.Count);
        Assert.Equal("我的 Plus", accounts[0].Name);
        Assert.Equal("plus", accounts[0].Credentials?.PlanType);
        Assert.True(accounts[0].IsActive);
        Assert.Equal(
            [LocalProxyKind.Codex, LocalProxyKind.ClaudeCode, LocalProxyKind.Unsupported,
             LocalProxyKind.Unsupported, LocalProxyKind.Unsupported, LocalProxyKind.Unsupported],
            accounts.Select(a => a.LocalProxyKind));
        Assert.Equal("401", accounts[3].ErrorMessage);
        Assert.Contains("account-contributions?page=1&limit=100", handler.LastRequest!.RequestUri!.ToString());
        Assert.Equal("Bearer", handler.LastRequest.Headers.Authorization?.Scheme);
    }

    [Fact]
    public async Task ContributionListingThatIsNotEnabledIsForbidden()
    {
        var handler = StubHandler.Envelope(HttpStatusCode.Forbidden, code: 403, dataJson: null, reason: "FEATURE_DISABLED");

        var ex = await Assert.ThrowsAsync<RelayApiException>(
            () => handler.CreateClient().ListContributionAccountsAsync("at"));

        Assert.Equal(RelayFailure.Forbidden, ex.Failure);
    }

    [Fact]
    public async Task LocalProxyCredentialIsPostedAndBindsEveryField()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"account_id":1,"name":"我的 Plus","platform":"openai","access_token":"at-openai","expires_at":"2026-09-23T10:00:00Z","chatgpt_account_id":"acct-1","fedramp":true}""");

        LocalProxyCredential credential = await handler.CreateClient().GetLocalProxyCredentialAsync("at", 1);

        Assert.Equal(HttpMethod.Post, handler.LastRequest!.Method);
        Assert.EndsWith("account-contributions/1/local-proxy-token", handler.LastRequest.RequestUri!.AbsolutePath);
        Assert.Equal(1, credential.AccountId);
        Assert.Equal("openai", credential.Platform);
        Assert.Equal("at-openai", credential.AccessToken);
        Assert.Equal(new DateTimeOffset(2026, 9, 23, 10, 0, 0, TimeSpan.Zero), credential.ExpiresAt);
        Assert.Equal("acct-1", credential.ChatGptAccountId);
        Assert.True(credential.FedRamp);
        Assert.DoesNotContain("at-openai", credential.ToString());
    }

    [Fact]
    public async Task LocalProxyCredentialForClaudeHasNoOpenAIFields()
    {
        var handler = StubHandler.Envelope(
            HttpStatusCode.OK,
            code: 0,
            """{"account_id":2,"name":"Max","platform":"anthropic","access_token":"sk-ant-oat"}""");

        LocalProxyCredential credential = await handler.CreateClient().GetLocalProxyCredentialAsync("at", 2);

        Assert.Equal("anthropic", credential.Platform);
        Assert.Null(credential.ExpiresAt);
        Assert.Equal(string.Empty, credential.ChatGptAccountId);
        Assert.False(credential.FedRamp);
    }
}
