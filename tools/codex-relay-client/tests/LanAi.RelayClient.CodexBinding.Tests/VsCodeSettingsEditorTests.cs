using System.Text;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// VS Code's user settings are a file the user keeps by hand, and are edited while the editor
/// may be open. What matters is that it comes back exactly, and that nothing the user did in
/// the meantime is lost.
/// </summary>
public sealed class VsCodeSettingsEditorTests : IDisposable
{
    private const string Key = "claudeCode.disableLoginPrompt";

    private readonly string _root = Path.Combine(Path.GetTempPath(), $"vscode-{Guid.NewGuid():N}");
    private readonly string _state;

    public VsCodeSettingsEditorTests()
    {
        _state = Path.Combine(_root, "state");
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private string Profile(string editor) => Path.Combine(_root, "appdata", editor, "User");

    private string SettingsOf(string editor) => Path.Combine(Profile(editor), "settings.json");

    private string Installed(string editor)
    {
        Directory.CreateDirectory(Profile(editor));
        return SettingsOf(editor);
    }

    private VsCodeSettingsEditor Editor(params string[] files) => new(_state, files);

    private static string Text(string path) => new UTF8Encoding(false).GetString(File.ReadAllBytes(path));

    private string JournalPath => Path.Combine(_state, "vscode-settings-journal.json");

    // ---- Which editors --------------------------------------------------------------------

    [Fact]
    public void OnlyEditorsThatHaveAProfileAreFound()
    {
        Installed("Code");
        Installed("Cursor");

        IReadOnlyList<string> found = VsCodeSettingsEditor.DiscoverSettingsFiles(Path.Combine(_root, "appdata"));

        Assert.Equal([SettingsOf("Code"), SettingsOf("Cursor")], found);
        Assert.False(Directory.Exists(Profile("Code - Insiders")), "no directory is created for an editor that is not there");
    }

    [Fact]
    public void TheFullFamilyOfEditorsIsCovered()
    {
        foreach (string editor in new[] { "Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf" })
        {
            Installed(editor);
        }

        Assert.Equal(5, VsCodeSettingsEditor.DiscoverSettingsFiles(Path.Combine(_root, "appdata")).Count);
    }

    [Fact]
    public void AnEditorThatIsNotInstalledIsSkippedAndNothingIsCreatedForIt()
    {
        string missing = SettingsOf("Code");

        PluginConfigResult result = Editor(missing).Apply();

        Assert.Equal(PluginConfigOutcome.Skipped, result.Outcome);
        Assert.False(Directory.Exists(Profile("Code")));
        Assert.False(File.Exists(JournalPath));
    }

    // ---- Setting it -------------------------------------------------------------------------

    [Fact]
    public void ASettingsFileIsCreatedWhenTheProfileHasNoneAndRemovedAgain()
    {
        string path = Installed("Code");
        Assert.False(File.Exists(path));

        PluginConfigResult applied = Editor(path).Apply();

        Assert.Equal(PluginConfigOutcome.Applied, applied.Outcome);
        Assert.Contains(Key, Text(path), StringComparison.Ordinal);

        Editor(path).Restore();

        Assert.False(File.Exists(path), "a file this created must not outlive it");
    }

    public static TheoryData<string> Originals() => new()
    {
        "{}",
        "{\n}\n",
        "{\n    \"editor.fontSize\": 14\n}\n",
        "{\n    \"editor.fontSize\": 14,\n    \"workbench.colorTheme\": \"Default Dark+\"\n}\n",
        "{\n    \"editor.fontSize\": 14,\n}\n",
        "{\r\n    \"editor.fontSize\": 14\r\n}\r\n",
        "{\n\t\"editor.fontSize\": 14\n}\n",
        "// my settings\n{\n    \"http.proxy\": \"http://127.0.0.1:7897\", // corporate\n    \"editor.fontSize\": 14 // last\n}\n",
        "{ \"editor.fontSize\": 14 }",
        "{\n    \"a\": { \"nested\": [1, 2, { \"claudeCode.disableLoginPrompt\": false }] }\n}\n",
        "{\n    \"claudeCode.disableLoginPrompt\": false,\n    \"a\": 1\n}\n",
        "{\n    \"claudeCode.disableLoginPrompt\": false, // I turned this off\n    \"a\": 1\n}\n",
    };

    /// <summary>
    /// The whole point of the two-tier restore: a file nobody touched comes back byte for byte,
    /// whatever shape it was in.
    /// </summary>
    [Theory]
    [MemberData(nameof(Originals))]
    public void AFileNobodyTouchedComesBackByteForByte(string original)
    {
        string path = Installed("Code");
        byte[] bytes = new UTF8Encoding(false).GetBytes(original);
        File.WriteAllBytes(path, bytes);

        Editor(path).Apply();
        Assert.NotEqual(bytes, File.ReadAllBytes(path));
        Editor(path).Restore();

        Assert.Equal(bytes, File.ReadAllBytes(path));
    }

    [Fact]
    public void AByteOrderMarkIsKeptThroughTheEditAndTheRestore()
    {
        string path = Installed("Code");
        byte[] original = [0xEF, 0xBB, 0xBF, .. new UTF8Encoding(false).GetBytes("{\n    \"a\": 1\n}\n")];
        File.WriteAllBytes(path, original);

        Editor(path).Apply();
        byte[] edited = File.ReadAllBytes(path);
        Editor(path).Restore();

        Assert.Equal([0xEF, 0xBB, 0xBF], edited[..3]);
        Assert.Contains(Key, Encoding.UTF8.GetString(edited), StringComparison.Ordinal);
        Assert.Equal(original, File.ReadAllBytes(path));
    }

    [Fact]
    public void AnAppliedFileKeepsItsCommentsAndOtherSettings()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "// keep me\n{\n    \"http.proxy\": \"http://x\", // and me\n    \"a\": 1\n}\n");

