using System.Net;
using System.Net.Http;
using System.Text;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class Sub2ApiSessionManagerTests
{
    private static readonly Uri LocalApi = new("http://127.0.0.1:8080/");

    [Fact]
    public async Task LoginAsync_PersistsOnlyRotatingSessionAndPublishesSafeRoleState()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"access-one","refresh_token":"refresh-one","expires_in":3600,"user":{"id":42,"role":"admin","balance":12.5,"frozen_balance":1.25}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":42,"role":"admin","balance":12.5,"frozen_balance":1.25}}
                """));
        using var manager = CreateManager(store, handler);

        Sub2ApiSessionAccess access = await manager.LoginAsync(
            LocalApi,
            "owner@example.test",
            "one-time-password",
            CancellationToken.None);

        Assert.True(access.IsAdministrator);
        Assert.Equal(42, access.UserId);
        Assert.True(manager.Current.IsAuthenticated);
        Assert.Equal("管理员", manager.Current.RoleLabel);
        Assert.Equal(12.5m, manager.Current.Balance);
        Assert.NotNull(store.Session);
        Assert.Equal("refresh-one", store.Session!.RefreshToken);
        Assert.DoesNotContain("one-time-password", store.Session.ToString(), StringComparison.Ordinal);
        Assert.DoesNotContain("access-one", store.Session.ToString(), StringComparison.Ordinal);
        Assert.Equal(2, handler.Requests.Count);
        Assert.EndsWith("/api/v1/auth/login", handler.Requests[0].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.EndsWith("/api/v1/auth/me", handler.Requests[1].Uri.AbsoluteUri, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LoginLocalControlAsync_UsesLoopbackTokenAndPersistsAdministratorSession()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"local-control-access","refresh_token":"local-control-refresh","expires_in":3600,"user":{"id":1,"role":"admin","balance":0,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":1,"role":"admin","balance":0,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);

        Sub2ApiSessionAccess access = await manager.LoginLocalControlAsync(
            LocalApi,
            "machine-local-token-with-more-than-32-characters",
            CancellationToken.None);

        Assert.True(access.IsAdministrator);
        Assert.Equal("local-control-refresh", store.Session?.RefreshToken);
        Assert.EndsWith("/api/v1/auth/local-control", handler.Requests[0].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.Equal("machine-local-token-with-more-than-32-characters", handler.Requests[0].LocalControlToken);
    }

    [Fact]
    public async Task LoginLocalControlAsync_ReplacesSavedLoopbackSessionWithoutRefreshingIt()
    {
        var store = new MemorySessionStore(CreateSavedSession("stale-refresh", 99, "admin"));
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"current-access","refresh_token":"current-refresh","expires_in":3600,"user":{"id":1,"role":"admin","balance":0,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":1,"role":"admin","balance":0,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);

        Sub2ApiSessionAccess access = await manager.LoginLocalControlAsync(
            LocalApi,
            "current-workspace-control-token",
            CancellationToken.None);

        Assert.Equal(1, access.UserId);
        Assert.Equal("current-refresh", store.Session!.RefreshToken);
        Assert.Equal(2, handler.Requests.Count);
        Assert.EndsWith("/api/v1/auth/local-control", handler.Requests[0].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.EndsWith("/api/v1/auth/me", handler.Requests[1].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.DoesNotContain(handler.Requests, request =>
            request.Uri.AbsolutePath.EndsWith("/api/v1/auth/refresh", StringComparison.Ordinal));
    }

    [Fact]
    public async Task LoginLocalControlAsync_RejectsNonLoopbackEndpointBeforeSending()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler();
        using var manager = CreateManager(store, handler);

        await Assert.ThrowsAsync<Sub2ApiSessionException>(() => manager.LoginLocalControlAsync(
            new Uri("https://relay.example.test/"),
            "machine-local-token-with-more-than-32-characters",
            CancellationToken.None));

        Assert.Empty(handler.Requests);
    }

    [Fact]
    public async Task RestoreAsync_RefreshesThenVerifiesCurrentUserAndRotatesSavedToken()
    {
        var store = new MemorySessionStore(CreateSavedSession("refresh-old", 7, "user"));
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"access-new","refresh_token":"refresh-new","expires_in":1800}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":7,"role":"user","balance":8.75,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);

        await manager.RestoreAsync(LocalApi, CancellationToken.None);

        Assert.True(manager.Current.IsAuthenticated);
        Assert.False(manager.Current.IsAdministrator);
        Assert.Equal("普通用户", manager.Current.RoleLabel);
        Assert.Equal("refresh-new", store.Session!.RefreshToken);
        Assert.Equal(2, handler.Requests.Count);
        Assert.EndsWith("/api/v1/auth/refresh", handler.Requests[0].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.EndsWith("/api/v1/auth/me", handler.Requests[1].Uri.AbsoluteUri, StringComparison.Ordinal);
        Assert.Equal("Bearer", handler.Requests[1].AuthorizationScheme);
        Assert.Equal("access-new", handler.Requests[1].AuthorizationParameter);
    }

    [Fact]
    public async Task GetAccessAsync_ReusesUnexpiredInMemoryAccess()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"access-one","refresh_token":"refresh-one","expires_in":3600,"user":{"id":5,"role":"user","balance":0,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":5,"role":"user","balance":0,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);
        Sub2ApiSessionAccess first = await manager.LoginAsync(
            LocalApi,
            "user@example.test",
            "password",
            CancellationToken.None);

        Sub2ApiSessionAccess second = await manager.GetAccessAsync(LocalApi, CancellationToken.None);

        Assert.Same(first, second);
        Assert.Equal(2, handler.Requests.Count);
    }

    [Fact]
    public async Task LoginAsync_AllowsHttpsCloudBackendAndPublishesItsEndpoint()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"cloud-access","refresh_token":"cloud-refresh","expires_in":3600,"user":{"id":15,"role":"user","balance":3,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":15,"role":"user","balance":3,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);
        var cloudApi = new Uri("https://relay.example.test/v1");

        Sub2ApiSessionAccess access = await manager.LoginAsync(
            cloudApi,
            "user@example.test",
            "password",
            CancellationToken.None);

        Assert.Equal("https://relay.example.test/", access.ApiBaseUri.AbsoluteUri);
        Assert.Equal(access.ApiBaseUri, manager.Current.ApiBaseUri);
        Assert.Equal("https://relay.example.test/", store.Session?.ApiBaseUri.AbsoluteUri);
        Assert.EndsWith("/api/v1/auth/login", handler.Requests[0].Uri.AbsoluteUri, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LoginAsync_AllowsPublicHttpOnlyAfterExplicitConfirmationWithoutPersistingSession()
    {
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"http-access","refresh_token":"http-refresh","expires_in":3600,"user":{"id":16,"role":"user","balance":3,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":16,"role":"user","balance":3,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);
        var cloudApi = new Uri("http://relay.example.test/v1");

        await Assert.ThrowsAsync<Sub2ApiSessionException>(() => manager.LoginAsync(
            cloudApi,
            "user@example.test",
            "password",
            CancellationToken.None));

        Sub2ApiSessionAccess access = await manager.LoginAsync(
            cloudApi,
            "user@example.test",
            "password",
            allowInsecurePublicHttp: true,
            CancellationToken.None);

        Assert.Equal("http://relay.example.test/", access.ApiBaseUri.AbsoluteUri);
        Assert.Null(store.Session);
        Assert.Equal(2, handler.Requests.Count);
    }

    [Fact]
    public async Task RestoreAsync_RejectsPublicHttpBeforeSendingSavedRefreshToken()
    {
        var cloudApi = new Uri("http://relay.example.test/v1");
        var store = new MemorySessionStore();
        var handler = new QueueHandler();
        using var manager = CreateManager(store, handler);

        await Assert.ThrowsAsync<Sub2ApiSessionException>(() => manager.RestoreAsync(cloudApi, CancellationToken.None));

        Assert.False(manager.Current.IsAuthenticated);
        Assert.Empty(handler.Requests);
    }

    [Fact]
    public async Task RestoreAsync_InvalidSavedSessionClearsItWithoutExposingServerBody()
    {
        var store = new MemorySessionStore(CreateSavedSession("expired-refresh", 9, "user"));
        var handler = new QueueHandler(new HttpResponseMessage(HttpStatusCode.Unauthorized)
        {
            Content = new StringContent("server-secret-details"),
        });
        using var manager = CreateManager(store, handler);

        await manager.RestoreAsync(LocalApi, CancellationToken.None);

        Assert.False(manager.Current.IsAuthenticated);
        Assert.Null(store.Session);
        Assert.DoesNotContain("server-secret-details", manager.Current.Status, StringComparison.Ordinal);
    }

    [Fact]
    public async Task LogoutAsync_ClearsLocalSessionEvenWhenGatewayIsOffline()
    {
        var store = new MemorySessionStore(CreateSavedSession("refresh-token", 11, "admin"));
        var handler = new QueueHandler(new HttpRequestException("offline"));
        using var manager = CreateManager(store, handler);

        await manager.LogoutAsync(CancellationToken.None);

        Assert.Null(store.Session);
        Assert.False(manager.Current.IsAuthenticated);
        Assert.Equal("已退出登录。", manager.Current.Status);
    }

    [Fact]
    public async Task SwitchingEndpoints_RestoresEachPreviouslySavedLoginIndependently()
    {
        var cloudApi = new Uri("https://relay.example.test/");
        var store = new MemorySessionStore();
        var handler = new QueueHandler(
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"local-access","refresh_token":"local-refresh","expires_in":3600,"user":{"id":21,"role":"user","balance":1,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":21,"role":"user","balance":1,"frozen_balance":0}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"cloud-access","refresh_token":"cloud-refresh","expires_in":3600,"user":{"id":22,"role":"admin","balance":2,"frozen_balance":0}}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":22,"role":"admin","balance":2,"frozen_balance":0}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"access_token":"local-access-new","refresh_token":"local-refresh-new","expires_in":3600}}
                """),
            Json(HttpStatusCode.OK, """
                {"code":0,"data":{"id":21,"role":"user","balance":3,"frozen_balance":0}}
                """));
        using var manager = CreateManager(store, handler);

        await manager.LoginAsync(LocalApi, "local@example.test", "password", CancellationToken.None);
        await manager.LoginAsync(cloudApi, "cloud@example.test", "password", CancellationToken.None);
        await manager.RestoreAsync(LocalApi, CancellationToken.None);

        Assert.Equal("local-refresh-new", store.Load(LocalApi)?.RefreshToken);
        Assert.Equal("cloud-refresh", store.Load(cloudApi)?.RefreshToken);
        Assert.Equal(LocalApi, manager.Current.ApiBaseUri);
        Assert.False(manager.Current.IsAdministrator);
    }

    private static Sub2ApiSessionManager CreateManager(MemorySessionStore store, HttpMessageHandler handler)
        => new(store, new HttpClient(handler) { Timeout = TimeSpan.FromSeconds(2) }, ownsHttpClient: true);

    private static LocalSub2ApiAccountSession CreateSavedSession(
        string refreshToken,
        long userId,
        string role,
        Uri? apiBaseUri = null)
    {
        Assert.True(LocalSub2ApiAccountSession.TryCreate(
            refreshToken,
            (apiBaseUri ?? LocalApi).AbsoluteUri,
            userId,
            role,
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session));
        return session!;
    }

    private static HttpResponseMessage Json(HttpStatusCode statusCode, string json)
        => new(statusCode)
        {
            Content = new StringContent(json, Encoding.UTF8, "application/json"),
        };

    private sealed class MemorySessionStore : ILocalSub2ApiAccountSessionStore
    {
        private readonly List<LocalSub2ApiAccountSession> _sessions = [];

        public MemorySessionStore(LocalSub2ApiAccountSession? session = null)
        {
            if (session is not null)
            {
                _sessions.Add(session);
            }
        }

        public LocalSub2ApiAccountSession? Session => _sessions.LastOrDefault();

        public LocalSub2ApiAccountSession? Load(Uri apiBaseUri) =>
            _sessions.FirstOrDefault(session => SameEndpoint(session.ApiBaseUri, apiBaseUri));

        public LocalSub2ApiAccountSession? LoadMostRecent() => _sessions.MaxBy(session => session.SavedAtUtc);

        public LocalSub2ApiAccountSessionSaveResult Save(LocalSub2ApiAccountSession session)
        {
            _sessions.RemoveAll(existing => SameEndpoint(existing.ApiBaseUri, session.ApiBaseUri));
            _sessions.Add(session);
            return LocalSub2ApiAccountSessionSaveResult.Saved;
        }

        public bool Clear(Uri apiBaseUri)
        {
            return _sessions.RemoveAll(session => SameEndpoint(session.ApiBaseUri, apiBaseUri)) > 0;
        }

        private static bool SameEndpoint(Uri left, Uri right) =>
            Uri.Compare(
                left,
                right,
                UriComponents.SchemeAndServer | UriComponents.Path,
                UriFormat.SafeUnescaped,
                StringComparison.OrdinalIgnoreCase) == 0;
    }

    private sealed class QueueHandler : HttpMessageHandler
    {
        private readonly Queue<object> _responses;

        public QueueHandler(params object[] responses) => _responses = new Queue<object>(responses);

        public List<RecordedRequest> Requests { get; } = [];

        protected override async Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            string body = request.Content is null
                ? string.Empty
                : await request.Content.ReadAsStringAsync(cancellationToken);
            Requests.Add(new RecordedRequest(
                request.RequestUri!,
                request.Headers.Authorization?.Scheme,
                request.Headers.Authorization?.Parameter,
                request.Headers.TryGetValues("X-Local-Control-Token", out IEnumerable<string>? tokenValues)
                    ? tokenValues.SingleOrDefault()
                    : null,
                body));

            object next = _responses.Dequeue();
            if (next is Exception exception)
            {
                throw exception;
            }

            return (HttpResponseMessage)next;
        }
    }

    private sealed record RecordedRequest(
        Uri Uri,
        string? AuthorizationScheme,
        string? AuthorizationParameter,
        string? LocalControlToken,
        string Body);
}
