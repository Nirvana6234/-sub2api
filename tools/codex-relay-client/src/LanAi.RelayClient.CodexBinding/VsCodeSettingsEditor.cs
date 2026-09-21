using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Sets <c>claudeCode.disableLoginPrompt</c> in the user settings of every VS Code-family
/// editor on the machine, and takes it back.
/// </summary>
/// <remarks>
/// <para>
/// <b>Why the editor needs this when <c>settings.json</c> in <c>~/.claude</c> already points
/// Claude Code at the relay.</b> The extension decides whether to ask the user to sign in with
/// its own check, and that check reads <c>process.env</c> plus the editor setting
/// <c>claudeCode.environmentVariables</c> — not Claude's <c>settings.json</c>. With no
/// Anthropic sign-in and no key in that environment it reports "no authentication found" and
/// shows the login prompt, even though the CLI it launches would authenticate through the
/// relay. <c>claudeCode.disableLoginPrompt</c> is the extension's own switch for exactly this
/// (its description: authentication is handled externally), so that is what is set. Read from
/// the extension's shipped source, version 2.1.258; cockpit-tools does not handle this at all.
/// </para>
/// <para>
/// <b>Touching a file the user keeps by hand.</b> It is JSONC, with comments and trailing
/// commas, so it is edited as text (see <see cref="JsoncText"/>) rather than parsed and
/// rewritten. A file that is not one JSON object is left alone.
/// </para>
/// <para>
/// <b>Putting it back.</b> If the file is exactly what this wrote, the original bytes come back
/// — a file this created is deleted. If the user has edited it since, only this change is
/// undone: the added line removed, or the old value returned, and only if that value is still
/// the one this set. A change made on purpose is not erased.
/// </para>
/// <para>
/// Never throws for what is normal on a real machine: an editor that is not installed, a file
/// that cannot be parsed, one that is locked. Each is reported as a result.
/// </para>
/// </remarks>
public sealed class VsCodeSettingsEditor
{
    public const string LoginPromptKey = "claudeCode.disableLoginPrompt";

    private const string AppliedValue = "true";
    private const string JournalFileName = "vscode-settings-journal.json";
    private const string BackupDirectoryName = "vscode-settings-original";

    private static readonly string[] EditorFolders = ["Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"];
    private static readonly byte[] Utf8Bom = [0xEF, 0xBB, 0xBF];

    private readonly string _stateDirectory;
    private readonly IReadOnlyList<string>? _explicitFiles;
    private readonly object _gate = new();

    /// <param name="stateDirectory">
    /// Where the journal and the copies of the original files are kept. Must outlive a restart
    /// of the client: it is the only thing that knows what to put back.
    /// </param>
    /// <param name="settingsFiles">
    /// The files to edit. Defaults to the user settings of each installed editor; given
    /// explicitly, they are used as they are.
    /// </param>
    public VsCodeSettingsEditor(string stateDirectory, IReadOnlyList<string>? settingsFiles = null)
    {
        _stateDirectory = string.IsNullOrWhiteSpace(stateDirectory)
            ? throw new ArgumentException("State directory cannot be empty.", nameof(stateDirectory))
            : Path.GetFullPath(stateDirectory);
        _explicitFiles = settingsFiles;
    }

    private string JournalPath => Path.Combine(_stateDirectory, JournalFileName);

    private string BackupDirectory => Path.Combine(_stateDirectory, BackupDirectoryName);

    /// <summary>
    /// The user settings file of each editor that has a profile on this machine.
    /// </summary>
    /// <remarks>
    /// Only editors whose <c>User</c> directory exists: that is the sign of one that is
    /// installed and has been run. Creating directories for editors that are not there would
    /// leave the user with folders for software they do not use.
    /// </remarks>
    internal static IReadOnlyList<string> DiscoverSettingsFiles(string? userDataRoot = null)
    {
        string root = userDataRoot ?? DefaultUserDataRoot();
        var files = new List<string>();
        foreach (string folder in EditorFolders)
        {
            string user = Path.Combine(root, folder, "User");
            if (Directory.Exists(user))
            {
                files.Add(Path.Combine(user, "settings.json"));
            }
        }

        return files;
    }

