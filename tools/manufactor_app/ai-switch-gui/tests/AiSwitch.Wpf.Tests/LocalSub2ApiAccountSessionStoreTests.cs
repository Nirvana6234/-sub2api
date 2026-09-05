using System.Text;
using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class LocalSub2ApiAccountSessionStoreTests
{
    [Fact]
    public void TryCreate_NormalizesLocalEndpointRoleAndTimestampWithoutRenderingRefreshToken()
    {
        const string refreshToken = "refresh-token-that-must-not-render";
        DateTimeOffset savedAt = new(2026, 7, 14, 10, 30, 0, TimeSpan.FromHours(8));

        bool created = LocalSub2ApiAccountSession.TryCreate(
            $"  {refreshToken}  ",
            "http://127.0.0.1:8080/v1",
            userId: 42,
            role: " ADMIN ",
            savedAt,
            out LocalSub2ApiAccountSession? session);

        Assert.True(created);
        Assert.NotNull(session);
        Assert.Equal(refreshToken, session.RefreshToken);
        Assert.Equal("http://127.0.0.1:8080/", session.ApiBaseUri.AbsoluteUri);
        Assert.Equal(42, session.UserId);
        Assert.Equal("admin", session.Role);
        Assert.True(session.IsAdministrator);
        Assert.Equal(savedAt.ToUniversalTime(), session.SavedAtUtc);
        Assert.DoesNotContain(refreshToken, session.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void TryCreate_AcceptsSecureCloudEndpoint()
    {
        bool created = LocalSub2ApiAccountSession.TryCreate(
            "refresh-token",
            "https://example.com/api/v1",
            userId: 7,
            role: "user",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session);

        Assert.True(created);
        Assert.Equal("https://example.com/", session?.ApiBaseUri.AbsoluteUri);
    }

    [Fact]
    public void TryCreate_RejectsPublicHttpEndpoint()
    {
        bool created = LocalSub2ApiAccountSession.TryCreate(
            "refresh-token",
            "http://example.com/api/v1",
            userId: 7,
            role: "user",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session);

        Assert.False(created);
        Assert.Null(session);
    }

    [Theory]
    [InlineData("http://127.0.0.1:8080", "operator")]
    [InlineData("http://127.0.0.1:8080", "")]
    public void TryCreate_RejectsInsecurePublicEndpointAndUnsupportedRoles(string apiBaseUrl, string role)
    {
        bool created = LocalSub2ApiAccountSession.TryCreate(
            "refresh-token",
            apiBaseUrl,
            userId: 7,
            role,
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session);

        Assert.False(created);
        Assert.Null(session);
    }

    [Fact]
    public void SaveAndLoad_RoundTripsCurrentWindowsUserProtectedSessionWithoutPlaintextSecrets()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        const string refreshToken = "refresh-token-that-must-not-appear-on-disk";
        using var temporary = new TemporarySessionFile();
        var store = new LocalSub2ApiAccountSessionStore(temporary.Path);
        Assert.True(LocalSub2ApiAccountSession.TryCreate(
            refreshToken,
            "http://127.0.0.1:8080/api/v1",
            userId: 135,
            role: "user",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session));

        Assert.Equal(LocalSub2ApiAccountSessionSaveResult.Saved, store.Save(session!));
        string protectedFileText = Encoding.UTF8.GetString(File.ReadAllBytes(temporary.Path));
        Assert.DoesNotContain(refreshToken, protectedFileText, StringComparison.Ordinal);
        Assert.DoesNotContain("access_token", protectedFileText, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("password", protectedFileText, StringComparison.OrdinalIgnoreCase);

        LocalSub2ApiAccountSession? loaded = store.Load(session!.ApiBaseUri);

        Assert.NotNull(loaded);
        Assert.Equal(refreshToken, loaded.RefreshToken);
        Assert.Equal("http://127.0.0.1:8080/", loaded.ApiBaseUri.AbsoluteUri);
        Assert.Equal(135, loaded.UserId);
        Assert.Equal("user", loaded.Role);
        Assert.False(loaded.IsAdministrator);
        Assert.DoesNotContain(refreshToken, loaded.ToString(), StringComparison.Ordinal);
    }

    [Fact]
    public void Clear_RemovesThePersistedSession()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        using var temporary = new TemporarySessionFile();
        var store = new LocalSub2ApiAccountSessionStore(temporary.Path);
        Assert.True(LocalSub2ApiAccountSession.TryCreate(
            "refresh-token",
            "http://127.0.0.1:8080",
            userId: 9,
            role: "user",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? session));
        Assert.Equal(LocalSub2ApiAccountSessionSaveResult.Saved, store.Save(session!));
        Assert.True(File.Exists(temporary.Path));

        bool cleared = store.Clear(session!.ApiBaseUri);

        Assert.True(cleared);
        Assert.False(File.Exists(temporary.Path));
        Assert.Null(store.Load(session.ApiBaseUri));
        Assert.False(store.Clear(session.ApiBaseUri));
    }

    [Fact]
    public void Load_CorruptedProtectedPayloadIsTreatedAsNoSession()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        using var temporary = new TemporarySessionFile();
        Directory.CreateDirectory(Path.GetDirectoryName(temporary.Path)!);
        File.WriteAllBytes(temporary.Path, Encoding.UTF8.GetBytes("not-a-dpapi-session"));
        var store = new LocalSub2ApiAccountSessionStore(temporary.Path);

        LocalSub2ApiAccountSession? loaded = store.Load(new Uri("http://127.0.0.1:8080/"));

        Assert.Null(loaded);
    }

    [Fact]
    public void MultipleEndpoints_AreRestoredIndependentlyAndLogoutClearsOnlyTheSelectedEndpoint()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        using var temporary = new TemporarySessionFile();
        var store = new LocalSub2ApiAccountSessionStore(temporary.Path);
        Assert.True(LocalSub2ApiAccountSession.TryCreate(
            "local-refresh",
            "http://127.0.0.1:8080/",
            userId: 31,
            role: "user",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? local));
        Assert.True(LocalSub2ApiAccountSession.TryCreate(
            "cloud-refresh",
            "https://relay.example.test/",
            userId: 32,
            role: "admin",
            DateTimeOffset.UtcNow,
            out LocalSub2ApiAccountSession? cloud));

        Assert.Equal(LocalSub2ApiAccountSessionSaveResult.Saved, store.Save(local!));
        Assert.Equal(LocalSub2ApiAccountSessionSaveResult.Saved, store.Save(cloud!));

        Assert.Equal("local-refresh", store.Load(local!.ApiBaseUri)?.RefreshToken);
        Assert.Equal("cloud-refresh", store.Load(cloud!.ApiBaseUri)?.RefreshToken);
        Assert.True(store.Clear(local.ApiBaseUri));
        Assert.Null(store.Load(local.ApiBaseUri));
        Assert.Equal("cloud-refresh", store.Load(cloud.ApiBaseUri)?.RefreshToken);
    }

    private sealed class TemporarySessionFile : IDisposable
    {
        private readonly string _directory;

        public TemporarySessionFile()
        {
            _directory = System.IO.Path.Combine(
                System.IO.Path.GetTempPath(),
                "lanai-local-session-tests",
                Guid.NewGuid().ToString("N"));
            Path = System.IO.Path.Combine(_directory, "sub2api-local-account-session.bin");
        }

        public string Path { get; }

        public void Dispose()
        {
            try
            {
                if (Directory.Exists(_directory))
                {
                    Directory.Delete(_directory, recursive: true);
                }
            }
            catch (IOException)
            {
                // Test cleanup must not hide a useful assertion failure.
            }
            catch (UnauthorizedAccessException)
            {
                // Same as above when an antivirus scanner briefly holds the file.
            }
        }
    }
}
