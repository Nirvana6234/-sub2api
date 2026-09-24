using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;

namespace LanAi.RelayClient.ViewModels;

/// <summary>The pages of the signed-in surface, in the order the left rail lists them.</summary>
public enum ClientPage
{
    Overview,
    Codex,
    Claude,
    LocalProxy,
    DesktopSync,
    Account,
    Settings,
}

/// <summary>One entry in the left rail.</summary>
public sealed partial class NavItemViewModel : ObservableObject
{
    internal NavItemViewModel(ClientPage page, string title, string glyph)
    {
        Page = page;
        Title = title;
        Glyph = glyph;
    }

    public ClientPage Page { get; }

    public string Title { get; }

    /// <summary>A single character drawn in front of the title.</summary>
    public string Glyph { get; }

    /// <summary>Something on this page wants the user's attention.</summary>
    [ObservableProperty]
    private bool hasBadge;
}

/// <summary>Which page of the signed-in surface is showing.</summary>
/// <remarks>
/// <para>
/// Settings is kept apart from <see cref="Items"/> because the rail draws it pinned to
/// the bottom, below the user's name, rather than in the list. It is still a page like
/// any other: <see cref="Selected"/> may be it, and the list then simply shows nothing
/// highlighted.
/// </para>
/// <para>
/// Deliberately knows nothing about what the pages contain. Views are built by the
/// head, once, and swapped by <see cref="ClientPage"/> — never looked up by name, which
/// the trimmed build would not survive.
/// </para>
/// </remarks>
public sealed partial class NavigationViewModel : ObservableObject
{
    public NavigationViewModel()
    {
        Items =
        [
            new NavItemViewModel(ClientPage.Overview, "仪表盘", "▦"),
            new NavItemViewModel(ClientPage.Codex, "Codex", "◉"),
            new NavItemViewModel(ClientPage.Claude, "Claude", "✱"),
            new NavItemViewModel(ClientPage.LocalProxy, "本地代理", "⇄"),
            new NavItemViewModel(ClientPage.DesktopSync, "同步会话", "⇅"),
            new NavItemViewModel(ClientPage.Account, "账户", "◎"),
        ];
        SettingsItem = new NavItemViewModel(ClientPage.Settings, "设置", "⚙");
        Selected = Items[0];
    }

    /// <summary>The pages listed in the rail, top to bottom.</summary>
    public ObservableCollection<NavItemViewModel> Items { get; }

    /// <summary>The page pinned to the bottom of the rail.</summary>
    public NavItemViewModel SettingsItem { get; }

    /// <summary>The page showing. Never null.</summary>
    /// <remarks>
    /// Not bound two-way to the rail's list: the list cannot hold
    /// <see cref="SettingsItem"/> and would write null back, so the view mirrors it by
    /// hand instead.
    /// </remarks>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CurrentPage))]
    [NotifyPropertyChangedFor(nameof(Title))]
    [NotifyPropertyChangedFor(nameof(IsSettingsSelected))]
    private NavItemViewModel selected = null!;

    public ClientPage CurrentPage => Selected.Page;

    public string Title => Selected.Title;

    public bool IsSettingsSelected => Selected == SettingsItem;

    public NavItemViewModel Item(ClientPage page) =>
        page == ClientPage.Settings ? SettingsItem : Items.First(i => i.Page == page);

    public void Navigate(ClientPage page) => Selected = Item(page);
}