        Editor(path).Apply();

        string edited = Text(path);
        Assert.Contains("// keep me", edited, StringComparison.Ordinal);
        Assert.Contains("\"http.proxy\": \"http://x\", // and me", edited, StringComparison.Ordinal);
        Assert.Contains("\"a\": 1", edited, StringComparison.Ordinal);
        Assert.Contains("\"claudeCode.disableLoginPrompt\": true", edited, StringComparison.Ordinal);
    }

    [Fact]
    public void ApplyingAgainChangesNothingAndTheOriginalIsStillRecoverable()
    {
        string path = Installed("Code");
        const string original = "{\n    \"a\": 1\n}\n";
        File.WriteAllText(path, original);

        Editor(path).Apply();
        string once = Text(path);
        PluginConfigResult second = Editor(path).Apply();
        Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, second.Outcome);
        Assert.Equal(once, once);
        Assert.Equal(original, Text(path));
    }

    [Fact]
    public void ASettingTheUserAlreadyHadOnIsNotRecordedOrTouched()
    {
        string path = Installed("Code");
        const string original = "{\n    \"claudeCode.disableLoginPrompt\": true\n}\n";
        File.WriteAllText(path, original);

        PluginConfigResult result = Editor(path).Apply();
        Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, result.Outcome);
        Assert.False(File.Exists(JournalPath), "there is nothing of ours to undo");
        Assert.Equal(original, Text(path));
    }

    // ---- The user edits the file in the meantime --------------------------------------------

    [Fact]
    public void WhatTheUserAddedAfterwardsSurvivesTheRestore()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();

        string edited = Text(path);
        int close = edited.LastIndexOf('}');
        File.WriteAllText(path, edited[..close].TrimEnd() + ",\n    \"mine\": \"added while open\"\n}\n");

        PluginConfigResult result = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.Restored, result.Outcome);
        string after = Text(path);
        Assert.Contains("\"mine\": \"added while open\"", after, StringComparison.Ordinal);
        Assert.Contains("\"a\": 1", after, StringComparison.Ordinal);
        Assert.DoesNotContain(Key, after, StringComparison.Ordinal);
    }

    [Fact]
    public void AValueTheUserChangedIsNotOverwrittenByTheRestore()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();
        File.WriteAllText(path, Text(path).Replace("true", "false", StringComparison.Ordinal));

        PluginConfigResult result = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, result.Outcome);
        Assert.Contains("\"claudeCode.disableLoginPrompt\": false", Text(path), StringComparison.Ordinal);
    }

    [Fact]
    public void ASettingTheUserRemovedIsNotAnError()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");

        PluginConfigResult result = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, result.Outcome);
        Assert.False(File.Exists(JournalPath), "the record is finished with");
    }

    [Fact]
    public void AFileTheUserDeletedIsNotRevived()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();
        File.Delete(path);

        Editor(path).Restore();

        Assert.False(File.Exists(path));
    }

    [Fact]
    public void ARestoreOfAFileThatCannotBeReadKeepsTheRecordForNextTime()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();
        string good = Text(path);
        File.WriteAllText(path, "{ \"a\": ");

        PluginConfigResult first = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.Skipped, first.Outcome);
        Assert.True(File.Exists(JournalPath));

        File.WriteAllText(path, good);
        PluginConfigResult second = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.Restored, second.Outcome);
        Assert.Equal("{\n    \"a\": 1\n}\n", Text(path));
    }

    // ---- Files it will not touch --------------------------------------------------------------

    [Theory]
    [InlineData("{ this is not json")]
    [InlineData("[1, 2, 3]")]
    [InlineData("{ \"a\": 1 } { \"b\": 2 }")]
    [InlineData("{ /* never closed \"a\": 1 }")]
    public void AFileThatIsNotOneObjectIsLeftByteForByteAlone(string content)
    {
        string path = Installed("Code");
        File.WriteAllText(path, content);

        PluginConfigResult result = Editor(path).Apply();

        Assert.Equal(PluginConfigOutcome.Skipped, result.Outcome);
        Assert.Equal(content, Text(path));
        Assert.False(File.Exists(JournalPath));
    }

    [Fact]
    public void OneUnreadableEditorDoesNotStopTheOthers()
    {
        string bad = Installed("Code");
        string good = Installed("Cursor");
        File.WriteAllText(bad, "{ broken");
        File.WriteAllText(good, "{\n    \"a\": 1\n}\n");

        PluginConfigResult result = Editor(bad, good).Apply();

        Assert.Equal(PluginConfigOutcome.Applied, result.Outcome);
        Assert.Contains(Key, Text(good), StringComparison.Ordinal);
        Assert.Equal("{ broken", Text(bad));
        Assert.Contains("Code", result.Detail, StringComparison.Ordinal);
    }

    [Fact]
    public void EveryEditorIsRestoredNotJustTheFirst()
    {
        string code = Installed("Code");
        string cursor = Installed("Cursor");
        File.WriteAllText(code, "{\n    \"a\": 1\n}\n");
        File.WriteAllText(cursor, "{\n    \"b\": 2\n}\n");
        Editor(code, cursor).Apply();

        Editor(code, cursor).Restore();

        Assert.Equal("{\n    \"a\": 1\n}\n", Text(code));
        Assert.Equal("{\n    \"b\": 2\n}\n", Text(cursor));
    }

    // ---- Across restarts and failures ---------------------------------------------------------

    [Fact]
    public void ANewEditorInstanceCanRestoreWhatAnEarlierOneApplied()
    {
        string path = Installed("Code");
        const string original = "{\n    \"a\": 1\n}\n";
        File.WriteAllText(path, original);
        Editor(path).Apply();

        // The client was closed and started again: nothing but the journal remembers.
        PluginConfigResult result = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.Restored, result.Outcome);
        Assert.Equal(original, Text(path));
    }

    /// <summary>
    /// The record is written before the file. If the write then fails there is a record of a
    /// change that was not made, which is harmless — never a changed file with nothing to
    /// undo it by.
    /// </summary>
    [Fact]
    public void AFailedWriteLeavesTheFileUntouchedAndTheRecordInPlace()
    {
        string path = Installed("Code");
        const string original = "{\n    \"a\": 1\n}\n";
        File.WriteAllText(path, original);

        // Occupies the temporary file's name so the write cannot happen.
        Directory.CreateDirectory(path + ".tmp");

        PluginConfigResult result = Editor(path).Apply();

        Assert.Equal(PluginConfigOutcome.Failed, result.Outcome);
        Assert.Equal(original, Text(path));
        Assert.True(File.Exists(JournalPath), "the record must exist before the file is touched");

        Directory.Delete(path + ".tmp");
        Editor(path).Restore();
        Assert.Equal(original, Text(path));
    }

    [Fact]
    public void AnUnreadableJournalDoesNotStopTheClient()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Directory.CreateDirectory(_state);
        File.WriteAllText(JournalPath, "not json at all");

        PluginConfigResult result = Editor(path).Apply();

        Assert.Equal(PluginConfigOutcome.Applied, result.Outcome);
    }

    [Fact]
    public void NothingIsLeftBehindOnceEverythingIsRestored()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");
        Editor(path).Apply();

        Editor(path).Restore();

        Assert.False(File.Exists(JournalPath));
        string backups = Path.Combine(_state, "vscode-settings-original");
        Assert.True(!Directory.Exists(backups) || Directory.GetFiles(backups).Length == 0);
    }

    [Fact]
    public void RestoringWithNothingAppliedIsHarmless()
    {
        string path = Installed("Code");
        File.WriteAllText(path, "{\n    \"a\": 1\n}\n");

        PluginConfigResult result = Editor(path).Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, result.Outcome);
        Assert.Equal("{\n    \"a\": 1\n}\n", Text(path));
    }
}
