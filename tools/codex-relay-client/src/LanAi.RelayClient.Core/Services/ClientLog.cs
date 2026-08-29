using System.Globalization;
using System.IO;
using System.Text;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>How much detail a log line carries.</summary>
internal enum LogLevel
{
    Info,
    Warning,
    Error,
}

/// <summary>
/// A small rolling log the user can hand back when something goes wrong.
/// </summary>
/// <remarks>
/// <para>
/// Deliberately not a logging framework. This client ships to people who cannot
/// read a stack trace, cannot run a debugger, and will describe every failure as
/// "打不开" — so the one thing that matters is that a file exists afterwards, in a
/// place they can be talked through finding. A dependency-free writer also keeps
/// the installer small, which the requirements care about.
/// </para>
/// <para>
/// Every operation swallows its own I/O failures. Logging is diagnostic support,
/// never a reason for the application to fail: a locked or full disk must not
/// turn a working client into a broken one.
/// </para>
/// </remarks>
internal static class ClientLog
{
    /// <summary>
    /// Size at which the current file is rolled over.
    /// </summary>
    /// <remarks>
    /// One previous file is kept. Two bounded files are enough to cover "it broke
    /// just now" while staying small enough to paste into a support conversation.
    /// </remarks>
    private const long MaxBytes = 1024 * 1024;

    private static readonly object Gate = new();

    private static string? _overridePath;

    public static string FilePath => _overridePath ?? DefaultFilePath();

    internal static string DefaultFilePath() => AppPaths.InData("logs", "client.log");

    /// <summary>Redirects the log, for tests that must not touch the user's profile.</summary>
    internal static void UseFile(string? path)
    {
        lock (Gate)
        {
            _overridePath = path;
        }
    }

    public static void Info(string message) => Write(LogLevel.Info, message, exception: null);

    public static void Warning(string message, Exception? exception = null) =>
        Write(LogLevel.Warning, message, exception);

    public static void Error(string message, Exception? exception = null) =>
        Write(LogLevel.Error, message, exception);

    private static void Write(LogLevel level, string message, Exception? exception)
    {
        var line = new StringBuilder()
            .Append(DateTimeOffset.Now.ToString("yyyy-MM-dd HH:mm:ss.fff zzz", CultureInfo.InvariantCulture))
            .Append("  ")
            .Append(level.ToString().ToUpperInvariant().PadRight(7))
            .Append(message);

        if (exception is not null)
        {
            // The full exception, not just its message: the message alone routinely
            // omits the inner exception that names the actual cause.
            line.AppendLine().Append(exception);
        }

        line.AppendLine();

        lock (Gate)
        {
            try
            {
                string path = FilePath;
                Directory.CreateDirectory(Path.GetDirectoryName(path)!);
                RollIfOversized(path);
                File.AppendAllText(path, line.ToString(), Encoding.UTF8);
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException)
            {
                // Diagnostics must never become the fault they were added to explain.
            }
        }
    }

    private static void RollIfOversized(string path)
    {
        var file = new FileInfo(path);
        if (!file.Exists || file.Length < MaxBytes)
        {
            return;
        }

        string previous = path + ".1";
        if (File.Exists(previous))
        {
            File.Delete(previous);
        }

        File.Move(path, previous);
    }
}
