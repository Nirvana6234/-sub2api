using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Input;
using LanAi.Workspace.Wpf.Services;
using LanAi.Workspace.Wpf.ViewModels;
using LanAi.Workspace.Wpf.Views;

namespace LanAi.Workspace.Wpf;

public partial class MainWindow : Window
{
    private const int WmGetMinMaxInfo = 0x0024;
    private const uint MonitorDefaultToNearest = 0x00000002;
    private bool _initialized;
    private int _shutdownStarted;
    private bool _shutdownComplete;
    private bool _exitRequested;
    private bool _trayNoticeShown;
    private HwndSource? _windowSource;
    private WorkspaceTrayIconService? _trayIcon;

    public MainWindow()
    {
        InitializeComponent();
        DataContext = new MainWindowViewModel();
        SourceInitialized += MainWindow_OnSourceInitialized;
        Loaded += MainWindow_OnLoaded;
        Closing += MainWindow_OnClosing;
        Closed += MainWindow_OnClosed;
        StateChanged += MainWindow_OnStateChanged;
        if (DataContext is MainWindowViewModel viewModel)
        {
            _trayIcon = new WorkspaceTrayIconService(
                OpenFromTray,
                () => OpenPageFromTray("connections"),
                () => OpenPageFromTray("gateway"),
                RequestExit);
            viewModel.Settings.SettingsChanged += Settings_OnSettingsChanged;
        }
    }

    private async void MainWindow_OnClosing(object? sender, CancelEventArgs e)
    {
        if (!_exitRequested &&
            DataContext is MainWindowViewModel shellViewModel &&
            shellViewModel.Settings.CurrentSettings.MinimizeToTray)
        {
            e.Cancel = true;
            HideToTray();
            return;
        }

        if (_shutdownComplete)
        {
            return;
        }

        e.Cancel = true;
        if (Interlocked.Exchange(ref _shutdownStarted, 1) != 0)
        {
            return;
        }

        TimeSpan shutdownLimit = TimeSpan.FromSeconds(4);
        try
        {
            Task terminalShutdown = TerminalView.ShutdownForApplicationAsync(shutdownLimit);
            Task shellShutdown = DataContext is MainWindowViewModel viewModel
                ? viewModel.ShutdownAsync(shutdownLimit)
                : Task.CompletedTask;
            await ApplicationShutdownCoordinator.RunCriticalThenBoundedAsync(
                shutdownLimit,
                shellShutdown,
                terminalShutdown);
        }
        catch
        {
            // A failed cleanup must not trap the user in the application.
        }
        finally
        {
            _shutdownComplete = true;
            if (!Dispatcher.HasShutdownStarted)
            {
                _ = Dispatcher.BeginInvoke(new Action(Close));
            }
        }
    }

    private async void MainWindow_OnLoaded(object sender, RoutedEventArgs e)
    {
        if (_initialized || DataContext is not MainWindowViewModel viewModel)
        {
            return;
        }

        _initialized = true;
        await viewModel.InitializeAsync();
        if (Environment.GetCommandLineArgs().Any(argument =>
                string.Equals(argument, "--background", StringComparison.OrdinalIgnoreCase)) &&
            viewModel.Settings.CurrentSettings.MinimizeToTray)
        {
            HideToTray();
        }
    }

    private void MainWindow_OnClosed(object? sender, EventArgs e)
    {
        if (_windowSource is not null)
        {
            _windowSource.RemoveHook(WindowMessageHook);
            _windowSource = null;
        }

        if (DataContext is MainWindowViewModel shellViewModel)
        {
            shellViewModel.Settings.SettingsChanged -= Settings_OnSettingsChanged;
        }

        if (DataContext is IDisposable disposable)
        {
            disposable.Dispose();
        }

        _trayIcon?.Dispose();
        _trayIcon = null;
    }

    private void MainWindow_OnSourceInitialized(object? sender, EventArgs e)
    {
        _windowSource = PresentationSource.FromVisual(this) as HwndSource;
        _windowSource?.AddHook(WindowMessageHook);
    }

    private IntPtr WindowMessageHook(
        IntPtr hwnd,
        int message,
        IntPtr wParam,
        IntPtr lParam,
        ref bool handled)
    {
        if (message == WmGetMinMaxInfo)
        {
            ApplyMonitorWorkArea(hwnd, lParam);
        }

        return IntPtr.Zero;
    }

