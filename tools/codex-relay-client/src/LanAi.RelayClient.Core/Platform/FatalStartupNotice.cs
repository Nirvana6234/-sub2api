using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using System.Text;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>
/// Last-resort, no-window way to tell the user startup failed.
/// </summary>
/// <remarks>
/// <para>
/// <b>Why this exists.</b> <c>App.OnFrameworkInitializationCompleted</c> claims the
/// single-instance lock, installs the crash handlers, and then builds the shell — and
/// everything up to and including the shell construction runs before any Avalonia
/// window exists to own a dialog. An exception there used to mean: logged if the
/// crash handlers had already been installed, otherwise not even that, and either way
/// the process ends with nothing on screen. To a user that is indistinguishable from
/// "双击完全无反应" — the exact report this class exists to stop being silent about.
/// </para>
/// <para>
/// Deliberately not an Avalonia window. The failure this handles may be Avalonia's own
/// platform/rendering init not having completed, so the one thing this must not depend
/// on is Avalonia working. A native message box (Windows) or <c>osascript</c> dialog
/// (macOS) needs nothing from the app that might be the thing that just failed.
/// </para>
/// </remarks>
internal static class FatalStartupNotice
{
    /// <summary>Shows a blocking, OS-native "it didn't start" dialog and returns once dismissed.</summary>
    public static void Show(Exception exception)
    {
        string message = "共飞-ChatGPT助手启动失败，未能进入主界面。\n\n"
            + $"{exception.GetType().Name}: {exception.Message}\n\n"
            + "详细信息已记录到：\n" + ClientLog.FilePath;

        try
        {
            if (OperatingSystem.IsWindows())
            {
                ShowWindows(message);
            }
            else if (OperatingSystem.IsMacOS())
            {
                ShowMacOS(message);
            }

            // No platform branch for anything else: a console/unknown target has
            // nowhere to put a dialog, and the log entry the caller already wrote is
            // the whole of what can be done there.
        }
        catch (Exception ex) when (ex is Win32Exception
            or InvalidOperationException
            or PlatformNotSupportedException
            or FileNotFoundException)
        {
            // The dialog is a courtesy on top of the log entry, not a replacement for
            // it. If even this fails, there is nothing further to fall back to.
            ClientLog.Warning("启动失败提示框未能显示", ex);
        }
    }

    [SupportedOSPlatform("windows")]
    private static void ShowWindows(string message)
    {
        const uint MB_OK = 0x00000000;
        const uint MB_ICONERROR = 0x00000010;
        const uint MB_TOPMOST = 0x00040000;
        const uint MB_SETFOREGROUND = 0x00010000;

        MessageBoxW(IntPtr.Zero, message, "共飞-ChatGPT助手", MB_OK | MB_ICONERROR | MB_TOPMOST | MB_SETFOREGROUND);
    }

    [SupportedOSPlatform("macos")]
    private static void ShowMacOS(string message)
    {
        var startInfo = new ProcessStartInfo("/usr/bin/osascript")
        {
            // ArgumentList, never a shell command line — see AppleScriptNotification
            // for why: the exception text is not operator-authored, but it can still
            // contain quotes or backslashes that must not be interpreted as script.
            UseShellExecute = false,
            CreateNoWindow = true,
        };

        startInfo.ArgumentList.Add("-e");
        startInfo.ArgumentList.Add(ComposeDialog(message));

        using Process? process = Process.Start(startInfo);

        // Blocking on purpose: the process is about to exit either way, and a dialog
        // that closes itself the instant it appears is no better than no dialog.
        process?.WaitForExit();
    }

    private static string ComposeDialog(string message)
    {
        var script = new StringBuilder();
        script.Append("display dialog \"").Append(Escape(message)).Append('"');
        script.Append(" with title \"共飞-ChatGPT助手\"");
        script.Append(" buttons {\"好\"} default button \"好\" with icon stop");
        return script.ToString();
    }

    /// <remarks>
    /// Same order and rationale as <c>AppleScriptNotification.Escape</c>: the backslash
    /// must be replaced first, or the quote-escaping pass would double-escape it.
    /// Newlines collapse to a space rather than an AppleScript <c>\n</c> escape —
    /// classic AppleScript string literals do not interpret one; it would show up as a
    /// literal backslash-n instead of a line break.
    /// </remarks>
    private static string Escape(string value) =>
        value.Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal)
            .Replace("\r", " ", StringComparison.Ordinal)
            .Replace("\n", " ", StringComparison.Ordinal);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern int MessageBoxW(IntPtr hWnd, string text, string caption, uint type);
}
