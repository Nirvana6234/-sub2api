using System.Buffers.Binary;
using System.IO.Pipes;

namespace LanAi.RelayClient.CodexBinding.DesktopSync;

/// <summary>Finds and opens the pipes the desktop app might be serving app-tools on.</summary>
/// <remarks>A seam for tests; the only production implementation is <see cref="NamedPipeAppToolsTransport"/>.</remarks>
public interface IAppToolsTransport
{
    /// <summary>Every pipe that could be the app-tools one. Several exist and they come and go.</summary>
    IReadOnlyList<string> ListCandidates();

    Task<Stream> ConnectAsync(string pipeName, CancellationToken cancellationToken);
}

/// <summary>The desktop app's pipes on Windows.</summary>
/// <remarks>
/// The app-tools pipe is named <c>codex-browser-use-&lt;uuid&gt;</c>, but so are two to
/// seven others that answer a different protocol, and the uuid changes every time the
/// desktop app restarts. Nothing about the name identifies it; only asking does.
/// </remarks>
public sealed class NamedPipeAppToolsTransport : IAppToolsTransport
{
    internal const string Prefix = "codex-browser-use-";

    private static readonly TimeSpan ConnectTimeout = TimeSpan.FromSeconds(2);

    public IReadOnlyList<string> ListCandidates()
    {
        if (!OperatingSystem.IsWindows())
        {
            return [];
        }

        try
        {
            return Directory
                .GetFiles(@"\\.\pipe\")
                .Select(Path.GetFileName)
                .Where(name => name is not null && name.StartsWith(Prefix, StringComparison.Ordinal))
                .Select(name => name!)
                .Order(StringComparer.Ordinal)
                .ToList();
        }
        catch (IOException)
        {
            return [];
        }
    }

    public async Task<Stream> ConnectAsync(string pipeName, CancellationToken cancellationToken)
    {
        var pipe = new NamedPipeClientStream(".", pipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
        try
        {
            await pipe.ConnectAsync(ConnectTimeout, cancellationToken).ConfigureAwait(false);
            return pipe;
        }
        catch
        {
            await pipe.DisposeAsync().ConfigureAwait(false);
            throw;
        }
    }
}

/// <summary>The pipe's framing: a 4-byte little-endian length, then that many bytes of UTF-8 JSON.</summary>
public static class AppToolsFraming
{
    /// <summary>The limit the desktop app's own client enforces.</summary>
    public const int MaxFrameBytes = 8 * 1024 * 1024;

    public static async Task WriteFrameAsync(Stream stream, ReadOnlyMemory<byte> payload, CancellationToken cancellationToken)
    {
        if (payload.Length > MaxFrameBytes)
        {
            throw new InvalidDataException($"App-tools request is {payload.Length} bytes; the limit is {MaxFrameBytes}.");
        }

        byte[] header = new byte[4];
        BinaryPrimitives.WriteUInt32LittleEndian(header, (uint)payload.Length);
        await stream.WriteAsync(header, cancellationToken).ConfigureAwait(false);
        await stream.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
        await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    public static async Task<byte[]> ReadFrameAsync(Stream stream, CancellationToken cancellationToken)
    {
        byte[] header = new byte[4];
        await stream.ReadExactlyAsync(header, cancellationToken).ConfigureAwait(false);
        uint length = BinaryPrimitives.ReadUInt32LittleEndian(header);
        if (length > MaxFrameBytes)
        {
            throw new InvalidDataException($"App-tools response claims {length} bytes; the limit is {MaxFrameBytes}.");
        }

        byte[] payload = new byte[length];
        await stream.ReadExactlyAsync(payload, cancellationToken).ConfigureAwait(false);
        return payload;
    }
}
