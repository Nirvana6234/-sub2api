using System.Diagnostics;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>
/// Holds spawned processes so they cannot outlive this one.
/// </summary>
/// <remarks>
/// <para>
/// This is the mechanism behind "Paseo is hosted by the client and dies with it".
/// Windows reaps nothing on its own: if the client is force-killed from Task
/// Manager, an unheld daemon keeps running — and keeps its relay connection open,
/// so a phone can still drive a machine whose owner believes the app is closed.
/// That is a security outcome, not an untidy one.
/// </para>
/// <para>
/// Graceful shutdown is <b>not</b> a substitute. It was measured: force-killing
/// the top process of the daemon chain tore the rest down within six seconds, but
/// with no shutdown record at all — an incidental consequence of broken pipes,
/// not a contract, and it does not cover the real case where the UI process is
/// the node processes' grandparent.
/// </para>
/// </remarks>
public interface IProcessCage : IDisposable
{
    /// <summary>Puts a process under the cage. Its descendants are covered too.</summary>
    void Hold(Process process);
}

/// <summary>A cage that holds nothing. For platforms without one, and for tests.</summary>
/// <remarks>
/// Named for what it is. A silently absent cage would be the worst of both worlds:
/// the code reads as protected while orphaned daemons survive.
/// </remarks>
public sealed class NullProcessCage : IProcessCage
{
    public void Hold(Process process)
    {
        // Intentionally nothing.
    }

    public void Dispose()
    {
        // Intentionally nothing.
    }
}
