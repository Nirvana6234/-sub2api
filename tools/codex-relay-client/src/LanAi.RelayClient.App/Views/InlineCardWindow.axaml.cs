using System.Collections.Specialized;
using System.ComponentModel;
using System.Runtime.InteropServices;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Media;
using Avalonia.Threading;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>
/// One small card beside one of the other person's messages in WeChat (docs §6).
/// </summary>
/// <remarks>
/// <b>It must never take the focus from WeChat.</b> <c>ShowActivated="False"</c> only covers the
/// first show; a click would still activate it, WeChat would drop to the background, the reader
/// would report that, and every card would hide under the user's cursor.
/// <c>WS_EX_NOACTIVATE</c> is what keeps a click from activating it at all.
/// </remarks>
public partial class InlineCardWindow : Window
{
    private const double Resting = 0.78;

    private readonly TextBlock _headline;
    private readonly TextBlock _advice;
    private readonly Border _stripe;
    private readonly Border _card;
    private readonly MenuItem _right;
    private readonly MenuItem _wrong;
    private WeChatIntentViewModel? _owner;
    private InlineCardViewModel? _current;

    public InlineCardWindow()
    {
        AvaloniaXamlLoader.Load(this);
        _headline = this.FindControl<TextBlock>("Headline")!;
        _advice = this.FindControl<TextBlock>("Advice")!;
        _stripe = this.FindControl<Border>("Stripe")!;
        _card = this.FindControl<Border>("Card")!;
        _right = this.FindControl<MenuItem>("RightItem")!;
        _wrong = this.FindControl<MenuItem>("WrongItem")!;

        // See-through at rest so the conversation under it stays readable; solid when pointed at.
        PointerEntered += (_, _) => Opacity = 1.0;
        PointerExited += (_, _) => Opacity = Resting;
    }

    internal void Present(WeChatIntentViewModel owner, InlineCardViewModel card)
    {
        _owner = owner;
        _current = card;
        _headline.Text = card.Headline;
        _advice.Text = card.Advice;
        _advice.IsVisible = card.Advice.Length > 0;
        _stripe.Background = new SolidColorBrush(Color.Parse(card.Stripe));
        _right.IsEnabled = _wrong.IsEnabled = !card.IsPending && !card.FeedbackGiven;
        ToolTip.SetTip(_card, card.Detail.Length > 0 ? card.Detail : null);
        Position = new PixelPoint(card.ScreenX, card.ScreenY);
    }

    protected override void OnOpened(EventArgs e)
    {
        base.OnOpened(e);
        NonActivating.Apply(this);
    }

    private void Right_OnClick(object? sender, RoutedEventArgs e) => Rate(correct: true);

    private void Wrong_OnClick(object? sender, RoutedEventArgs e) => Rate(correct: false);

    private void Rate(bool correct)
    {
        if (_owner is not null && _current is not null)
        {
            _owner.GiveFeedback(_current, correct);
        }
    }

    private void Mute_OnClick(object? sender, RoutedEventArgs e) => _owner?.MuteCurrentChat();
}

/// <summary>
/// Keeps one <see cref="InlineCardWindow"/> per entry of <see cref="IntentOverlayViewModel.InlineCards"/>,
/// pooled: the list is rebuilt on every settled screen, and opening windows at that rate would flicker.
/// </summary>
internal sealed class InlineCardHost
{
    private readonly WeChatIntentViewModel _owner;
    private readonly IntentOverlayViewModel _overlay;
    private readonly List<InlineCardWindow> _pool = [];
    private bool _syncQueued;

    public InlineCardHost(WeChatIntentViewModel owner)
    {
        _owner = owner ?? throw new ArgumentNullException(nameof(owner));
        _overlay = owner.Overlay;
        _overlay.InlineCards.CollectionChanged += OnCardsChanged;
        _overlay.PropertyChanged += OnOverlayChanged;
    }

    private void OnCardsChanged(object? sender, NotifyCollectionChangedEventArgs e) => QueueSync();

    private void OnOverlayChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName == nameof(IntentOverlayViewModel.IsVisible))
        {
            QueueSync();
        }
    }

    /// <summary>The list is cleared and refilled in one go; show the result once, not every step.</summary>
    private void QueueSync()
    {
        if (_syncQueued)
        {
            return;
        }

        _syncQueued = true;
        Dispatcher.UIThread.Post(Sync, DispatcherPriority.Background);
    }

    private void Sync()
    {
        _syncQueued = false;
        int shown = _overlay.IsVisible ? _overlay.InlineCards.Count : 0;
        for (int i = 0; i < shown; i++)
        {
            if (i == _pool.Count)
            {
                _pool.Add(new InlineCardWindow());
            }

            _pool[i].Present(_owner, _overlay.InlineCards[i]);
            if (!_pool[i].IsVisible)
            {
                _pool[i].Show();
            }
        }

        for (int i = shown; i < _pool.Count; i++)
        {
            if (_pool[i].IsVisible)
            {
                _pool[i].Hide();
            }
        }
    }
}

/// <summary><c>WS_EX_NOACTIVATE</c>: a click on the window does not take the focus from WeChat.</summary>
internal static class NonActivating
{
    public static void Apply(Window window)
    {
        if (!OperatingSystem.IsWindows() || window.TryGetPlatformHandle() is not { } handle)
        {
            return;
        }

        const int GwlExStyle = -20;
        const long WsExNoActivate = 0x08000000;
        const long WsExToolWindow = 0x00000080;
        long style = GetWindowLongPtr(handle.Handle, GwlExStyle).ToInt64();
        SetWindowLongPtr(handle.Handle, GwlExStyle, new IntPtr(style | WsExNoActivate | WsExToolWindow));
    }

    [DllImport("user32.dll", EntryPoint = "GetWindowLongPtrW")]
    private static extern IntPtr GetWindowLongPtr(IntPtr hwnd, int index);

    [DllImport("user32.dll", EntryPoint = "SetWindowLongPtrW")]
    private static extern IntPtr SetWindowLongPtr(IntPtr hwnd, int index, IntPtr value);
}
