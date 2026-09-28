using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json.Nodes;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Signing in to an official account within the client (任务计划 §3.3 / §3.4, A2 / A3): the links
/// the relay server would build, the browser coming back by itself or by paste, the state that
/// ties a code to its sign-in, and the exchange.
/// </summary>
public sealed class OfficialSignInTests
{
    private static readonly DateTimeOffset Now = new(2026, 9, 28, 12, 0, 0, TimeSpan.Zero);

    private sealed class Handler(Func<HttpRequestMessage, HttpResponseMessage> respond) : HttpMessageHandler
    {
        public List<(HttpRequestMessage Request, string Body)> Seen { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            string body = request.Content is null ? string.Empty : await request.Content.ReadAsStringAsync(cancellationToken);
            lock (Seen)
            {
                Seen.Add((request, body));
            }

            return respond(request);
        }
    }

    private static HttpResponseMessage Json(HttpStatusCode status, string body) =>
        new(status) { Content = new StringContent(body, Encoding.UTF8, "application/json") };

    private static string IdToken(string plan = "plus") => TestJwt.Make(new JsonObject
    {
        ["email"] = "me@example.com",
        ["https://api.openai.com/auth"] = new JsonObject { ["chatgpt_plan_type"] = plan, ["chatgpt_account_id"] = "acc-9" },
    });

    private static readonly string AccessToken = TestJwt.Expiring(Now.AddHours(1));

    private static Handler OpenAiServer() => new(request => request.RequestUri!.AbsolutePath switch
    {
        "/oauth/token" => Json(HttpStatusCode.OK, new JsonObject
        {
            ["access_token"] = AccessToken,
            ["refresh_token"] = "rt-new",
            ["id_token"] = IdToken(),
            ["expires_in"] = 3600,
        }.ToJsonString()),
        "/backend-api/subscriptions" => Json(HttpStatusCode.OK, """{"plan_type":"plus","active_until":"2026-10-28T00:00:00Z"}"""),
        _ => new HttpResponseMessage(HttpStatusCode.NotFound),
    });

    private static Handler ClaudeServer() => new(_ => Json(HttpStatusCode.OK, """
        {"access_token":"cat","refresh_token":"crt","expires_in":28800,"scope":"user:inference",
         "account":{"uuid":"uuid-3","email_address":"me@example.com"},"organization":{"uuid":"org-3"}}
        """));

    private static OfficialSignInEndpoints Endpoints(int port = 1455) => OfficialSignInEndpoints.Official with { OpenAiCallbackPort = port };

    /// <summary>
    /// A ChatGPT sign-in listening on a free port. Tests run in parallel: a port found free can be
    /// taken by another test before the session listens on it, and the session then (rightly)
    /// falls back to pasting — so another port is tried.
    /// </summary>
    private static (OfficialSignInSession Session, int Port) StartListening(Handler server)
    {
        for (int attempt = 0; ; attempt++)
        {
            int port = LoopbackHttpListener.ProbeFreePort();
            OfficialSignInSession session = OfficialSignInSession.Start(
                LocalProxyKind.Codex, new OfficialTokenExchanger(Endpoints(port), _ => server, () => Now));
            if (session.IsAutomatic || attempt >= 10)
            {
                return (session, port);
            }

            session.Dispose();
        }
    }

    [Fact]
    public void TheChatGptLinkIsTheOneTheRelayServerBuilds()
    {
        Uri url = OfficialAuthorization.OpenAiUrl(OfficialSignInEndpoints.Official, "st", "ch");
        Dictionary<string, string> query = OfficialAuthorization.ParseQuery(url.Query.TrimStart('?'));

        Assert.Equal("https://auth.openai.com/oauth/authorize", url.GetLeftPart(UriPartial.Path));
        Assert.Equal("app_EMoamEEZ73f0CkXaXp7hrann", query["client_id"]);
        Assert.Equal("http://localhost:1455/auth/callback", query["redirect_uri"]);
        Assert.Equal("openid profile email offline_access", query["scope"]);
        Assert.Equal("S256", query["code_challenge_method"]);
        Assert.Equal("true", query["codex_cli_simplified_flow"]);
        Assert.Equal("true", query["id_token_add_organizations"]);
        Assert.Equal("st", query["state"]);
    }

