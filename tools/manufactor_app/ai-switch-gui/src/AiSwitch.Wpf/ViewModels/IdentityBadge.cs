using LanAi.Workspace.Wpf.Services;

namespace LanAi.Workspace.Wpf.ViewModels;

/// <summary>
/// Wording for the sidebar identity badge.
/// </summary>
/// <remarks>
/// Kept as pure functions so the labels can be unit tested without constructing
/// the main window view model, which owns real services. The machine-local
/// control session gets its own label because it is not a cloud account sign-in
/// and must never read as one.
/// </remarks>
internal static class IdentityBadge
{
    public static string DisplayName(Sub2ApiSessionState session)
    {
        ArgumentNullException.ThrowIfNull(session);
        if (!session.IsAuthenticated)
        {
            return "未登录";
        }

        if (session.IsLocalControl)
        {
            return "本机 · 管理员";
        }

        return string.IsNullOrWhiteSpace(session.Username) ? session.RoleLabel : session.Username;
    }

    public static string StatusLabel(Sub2ApiSessionState session)
    {
        ArgumentNullException.ThrowIfNull(session);
        if (!session.IsAuthenticated)
        {
            return "点击登录";
        }

        return session.IsLocalControl ? "本机工作区" : session.RoleLabel;
    }

    public static string Initial(Sub2ApiSessionState session)
    {
        ArgumentNullException.ThrowIfNull(session);
        if (!session.IsAuthenticated)
        {
            return "?";
        }

        if (session.IsLocalControl)
        {
            return "本";
        }

        string name = session.Username;
        return string.IsNullOrWhiteSpace(name) ? "共" : name[..1].ToUpperInvariant();
    }
}
