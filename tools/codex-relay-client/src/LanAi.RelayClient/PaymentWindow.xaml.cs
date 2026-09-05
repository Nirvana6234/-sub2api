using System.IO;
using System.Windows.Media.Imaging;
using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient;

public partial class PaymentWindow : Window
{
    private readonly PaymentViewModel _viewModel;
    private readonly DispatcherTimer _countdownTimer;
    private bool _allowClose;

    internal PaymentWindow(PaymentViewModel viewModel)
    {
        _viewModel = viewModel ?? throw new ArgumentNullException(nameof(viewModel));
        InitializeComponent();
        DataContext = _viewModel;

        _countdownTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _countdownTimer.Tick += CountdownTimer_OnTick;
        _viewModel.PropertyChanged += ViewModel_OnPropertyChanged;
        _viewModel.PaymentCompleted += ViewModel_OnPaymentCompleted;
        Loaded += Window_OnLoaded;
        Closed += Window_OnClosed;
    }

    private async void Window_OnLoaded(object sender, RoutedEventArgs e)
    {
        await _viewModel.LoadAsync();
    }

    private void ViewModel_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        // The view model now exposes PNG bytes rather than a BitmapSource, which is
        // what let it move to the shared project. Each head decodes for itself; this
        // is WPF's two lines.
        if (e.PropertyName == nameof(PaymentViewModel.QrCodePng))
        {
            QrCodeImage.Source = DecodeQrCode(_viewModel.QrCodePng);
        }

        if (e.PropertyName == nameof(PaymentViewModel.IsOrderActive))
        {
            if (_viewModel.IsOrderActive)
            {
                _countdownTimer.Start();
            }
            else
            {
                _countdownTimer.Stop();
            }
        }
    }

    private static BitmapImage? DecodeQrCode(byte[]? png)
    {
        if (png is null || png.Length == 0)
        {
            return null;
        }

        using var stream = new MemoryStream(png);
        var image = new BitmapImage();
        image.BeginInit();
        image.CacheOption = BitmapCacheOption.OnLoad;
        image.StreamSource = stream;
        image.EndInit();
        image.Freeze();
        return image;
    }

    private void CountdownTimer_OnTick(object? sender, EventArgs e)
    {
        _viewModel.UpdateCountdown(DateTimeOffset.UtcNow);
        if (_viewModel.SecondsRemaining <= 0)
        {
            _countdownTimer.Stop();
        }
    }

    private void PresetAmount_OnClick(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: decimal amount })
        {
            _viewModel.SetPresetAmount(amount);
        }
    }

    private async void PaymentAction_OnClick(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: string paymentType } && !string.IsNullOrWhiteSpace(paymentType))
        {
            await _viewModel.CreateOrderAsync(paymentType);
        }
    }

    private async void CancelOrder_OnClick(object sender, RoutedEventArgs e)
    {
        await _viewModel.CancelOrderAsync();
    }

    private void Close_OnClick(object sender, RoutedEventArgs e)
    {
        Close();
    }

    private void ViewModel_OnPaymentCompleted(object? sender, EventArgs e)
    {
        MessageBox.Show(
            "充值完成，账户余额即将刷新。",
            "账户充值",
            MessageBoxButton.OK,
            MessageBoxImage.Information);
        _allowClose = true;
        Close();
    }

    private void Window_OnClosing(object? sender, CancelEventArgs e)
    {
        if (_allowClose || !_viewModel.IsOrderActive)
        {
            return;
        }

        e.Cancel = true;
        _ = CancelAndCloseAsync();
    }

    private async Task CancelAndCloseAsync()
    {
        await _viewModel.CancelOrderAsync();
        _allowClose = true;
        Close();
    }

    private void Window_OnClosed(object? sender, EventArgs e)
    {
        _countdownTimer.Stop();
        _viewModel.Dispose();
    }
}
