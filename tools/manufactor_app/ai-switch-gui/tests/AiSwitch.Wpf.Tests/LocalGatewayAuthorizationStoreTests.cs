using LanAi.Workspace.Wpf.Services;

namespace AiSwitch.Wpf.Tests;

public sealed class LocalGatewayAuthorizationStoreTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("admin-key\r\nmalicious")]
    public void TryNormalizeAdministratorApiKey_RejectsEmptyAndControlCharacters(string? input)
    {
        Assert.False(LocalGatewayAuthorizationStore.TryNormalizeAdministratorApiKey(input, out string? normalized));
        Assert.Null(normalized);
    }

    [Fact]
    public void TryNormalizeAdministratorApiKey_TrimsButNeverRendersTheValueThroughToString()
    {
        const string secret = "administrator-key-that-must-not-render";

        Assert.True(LocalGatewayAuthorizationStore.TryNormalizeAdministratorApiKey($"  {secret}  ", out string? normalized));
        LocalGatewayAuthorization authorization = LocalGatewayAuthorization.Available(
            normalized!,
            LocalGatewayAuthorizationSource.WindowsCredentialManager);

        Assert.Equal(secret, authorization.AdministratorApiKey);
        Assert.DoesNotContain(secret, authorization.ToString(), StringComparison.Ordinal);
    }
}