    private static string DefaultUserDataRoot()
    {
        if (OperatingSystem.IsWindows())
        {
            return Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
        }

        string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        if (OperatingSystem.IsMacOS())
        {
            return Path.Combine(home, "Library", "Application Support");
        }

        string? xdg = Environment.GetEnvironmentVariable("XDG_CONFIG_HOME");
        return string.IsNullOrWhiteSpace(xdg) ? Path.Combine(home, ".config") : xdg;
    }

    private IReadOnlyList<string> TargetFiles() => _explicitFiles ?? DiscoverSettingsFiles();

    /// <summary>Turns the login prompt off in every editor found.</summary>
    public PluginConfigResult Apply()
    {
        lock (_gate)
        {
            var journal = ReadJournal();
            var results = new List<(string Path, PluginConfigResult Result)>();
            // No journal write here at the end: ApplyTo writes it before each file it changes, and
            // that is the only ordering that matters. A second write afterwards would put back
            // a record that a crash between the file and it had lost — and hide, in tests, the
            // difference between the two orders.
            foreach (string path in TargetFiles())
            {
                results.Add((path, ApplyTo(path, journal)));
            }

            return Combine(results);
        }
    }

    /// <summary>Puts every editor's settings back to how they were.</summary>
    public PluginConfigResult Restore()
    {
        lock (_gate)
        {
            var journal = ReadJournal();
            var results = new List<(string Path, PluginConfigResult Result)>();
            foreach (Entry entry in journal.Entries.ToArray())
            {
                PluginConfigResult result = RestoreFrom(entry);
                results.Add((entry.Path, result));

                // Kept when the file could not be read: that is a state the user may fix, and
                // the record is the only thing that knows what to put back once they do.
                if (result.Outcome != PluginConfigOutcome.Skipped)
                {
                    journal.Entries.Remove(entry);
                    DeleteBackup(entry);
                }
            }

            try
            {
                if (journal.Entries.Count == 0)
                {
                    // Checked first: with no state directory yet, deleting a file that was never
                    // there throws, and "nothing to restore" must not be reported as a failure.
                    if (File.Exists(JournalPath))
                    {
                        File.Delete(JournalPath);
                    }
                }
                else
                {
                    WriteJournal(journal);
                }
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                results.Add((_stateDirectory, new PluginConfigResult(PluginConfigOutcome.Failed, ex.Message)));
            }

            return Combine(results);
        }
    }

    private PluginConfigResult ApplyTo(string path, Journal journal)
    {
        try
        {
            string? directory = Path.GetDirectoryName(path);
            if (directory is null || !Directory.Exists(directory))
            {
                return new PluginConfigResult(PluginConfigOutcome.Skipped, "the editor's profile directory does not exist.");
            }

            bool existed = File.Exists(path);
            byte[] original = existed ? File.ReadAllBytes(path) : [];
            bool bom = original.AsSpan().StartsWith(Utf8Bom);
            string text = existed
                ? new UTF8Encoding(false).GetString(original, bom ? 3 : 0, original.Length - (bom ? 3 : 0))
                : "{\n}\n";
            if (existed && string.IsNullOrWhiteSpace(text))
            {
                text = "{\n}\n";
            }

            JsoncText.Edit? edit = JsoncText.Set(text, LoginPromptKey, AppliedValue);
            if (edit is null)
            {
                return new PluginConfigResult(
                    PluginConfigOutcome.Skipped,
                    "the file is not a single JSON object (with comments allowed), so it was left unchanged.");
            }

            Entry? known = journal.Entries.FirstOrDefault(e => PathsEqual(e.Path, path));
            if (!edit.Inserted && string.Equals(edit.PreviousValue, AppliedValue, StringComparison.Ordinal))
            {
                // Already on. If this client turned it on earlier, that is still ours to undo, so
                // the record stays; if the user had it on, there is nothing to undo at all.
                return new PluginConfigResult(PluginConfigOutcome.NothingToDo);
            }

            if (known is not null)
            {
                // The record describes a file that no longer looks the way this left it — the
                // setting is gone or changed — so it is a fresh start from what is there now.
                journal.Entries.Remove(known);
                DeleteBackup(known);
            }

            string? backupName = null;
            if (existed)
            {
                Directory.CreateDirectory(BackupDirectory);
                backupName = BackupName(path);
                File.WriteAllBytes(Path.Combine(BackupDirectory, backupName), original);
            }

            byte[] written = Encode(edit.Text, bom);
            var entry = new Entry(
                path,
                existed,
                HadKey: !edit.Inserted,
                edit.PreviousValue,
                Convert.ToHexString(SHA256.HashData(written)),
                backupName);

            // The record first, then the file: a failed or interrupted write leaves a record of
            // a change that was not made, which restoring recognises and ignores.
            journal.Entries.Add(entry);
            WriteJournal(journal);
            AtomicWrite(path, written);
            return new PluginConfigResult(PluginConfigOutcome.Applied);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or DecoderFallbackException)
        {
            return new PluginConfigResult(PluginConfigOutcome.Failed, ex.Message);
        }
    }

