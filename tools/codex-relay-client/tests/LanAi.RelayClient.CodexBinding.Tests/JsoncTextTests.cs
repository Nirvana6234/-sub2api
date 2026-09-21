using System.Text.Json;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// The scanner and the three edits VS Code's settings file is subjected to. The interesting
/// cases are the ones a parser would get right and a text edit would get wrong: a string
/// that looks like a key, a comment that looks like a property, a comma in the wrong place.
/// </summary>
public sealed class JsoncTextTests
{
    private const string Key = "claudeCode.disableLoginPrompt";

    /// <summary>The way VS Code reads the file: comments and trailing commas are fine.</summary>
    private static JsonDocument Parse(string text) => JsonDocument.Parse(
        text.TrimStart('\xFEFF'),
        new JsonDocumentOptions { CommentHandling = JsonCommentHandling.Skip, AllowTrailingCommas = true });

    private static string Set(string text) =>
        Assert.IsType<JsoncText.Edit>(JsoncText.Set(text, Key, "true")).Text;

    private static void AssertKeyIsTrueOnceAtTopLevel(string text)
    {
        using JsonDocument document = Parse(text);
        Assert.True(document.RootElement.GetProperty(Key).GetBoolean());
        Assert.Equal(1, document.RootElement.EnumerateObject().Count(p => p.Name == Key));
    }

    // ---- Scanning -----------------------------------------------------------------------

    [Fact]
    public void ItFindsTheTopLevelPropertiesAndTheirValues()
    {
        const string text = "{\n  \"a\": 1,\n  \"b\": \"two\",\n  \"c\": { \"d\": [1, 2] }\n}";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        Assert.Equal(["a", "b", "c"], document.Properties.Select(p => p.Name));
        JsoncText.Property c = document.Properties[2];
        Assert.Equal("{ \"d\": [1, 2] }", text[c.ValueStart..c.ValueEnd]);
        Assert.Equal(-1, c.CommaIndex);
        Assert.True(document.Properties[0].CommaIndex > 0);
    }

    [Fact]
    public void AUrlInAStringIsNotAComment()
    {
        const string text = "{\n  \"http.proxy\": \"http://127.0.0.1:7897\",\n  \"x\": 1\n}";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        Assert.Equal(["http.proxy", "x"], document.Properties.Select(p => p.Name));
    }

    [Fact]
    public void ABraceOrColonInsideAStringDoesNotConfuseIt()
    {
        const string text = "{ \"a\": \"}{ : , \\\" \", \"b\": 2 }";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        Assert.Equal(["a", "b"], document.Properties.Select(p => p.Name));
    }

    [Fact]
    public void ACommentThatLooksLikeAPropertyIsNotOne()
    {
        const string text = "{\n  // \"claudeCode.disableLoginPrompt\": false,\n  /* \"other\": 1, */\n  \"real\": 1\n}";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        Assert.Equal(["real"], document.Properties.Select(p => p.Name));
    }

    [Fact]
    public void AKeyOnlyInsideANestedObjectIsNotATopLevelProperty()
    {
        const string text = "{ \"editor\": { \"claudeCode.disableLoginPrompt\": false } }";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        Assert.Null(JsoncText.Find(document, Key));
    }

    [Fact]
    public void TheLastOfADuplicatedKeyIsTheOneThatCounts()
    {
        const string text = "{ \"k\": 1, \"k\": 2 }";

        JsoncText.Document document = Assert.IsType<JsoncText.Document>(JsoncText.Scan(text));

        JsoncText.Property found = Assert.IsType<JsoncText.Property>(JsoncText.Find(document, "k"));
        Assert.Equal("2", text[found.ValueStart..found.ValueEnd]);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("[1, 2]")]
    [InlineData("\"just a string\"")]
    [InlineData("{ \"a\": 1")]
    [InlineData("{ \"a\" 1 }")]
    [InlineData("{ \"a\": }")]
    [InlineData("{ a: 1 }")]
    [InlineData("{ \"a\": 1 } { \"b\": 2 }")]
    [InlineData("{ \"a\": 1 } trailing")]
    [InlineData("{ \"a\": \"unterminated }")]
    [InlineData("{ /* never closed \"a\": 1 }")]
    [InlineData("{ \"a\": 1,, \"b\": 2 }")]
    public void ADocumentThatIsNotOneObjectIsRefused(string text)
    {
        Assert.Null(JsoncText.Scan(text));
    }

    [Fact]
    public void TrailingCommasAndCommentsAreAccepted()
    {
        Assert.NotNull(JsoncText.Scan("// header\n{\n  \"a\": 1, // one\n  \"b\": 2,\n}\n// footer\n"));
    }

    // ---- Adding the setting -------------------------------------------------------------