    [Fact]
    public void TheClaudeLinkIsTheOneTheRelayServerBuilds()
    {
        Uri url = OfficialAuthorization.ClaudeUrl(OfficialSignInEndpoints.Official, "st", "ch");
        Dictionary<string, string> query = OfficialAuthorization.ParseQuery(url.Query.TrimStart('?'));

        Assert.Equal("https://claude.com/cai/oauth/authorize", url.GetLeftPart(UriPartial.Path));
        Assert.Equal("true", query["code"]);
        Assert.Equal("9d1c250a-e61b-44d9-88ed-5944d1962f5e", query["client_id"]);
        Assert.Equal("https://platform.claude.com/oauth/code/callback", query["redirect_uri"]);
        Assert.Contains("user:inference", query["scope"], StringComparison.Ordinal);
        Assert.Contains("org:create_api_key", query["scope"], StringComparison.Ordinal);
    }

    [Fact]
    public void PkceIsS256OfTheVerifier()
    {
        string verifier = Pkce.NewVerifier();
        Assert.Equal(128, verifier.Length);
        // RFC 7636 appendix B.
        Assert.Equal("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", Pkce.Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"));
    }

    [Theory]
    [InlineData("http://localhost:1455/auth/callback?code=abc%2Fd&scope=openid&state=st1", "abc/d", "st1")]
    [InlineData("  code=abc&state=st1  ", "abc", "st1")]
    [InlineData("?code=abc&state=st1", "abc", "st1")]
    [InlineData("claudecode#st1", "claudecode", "st1")]
    [InlineData("claudecode", "claudecode", null)]
    public void WhatTheUserPastesIsRead(string pasted, string code, string? state) =>
        Assert.Equal((code, state), OfficialAuthorization.ParsePasted(pasted));

    [Theory]
    [InlineData("")]
    [InlineData("http://localhost:1455/auth/callback?error=access_denied&state=st1")]
    [InlineData("not a code at all")]
    [InlineData("#st1")]
    public void NothingUsableIsRefused(string pasted) => Assert.Null(OfficialAuthorization.ParsePasted(pasted));

    [Fact]
    public async Task AChatGptCodeIsTradedWithItsVerifierAndTheSubscriptionIsLookedUp()
    {
        Handler server = OpenAiServer();
        var exchanger = new OfficialTokenExchanger(handler: _ => server, clock: () => Now);

        OfficialAccount account = await exchanger.ExchangeOpenAiAsync("the-code", "the-verifier", CancellationToken.None);

        Assert.Equal(OfficialAccount.OpenAi, account.Platform);
        Assert.Equal("rt-new", account.RefreshToken);
        Assert.Equal("me@example.com", account.Email);
        Assert.Equal("plus", account.PlanType);
        Assert.Equal("acc-9", account.ChatGptAccountId);
        Assert.Equal(Now.AddHours(1), account.ExpiresAt);
        Assert.Equal("2026-10-28T00:00:00Z", account.SubscriptionExpiresAt);

        (HttpRequestMessage token, string form) = server.Seen[0];
        Assert.Equal("https://auth.openai.com/oauth/token", token.RequestUri!.ToString());
        Dictionary<string, string> fields = OfficialAuthorization.ParseQuery(form);
        Assert.Equal("authorization_code", fields["grant_type"]);
        Assert.Equal("the-code", fields["code"]);
        Assert.Equal("the-verifier", fields["code_verifier"]);
        Assert.Equal("http://localhost:1455/auth/callback", fields["redirect_uri"]);
        Assert.Equal("codex_cli_rs", token.Headers.GetValues("originator").Single());

        (HttpRequestMessage subscription, _) = server.Seen[1];
        Assert.Equal("https://chatgpt.com/backend-api/subscriptions?account_id=acc-9", subscription.RequestUri!.ToString());
        Assert.Equal("Bearer", subscription.Headers.Authorization!.Scheme);
    }

    [Fact]
    public async Task ASubscriptionLookupThatFailsDoesNotStopTheSignIn()
    {
        var server = new Handler(request => request.RequestUri!.AbsolutePath == "/oauth/token"
            ? Json(HttpStatusCode.OK, new JsonObject
            {
                ["access_token"] = AccessToken,
                ["refresh_token"] = "rt-new",
                ["id_token"] = IdToken(),
            }.ToJsonString())
            : new HttpResponseMessage(HttpStatusCode.Forbidden));
        var exchanger = new OfficialTokenExchanger(handler: _ => server, clock: () => Now);

        OfficialAccount account = await exchanger.ExchangeOpenAiAsync("c", "v", CancellationToken.None);

        Assert.Equal(string.Empty, account.SubscriptionExpiresAt);
        Assert.Equal("rt-new", account.RefreshToken);
    }

    [Fact]
    public async Task AClaudeCodeIsTradedAsJsonWithItsState()
    {
        Handler server = ClaudeServer();
        var exchanger = new OfficialTokenExchanger(handler: _ => server, clock: () => Now);

        OfficialAccount account = await exchanger.ExchangeClaudeAsync("the-code", "st1", "the-verifier", CancellationToken.None);

        Assert.Equal(OfficialAccount.Anthropic, account.Platform);
        Assert.Equal("crt", account.RefreshToken);
        Assert.Equal("uuid-3", account.AccountUuid);
        Assert.Equal("org-3", account.OrgUuid);
        Assert.Equal("me@example.com", account.Email);
        Assert.Equal(Now.AddSeconds(28800), account.ExpiresAt);

        (HttpRequestMessage request, string body) = server.Seen.Single();
        Assert.Equal("https://platform.claude.com/v1/oauth/token", request.RequestUri!.ToString());
        JsonObject sent = (JsonObject)JsonNode.Parse(body)!;
        Assert.Equal("the-code", (string?)sent["code"]);
        Assert.Equal("st1", (string?)sent["state"]);
        Assert.Equal("the-verifier", (string?)sent["code_verifier"]);
        Assert.Equal("9d1c250a-e61b-44d9-88ed-5944d1962f5e", (string?)sent["client_id"]);
        Assert.Equal("https://platform.claude.com/oauth/code/callback", (string?)sent["redirect_uri"]);
    }

    [Fact]
    public async Task ARefusedCodeAndAnUnreachableHostAreSaidInWords()
    {
        var refused = new OfficialTokenExchanger(handler: _ => new Handler(_ => Json(HttpStatusCode.BadRequest, """{"error":"invalid_grant"}""")));
        var unreachable = new OfficialTokenExchanger(handler: _ => new Handler(_ => throw new HttpRequestException("down")));

        var a = await Assert.ThrowsAsync<OfficialSignInException>(() => refused.ExchangeOpenAiAsync("c", "v", CancellationToken.None));
        var b = await Assert.ThrowsAsync<OfficialSignInException>(() => unreachable.ExchangeClaudeAsync("c", null, "v", CancellationToken.None));

        Assert.Contains("重新登录", a.UserMessage, StringComparison.Ordinal);
        Assert.Contains("platform.claude.com", b.UserMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task AnAnswerWithoutARefreshTokenIsNotKept()
    {
        var exchanger = new OfficialTokenExchanger(handler: _ => new Handler(_ => Json(HttpStatusCode.OK, """{"access_token":"a"}""")));

        await Assert.ThrowsAsync<OfficialSignInException>(() => exchanger.ExchangeOpenAiAsync("c", "v", CancellationToken.None));
    }

    [Fact]
    public async Task TheBrowserComingBackToLocalhostFinishesTheChatGptSignIn()
    {
        Handler server = OpenAiServer();
        (OfficialSignInSession started, int port) = StartListening(server);
        using OfficialSignInSession session = started;
        Assert.True(session.IsAutomatic);
        string state = OfficialAuthorization.ParseQuery(session.AuthorizeUrl.Query.TrimStart('?'))["state"];

        using var browser = new HttpClient();
        HttpResponseMessage stray = await browser.GetAsync($"http://localhost:{port}/auth/callback?code=evil&state=wrong");
        HttpResponseMessage page = await browser.GetAsync($"http://localhost:{port}/auth/callback?code=real&state={state}");
        OfficialAccount account = await session.Completion.WaitAsync(TimeSpan.FromSeconds(10));

        Assert.Equal(HttpStatusCode.BadRequest, stray.StatusCode);
        Assert.Equal(HttpStatusCode.OK, page.StatusCode);
        Assert.Contains("授权完成", await page.Content.ReadAsStringAsync(), StringComparison.Ordinal);
        Assert.Equal("rt-new", account.RefreshToken);
        Assert.Equal("real", OfficialAuthorization.ParseQuery(server.Seen[0].Body)["code"]);
    }

    [Fact]
    public async Task AHeldPortMeansPastingAndAPastedAddressFinishesTheSignIn()
    {
        using var holder = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        holder.Start();
        int port = ((IPEndPoint)holder.LocalEndpoint).Port;
        Handler server = OpenAiServer();
        using OfficialSignInSession session = OfficialSignInSession.Start(
            LocalProxyKind.Codex, new OfficialTokenExchanger(Endpoints(port), _ => server, () => Now));
        string state = OfficialAuthorization.ParseQuery(session.AuthorizeUrl.Query.TrimStart('?'))["state"];

        Assert.False(session.IsAutomatic);
        Assert.Throws<OfficialSignInException>(() => session.SubmitPasted("hello"));
        Assert.Throws<OfficialSignInException>(() => session.SubmitPasted($"http://localhost:{port}/auth/callback?code=x&state=someone-else"));
        Assert.Throws<OfficialSignInException>(() => session.SubmitPasted("code=x"));
        session.SubmitPasted($"http://localhost:{port}/auth/callback?code=pasted&state={state}");
        session.SubmitPasted($"http://localhost:{port}/auth/callback?code=pasted&state={state}");

        OfficialAccount account = await session.Completion.WaitAsync(TimeSpan.FromSeconds(10));
        Assert.Equal("rt-new", account.RefreshToken);
        Assert.Single(server.Seen, s => s.Request.RequestUri!.AbsolutePath == "/oauth/token");
    }

    [Fact]
    public async Task AClaudeSignInIsFinishedByPastingTheCode()
    {
        Handler server = ClaudeServer();
        using OfficialSignInSession session = OfficialSignInSession.Start(
            LocalProxyKind.ClaudeCode, new OfficialTokenExchanger(handler: _ => server, clock: () => Now));
        string state = OfficialAuthorization.ParseQuery(session.AuthorizeUrl.Query.TrimStart('?'))["state"];

        Assert.False(session.IsAutomatic);
        session.SubmitPasted($"code-from-page#{state}");

        OfficialAccount account = await session.Completion.WaitAsync(TimeSpan.FromSeconds(10));
        Assert.Equal("uuid-3", account.AccountUuid);
        Assert.Equal(state, (string?)JsonNode.Parse(server.Seen.Single().Body)!["state"]);
    }

    [Fact]
    public async Task DecliningInTheBrowserAndCancellingEndTheSignInInWords()
    {
        (OfficialSignInSession listening, int port) = StartListening(OpenAiServer());
        using OfficialSignInSession declined = listening;
        Assert.True(declined.IsAutomatic);
        string state = OfficialAuthorization.ParseQuery(declined.AuthorizeUrl.Query.TrimStart('?'))["state"];
        using (var browser = new HttpClient())
        {
            await browser.GetAsync($"http://localhost:{port}/auth/callback?error=access_denied&state={state}");
        }

        var a = await Assert.ThrowsAsync<OfficialSignInException>(() => declined.Completion.WaitAsync(TimeSpan.FromSeconds(10)));
        Assert.Contains("取消了授权", a.UserMessage, StringComparison.Ordinal);

        using OfficialSignInSession cancelled = OfficialSignInSession.Start(
            LocalProxyKind.ClaudeCode, new OfficialTokenExchanger(handler: _ => ClaudeServer()));
        cancelled.Cancel();
        var b = await Assert.ThrowsAsync<OfficialSignInException>(() => cancelled.Completion.WaitAsync(TimeSpan.FromSeconds(10)));
        Assert.Equal("已取消登录。", b.UserMessage);
    }

    [Fact]
    public async Task NothingSecretReachesTheLog()
    {
        var log = new StringBuilder();
        using (LanAi.RelayClient.Services.ClientLog.Capture(log))
        {
            var exchanger = new OfficialTokenExchanger(handler: _ => OpenAiServer(), clock: () => Now);
            await exchanger.ExchangeOpenAiAsync("secret-code", "secret-verifier", CancellationToken.None);
        }

        Assert.DoesNotContain("secret", log.ToString(), StringComparison.Ordinal);
        Assert.DoesNotContain("rt-new", log.ToString(), StringComparison.Ordinal);
        Assert.DoesNotContain("me@example.com", log.ToString(), StringComparison.Ordinal);
    }
}