    private PluginConfigResult RestoreFrom(Entry entry)
    {
        try
        {
            if (!File.Exists(entry.Path))
            {
                return new PluginConfigResult(PluginConfigOutcome.NothingToDo, "the file is already gone.");
            }

            byte[] current = File.ReadAllBytes(entry.Path);

            // Exactly what was written: nothing has happened to the file since, so the
            // original can simply be put back, byte for byte.
            if (string.Equals(Convert.ToHexString(SHA256.HashData(current)), entry.WrittenSha256, StringComparison.Ordinal))
            {
                if (!entry.FileExisted)
                {
                    File.Delete(entry.Path);
                    return new PluginConfigResult(PluginConfigOutcome.Restored);
                }

                string backupPath = Path.Combine(BackupDirectory, entry.BackupName ?? string.Empty);
                if (entry.BackupName is null || !File.Exists(backupPath))
                {
                    // The copy is gone. Fall through to undoing just this change.
                    return UndoOnlyThisChange(entry, current);
                }

                AtomicWrite(entry.Path, File.ReadAllBytes(backupPath));
                return new PluginConfigResult(PluginConfigOutcome.Restored);
            }

            return UndoOnlyThisChange(entry, current);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            return new PluginConfigResult(PluginConfigOutcome.Failed, ex.Message);
        }
    }

    /// <summary>
    /// The file has changed since this wrote it. Undo this one edit and leave the rest.
    /// </summary>
    private static PluginConfigResult UndoOnlyThisChange(Entry entry, byte[] current)
    {
        bool bom = current.AsSpan().StartsWith(Utf8Bom);
        string text = new UTF8Encoding(false).GetString(current, bom ? 3 : 0, current.Length - (bom ? 3 : 0));

        if (JsoncText.Scan(text) is null)
        {
            return new PluginConfigResult(
                PluginConfigOutcome.Skipped,
                "the file can no longer be read as a single JSON object, so it was left as it is.");
        }

        string? undone = JsoncText.Unset(text, LoginPromptKey, AppliedValue, entry.HadKey ? entry.PreviousValue : null);
        if (undone is null)
        {
            // The setting is gone, or is no longer the value this set. Either way it is the user's.
            return new PluginConfigResult(PluginConfigOutcome.NothingToDo);
        }

        AtomicWrite(entry.Path, Encode(undone, bom));
        return new PluginConfigResult(PluginConfigOutcome.Restored);
    }

    private static PluginConfigResult Combine(List<(string Path, PluginConfigResult Result)> results)
    {
        string detail = string.Join(
            " ",
            results.Where(r => r.Result.Detail is not null).Select(r => $"[{r.Path}] {r.Result.Detail}"));
        string? joined = detail.Length == 0 ? null : detail;

        foreach (PluginConfigOutcome outcome in new[]
                 {
                     PluginConfigOutcome.Failed,
                     PluginConfigOutcome.Applied,
                     PluginConfigOutcome.Restored,
                     PluginConfigOutcome.Skipped,
                 })
        {
            if (results.Any(r => r.Result.Outcome == outcome))
            {
                return new PluginConfigResult(outcome, joined);
            }
        }

        return new PluginConfigResult(PluginConfigOutcome.NothingToDo, joined);
    }

    private static byte[] Encode(string text, bool bom)
    {
        byte[] body = new UTF8Encoding(false).GetBytes(text);
        return bom ? [.. Utf8Bom, .. body] : body;
    }

    private static void AtomicWrite(string path, byte[] contents)
    {
        string temporaryPath = path + ".tmp";
        File.WriteAllBytes(temporaryPath, contents);
        File.Move(temporaryPath, path, overwrite: true);
    }

    private static bool PathsEqual(string a, string b) =>
        string.Equals(Path.GetFullPath(a), Path.GetFullPath(b), StringComparison.OrdinalIgnoreCase);