    public static TheoryData<string> Shapes() => new()
    {
        "{}",
        "{ }",
        "{\n}",
        "{\n}\n",
        "{}\n",
        "{ \"a\": 1 }",
        "{\"a\":1}",
        "{\n    \"a\": 1\n}",
        "{\n    \"a\": 1,\n    \"b\": \"two\"\n}\n",
        "{\n    \"a\": 1,\n    \"b\": \"two\",\n}\n",
        "{\r\n    \"a\": 1,\r\n    \"b\": 2\r\n}\r\n",
        "{\n\t\"a\": 1,\n\t\"b\": 2\n}\n",
        "{\n  \"a\": 1,\n  \"b\": 2\n}",
        "// user settings\n{\n    \"a\": 1 // keep this\n}\n",
        "{\n    \"a\": 1, // one\n    \"b\": 2 // last\n}\n",
        "{\n    /* only a comment */\n}\n",
        "{\n    \"a\": 1\n    // \"b\": 2\n}\n",
        "{\n    \"nested\": { \"x\": [1, 2, { \"y\": 3 }] },\n    \"url\": \"http://example.com/a//b\"\n}\n",
        "\xFEFF{\n    \"a\": 1\n}\n",
    };

    [Theory]
    [MemberData(nameof(Shapes))]
    public void AddingTheSettingLeavesAValidDocumentWithItOnceAtTheTopLevel(string original)
    {
        string edited = Set(original);

        AssertKeyIsTrueOnceAtTopLevel(edited);
    }

    [Theory]
    [MemberData(nameof(Shapes))]
    public void EverythingTheUserHadIsStillThereAfterAddingTheSetting(string original)
    {
        string edited = Set(original);

        using JsonDocument before = Parse(original.TrimStart('\xFEFF'));
        using JsonDocument after = Parse(edited.TrimStart('\xFEFF'));
        foreach (JsonProperty property in before.RootElement.EnumerateObject())
        {
            Assert.Equal(property.Value.GetRawText(), after.RootElement.GetProperty(property.Name).GetRawText());
        }

        // Comments are not part of the parsed model, so they are checked as text.
        foreach (string comment in new[] { "// user settings", "// keep this", "// one", "// last", "/* only a comment */", "// \"b\": 2" })
        {
            if (original.Contains(comment, StringComparison.Ordinal))
            {
                Assert.Contains(comment, edited, StringComparison.Ordinal);
            }
        }
    }

    [Fact]
    public void ANewLineFollowsTheFilesIndentationAndLineEndings()
    {
        string tabs = Set("{\n\t\"a\": 1\n}\n");
        Assert.Contains("\n\t\"claudeCode.disableLoginPrompt\": true\n", tabs, StringComparison.Ordinal);

        string crlf = Set("{\r\n  \"a\": 1\r\n}\r\n");
        Assert.Contains("\r\n  \"claudeCode.disableLoginPrompt\": true\r\n", crlf, StringComparison.Ordinal);
        Assert.DoesNotContain("\n\n", crlf.Replace("\r\n", string.Empty), StringComparison.Ordinal);

        string twoSpaces = Set("{\n  \"a\": 1\n}\n");
        Assert.Contains("\n  \"claudeCode.disableLoginPrompt\": true\n", twoSpaces, StringComparison.Ordinal);
    }

    /// <summary>
    /// A comment written about a property stays next to that property. The comma the
    /// property now needs goes before the comment, not after it.
    /// </summary>
    [Fact]
    public void TheCommaGoesBeforeATrailingCommentNotAfterIt()
    {
        string edited = Set("{\n    \"a\": 1 // about a\n}\n");

        Assert.Contains("\"a\": 1, // about a", edited, StringComparison.Ordinal);
        AssertKeyIsTrueOnceAtTopLevel(edited);
    }

    [Fact]
    public void ATrailingCommaStyleIsFollowed()
    {
        string edited = Set("{\n    \"a\": 1,\n}\n");

        Assert.Contains("\"claudeCode.disableLoginPrompt\": true,", edited, StringComparison.Ordinal);
    }

    [Fact]
    public void ANoTrailingCommaStyleIsFollowed()
    {
        string edited = Set("{\n    \"a\": 1\n}\n");

        Assert.DoesNotContain("true,", edited, StringComparison.Ordinal);
    }

    [Fact]
    public void ASingleLineObjectStaysOnOneLine()
    {
        string edited = Set("{ \"a\": 1 }");

        Assert.DoesNotContain("\n", edited, StringComparison.Ordinal);
        AssertKeyIsTrueOnceAtTopLevel(edited);
    }

    // ---- Changing a value that is already there ----------------------------------------

