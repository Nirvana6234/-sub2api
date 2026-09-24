namespace AiSwitchGui;

internal static class Program
{
    private const string SingleInstanceMutexName = @"Local\LocalGatewayManager.SingleInstance";
    public const string ActivateEventName = @"Local\LocalGatewayManager.Activate";

    [STAThread]
    private static void Main()
    {
        using var mutex = new Mutex(initiallyOwned: true, SingleInstanceMutexName, out var isFirstInstance);
        if (!isFirstInstance)
        {
            SignalExistingInstance();
            return;
        }

        ApplicationConfiguration.Initialize();
        Application.Run(new MainForm());
    }

    private static void SignalExistingInstance()
    {
        try
        {
            using var activateEvent = EventWaitHandle.OpenExisting(ActivateEventName);
            activateEvent.Set();
        }
        catch
        {
            // If the first instance is still starting, there may be no event yet.
            // The mutex still prevents launching duplicate UI windows.
        }
    }
}
