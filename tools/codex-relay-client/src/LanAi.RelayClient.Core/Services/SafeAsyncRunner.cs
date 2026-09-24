namespace LanAi.RelayClient.Services;

/// <summary>Observes UI fire-and-forget work and turns failures into diagnostics.</summary>
internal sealed class SafeAsyncRunner
{
    private readonly Action<Exception> _log;
    private readonly Func<Exception, Task>? _report;

    public SafeAsyncRunner(
        Action<Exception>? log = null,
        Func<Exception, Task>? report = null)
    {
        _log = log ?? (exception => ClientLog.Error("界面异步操作失败", exception));
        _report = report;
    }

    public async Task RunAsync(
        Func<Task> operation,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(operation);

        try
        {
            await operation().ConfigureAwait(true);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception exception)
        {
            _log(exception);
            if (_report is not null)
            {
                await _report(exception).ConfigureAwait(true);
            }
        }
    }
}
