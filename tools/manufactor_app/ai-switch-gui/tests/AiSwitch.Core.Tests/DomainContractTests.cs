namespace LanAi.Workspace.Core.Tests;

public sealed class DomainContractTests
{
    [Fact]
    public void ConnectionProfile_StoresCredentialReferenceInsteadOfSecret()
    {
        var profile = new ConnectionProfile
        {
            Id = "lan",
            Name = "局域网中转",
            Kind = ConnectionProfileKind.Lan,
            BaseUrl = "http://192.168.1.10:8080",
            ApiKeyCredentialId = "credential/lan",
        };

        Assert.Equal("credential/lan", profile.ApiKeyCredentialId);
        Assert.DoesNotContain(
            typeof(ConnectionProfile).GetProperties(),
            property => string.Equals(property.Name, "ApiKey", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public void ProjectAndConversation_CanRetainIndependentConnectionPolicies()
    {
        var project = new ProjectRecord
        {
            Id = "project-id",
            DisplayName = "demo",
            RootPath = "C:\\demo",
            PathFingerprint = "fingerprint",
            DefaultConnectionProfileId = "current-profile",
            ResumePolicy = ResumePolicy.CurrentConnection,
        };
        var conversation = new ConversationRecord
        {
            Id = "conversation-id",
            ProjectId = project.Id,
            NativeClient = CliKind.Codex,
            NativeSessionId = "native-id",
            OriginalWorkingDirectory = project.RootPath,
            SourceProfileIdAtStart = "original-profile",
            ResumePolicy = ResumePolicy.PinnedConnection,
        };

        Assert.Equal(ResumePolicy.CurrentConnection, project.ResumePolicy);
        Assert.Equal(ResumePolicy.PinnedConnection, conversation.ResumePolicy);
        Assert.Equal("original-profile", conversation.SourceProfileIdAtStart);
    }

    [Fact]
    public void CliLaunchRequest_CarriesNoPlaintextCredential()
    {
        var request = new CliLaunchRequest
        {
            ProjectId = "project-id",
            Cli = CliKind.ClaudeCode,
            WorkingDirectory = "C:\\demo",
            ConnectionProfileId = "lan-profile",
        };

        Assert.Equal("lan-profile", request.ConnectionProfileId);
        Assert.DoesNotContain(
            typeof(CliLaunchRequest).GetProperties(),
            property => property.Name.Contains("ApiKey", StringComparison.OrdinalIgnoreCase));
    }

    [Theory]
    [InlineData(CliKind.Codex, "https://relay.example", "https://relay.example/v1")]
    [InlineData(CliKind.Codex, "https://relay.example/v1/responses", "https://relay.example/v1")]
    [InlineData(CliKind.GrokCli, "https://relay.example/models", "https://relay.example/v1")]
    [InlineData(CliKind.ClaudeCode, "https://relay.example/v1", "https://relay.example")]
    [InlineData(CliKind.ClaudeCode, "https://relay.example/api/v1/messages", "https://relay.example/api")]
    [InlineData(CliKind.GeminiCli, "https://relay.example/v1beta/models", "https://relay.example")]
    [InlineData(CliKind.GeminiCli, "https://relay.example/api/v1", "https://relay.example/api")]
    public void ConnectionEndpointNormalizer_AcceptsRootVersionAndEndpointUrls(
        CliKind client,
        string input,
        string expected)
    {
        Assert.Equal(expected, ConnectionEndpointNormalizer.Normalize(client, input));
    }
}