    private static string BackupName(string path) =>
        Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(Path.GetFullPath(path).ToLowerInvariant())))[..16] + ".bak";

    private void DeleteBackup(Entry entry)
    {
        if (entry.BackupName is null)
        {
            return;
        }

        try
        {
            File.Delete(Path.Combine(BackupDirectory, entry.BackupName));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // A copy that cannot be deleted costs disk, nothing else.
        }
    }

    // ---- The journal ------------------------------------------------------------------

    private sealed class Journal
    {
        public List<Entry> Entries { get; } = [];
    }

    /// <param name="FileExisted">Whether the user had this file before this created it.</param>
    /// <param name="HadKey">Whether the setting was already in it.</param>
    /// <param name="PreviousValue">Its value text, when it was.</param>
    /// <param name="WrittenSha256">The hash of the bytes this wrote, to tell whether the file has been touched since.</param>
    /// <param name="BackupName">The copy of the original, when there was an original.</param>
    private sealed record Entry(
        string Path,
        bool FileExisted,
        bool HadKey,
        string? PreviousValue,
        string WrittenSha256,
        string? BackupName);

    /// <remarks>
    /// Read with <see cref="JsonDocument"/> and written with <see cref="Utf8JsonWriter"/>: this
    /// project is trimmable, and reflection-based JSON binds to defaults without complaint when
    /// trimmed. A journal that read as empty would mean settings that are never put back.
    /// </remarks>
    private Journal ReadJournal()
    {
        var journal = new Journal();
        if (!File.Exists(JournalPath))
        {
            return journal;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(File.ReadAllBytes(JournalPath));
            if (document.RootElement.TryGetProperty("entries", out JsonElement entries) &&
                entries.ValueKind == JsonValueKind.Array)
            {
                foreach (JsonElement e in entries.EnumerateArray())
                {
                    string? path = ReadString(e, "path");
                    string? sha = ReadString(e, "writtenSha256");
                    if (path is null || sha is null)
                    {
                        continue;
                    }

                    journal.Entries.Add(new Entry(
                        path,
                        FileExisted: e.TryGetProperty("fileExisted", out JsonElement existed) && existed.ValueKind == JsonValueKind.True,
                        HadKey: e.TryGetProperty("hadKey", out JsonElement had) && had.ValueKind == JsonValueKind.True,
                        PreviousValue: ReadString(e, "previousValue"),
                        WrittenSha256: sha,
                        BackupName: ReadString(e, "backupName")));
                }
            }
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            // Unreadable means nothing remembered. The setting stays as it is rather than being
            // overwritten on the strength of a record that could not be read.
            return new Journal();
        }

        return journal;
    }

    private static string? ReadString(JsonElement parent, string name) =>
        parent.TryGetProperty(name, out JsonElement element) && element.ValueKind == JsonValueKind.String
            ? element.GetString()
            : null;

    private void WriteJournal(Journal journal)
    {
        if (journal.Entries.Count == 0)
        {
            if (File.Exists(JournalPath))
            {
                File.Delete(JournalPath);
            }

            return;
        }

        Directory.CreateDirectory(_stateDirectory);
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream, new JsonWriterOptions { Indented = true }))
        {
            writer.WriteStartObject();
            writer.WriteStartArray("entries");
            foreach (Entry entry in journal.Entries)
            {
                writer.WriteStartObject();
                writer.WriteString("path", entry.Path);
                writer.WriteBoolean("fileExisted", entry.FileExisted);
                writer.WriteBoolean("hadKey", entry.HadKey);
                if (entry.PreviousValue is null)
                {
                    writer.WriteNull("previousValue");
                }
                else
                {
                    writer.WriteString("previousValue", entry.PreviousValue);
                }

                writer.WriteString("writtenSha256", entry.WrittenSha256);
                if (entry.BackupName is null)
                {
                    writer.WriteNull("backupName");
                }
                else
                {
                    writer.WriteString("backupName", entry.BackupName);
                }

                writer.WriteEndObject();
            }

            writer.WriteEndArray();
            writer.WriteEndObject();
        }

        string temporaryPath = JournalPath + ".tmp";
        File.WriteAllBytes(temporaryPath, stream.ToArray());
        File.Move(temporaryPath, JournalPath, overwrite: true);
    }
}
