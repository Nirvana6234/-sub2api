using System.Reflection;
using LanAi.Workspace.Wpf.ViewModels;
using Xunit;

namespace AiSwitch.Wpf.Tests;

/// <summary>
/// WPF data binding can only reach public members. A binding that fails resolves
/// to no value at all, and for <c>Visibility</c> that silently leaves the element
/// at its default (<c>Visible</c>) — which once pinned the sign-in card on screen
/// permanently. These tests guard the members MainWindow.xaml binds to.
/// </summary>
public sealed class IdentityBindingSurfaceTests
{
    [Theory]
    [InlineData(nameof(MainWindowViewModel.SignInPrompt))]
    [InlineData("IdentityDisplayName")]
    [InlineData("IdentityStatusLabel")]
    [InlineData("IdentityInitial")]
    [InlineData("IsIdentitySignedIn")]
    [InlineData("OpenIdentityCommand")]
    public void MainWindowBoundMembersArePublic(string memberName)
    {
        PropertyInfo? property = typeof(MainWindowViewModel).GetProperty(
            memberName,
            BindingFlags.Public | BindingFlags.Instance);

        Assert.NotNull(property);
        Assert.True(
            property!.GetMethod?.IsPublic,
            $"{memberName} must expose a public getter or WPF cannot bind to it.");
    }

    [Fact]
    public void SignInPromptTypeIsPublicSoBindingsResolve()
    {
        Assert.True(
            typeof(SignInPromptViewModel).IsPublic,
            "SignInPromptViewModel must be public; an internal type makes every nested binding fail.");
    }

    [Theory]
    [InlineData("IsVisible")]
    [InlineData("Email")]
    [InlineData("ErrorMessage")]
    [InlineData("HasError")]
    [InlineData("CanSubmit")]
    [InlineData("CancelCommand")]
    public void SignInPromptBoundMembersArePublic(string memberName)
    {
        PropertyInfo? property = typeof(SignInPromptViewModel).GetProperty(
            memberName,
            BindingFlags.Public | BindingFlags.Instance);

        Assert.NotNull(property);
        Assert.True(property!.GetMethod?.IsPublic, $"{memberName} must be publicly readable.");
    }

    [Theory]
    [InlineData("Username")]
    [InlineData("Email")]
    [InlineData("RoleLabel")]
    [InlineData("BalanceText")]
    [InlineData("FrozenBalanceText")]
    [InlineData("SourceText")]
    [InlineData("SessionKindText")]
    [InlineData("IsSignedIn")]
    [InlineData("SignOutCommand")]
    public void ProfileBoundMembersArePublic(string memberName)
    {
        PropertyInfo? property = typeof(ProfileViewModel).GetProperty(
            memberName,
            BindingFlags.Public | BindingFlags.Instance);

        Assert.NotNull(property);
        Assert.True(property!.GetMethod?.IsPublic, $"{memberName} must be publicly readable.");
    }
}
