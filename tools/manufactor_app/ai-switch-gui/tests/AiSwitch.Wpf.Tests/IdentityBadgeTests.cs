using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;
using Xunit;

namespace AiSwitch.Wpf.Tests;

public sealed class IdentityBadgeTests
{
    private static Sub2ApiSessionState SignedIn(
        string username,
        bool isLocalControl = false,
        bool isAdministrator = false)
        => new(
            true,
            false,
            isAdministrator,
            isAdministrator ? "管理员" : "普通用户",
            0m,
            0m,
            DateTimeOffset.UtcNow.AddMinutes(30),
            new Uri("http://127.0.0.1:8080/"),
            "已登录")
        {
            Username = username,
            IsLocalControl = isLocalControl,
        };

    [Fact]
    public void SignedOutStateInvitesTheUserToSignIn()
    {
        Sub2ApiSessionState session = Sub2ApiSessionState.SignedOut;

        Assert.Equal("未登录", IdentityBadge.DisplayName(session));
        Assert.Equal("点击登录", IdentityBadge.StatusLabel(session));
        Assert.Equal("?", IdentityBadge.Initial(session));
    }

    [Fact]
    public void AccountSignInShowsTheUsername()
    {
        Sub2ApiSessionState session = SignedIn("zhoubo");

        Assert.Equal("zhoubo", IdentityBadge.DisplayName(session));
        Assert.Equal("普通用户", IdentityBadge.StatusLabel(session));
        Assert.Equal("Z", IdentityBadge.Initial(session));
    }

    [Fact]
    public void LocalControlSessionIsNeverShownAsACloudAccount()
    {
        // The startup local-control login returns the first administrator's real
        // user record, so the username alone cannot distinguish it.
        Sub2ApiSessionState session = SignedIn("admin", isLocalControl: true, isAdministrator: true);

        Assert.Equal("本机 · 管理员", IdentityBadge.DisplayName(session));
        Assert.Equal("本机工作区", IdentityBadge.StatusLabel(session));
        Assert.Equal("本", IdentityBadge.Initial(session));
    }

    [Fact]
    public void MissingUsernameFallsBackToTheRoleLabel()
    {
        Sub2ApiSessionState session = SignedIn("   ");

        Assert.Equal("普通用户", IdentityBadge.DisplayName(session));
        Assert.Equal("共", IdentityBadge.Initial(session));
    }
}
