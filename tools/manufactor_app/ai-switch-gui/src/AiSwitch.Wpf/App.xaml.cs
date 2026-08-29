using System.Windows;

namespace LanAi.Workspace.Wpf;

public partial class App : Application
{
    private const string SingleInstanceMutexName = @"Local\LanAi.Workspace.SingleInstance";
    private Mutex? _singleInstanceMutex;

    protected override void OnStartup(StartupEventArgs e)
    {
        _singleInstanceMutex = new Mutex(
            initiallyOwned: true,
            SingleInstanceMutexName,
            out bool isFirstInstance);
        if (!isFirstInstance)
        {
            MessageBox.Show(
                "共飞AI工作台已经在运行。请从任务栏或系统托盘打开现有窗口。",
                "共飞AI工作台",
                MessageBoxButton.OK,
                MessageBoxImage.Information);
            _singleInstanceMutex.Dispose();
            _singleInstanceMutex = null;
            Shutdown();
            return;
        }

        base.OnStartup(e);
    }

    protected override void OnExit(ExitEventArgs e)
    {
        try
        {
            _singleInstanceMutex?.ReleaseMutex();
        }
        catch (ApplicationException)
        {
        }
        finally
        {
            _singleInstanceMutex?.Dispose();
            _singleInstanceMutex = null;
        }

        base.OnExit(e);
    }
}
