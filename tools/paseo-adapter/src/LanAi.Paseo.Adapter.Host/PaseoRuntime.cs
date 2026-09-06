using System.Security.Cryptography;
using System.Text.Json;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>
/// One call to get a working adapter: private daemon, bridge, secured pipe, and a
/// connected <see cref="PaseoAdapterClient"/> — all caged so they die with this
/// process.
/// </summary>
/// <remarks>
/// <para>
/// This is the piece that makes "hosted by the client, under the client's
/// control" real. Consumers get a client and a lifecycle; they do not get a
/// process tree to manage, and they cannot accidentally leave one behind.
/// </para>
/// <para>
/// Daemon and bridge are <b>siblings</b>, not parent and child. Their failure
/// domains differ: a bridge crash must not restart a daemon that has live agent
/// turns, and a daemon restart must not invalidate the pipe the consumer is
/// holding. The cage holds both.
/// </para>
/// </remarks>
public sealed class PaseoRuntime : IAsyncDisposable
{
    private readonly PaseoRuntimeOptions _options;
    private readonly IProcessCage _cage;
    private readonly IProcessRunner _runner;
    private readonly DaemonSupervisor _supervisor;
    private IHostedProcess? _bridge;
    private PaseoAdapterClient? _client;
    private bool _disposed;

    private PaseoRuntime(
        PaseoRuntimeOptions options,
        IProcessCage cage,
        IProcessRunner runner,
        DaemonSupervisor supervisor)
    {
        _options = options;
        _cage = cage;
        _runner = runner;
        _supervisor = supervisor;
    }

    /// <summary>Daemon lifecycle, for consumers that want to show or drive it.</summary>
    public DaemonSupervisor Supervisor => _supervisor;

    /// <summary>The connected client. Valid until <see cref="DisposeAsync"/>.</summary>
    public PaseoAdapterClient Client =>
        _client ?? throw new InvalidOperationException("The runtime has not been started");

    /// <summary>Starts everything and returns once the adapter is usable.</summary>
    public static async Task<PaseoRuntime> StartAsync(
        PaseoRuntimeOptions options,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(options);

        var cage = ProcessCage.Create();
        var runner = new ProcessRunner(cage);
        var supervisor = new DaemonSupervisor(options, runner, new HttpDaemonHealthProbe());
        var runtime = new PaseoRuntime(options, cage, runner, supervisor);
        try
        {
            await runtime.StartInternalAsync(cancellationToken).ConfigureAwait(false);
            return runtime;
        }
        catch
        {
            await runtime.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }

    private async Task StartInternalAsync(CancellationToken cancellationToken)
    {
        await _supervisor.StartAsync(cancellationToken).ConfigureAwait(false);

        // The consumer owns the pipe and secures it; the bridge dials in. Doing it
        // the other way round would mean trusting a Node-created pipe's default
        // ACL, which cannot be narrowed to the current user the same way.
        var pipeName = $"lanai-paseo-{Guid.NewGuid():N}";
        var token = Convert.ToHexString(RandomNumberGenerator.GetBytes(24));
        var channel = new NamedPipeChannel(pipeName);
        var client = new PaseoAdapterClient(channel);

        var environment = new Dictionary<string, string>
        {
            ["COFLY_PIPE"] = channel.PipePath,
            ["COFLY_TOKEN"] = token,
            ["COFLY_DAEMON_URL"] = $"ws://127.0.0.1:{_supervisor.Port}/ws",
            ["COFLY_DAEMON_PASSWORD"] = _supervisor.Password,
            // The consent record. It reaches the bridge only through this
            // variable, which is why no consumer can name an unregistered
            // directory: the map is not on the contract.
            ["COFLY_WORKDIRS"] = SerializeWorkdirs(_options.Workdirs),
        };

        if (_options.AllowRelayOperations)
        {
            // Absent unless granted: the bridge refuses relay.* without it, so a
            // consumer cannot turn remote access on unless this host — the one
            // that can ask the user — decided it may.
            environment["COFLY_ALLOW_RELAY"] = "1";
        }

        _bridge = _runner.Start(_options.NodeExecutablePath, [_options.BridgeEntryPath], environment);
        _client = client;

        await client.ConnectAsync(token, cancellationToken).ConfigureAwait(false);
    }

    private static string SerializeWorkdirs(IReadOnlyList<WorkdirRegistration> workdirs)
    {
        var buffer = new System.Buffers.ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartArray();
            foreach (var workdir in workdirs)
            {
                writer.WriteStartObject();
                writer.WriteString("key", workdir.Key);
                writer.WriteString("path", workdir.Path);
                writer.WriteString("label", workdir.Label);
                writer.WriteEndObject();
            }

            writer.WriteEndArray();
        }

        return System.Text.Encoding.UTF8.GetString(buffer.WrittenSpan);
    }

    /// <summary>
    /// Ordered shutdown: client, then bridge, then daemon, then the cage.
    /// </summary>
    /// <remarks>
    /// The order is the contract. A consumer that restores the user's
    /// <c>~/.codex</c> on exit must do it <b>after</b> this returns, or a still-live
    /// codex process reads a half-restored configuration. The cage is disposed last
    /// and only ever kills what the ordered path failed to stop.
    /// </remarks>
    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;

        if (_client is not null)
        {
            await _client.DisposeAsync().ConfigureAwait(false);
        }

        if (_bridge is not null)
        {
            // The bridge exits on its own when the pipe closes; kill is the
            // backstop for one that did not notice.
            _bridge.Kill();
            _bridge.Dispose();
        }

        await _supervisor.DisposeAsync().ConfigureAwait(false);
        _cage.Dispose();
    }
}
