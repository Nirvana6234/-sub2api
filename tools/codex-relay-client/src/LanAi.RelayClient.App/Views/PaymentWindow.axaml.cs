using System.ComponentModel;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Media.Imaging;
using Avalonia.Threading;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>The recharge screen.</summary>
/// <remarks>
/// <para>
/// A window rather than a surface in the shell, matching the WPF original: it is modal
/// over the dashboard, and an order with a live countdown should not be something the
/// user can navigate away from by accident.
/// </para>
/// <para>
/// The countdown lives here rather than in the view model — as it did in WPF — because
/// the view model only recomputes remaining seconds from a timestamp when asked. That
/// keeps <see cref="PaymentViewModel"/> free of any timer, which is why it needed no
/// changes to move to the shared project beyond the QR code type.
/// </para>
/// </remarks>
public partial class PaymentWindow : Window
{
    private readonly PaymentViewModel? _viewModel;
    private readonly DispatcherTimer? _countdownTimer;
    private bool _allowClose;

    /// <summary>Design-time constructor. Not used at runtime.</summary>
    public PaymentWindow()
    {
        InitializeComponent();
    }

    internal PaymentWindow(PaymentViewModel viewModel)
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));

        InitializeComponent();
        DataContext = _viewModel;

        _countdownTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _countdownTimer.Tick += CountdownTimer_OnTick;
        _viewModel.PropertyChanged += ViewModel_OnPropertyChanged;
        _viewModel.PaymentCompleted += ViewModel_OnPaymentCompleted;

        Opened += Window_OnOpened;
        Closing += Window_OnClosing;
        Closed += Window_OnClosed;
    }

    /// <summary>True once the server confirmed payment, so the caller can refresh.</summary>
    internal bool IsCompleted => _viewModel?.IsCompleted ?? false;

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private async void Window_OnOpened(object? sender, EventArgs e)
    {
        if (_viewModel is not null)
        {
            await _viewModel.LoadAsync();
        }
    }

    private void ViewModel_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (_viewModel is null)
        {
            return;
        }

        // The view model exposes PNG bytes rather than a framework image, which is what
        // let it move to the shared project. This is Avalonia's decode; WPF has its own.
        if (e.PropertyName == nameof(PaymentViewModel.QrCodePng))
        {
            this.FindControl<Image>("QrCodeImage")!.Source = DecodeQrCode(_viewModel.QrCodePng);
        }

        if (e.PropertyName == nameof(PaymentViewModel.IsOrderActive))
        {
            if (_viewModel.IsOrderActive)
            {
                _countdownTimer!.Start();
            }
            else
            {
                _countdownTimer!.Stop();
            }
        }
    }

    private static Bitmap? DecodeQrCode(byte[]? png)
    {
        if (png is null || png.Length == 0)
        {
            return null;
        }

        using var stream = new MemoryStream(png);
        return new Bitmap(stream);
    }

    private void CountdownTimer_OnTick(object? sender, EventArgs e)
    {
        _viewModel!.UpdateCountdown(DateTimeOffset.UtcNow);
        if (_viewModel.SecondsRemaining <= 0)
        {
            _countdownTimer!.Stop();
        }
    }

    private void PresetAmount_OnClick(object? sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: decimal amount })
        {
            _viewModel?.SetPresetAmount(amount);
        }
    }

    private async void PaymentAction_OnClick(object? sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: string paymentType } &&
            !string.IsNullOrWhiteSpace(paymentType) &&
            _viewModel is not null)
        {
            await _viewModel.CreateOrderAsync(paymentType);
        }
    }

    private async void CancelOrder_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_viewModel is not null)
        {
            await _viewModel.CancelOrderAsync();
        }
    }

    private void Close_OnClick(object? sender, RoutedEventArgs e) => Close();

    private async void ViewModel_OnPaymentCompleted(object? sender, EventArgs e)
    {
        await NoticeDialog.ShowNoticeAsync(this, "充值完成，账户余额即将刷新。");
        _allowClose = true;
        Close();
    }

    /// <remarks>
    /// Closing with an order still live cancels it first. Leaving it open would let the
    /// user pay against an order the client has forgotten about — the money leaves their
    /// wallet and nothing on screen ever acknowledges it.
    /// </remarks>
    private void Window_OnClosing(object? sender, WindowClosingEventArgs e)
    {
        if (_allowClose || _viewModel is null || !_viewModel.IsOrderActive)
        {
            return;
        }

        e.Cancel = true;
        _ = CancelAndCloseAsync();
    }

    private async Task CancelAndCloseAsync()
    {
        await _viewModel!.CancelOrderAsync();
        _allowClose = true;
        Close();
    }

    private void Window_OnClosed(object? sender, EventArgs e)
    {
        _countdownTimer?.Stop();
        _viewModel?.Dispose();
    }
}
