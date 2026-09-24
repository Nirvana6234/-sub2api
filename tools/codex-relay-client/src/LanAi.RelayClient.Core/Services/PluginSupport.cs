using System.IO;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>What the dashboard wants the editor plug-ins set up as, right now.</summary>
/// <param name="Enabled">The 支持插件 checkbox.</param>
/// <param name="GroupId">
/// The Claude group chosen for the plug-ins — a selection of its own, independent of whatever
/// group Codex is routed through. Null when the checkbox is on but nothing has been chosen yet.
/// </param>
/// <param name="Model">The Claude model chosen in the client.</param>
/// <param name="LocalProxyAccountId">
/// Set when Claude Code goes straight to Anthropic with one of the user's own accounts; then no
/// Claude group is needed for it to be set up.
/// </param>
internal sealed record PluginSupportRequest(
    bool Enabled,
    long? GroupId,
    string? GroupName,
    string? Model,
    long? LocalProxyAccountId = null);

internal enum PluginSupportState
{
    /// <summary>Nothing to do here: no local relay, or the client is releasing.</summary>
    NotApplicable,

    /// <summary>The checkbox is off. Anything this client set earlier has been put back.</summary>
    Off,

    /// <summary>The box is on but no Claude group has been chosen yet, so nothing is set.</summary>
    NoGroupChosen,

    /// <summary>Claude Code is pointed at the relay.</summary>
    Active,

    /// <summary>It could not be set up. <see cref="PluginSupportResult.Note"/> says what to tell the user.</summary>
    Problem,
}

/// <param name="Note">Wording for the user, in Chinese, when there is something worth saying.</param>
internal sealed record PluginSupportResult(PluginSupportState State, string? Note = null);

/// <summary>Both halves of the change, reported separately.</summary>
internal sealed record PluginBindingReport(PluginConfigResult Claude, PluginConfigResult VsCode);

/// <summary>The two files an editor plug-in's Claude Code reads, behind one seam.</summary>
internal interface IPluginBinding
{
    PluginBindingReport Apply(string relayOrigin, string relayToken, string? model);

    PluginBindingReport Restore();
}

/// <summary>
/// Points Claude Code, and the editor extension built on it, at the local relay.
/// </summary>
/// <remarks>
/// <para>
/// Two files, in a fixed order. <c>~/.claude/settings.json</c> is what makes Claude Code use
/// the relay at all. The editor setting only stops the extension asking the user to sign in
/// to Anthropic, which is pointless — and misleading, since the extension would then show no
/// account at all — if Claude Code itself is not pointed anywhere. So the editor is touched
/// only once the first has taken, and undone before it.
/// </para>
/// <para>
/// The same model is given for every model slot. The client already has its own choice of
/// Claude model; letting the CLI's <c>/model</c> picker offer sonnet and opus separately would
/// route to names the group may not serve.
/// </para>
/// </remarks>
internal sealed class ClaudePluginBinding(ClaudeCodeSettingsWriter settings, VsCodeSettingsEditor editor) : IPluginBinding
{
    private static readonly PluginConfigResult NotAttempted =
        new(PluginConfigOutcome.Skipped, "not attempted: Claude Code's own settings were not changed.");

    private readonly ClaudeCodeSettingsWriter _settings = settings ?? throw new ArgumentNullException(nameof(settings));
    private readonly VsCodeSettingsEditor _editor = editor ?? throw new ArgumentNullException(nameof(editor));

    public PluginBindingReport Apply(string relayOrigin, string relayToken, string? model)
    {
        var env = new Dictionary<string, string?>
        {
            // No /v1: the SDK appends /v1/messages itself, and a base that already ends in it
            // produces /v1/v1/messages.
            ["ANTHROPIC_BASE_URL"] = relayOrigin,
            ["ANTHROPIC_AUTH_TOKEN"] = relayToken,

            // Removed while the relay is in use, so a key of the user's own never competes with
            // the token. It comes back when this is undone.
            ["ANTHROPIC_API_KEY"] = null,

            // Otherwise Claude Code reaches for api.anthropic.com for updates and telemetry with
            // a token that only means something here.
            ["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1",
        };

        if (!string.IsNullOrWhiteSpace(model))
        {
            env["ANTHROPIC_MODEL"] = model;
            env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = model;
            env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = model;
            env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = model;
        }

        PluginConfigResult claude = _settings.Apply(env);
        bool claudeTook = claude.Outcome is PluginConfigOutcome.Applied or PluginConfigOutcome.NothingToDo;
        PluginConfigResult vsCode = claudeTook ? _editor.Apply() : NotAttempted;
        return new PluginBindingReport(claude, vsCode);
    }

    public PluginBindingReport Restore()
    {
        // The editor first: it is the one that depends on the other.
        PluginConfigResult vsCode = _editor.Restore();
        PluginConfigResult claude = _settings.Restore();
        return new PluginBindingReport(claude, vsCode);
    }
}

/// <summary>Remembers whether the user wants editor plug-ins set up.</summary>
internal interface IPluginSupportPreferenceStore
{
    /// <returns>The remembered choice, or null when the user has never expressed one.</returns>
    bool? Load();

    void Save(bool enabled);
}

/// <summary>
/// Keeps the 支持插件 choice in a small file of its own, for the same reason the
/// compression choice has one: it is a preference about this machine, not about which relay
/// is in use, and it must not be reset when the client is pointed at another server.
/// </summary>
/// <remarks>
/// Read with <see cref="JsonDocument"/> and written with <see cref="Utf8JsonWriter"/>, not
/// bound to a type: this assembly is trimmed. Every failure degrades to "no preference".
/// </remarks>
internal sealed class PluginSupportPreferenceStore : IPluginSupportPreferenceStore
{
    private readonly string _filePath;

    public PluginSupportPreferenceStore(string? filePath = null) =>
        _filePath = filePath ?? AppPaths.InData("plugin-support.json");

    public bool? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(File.ReadAllBytes(_filePath));
            return document.RootElement.TryGetProperty("enabled", out JsonElement enabled) &&
                   enabled.ValueKind is JsonValueKind.True or JsonValueKind.False
                ? enabled.GetBoolean()
                : null;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    public void Save(bool enabled)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            using var stream = new MemoryStream();
            using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
            {
                writer.WriteStartObject();
                writer.WriteBoolean("enabled", enabled);
                writer.WriteEndObject();
            }

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, stream.ToArray());
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Losing a preference is a far smaller harm than failing the change the user just made.
        }
    }
}
