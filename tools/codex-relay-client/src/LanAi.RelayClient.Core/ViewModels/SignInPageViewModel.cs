namespace LanAi.RelayClient.ViewModels;

/// <summary>What the sign-in screen binds to: the form plus the version banner.</summary>
/// <remarks>
/// <para>
/// A composition, not new behaviour. The WPF window reached the update banner with
/// <c>{Binding ClientUpdate.HasUpdate, ElementName=RootWindow}</c> — a workaround for
/// having three panels with three different data contexts inside one window. Avalonia
/// can express the same thing, but under compiled bindings the annotation is only as
/// good as the runtime assignment behind it, and <c>ElementName</c> hops are exactly
/// where that goes wrong quietly.
/// </para>
/// <para>
/// Giving the screen one root object instead means the view's <c>x:DataType</c> is
/// checked against what the constructor actually receives, so a mismatch is a compile
/// error rather than a blank strip where the version used to be.
/// </para>
/// </remarks>
public sealed class SignInPageViewModel
{
    internal SignInPageViewModel(SignInViewModel signIn, ClientUpdateViewModel clientUpdate)
    {
        SignIn = signIn ?? throw new ArgumentNullException(nameof(signIn));
        ClientUpdate = clientUpdate ?? throw new ArgumentNullException(nameof(clientUpdate));
    }

    public SignInViewModel SignIn { get; }

    public ClientUpdateViewModel ClientUpdate { get; }
}