    private static void ApplyMonitorWorkArea(IntPtr hwnd, IntPtr lParam)
    {
        IntPtr monitor = MonitorFromWindow(hwnd, MonitorDefaultToNearest);
        if (monitor == IntPtr.Zero)
        {
            return;
        }

        var monitorInfo = new MonitorInfo
        {
            Size = Marshal.SizeOf<MonitorInfo>(),
        };
        if (!GetMonitorInfo(monitor, ref monitorInfo))
        {
            return;
        }

        MinMaxInfo minMaxInfo = Marshal.PtrToStructure<MinMaxInfo>(lParam);
        minMaxInfo.MaxPosition = new NativePoint(
            monitorInfo.WorkArea.Left - monitorInfo.MonitorArea.Left,
            monitorInfo.WorkArea.Top - monitorInfo.MonitorArea.Top);
        minMaxInfo.MaxSize = new NativePoint(
            monitorInfo.WorkArea.Right - monitorInfo.WorkArea.Left,
            monitorInfo.WorkArea.Bottom - monitorInfo.WorkArea.Top);
        minMaxInfo.MaxTrackSize = minMaxInfo.MaxSize;
        Marshal.StructureToPtr(minMaxInfo, lParam, false);
    }

    private void TitleBar_OnMouseLeftButtonDown(object sender, MouseButtonEventArgs e)
    {
        if (e.ClickCount == 2)
        {
            ToggleMaximize();
            return;
        }

        DragMove();
    }

    private void Minimize_OnClick(object sender, RoutedEventArgs e) => WindowState = WindowState.Minimized;

    private void Maximize_OnClick(object sender, RoutedEventArgs e) => ToggleMaximize();

    private void Close_OnClick(object sender, RoutedEventArgs e) => Close();

    // The password never reaches the view model as state: it is read straight from
    // the box, passed to the sign-in call, and the box is cleared afterwards. This
    // mirrors how the gateway page handles its own password field.
    private async void SignInSubmit_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is not MainWindowViewModel viewModel)
        {
            return;
        }

        bool signedIn = await viewModel.SignInPrompt
            .SubmitAsync(SignInPasswordBox.Password, CancellationToken.None)
            .ConfigureAwait(true);
        if (signedIn)
        {
            SignInPasswordBox.Clear();
        }
    }

    private void SignInCancel_OnClick(object sender, RoutedEventArgs e) => SignInPasswordBox.Clear();

    private void SignInPasswordBox_OnKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key == Key.Enter)
        {
            e.Handled = true;
            SignInSubmit_OnClick(sender, e);
        }
    }

    private void MainWindow_OnStateChanged(object? sender, EventArgs e)
    {
        if (WindowState == WindowState.Minimized &&
            DataContext is MainWindowViewModel viewModel &&
            viewModel.Settings.CurrentSettings.MinimizeToTray)
        {
            HideToTray();
        }
    }

    private void Settings_OnSettingsChanged(object? sender, EventArgs e)
    {
        if (DataContext is MainWindowViewModel viewModel &&
            !viewModel.Settings.CurrentSettings.MinimizeToTray && !IsVisible)
        {
            OpenFromTray();
        }
    }

    private void HideToTray()
    {
        Hide();
        if (!_trayNoticeShown)
        {
            _trayNoticeShown = true;
            _trayIcon?.ShowMinimizedNotice();
        }
    }

    private void OpenFromTray()
    {
        Dispatcher.Invoke(() =>
        {
            Show();
            WindowState = WindowState.Normal;
            Activate();
            Topmost = true;
            Topmost = false;
            Focus();
        });
    }

    private void OpenPageFromTray(string pageId)
    {
        Dispatcher.Invoke(() =>
        {
            if (DataContext is MainWindowViewModel viewModel)
            {
                viewModel.NavigateFromDesktopShell(pageId);
            }

            OpenFromTray();
        });
    }

    private void RequestExit()
    {
        Dispatcher.Invoke(() =>
        {
            _exitRequested = true;
            Close();
        });
    }

    private void ToggleMaximize()
    {
        WindowState = WindowState == WindowState.Maximized
            ? WindowState.Normal
            : WindowState.Maximized;
    }

    [DllImport("user32.dll")]
    private static extern IntPtr MonitorFromWindow(IntPtr hwnd, uint flags);

    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetMonitorInfo(IntPtr monitor, ref MonitorInfo monitorInfo);

    [StructLayout(LayoutKind.Sequential)]
    private struct NativePoint(int x, int y)
    {
        public int X = x;
        public int Y = y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct NativeRect
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Auto)]
    private struct MonitorInfo
    {
        public int Size;
        public NativeRect MonitorArea;
        public NativeRect WorkArea;
        public uint Flags;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MinMaxInfo
    {
        public NativePoint Reserved;
        public NativePoint MaxSize;
        public NativePoint MaxPosition;
        public NativePoint MinTrackSize;
        public NativePoint MaxTrackSize;
    }
}