    [Fact]
    public void AnExistingValueIsReplacedInPlaceAndReportedAsWhatItWas()
    {
        JsoncText.Edit edit = Assert.IsType<JsoncText.Edit>(
            JsoncText.Set("{\n    // login\n    \"claudeCode.disableLoginPrompt\": false, // was off\n    \"a\": 1\n}\n", Key, "true"));

        Assert.False(edit.Inserted);
        Assert.Equal("false", edit.PreviousValue);
        Assert.Equal("{\n    // login\n    \"claudeCode.disableLoginPrompt\": true, // was off\n    \"a\": 1\n}\n", edit.Text);
    }

    [Fact]
    public void OnlyTheLastOfADuplicatedKeyIsReplaced()
    {
        JsoncText.Edit edit = Assert.IsType<JsoncText.Edit>(
            JsoncText.Set("{ \"claudeCode.disableLoginPrompt\": 1, \"claudeCode.disableLoginPrompt\": 2 }", Key, "true"));

        Assert.Equal("{ \"claudeCode.disableLoginPrompt\": 1, \"claudeCode.disableLoginPrompt\": true }", edit.Text);
        Assert.Equal("2", edit.PreviousValue);
    }

    [Fact]
    public void AKeyThatExistsOnlyInACommentOrANestedObjectIsAddedNotReplaced()
    {
        JsoncText.Edit edit = Assert.IsType<JsoncText.Edit>(JsoncText.Set(
            "{\n    // \"claudeCode.disableLoginPrompt\": false\n    \"n\": { \"claudeCode.disableLoginPrompt\": false }\n}\n",
            Key,
            "true"));

        Assert.True(edit.Inserted);
        Assert.Contains("// \"claudeCode.disableLoginPrompt\": false", edit.Text, StringComparison.Ordinal);
        Assert.Contains("\"n\": { \"claudeCode.disableLoginPrompt\": false }", edit.Text, StringComparison.Ordinal);
    }

    [Fact]
    public void ADocumentItCannotReadIsNotEdited()
    {
        Assert.Null(JsoncText.Set("{ \"a\": ", Key, "true"));
    }

    // ---- Taking it out again ------------------------------------------------------------

    [Theory]
    [MemberData(nameof(Shapes))]
    public void UndoingAnAddedSettingLeavesAValidDocumentWithoutIt(string original)
    {
        string edited = Set(original);

        string? undone = JsoncText.Unset(edited, Key, "true", previousValue: null);

        Assert.NotNull(undone);
        using JsonDocument document = Parse(undone!.TrimStart('\xFEFF'));
        Assert.False(document.RootElement.TryGetProperty(Key, out _));
    }

    /// <summary>
    /// Where the property was added on a line of its own and the previous one already had a
    /// trailing comma, or where there were no others, undoing it gives the original text.
    /// </summary>
    [Theory]
    [InlineData("{\n}\n")]
    [InlineData("{\n    \"a\": 1,\n}\n")]
    [InlineData("{\n    \"a\": 1,\n    \"b\": 2,\n}\n")]
    [InlineData("{\r\n    \"a\": 1,\r\n}\r\n")]
    public void UndoingBringsBackTheOriginalTextWhereItCan(string original)
    {
        string edited = Set(original);

        Assert.Equal(original, JsoncText.Unset(edited, Key, "true", previousValue: null));
    }

    [Fact]
    public void UndoingAReplacedValueBringsBackTheOldOne()
    {
        const string original = "{\n    \"claudeCode.disableLoginPrompt\": false, // was off\n    \"a\": 1\n}\n";
        JsoncText.Edit edit = Assert.IsType<JsoncText.Edit>(JsoncText.Set(original, Key, "true"));

        Assert.Equal(original, JsoncText.Unset(edit.Text, Key, "true", edit.PreviousValue));
    }

    [Fact]
    public void AValueTheUserChangedInTheMeantimeIsNotTouched()
    {
        string edited = Set("{\n    \"a\": 1\n}\n").Replace("true", "false", StringComparison.Ordinal);

        Assert.Null(JsoncText.Unset(edited, Key, "true", previousValue: null));
    }

    [Fact]
    public void ASettingThatIsAlreadyGoneIsNotAnError()
    {
        Assert.Null(JsoncText.Unset("{\n    \"a\": 1\n}\n", Key, "true", previousValue: null));
    }

    [Fact]
    public void ThingsTheUserAddedAfterwardsSurviveTheUndo()
    {
        string edited = Set("{\n    \"a\": 1\n}\n");
        int close = edited.LastIndexOf('}');
        string withMore = edited[..close] + "    \"mine\": \"added later\"\n" + edited[close..];
        withMore = withMore.Replace("true\n    \"mine\"", "true,\n    \"mine\"", StringComparison.Ordinal);

        string? undone = JsoncText.Unset(withMore, Key, "true", previousValue: null);

        Assert.NotNull(undone);
        using JsonDocument document = Parse(undone!);
        Assert.Equal("added later", document.RootElement.GetProperty("mine").GetString());
        Assert.False(document.RootElement.TryGetProperty(Key, out _));
    }
}
