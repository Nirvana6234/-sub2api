using System.IO;
using System.Text.Json;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>The loopback port and local token the relay used last time.</summary>
internal sealed record RelayEndpoint(int? Port, string? Token);

/// <summary>Remembers where the relay was listening, so an editor's configuration stays valid across restarts.</summary>
internal interface IRelayEndpointStore
{
    RelayEndpoint Load();

    void Save(int port, string token);
}

/// <summary>
/// Keeps the relay's port and local token in a small file beside the client's other
/// state.
/// </summary>
/// <remarks>
/// <para>
/// Needed because an editor is not launched by this client. Codex is: the client writes
/// its configuration, starts it, and puts the configuration back on exit, so a port
/// chosen afresh each run is harmless. Claude Code inside an editor is started by the
/// user whenever they like, with this client possibly not yet running, and reads a
/// configuration that outlives the process that wrote it. If the port or the token
/// changed on every start, that configuration would point at a dead port with a dead
/// token after any restart, and stay that way until it was rewritten.
/// </para>
/// <para>
/// Not encrypted, unlike the session: the token is not a credential. It is the same
/// value the client already writes in the clear into Codex's <c>auth.json</c>, it
/// authenticates only to a listener on this machine's loopback, and the account session
/// behind it never leaves the client. The listener still refuses browsers and other
/// hosts regardless of the token.
/// </para>
/// <para>
/// Every failure degrades to "nothing remembered", which costs a fresh port and token
/// rather than a failed start.
/// </para>
/// </remarks>
internal sealed class RelayEndpointStore : IRelayEndpointStore
{
    private readonly string _filePath;

    public RelayEndpointStore(string? filePath = null) =>
        _filePath = filePath ?? AppPaths.InData("relay-endpoint.json");

    public RelayEndpoint Load()
    {
        if (!File.Exists(_filePath))
        {
            return new RelayEndpoint(null, null);
        }

        try
        {
            // JsonDocument rather than a bound type: this assembly is trimmed, and a
            // reflection-bound read yields defaults without complaint.
            using JsonDocument document = JsonDocument.Parse(File.ReadAllBytes(_filePath));
            JsonElement root = document.RootElement;
            int? port = root.TryGetProperty("port", out JsonElement portElement) &&
                        portElement.ValueKind == JsonValueKind.Number &&
                        portElement.TryGetInt32(out int parsedPort)
                ? parsedPort
                : null;
            string? token = root.TryGetProperty("token", out JsonElement tokenElement) &&
                            tokenElement.ValueKind == JsonValueKind.String
                ? tokenElement.GetString()
                : null;
            return new RelayEndpoint(port, token);
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return new RelayEndpoint(null, null);
        }
    }

    public void Save(int port, string token)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            using var stream = new MemoryStream();
            using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
            {
                writer.WriteStartObject();
                writer.WriteNumber("port", port);
                writer.WriteString("token", token);
                writer.WriteEndObject();
            }

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, stream.ToArray());
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Nothing remembered is a fresh port next time, not a failure now.
        }
    }
}
