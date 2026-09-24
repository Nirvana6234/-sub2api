using System.Text.Json;
using System.Text.Json.Nodes;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// <c>settings.json</c> belongs to the user. What matters here is what survives the write
/// and what comes back afterwards, far more than what the write puts there.
/// </summary>
public sealed class ClaudeCodeSettingsWriterTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), $"claude-config-{Guid.NewGuid():N}");
    private readonly string _configDirectory;
    private readonly string _journalPath;
    private readonly ClaudeCodeSettingsWriter _writer;

    public ClaudeCodeSettingsWriterTests()
    {
        _configDirectory = Path.Combine(_root, ".claude");
        _journalPath = Path.Combine(_root, "journal", "claude-settings.json");
        _writer = NewWriter();
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
        {
            Directory.Delete(_root, recursive: true);
        }
    }

    private string SettingsPath => Path.Combine(_configDirectory, "settings.json");

    private ClaudeCodeSettingsWriter NewWriter(string? configDirectory = null) =>
        new(_journalPath, configDirectory ?? _configDirectory);

    private static Dictionary<string, string?> Relay(string model = "claude-opus-5") => new()
    {
        ["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:61231",
        ["ANTHROPIC_AUTH_TOKEN"] = new string('a', 64),
        ["ANTHROPIC_API_KEY"] = null,
        ["ANTHROPIC_MODEL"] = model,
        ["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1",
    };

    private void Given(string json)
    {
        Directory.CreateDirectory(_configDirectory);
        File.WriteAllText(SettingsPath, json);
    }

    private JsonObject Settings() => (JsonObject)JsonNode.Parse(File.ReadAllText(SettingsPath))!;

    private string? Env(string key) => Settings()["env"]?[key]?.GetValue<string>();

    // ---- Setting it ---------------------------------------------------------------------

    [Fact]
    public void ASettingsFileIsCreatedWhenThereIsNone()
    {
        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Applied, result.Outcome);
        Assert.Equal("http://127.0.0.1:61231", Env("ANTHROPIC_BASE_URL"));
        Assert.Equal(new string('a', 64), Env("ANTHROPIC_AUTH_TOKEN"));
        Assert.Equal("claude-opus-5", Env("ANTHROPIC_MODEL"));
        Assert.Equal("1", Env("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"));
    }

    [Fact]
    public void EverythingElseInTheFileIsLeftAsItWas()
    {
        Given("""
              {
                "model": "sonnet",
                "permissions": { "allow": ["Bash(git status)"] },
                "env": { "MY_OWN": "keep-me", "DISABLE_TELEMETRY": "1" },
                "hooks": {}
              }
              """);

        _writer.Apply(Relay());

        JsonObject settings = Settings();
        Assert.Equal("sonnet", settings["model"]!.GetValue<string>());
        Assert.Equal("Bash(git status)", settings["permissions"]!["allow"]![0]!.GetValue<string>());
        Assert.NotNull(settings["hooks"]);
        Assert.Equal("keep-me", Env("MY_OWN"));
        Assert.Equal("1", Env("DISABLE_TELEMETRY"));
    }

    /// <summary>
    /// The default JSON encoder rewrites non-ASCII text and characters like &amp; into \u
    /// escapes. Text the user typed should come back as they typed it.
    /// </summary>
    [Fact]
    public void TextTheUserTypedIsNotRewrittenIntoEscapes()
    {
        Given("{ \"env\": { \"NOTE\": \"中文 & <b> + ok\" } }");

        _writer.Apply(Relay());

        string written = File.ReadAllText(SettingsPath);
        Assert.Contains("中文 & <b> + ok", written, StringComparison.Ordinal);
        Assert.DoesNotContain("\\u", written, StringComparison.Ordinal);
    }

    [Fact]
    public void ApplyingTheSameThingAgainChangesNothing()
    {
        _writer.Apply(Relay());
        string before = File.ReadAllText(SettingsPath);

        PluginConfigResult second = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.NothingToDo, second.Outcome);
        Assert.Equal(before, File.ReadAllText(SettingsPath));
    }

    [Fact]
    public void ApplyRefusesAKeyItDoesNotManage()
    {
        Assert.Throws<ArgumentException>(() => _writer.Apply(new Dictionary<string, string?> { ["PATH"] = "x" }));
    }

    // ---- Undoing it ---------------------------------------------------------------------

    [Fact]
    public void RestoringAFileThisCreatedLeavesNoFileBehind()
    {
        _writer.Apply(Relay());

        PluginConfigResult result = _writer.Restore();

        Assert.Equal(PluginConfigOutcome.Restored, result.Outcome);
        Assert.False(File.Exists(SettingsPath), "an empty {} left in the user's directory would be litter");
    }

    /// <summary>
    /// Removing what was written is not restoring: a user with their own ANTHROPIC_MODEL
    /// would lose it. What comes back must be what they had.
    /// </summary>
    [Fact]
    public void AValueTheUserHadComesBackAndAKeyTheyDidNotHaveIsRemoved()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\", \"ANTHROPIC_API_KEY\": \"sk-theirs\", \"MY_OWN\": \"x\" } }");
        _writer.Apply(Relay());
        Assert.Equal("claude-opus-5", Env("ANTHROPIC_MODEL"));
        Assert.Null(Env("ANTHROPIC_API_KEY"));

        _writer.Restore();

        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
        Assert.Equal("sk-theirs", Env("ANTHROPIC_API_KEY"));
        Assert.Equal("x", Env("MY_OWN"));
        Assert.Null(Env("ANTHROPIC_AUTH_TOKEN"));
        Assert.Null(Env("ANTHROPIC_BASE_URL"));
    }

    /// <summary>
    /// The second apply reads this client's own value as "current". Recording that would
    /// replace the user's real original with something this client wrote.
    /// </summary>
    [Fact]
    public void TheOriginalSurvivesApplyingMoreThanOnce()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");

        _writer.Apply(Relay("claude-opus-5"));
        _writer.Apply(Relay("claude-sonnet-5"));
        _writer.Apply(Relay("claude-opus-5"));
        _writer.Restore();

        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
    }

    [Fact]
    public void AKeyThatIsNoLongerRequestedIsPutBackToHowItWas()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        _writer.Apply(Relay("claude-opus-5"));

        Dictionary<string, string?> withoutModel = Relay();
        withoutModel.Remove("ANTHROPIC_MODEL");
        _writer.Apply(withoutModel);

        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
        Assert.Equal("http://127.0.0.1:61231", Env("ANTHROPIC_BASE_URL"));
    }

    /// <summary>
    /// A key the user edited while this was active is theirs. Putting the original back over
    /// it would erase a change made on purpose.
    /// </summary>
    [Fact]
    public void AKeyTheUserEditedInTheMeantimeIsLeftAlone()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        _writer.Apply(Relay());

        JsonObject edited = Settings();
        edited["env"]!["ANTHROPIC_MODEL"] = "changed-by-hand";
        File.WriteAllText(SettingsPath, edited.ToJsonString());

        _writer.Restore();

        Assert.Equal("changed-by-hand", Env("ANTHROPIC_MODEL"));
        Assert.Null(Env("ANTHROPIC_AUTH_TOKEN"));
    }

    [Fact]
    public void AKeyRemovedOnPurposeIsNotBroughtBackOverAValueTheUserSetLater()
    {
        Given("{ \"env\": { \"ANTHROPIC_API_KEY\": \"sk-theirs\" } }");
        _writer.Apply(Relay());
        Assert.Null(Env("ANTHROPIC_API_KEY"));

        JsonObject edited = Settings();
        edited["env"]!["ANTHROPIC_API_KEY"] = "sk-set-while-active";
        File.WriteAllText(SettingsPath, edited.ToJsonString());

        _writer.Restore();

        Assert.Equal("sk-set-while-active", Env("ANTHROPIC_API_KEY"));
    }

    /// <summary>
    /// Handed back as it was written. A number that came back as the string "1" is a different
    /// value to whatever reads it.
    /// </summary>
    [Fact]
    public void AValueOfAnotherTypeComesBackAsThatType()
    {
        Given("{ \"env\": { \"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC\": 0 } }");
        _writer.Apply(Relay());

        _writer.Restore();

        JsonNode? restored = Settings()["env"]!["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"];
        Assert.Equal(JsonValueKind.Number, restored!.GetValueKind());
        Assert.Equal(0, restored.GetValue<int>());
    }

    [Fact]
    public void RestoringTwiceOrWithNothingAppliedIsHarmless()
    {
        Assert.Equal(PluginConfigOutcome.NothingToDo, _writer.Restore().Outcome);

        Given("{ \"env\": { \"MY_OWN\": \"x\" } }");
        _writer.Apply(Relay());
        _writer.Restore();

        Assert.Equal(PluginConfigOutcome.NothingToDo, _writer.Restore().Outcome);
        Assert.Equal("x", Env("MY_OWN"));
    }

    [Fact]
    public void AnExistingFileIsKeptEvenWhenItHoldsNothingAfterwards()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        _writer.Apply(Relay());

        _writer.Restore();

        Assert.True(File.Exists(SettingsPath), "it was the user's file to begin with");
    }

    // ---- Files it will not touch --------------------------------------------------------

    [Fact]
    public void AFileWithCommentsIsLeftByteForByteAlone()
    {
        const string original = "{\n  // my notes\n  \"env\": { \"MY_OWN\": \"x\" },\n}\n";
        Given(original);

        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Skipped, result.Outcome);
        Assert.Equal(original, File.ReadAllText(SettingsPath));
        Assert.False(File.Exists(_journalPath), "nothing was changed, so nothing is recorded");
    }

    [Theory]
    [InlineData("{ this is not json")]
    [InlineData("[1, 2, 3]")]
    [InlineData("{ \"env\": \"a string, not an object\" }")]
    [InlineData("{ \"env\": [1] }")]
    public void UnreadableOrUnexpectedFilesAreLeftAlone(string content)
    {
        Given(content);

        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Skipped, result.Outcome);
        Assert.Equal(content, File.ReadAllText(SettingsPath));
    }

    [Fact]
    public void AnEmptyFileIsTreatedAsAnEmptyObject()
    {
        Given(string.Empty);

        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Applied, result.Outcome);
        Assert.Equal("claude-opus-5", Env("ANTHROPIC_MODEL"));
    }

    /// <summary>
    /// Edited into something unreadable while active, the file can no longer be restored, and
    /// the record is the only thing that knows what to put back once the user fixes it.
    /// </summary>
    [Fact]
    public void ARestoreThatCannotReadTheFileKeepsTheRecordForNextTime()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        _writer.Apply(Relay());
        string good = File.ReadAllText(SettingsPath);
        File.WriteAllText(SettingsPath, "{ // now with a comment\n" + good.TrimStart('{'));

        PluginConfigResult first = _writer.Restore();

        Assert.Equal(PluginConfigOutcome.Skipped, first.Outcome);
        Assert.True(File.Exists(_journalPath));

        File.WriteAllText(SettingsPath, good);
        PluginConfigResult second = _writer.Restore();

        Assert.Equal(PluginConfigOutcome.Restored, second.Outcome);
        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
    }

    /// <summary>
    /// The record is written before the file. If the write then fails, or the client dies
    /// between the two, there is a record of changes that were not made, which restoring
    /// recognises and ignores — never the reverse, a changed file with nothing to undo it by.
    /// </summary>
    [Fact]
    public void AFailedWriteStillLeavesTheRecordAndTheFileUntouched()
    {
        const string original = "{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }";
        Given(original);

        // Occupies the temporary file's name so the write cannot happen.
        Directory.CreateDirectory(SettingsPath + ".tmp");

        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Failed, result.Outcome);
        Assert.Equal(original, File.ReadAllText(SettingsPath));
        Assert.True(File.Exists(_journalPath), "the record must exist before the file is touched");

        // And it is harmless: restoring changes nothing that was never changed.
        Directory.Delete(SettingsPath + ".tmp");
        _writer.Restore();
        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
    }

    // ---- Across restarts and moves ------------------------------------------------------

    [Fact]
    public void ANewWriterCanRestoreWhatAnEarlierOneApplied()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        NewWriter().Apply(Relay());

        // The client was closed and started again: nothing but the journal file remembers.
        PluginConfigResult result = NewWriter().Restore();

        Assert.Equal(PluginConfigOutcome.Restored, result.Outcome);
        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
    }

    [Fact]
    public void ChangingTheConfigDirectoryPutsTheOldOneBackFirst()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        NewWriter().Apply(Relay());
        string otherDirectory = Path.Combine(_root, "other-claude");

        NewWriter(otherDirectory).Apply(Relay());

        Assert.Equal("mine", Env("ANTHROPIC_MODEL"));
        Assert.Null(Env("ANTHROPIC_AUTH_TOKEN"));
        string other = File.ReadAllText(Path.Combine(otherDirectory, "settings.json"));
        Assert.Contains("ANTHROPIC_AUTH_TOKEN", other, StringComparison.Ordinal);
    }

    /// <summary>
    /// Deleted while active, the file has nothing of the user's left to restore. Keeping the
    /// old record would put keys back into a file they chose to remove.
    /// </summary>
    [Fact]
    public void AFileDeletedWhileActiveIsNotRevivedWithTheOldOriginals()
    {
        Given("{ \"env\": { \"ANTHROPIC_MODEL\": \"mine\" } }");
        _writer.Apply(Relay());
        File.Delete(SettingsPath);

        _writer.Apply(Relay());
        _writer.Restore();

        Assert.False(File.Exists(SettingsPath), "the second apply created it, so restoring removes it");
    }

    [Fact]
    public void AnUnreadableJournalDoesNotStopTheClientAndNeverOverwritesTheUsersFile()
    {
        Given("{ \"env\": { \"MY_OWN\": \"x\" } }");
        Directory.CreateDirectory(Path.GetDirectoryName(_journalPath)!);
        File.WriteAllText(_journalPath, "not json at all");

        PluginConfigResult result = _writer.Apply(Relay());

        Assert.Equal(PluginConfigOutcome.Applied, result.Outcome);
        Assert.Equal("x", Env("MY_OWN"));
    }
}
