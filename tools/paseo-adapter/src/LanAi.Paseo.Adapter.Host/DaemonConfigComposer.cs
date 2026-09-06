using System.Buffers;
using System.Text;
using System.Text.Json;

namespace LanAi.Paseo.Adapter.Host;

/// <summary>
/// Writes the daemon's <c>config.json</c>: the file that turns a general-purpose
/// Paseo daemon into the narrow, codex-only one this product ships.
/// </summary>
/// <remarks>
/// <para>
/// <b>Rewritten before every start, never merged.</b> Two reasons, both measured.
/// First, the daemon edits this file itself: a fresh home came back with
/// <c>listen: 127.0.0.1:6767</c>, <c>cors.allowedOrigins: ["https://app.paseo.sh"]</c>
/// and an <c>app.baseUrl</c> nobody asked for. Second, a user hand-editing our
/// private home is not a supported path — if merging were attempted, a stray
/// <c>"claude": {"enabled": true}</c> would silently re-enable a provider and
/// nothing would report it.
/// </para>
/// <para>
/// Every switch here is one of the "enforceable" rows from the capability matrix:
/// what this file turns off is genuinely unreachable, as opposed to merely hidden
/// in a UI.
/// </para>
/// </remarks>
public static class DaemonConfigComposer
{
    /// <summary>Providers disabled outright. The adapter is codex-only by design.</summary>
    /// <remarks>
    /// Also the biggest size lever: the Claude provider drags a ~239 MB
    /// platform binary into the shipped <c>node_modules</c>.
    /// </remarks>
    private static readonly string[] DisabledProviders = ["claude", "copilot", "opencode", "pi", "omp"];

    public static string Compose(int port, string? relayEndpoint = null, bool relayUseTls = true)
    {
        var buffer = new ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteNumber("version", 1);

            writer.WritePropertyName("daemon");
            writer.WriteStartObject();
            // Loopback only, always. Remote access is the relay's job and it is an
            // outbound connection; binding anything else would expose the daemon
            // to the local network with no gate in front of it.
            writer.WriteString("listen", $"127.0.0.1:{port}");

            writer.WritePropertyName("cors");
            writer.WriteStartObject();
            // The daemon defaults this to https://app.paseo.sh. We serve no web
            // client, so no origin is allowed to reach it from a browser.
            writer.WritePropertyName("allowedOrigins");
            writer.WriteStartArray();
            writer.WriteEndArray();
            writer.WriteEndObject();

            writer.WritePropertyName("relay");
            writer.WriteStartObject();
            // Remote access stays off until a person consents. Pairing is the gate.
            // The endpoint is configured anyway so that enabling it later reaches
            // our own relay rather than the public one — an endpoint switched at
            // enable time would be a second chance to get it wrong.
            writer.WriteBoolean("enabled", false);
            if (!string.IsNullOrWhiteSpace(relayEndpoint))
            {
                writer.WriteString("endpoint", relayEndpoint);
                writer.WriteBoolean("useTls", relayUseTls);
                writer.WriteBoolean("publicUseTls", relayUseTls);
            }

            writer.WriteEndObject();

            writer.WritePropertyName("mcp");
            writer.WriteStartObject();
            // No MCP endpoint and no MCP injected into agents: both widen what an
            // agent can reach, and neither is part of the narrow contract.
            writer.WriteBoolean("enabled", false);
            writer.WriteBoolean("injectIntoAgents", false);
            writer.WriteEndObject();

            writer.WriteEndObject(); // daemon

            writer.WritePropertyName("features");
            writer.WriteStartObject();
            // Voice off. Beyond being outside the contract, leaving it on makes a
            // fresh daemon start an unattended download of local speech models.
            WriteDisabledFeature(writer, "dictation");
            WriteDisabledFeature(writer, "voiceMode");
            WriteDisabledFeature(writer, "webUi");
            writer.WriteEndObject();

            writer.WritePropertyName("agents");
            writer.WriteStartObject();
            writer.WritePropertyName("providers");
            writer.WriteStartObject();
            foreach (var provider in DisabledProviders)
            {
                writer.WritePropertyName(provider);
                writer.WriteStartObject();
                writer.WriteBoolean("enabled", false);
                writer.WriteEndObject();
            }

            writer.WriteEndObject();
            writer.WriteEndObject(); // agents

            writer.WriteEndObject();
        }

        return Encoding.UTF8.GetString(buffer.WrittenSpan);
    }

    /// <summary>Writes the config into <paramref name="homePath"/>, creating the home if needed.</summary>
    public static void Write(string homePath, int port, string? relayEndpoint = null, bool relayUseTls = true)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(homePath);
        Directory.CreateDirectory(homePath);
        File.WriteAllText(
            Path.Combine(homePath, "config.json"),
            Compose(port, relayEndpoint, relayUseTls),
            new UTF8Encoding(false));
    }

    /// <summary>Path of the daemon's own log inside a private home.</summary>
    /// <remarks>Surfaced on faults so a consumer can offer "export logs" without reconstructing it.</remarks>
    public static string LogPath(string homePath) => Path.Combine(homePath, "daemon.log");

    private static void WriteDisabledFeature(Utf8JsonWriter writer, string name)
    {
        writer.WritePropertyName(name);
        writer.WriteStartObject();
        writer.WriteBoolean("enabled", false);
        writer.WriteEndObject();
    }
}
