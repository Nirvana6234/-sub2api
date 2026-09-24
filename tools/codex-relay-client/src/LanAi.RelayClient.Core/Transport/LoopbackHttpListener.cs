using System.Net;

namespace LanAi.RelayClient.Transport;

/// <summary>Starts an <see cref="HttpListener"/> on a free loopback port.</summary>
/// <remarks>
/// <para>
/// A port a probe socket found free can be taken before the listener registers it —
/// by another process, or by another <see cref="HttpListener"/> in this one, which a
/// probe does not even see: http.sys holds those registrations, not a user-mode
/// socket. So starting is the test, and a collision is retried on a fresh port.
/// </para>
/// <para>
/// The preferred port is still probed first: a plain socket holding it does not stop
/// http.sys from registering the prefix, only from ever receiving a request.
/// </para>
/// <para>
/// A new instance each attempt: on Windows a failed <see cref="HttpListener.Start"/>
/// closes the listener for good.
/// </para>
/// </remarks>
internal static class LoopbackHttpListener
{
    internal const int Attempts = 8;

    /// <summary>Listening on <c>http://127.0.0.1:{port}/</c>; <paramref name="preferredPort"/> first when given.</summary>
    /// <exception cref="HttpListenerException">No attempt could be started.</exception>
    public static HttpListener Start(int? preferredPort, out int port)
    {
        HttpListenerException? last = null;
        for (int attempt = 0; attempt < Attempts; attempt++)
        {
            int candidate = attempt == 0 && preferredPort is int preferred && IsFree(preferred) ? preferred : ProbeFreePort();
            var listener = new HttpListener();
            listener.Prefixes.Add($"http://127.0.0.1:{candidate}/");
            try
            {
                listener.Start();
                port = candidate;
                return listener;
            }
            catch (HttpListenerException ex)
            {
                listener.Close();
                last = ex;
            }
        }

        throw last!;
    }

    private static bool IsFree(int port)
    {
        try
        {
            using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, port);
            probe.Start();
            return true;
        }
        catch (System.Net.Sockets.SocketException)
        {
            return false;
        }
    }

    /// <summary>A port the OS just handed out; only a candidate, see the remarks above.</summary>
    internal static int ProbeFreePort()
    {
        using var probe = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
        probe.Start();
        return ((IPEndPoint)probe.LocalEndpoint).Port;
    }
}
