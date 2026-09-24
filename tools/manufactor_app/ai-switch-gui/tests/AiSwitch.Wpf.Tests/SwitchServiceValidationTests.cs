using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using System.Security.Authentication;
using AiSwitchGui;

namespace AiSwitch.Wpf.Tests;

public sealed class SwitchServiceValidationTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "LanAi.SwitchValidation.Tests",
        Guid.NewGuid().ToString("N"));

    [Fact]
    public async Task ValidateProfileAsync_RetriesTransientConnectionFailures()
    {
        var handler = new SequenceHandler(attempt =>
        {
            if (attempt < 3)
            {
                throw new HttpRequestException(
                    "connection reset",
                    new SocketException((int)SocketError.ConnectionReset));
            }

            return new HttpResponseMessage(HttpStatusCode.OK);
        });
        SwitchService service = CreateService(handler);

        OperationResult result = await service.ValidateProfileAsync(
            CreateStore(),
            TargetMode.Cloud,
            CancellationToken.None);

        Assert.True(result.Success, result.Summary);
        Assert.Equal(3, handler.Attempts);
    }

    [Fact]
    public async Task ValidateProfileAsync_ExplainsCertificateNameMismatchWithoutRetrying()
    {
        var handler = new SequenceHandler(_ => throw new HttpRequestException(
            "The SSL connection could not be established, see inner exception.",
            new AuthenticationException(
                "The remote certificate is invalid according to the validation procedure: RemoteCertificateNameMismatch")));
        SwitchService service = CreateService(handler);

        OperationResult result = await service.ValidateProfileAsync(
            CreateStore(),
            TargetMode.Cloud,
            CancellationToken.None);

        Assert.False(result.Success);
        Assert.Equal(1, handler.Attempts);
        Assert.Contains("TLS 证书域名不匹配", result.Summary, StringComparison.Ordinal);
        Assert.Contains("DNS、反向代理和证书配置", result.Summary, StringComparison.Ordinal);
    }

    private SwitchService CreateService(HttpMessageHandler handler)
    {
        string userProfile = Path.Combine(_root, "user");
        string localAppData = Path.Combine(_root, "local");
        string profilesRoot = Path.Combine(userProfile, "ai-switch-gui");
        Directory.CreateDirectory(profilesRoot);
        var paths = new ConfigPaths(profilesRoot, userProfile, localAppData);
        return new SwitchService(
            paths,
            new ProfileRepository(paths),
            new HttpClient(handler),
            writeUserEnvironment: false);
    }

    private static ProfileStore CreateStore()
    {
        var profile = new ProfileDefinition
        {
            Id = "remote-test",
            Name = "测试来源",
            Codex = new ClientProfile
            {
                BaseUrl = "https://relay.example.test/v1",
                Secret = "test-secret",
            },
        };
        return new ProfileStore
        {
            Cloud = profile,
            CloudSources = [profile],
            SelectedCloudSourceId = profile.Id,
        };
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private sealed class SequenceHandler(Func<int, HttpResponseMessage> responseFactory) : HttpMessageHandler
    {
        private int _attempts;

        public int Attempts => Volatile.Read(ref _attempts);

        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            int attempt = Interlocked.Increment(ref _attempts);
            return Task.FromResult(responseFactory(attempt));
        }
    }
}
