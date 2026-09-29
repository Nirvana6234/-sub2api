using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexModelCatalogTests
{
    // Shaped like `codex debug models --bundled`: two exotic OpenAI models and one plain one.
    private const string Bundled = """
        {"models":[
          {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","description":"exotic","priority":1,"visibility":"list","tool_mode":"code_mode_only","use_responses_lite":true,"multi_agent_version":"v2","shell_type":"unified_exec","base_instructions":"astra","service_tiers":[{"id":"fast"}],"supported_reasoning_levels":[{"effort":"low","description":"x"}]},
          {"slug":"gpt-5.5","display_name":"GPT-5.5","description":"plain","priority":7,"visibility":"list","tool_mode":null,"use_responses_lite":false,"multi_agent_version":null,"shell_type":"unified_exec","base_instructions":"plain","upgrade":{"model":"gpt-6-astra"},"availability_nux":{"message":"hi"},"model_messages":{"x":1},"service_tiers":[{"id":"fast"}],"default_service_tier":"fast","supported_reasoning_levels":[{"effort":"low","description":"x"},{"effort":"high","description":"y"}]},
          {"slug":"gpt-5.4","display_name":"GPT-5.4","description":"hidden plain","priority":8,"visibility":"hide","tool_mode":null,"use_responses_lite":false,"shell_type":"unified_exec","base_instructions":"hidden"}
        ]}
        """;

    private static JsonObject[] Entries(string catalog) =>
        [.. ((JsonArray)JsonNode.Parse(catalog)!["models"]!).Select(n => (JsonObject)n!)];

    // ---- CodexGroupModels ---------------------------------------------------

    [Fact]
    public void TheDefaultComesFirstAndTheRestKeepAStableOrder()
    {
        var models = CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5", "claude-haiku-4"], preferred: "claude-sonnet-5");

        Assert.Equal(["claude-sonnet-5", "claude-haiku-4", "claude-opus-5"], models!.Models);
        Assert.Equal("claude-sonnet-5", models.DefaultModel);
    }

    [Fact]
    public void APreferenceTheGroupDoesNotServeIsNotTheDefault()
    {
        // The choice may have been made for another group.
        var models = CodexGroupModels.From(["claude-opus-5", "claude-haiku-4"], preferred: "claude-sonnet-5");

        Assert.Equal("claude-haiku-4", models!.DefaultModel);
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    public void NoWhitelistMeansNoModels(string? _)
    {
        Assert.Null(CodexGroupModels.From(null));
        Assert.Null(CodexGroupModels.From([]));
        Assert.Null(CodexGroupModels.From(["", "  "]));
    }

    [Fact]
    public void BlankAndRepeatedEntriesAreIgnoredAndTheMatchIgnoresCase()
    {
        var models = CodexGroupModels.From(["A-model", " ", "a-MODEL", "b-model"]);

        Assert.Equal(2, models!.Models.Count);
        Assert.True(models.Contains("B-MODEL"));
        Assert.False(models.Contains("c-model"));
    }

    [Fact]
    public void TheSignatureChangesExactlyWhenThePickerWould()
    {
        var a = CodexGroupModels.From(["x", "y"], "x");
        var sameAgain = CodexGroupModels.From(["y", "x"], "x");
        var otherDefault = CodexGroupModels.From(["x", "y"], "y");

        Assert.Equal(a!.Signature, sameAgain!.Signature);
        Assert.NotEqual(a.Signature, otherDefault!.Signature);
        Assert.Equal(string.Empty, CodexGroupModels.SignatureOf(null));
        Assert.NotEqual(string.Empty, a.Signature);
    }

    // ---- Building the catalog -----------------------------------------------

    [Fact]
    public void BuildsOneEntryPerModelInOrder_WithThePriorityFollowingIt()
    {
        string? catalog = CodexModelCatalog.Build(Bundled, CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"], "claude-opus-5")!);

        JsonObject[] entries = Entries(catalog!);
        Assert.Equal(["claude-opus-5", "claude-sonnet-5"], entries.Select(e => (string)e["slug"]!));
        Assert.Equal([1, 2], entries.Select(e => (int)e["priority"]!));
        Assert.All(entries, e => Assert.Equal("list", (string)e["visibility"]!));
        Assert.Equal("Claude Opus 5", (string)entries[0]["display_name"]!);
    }

    [Fact]
    public void CopiesThePlainestBundledEntryAndNotTheExoticOne()
    {
        string catalog = CodexModelCatalog.Build(Bundled, CodexGroupModels.From(["claude-sonnet-5"])!)!;

        JsonObject entry = Entries(catalog).Single();

        // The plain one's own text, not the first entry's.
        Assert.Equal("plain", (string)entry["base_instructions"]!);
        Assert.Null(entry["tool_mode"]);
        Assert.False((bool)entry["use_responses_lite"]!);
        Assert.Equal(2, ((JsonArray)entry["supported_reasoning_levels"]!).Count);
    }

    [Fact]
    public void DropsWhatBelongedToTheModelTheEntryWasCopiedFrom()
    {
        string catalog = CodexModelCatalog.Build(Bundled, CodexGroupModels.From(["claude-sonnet-5"])!)!;

        JsonObject entry = Entries(catalog).Single();

        Assert.Null(entry["upgrade"]);
        Assert.Null(entry["availability_nux"]);
        Assert.Null(entry["model_messages"]);
        Assert.Null(entry["default_service_tier"]);
        Assert.Empty((JsonArray)entry["service_tiers"]!);
    }

    [Fact]
    public void WhenEveryBundledModelIsExoticTheCopyStillHasThoseBehavioursSwitchedOff()
    {
        const string allExotic = """
            {"models":[{"slug":"gpt-9","priority":1,"visibility":"list","tool_mode":"code_mode_only","use_responses_lite":true,"multi_agent_version":"v2","multi_agent_reasoning_effort":"xhigh","shell_type":"unified_exec"}]}
            """;

        JsonObject entry = Entries(CodexModelCatalog.Build(allExotic, CodexGroupModels.From(["claude-sonnet-5"])!)!).Single();

        Assert.Null(entry["tool_mode"]);
        Assert.False((bool)entry["use_responses_lite"]!);
        Assert.Null(entry["multi_agent_version"]);
        Assert.Null(entry["multi_agent_reasoning_effort"]);
    }

    [Theory]
    [InlineData("")]
    [InlineData("not json")]
    [InlineData("{}")]
    [InlineData("""{"models":[]}""")]
    [InlineData("""{"models":[{"no_slug":1}]}""")]
    public void GivesNothingWhenThereIsNothingUsableToCopy(string bundled)
    {
        Assert.Null(CodexModelCatalog.Build(bundled, CodexGroupModels.From(["claude-sonnet-5"])!));
    }

    [Theory]
    [InlineData("claude-sonnet-5", "Claude Sonnet 5")]
    [InlineData("gpt_5.5", "Gpt 5.5")]
    [InlineData("kimi-k2.5", "Kimi K2.5")]
    public void NamesAModelFromItsId(string slug, string expected) =>
        Assert.Equal(expected, CodexModelCatalog.DisplayName(slug));

    // ---- Moving a request onto a served model --------------------------------

    private static string? Substitute(string body, CodexGroupModels models, out string? original)
    {
        byte[]? result = CodexModelCatalog.SubstituteUnservedModel(Encoding.UTF8.GetBytes(body), models, out original);
        return result is null ? null : Encoding.UTF8.GetString(result);
    }

    [Fact]
    public void ReplacesOnlyTheTopLevelModelValueAndEveryOtherByteSurvives()
    {
        var models = CodexGroupModels.From(["claude-sonnet-5"])!;
        const string body = "{ \"input\" : [ {\"model\":\"inner\"} ],\n  \"model\":  \"gpt-5.5\" , \"stream\":true,\"tools\":[{\"model\":\"t\"}] }";

        string? rewritten = Substitute(body, models, out string? original);

        Assert.Equal(body.Replace("\"gpt-5.5\"", "\"claude-sonnet-5\""), rewritten);
        Assert.Equal("gpt-5.5", original);
    }

    [Fact]
    public void EscapesTheDefaultWhereJsonRequiresIt()
    {
        var models = CodexGroupModels.From(["odd\"name"])!;

        string? rewritten = Substitute("""{"model":"gpt"}""", models, out _);

        using JsonDocument doc = JsonDocument.Parse(rewritten!);
        Assert.Equal("odd\"name", doc.RootElement.GetProperty("model").GetString());
    }

    [Theory]
    [InlineData("""{"model":"claude-sonnet-5"}""")]
    [InlineData("""{"model":"Claude-Sonnet-5"}""")]
    [InlineData("""{"input":[{"model":"gpt"}]}""")]
    [InlineData("""{"model":null}""")]
    [InlineData("""{"model":{"name":"gpt"}}""")]
    [InlineData("""["model","gpt"]""")]
    [InlineData("not json at all")]
    [InlineData("")]
    public void ReturnsNullWhenThereIsNothingToChange(string body)
    {
        Assert.Null(Substitute(body, CodexGroupModels.From(["claude-sonnet-5"])!, out string? original));
        Assert.Null(original);
    }

    [Fact]
    public void HandlesABodyOfSeveralMegabytes()
    {
        string filler = new('x', 4 * 1024 * 1024);
        string body = "{\"input\":\"" + filler + "\",\"model\":\"gpt-5.5\"}";

        string? rewritten = Substitute(body, CodexGroupModels.From(["claude-sonnet-5"])!, out _);

        Assert.Equal(body.Length - "gpt-5.5".Length + "claude-sonnet-5".Length, rewritten!.Length);
        Assert.EndsWith("\"model\":\"claude-sonnet-5\"}", rewritten, StringComparison.Ordinal);
    }

    // ---- Where the bundled catalog comes from --------------------------------

    [Fact]
    public async Task TheCatalogSourceReadsOncePerBinaryAndAnswersNullWithoutOne()
    {
        int runs = 0;
        string exe = Path.GetTempFileName();
        try
        {
            var source = new CodexBundledCatalogSource(() => exe, (_, _) => { runs++; return Task.FromResult<string?>("{\"models\":[]}"); });

            Assert.Equal("{\"models\":[]}", await source.GetBundledCatalogAsync());
            Assert.Equal("{\"models\":[]}", await source.GetBundledCatalogAsync());
            Assert.Equal(1, runs);

            // An updated Codex is a different file stamp and is read again.
            File.SetLastWriteTimeUtc(exe, DateTime.UtcNow.AddMinutes(5));
            await source.GetBundledCatalogAsync();
            Assert.Equal(2, runs);
        }
        finally
        {
            File.Delete(exe);
        }

        var missing = new CodexBundledCatalogSource(() => null, (_, _) => throw new InvalidOperationException("must not run"));
        Assert.Null(await missing.GetBundledCatalogAsync());
    }

    [Fact]
    public async Task ACatalogThatFailedToLoadIsAskedForAgainNextTime()
    {
        int runs = 0;
        string exe = Path.GetTempFileName();
        try
        {
            var source = new CodexBundledCatalogSource(() => exe, (_, _) => { runs++; return Task.FromResult<string?>(runs == 1 ? null : "{\"models\":[]}"); });

            Assert.Null(await source.GetBundledCatalogAsync());
            Assert.NotNull(await source.GetBundledCatalogAsync());
        }
        finally
        {
            File.Delete(exe);
        }
    }

    /// <summary>
    /// Runs the real installed Codex, when there is one: what it prints must be something the
    /// builder can copy from, on every Codex version this machine happens to have.
    /// </summary>
    [Fact]
    public async Task TheInstalledCodexsOwnCatalogIsSomethingWeCanBuildFrom()
    {
        string? exe = CodexBundledCatalogSource.LocateInstalledCodex();
        if (exe is null)
        {
            return; // No Codex here; nothing to prove.
        }

        string? bundled = await new CodexBundledCatalogSource(() => exe).GetBundledCatalogAsync();

        Assert.NotNull(bundled);
        string? catalog = CodexModelCatalog.Build(bundled, CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"])!);
        Assert.NotNull(catalog);
        Assert.Equal(2, Entries(catalog).Length);
    }
}
