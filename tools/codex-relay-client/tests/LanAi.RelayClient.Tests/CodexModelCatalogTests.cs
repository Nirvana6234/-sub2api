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
    public void TheDefaultComesFirstAndTheRestFollowNewestFirst()
    {
        var models = CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5", "claude-haiku-4"], preferred: "claude-haiku-4");

        Assert.Equal(["claude-haiku-4", "claude-sonnet-5", "claude-opus-5"], models!.Models);
        Assert.Equal("claude-haiku-4", models.DefaultModel);
    }

    [Fact]
    public void APreferenceTheGroupDoesNotServeIsNotTheDefault()
    {
        // The choice may have been made for another group.
        var models = CodexGroupModels.From(["claude-opus-5", "claude-haiku-4"], preferred: "claude-sonnet-5");

        // Not the preference; the newest model of the group instead.
        Assert.Equal("claude-opus-5", models!.DefaultModel);
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
    }

    /// <summary>
    /// A model reached through the bridge ran on Codex's fallback metadata before it was
    /// listed. Diffing the real /responses request with and without the entry showed the gpt
    /// template adding a reasoning effort (which the bridge turns into extended thinking), a
    /// verbosity setting, a freeform apply_patch tool and a tool-search tool. Listing a model
    /// must change none of that.
    /// </summary>
    [Fact]
    public void AnEntryAsksCodexForNothingItDidNotAskForAnUnlistedModel()
    {
        const string generous = """
            {"models":[{"slug":"gpt-5.5","priority":1,"visibility":"list","shell_type":"unified_exec",
              "default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low","description":"x"}],
              "supports_reasoning_effort_updates":true,"support_verbosity":true,"default_verbosity":"low",
              "apply_patch_tool_type":"freeform","web_search_tool_type":"text_and_image","supports_search_tool":true,
              "supports_experimental_context":true,"supports_image_detail_original":true,
              "experimental_supported_tools":["a"],"include_skills_usage_instructions":true,
              "include_plugin_usage_instructions":true,"include_apps_usage_instructions":true,
              "default_reasoning_summary":"none","base_instructions":"b"}]}
            """;

        JsonObject entry = Entries(CodexModelCatalog.Build(generous, CodexGroupModels.From(["claude-sonnet-5"])!)!).Single();

        Assert.Null(entry["default_reasoning_level"]);
        Assert.Empty((JsonArray)entry["supported_reasoning_levels"]!);
        Assert.False((bool)entry["supports_reasoning_effort_updates"]!);
        Assert.False((bool)entry["support_verbosity"]!);
        Assert.Null(entry["default_verbosity"]);
        Assert.Null(entry["apply_patch_tool_type"]);
        Assert.Equal("text", (string)entry["web_search_tool_type"]!);
        Assert.False((bool)entry["supports_search_tool"]!);
        Assert.False((bool)entry["supports_experimental_context"]!);
        Assert.False((bool)entry["supports_image_detail_original"]!);
        Assert.Empty((JsonArray)entry["experimental_supported_tools"]!);
        Assert.False((bool)entry["include_skills_usage_instructions"]!);
        Assert.False((bool)entry["include_plugin_usage_instructions"]!);
        Assert.False((bool)entry["include_apps_usage_instructions"]!);
        Assert.Equal("auto", (string)entry["default_reasoning_summary"]!);
        Assert.True((bool)entry["supports_reasoning_summary_parameter"]!);
    }

    /// <summary>
    /// A GPT model the group serves but Codex has never heard of (gpt-6.1-sol) must still
    /// get the effort picker: the server saw "no reasoning effort" on every request from a
    /// client that listed such a model with its levels emptied.
    /// </summary>
    [Theory]
    [InlineData("gpt-6.1-sol")]
    [InlineData("gpt-5.6-sol")]
    [InlineData("codex-auto-review")]
    [InlineData("o4-mini")]
    public void AnOpenAiFamilyModelKeepsTheReasoningLevelsOfTheTemplate(string slug)
    {
        const string generous = """
            {"models":[{"slug":"gpt-5.5","priority":1,"visibility":"list","shell_type":"unified_exec",
              "default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low","description":"x"},{"effort":"high","description":"y"}],
              "supports_reasoning_effort_updates":true,"support_verbosity":true,"default_verbosity":"low",
              "apply_patch_tool_type":"freeform","base_instructions":"b"}]}
            """;

        JsonObject entry = Entries(CodexModelCatalog.Build(generous, CodexGroupModels.From([slug])!)!).Single();

        Assert.Equal("medium", (string)entry["default_reasoning_level"]!);
        Assert.Equal(2, ((JsonArray)entry["supported_reasoning_levels"]!).Count);
        Assert.True((bool)entry["supports_reasoning_effort_updates"]!);
        // Only the reasoning fields: the rest of the unlisted-model contract is unchanged.
        Assert.False((bool)entry["support_verbosity"]!);
        Assert.Null(entry["apply_patch_tool_type"]);
    }

    [Theory]
    [InlineData("claude-sonnet-5", false)]
    [InlineData("grok-4.7", false)]
    [InlineData("deepseek-v4", false)]
    [InlineData("gpt-6.1-sol", true)]
    [InlineData("GPT-5.5", true)]
    [InlineData("o3", true)]
    [InlineData("omni-model", false)]
    [InlineData("", false)]
    public void OnlyOpenAiFamilyModelsAreRecognised(string slug, bool expected)
    {
        Assert.Equal(expected, CodexModelCatalog.IsOpenAiFamily(slug));
    }

    [Fact]
    public void DropsWhatBelongedToTheModelTheEntryWasCopiedFrom()
    {
        string catalog = CodexModelCatalog.Build(Bundled, CodexGroupModels.From(["claude-sonnet-5"])!)!;

        JsonObject entry = Entries(catalog).Single();

        Assert.Null(entry["upgrade"]);
        Assert.Null(entry["availability_nux"]);
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
    public void APinnedModelReplacesWhateverCodexAsksFor()
    {
        // A Claude group without a whitelist: Codex may ask for the model it started with or one
        // from its own GPT list; either way the request goes to the model chosen on the page.
        CodexGroupModels pinned = CodexGroupModels.Pinned("claude-opus-5");

        Assert.Contains("\"claude-opus-5\"", Substitute("""{"model":"claude-sonnet-5","input":[]}""", pinned, out string? fromClaude), StringComparison.Ordinal);
        Assert.Equal("claude-sonnet-5", fromClaude);
        Assert.Contains("\"claude-opus-5\"", Substitute("""{"model":"gpt-5.5","input":[]}""", pinned, out _), StringComparison.Ordinal);
        Assert.Null(Substitute("""{"model":"claude-opus-5","input":[]}""", pinned, out _));
        Assert.True(pinned.IsPinned);
        Assert.False(CodexGroupModels.From(["claude-opus-5"])!.IsPinned);
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

    // ---- A Claude model's reasoning picker ------------------------------------

    /// <summary>The plain template of the real catalog today: Codex's levels, a default, and two the bridge refuses.</summary>
    private const string WithReasoningLevels = """
        {"models":[{"slug":"gpt-5.5","priority":1,"visibility":"list","shell_type":"unified_exec",
          "default_reasoning_level":"medium",
          "supported_reasoning_levels":[
            {"effort":"low","description":"l"},{"effort":"medium","description":"m"},{"effort":"high","description":"h"},
            {"effort":"xhigh","description":"x"},{"effort":"max","description":"mx"},{"effort":"ultra","description":"u"}],
          "supports_reasoning_effort_updates":true,"base_instructions":"b"}]}
        """;

    [Fact]
    public void AClaudeModelGetsThePickerWhenAThinkingStrengthIsChosenAndOnlyWithLevelsTheBridgeAccepts()
    {
        CodexGroupModels models = CodexGroupModels.From(["claude-sonnet-5"])!.WithClaudeEffort("high");

        JsonObject entry = Entries(CodexModelCatalog.Build(WithReasoningLevels, models)!).Single();

        string[] offered = [.. ((JsonArray)entry["supported_reasoning_levels"]!).Select(l => (string)l!["effort"]!)];
        Assert.Equal(["low", "medium", "high", "xhigh"], offered);
        Assert.Equal("high", (string)entry["default_reasoning_level"]!);
        Assert.True((bool)entry["supports_reasoning_effort_updates"]!);
        // Wording is Codex's own, not rewritten here.
        Assert.Equal("m", (string)((JsonArray)entry["supported_reasoning_levels"]!)[1]!["description"]!);
    }

    [Fact]
    public void WithNoThinkingStrengthAClaudeModelStillGetsNoPicker()
    {
        JsonObject entry = Entries(CodexModelCatalog.Build(WithReasoningLevels, CodexGroupModels.From(["claude-sonnet-5"])!)!).Single();

        Assert.Null(entry["default_reasoning_level"]);
        Assert.Empty((JsonArray)entry["supported_reasoning_levels"]!);
        Assert.False((bool)entry["supports_reasoning_effort_updates"]!);
    }

    [Theory]
    [InlineData("max")]
    [InlineData("ultra")]
    [InlineData("minimal")]
    [InlineData("")]
    [InlineData(null)]
    public void AnEffortTheBridgeWouldRefuseIsNeverOffered(string? effort)
    {
        // A level in the picker that fails every request is worse than none.
        CodexGroupModels models = CodexGroupModels.From(["claude-sonnet-5"])!.WithClaudeEffort(effort);

        Assert.Null(models.ClaudeEffort);
        JsonObject entry = Entries(CodexModelCatalog.Build(WithReasoningLevels, models)!).Single();
        Assert.Empty((JsonArray)entry["supported_reasoning_levels"]!);
    }

    [Fact]
    public void TheClaudeEffortDoesNotTouchAnOpenAiModelsOwnLevels()
    {
        CodexGroupModels models = CodexGroupModels.From(["gpt-6.1-sol", "claude-sonnet-5"])!.WithClaudeEffort("low");

        JsonObject[] entries = Entries(CodexModelCatalog.Build(WithReasoningLevels, models)!);
        JsonObject gpt = entries.Single(e => (string)e["slug"]! == "gpt-6.1-sol");
        JsonObject claude = entries.Single(e => (string)e["slug"]! == "claude-sonnet-5");

        Assert.Equal("medium", (string)gpt["default_reasoning_level"]!);
        Assert.Equal(6, ((JsonArray)gpt["supported_reasoning_levels"]!).Count);
        Assert.Equal("low", (string)claude["default_reasoning_level"]!);
    }

    [Fact]
    public void ThePickerIsOfferedForAPinnedClaudeModelToo()
    {
        JsonObject entry = Entries(CodexModelCatalog.Build(WithReasoningLevels, CodexGroupModels.Pinned("claude-opus-5").WithClaudeEffort("medium"))!).Single();

        Assert.Equal("medium", (string)entry["default_reasoning_level"]!);
        Assert.Equal(4, ((JsonArray)entry["supported_reasoning_levels"]!).Count);
    }

    [Fact]
    public void ChangingTheThinkingStrengthChangesWhatCodexIsToldTheListIs()
    {
        // Codex keeps the list it was given until the version it is told changes.
        CodexGroupModels none = CodexGroupModels.From(["claude-sonnet-5"])!;

        Assert.NotEqual(none.Signature, none.WithClaudeEffort("high").Signature);
        Assert.NotEqual(none.WithClaudeEffort("low").Signature, none.WithClaudeEffort("high").Signature);
        Assert.Equal(none.Signature, none.WithClaudeEffort(null).Signature);
    }

    [Fact]
    public async Task TheStartupProbeLogsWhereItLookedWhenCodexIsNotFound()
    {
        var log = new System.Text.StringBuilder();
        using (ClientLog.Capture(log))
        {
            var source = new CodexBundledCatalogSource(
                () => null,
                (_, _) => throw new InvalidOperationException("must not run"),
                () => "C:\\somewhere\\codex.exe（无）");

            await source.ProbeAsync();
            await source.ProbeAsync(); // Once per source, not once per ask.
        }

        string text = log.ToString();
        Assert.Contains("没有找到 Codex 的命令行程序", text, StringComparison.Ordinal);
        Assert.Contains("C:\\somewhere\\codex.exe（无）", text, StringComparison.Ordinal);
        Assert.Equal(1, System.Text.RegularExpressions.Regex.Matches(text, "没有找到 Codex 的命令行程序").Count);
    }

    [Fact]
    public async Task TheStartupProbeLogsHowManyModelsItReadAndNeverThrows()
    {
        string exe = Path.GetTempFileName();
        try
        {
            var log = new System.Text.StringBuilder();
            using (ClientLog.Capture(log))
            {
                await new CodexBundledCatalogSource(() => exe, (_, _) => Task.FromResult<string?>("{\"models\":[{},{},{}]}")).ProbeAsync();
                await new CodexBundledCatalogSource(() => exe, (_, _) => throw new InvalidOperationException("boom")).ProbeAsync();
            }

            string text = log.ToString();
            Assert.Contains("3 个模型", text, StringComparison.Ordinal);
            Assert.Contains("启动时检测 Codex 自带模型目录失败", text, StringComparison.Ordinal);
        }
        finally
        {
            File.Delete(exe);
        }
    }

    [Fact]
    public async Task WhenTheFirstCopyOfCodexWillNotRunTheNextOneIsUsed()
    {
        string broken = Path.GetTempFileName();
        string working = Path.GetTempFileName();
        try
        {
            var ran = new List<string>();
            var source = new CodexBundledCatalogSource(
                run: (exe, _) =>
                {
                    ran.Add(exe);
                    return Task.FromResult<string?>(exe == broken ? null : "{\"models\":[{}]}");
                },
                locateAll: () => [broken, working]);

            Assert.Equal("{\"models\":[{}]}", await source.GetBundledCatalogAsync());
            Assert.Equal([broken, working], ran);

            // What was read is kept: no second start of anything, neither the broken one nor the good one.
            Assert.Equal("{\"models\":[{}]}", await source.GetBundledCatalogAsync());
            Assert.Equal(2, ran.Count);
        }
        finally
        {
            File.Delete(broken);
            File.Delete(working);
        }
    }

    [Fact]
    public async Task WhenEveryCopyFailsEachIsAskedAgainOnTheNextRequest()
    {
        string flaky = Path.GetTempFileName();
        string other = Path.GetTempFileName();
        try
        {
            var ran = new List<string>();
            int flakyRuns = 0;
            var source = new CodexBundledCatalogSource(
                run: (exe, _) =>
                {
                    ran.Add(exe);
                    if (exe == flaky) { flakyRuns++; }
                    return Task.FromResult<string?>(null);
                },
                locateAll: () => [flaky, other]);

            Assert.Null(await source.GetBundledCatalogAsync());
            Assert.Null(await source.GetBundledCatalogAsync());

            // Nothing was read, so nothing is remembered as good: Codex may simply have been
            // starting, and a request that finds both still failing must not give up for good.
            Assert.Equal(2, flakyRuns);
            Assert.Equal(4, ran.Count);
        }
        finally
        {
            File.Delete(flaky);
            File.Delete(other);
        }
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

    // ---- Order: newest first ---------------------------------------------------------

    [Fact]
    public void WithoutAPreferenceTheDefaultIsTheNewestModel_NotTheAlphabeticallyFirst()
    {
        // The old ordering put gpt-5.2 ahead of gpt-6 (and codex-auto-review ahead of both).
        var models = CodexGroupModels.From(["gpt-5.2", "gpt-5.4-mini", "gpt-5.4", "gpt-5.5", "gpt-6-astra", "gpt-5.10", "gpt-5.6"])!;

        Assert.Equal(["gpt-6-astra", "gpt-5.10", "gpt-5.6", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.2"], models.Models);
        Assert.Equal("gpt-6-astra", models.DefaultModel);
    }

    [Fact]
    public void ThePlainModelComesBeforeItsVariants()
    {
        var models = CodexGroupModels.From(["gpt-5.6-sol", "gpt-5.6", "gpt-5.6-terra"])!;

        Assert.Equal("gpt-5.6", models.Models[0]);
    }

    [Fact]
    public void AWildcardEntryNamesNoModelAndNeverReachesThePicker()
    {
        var models = CodexGroupModels.From(["gpt-5*", "gpt-5.5", "gpt-?"])!;

        Assert.Equal(["gpt-5.5"], models.Models);
        Assert.Null(CodexGroupModels.From(["gpt-5*"]));
        Assert.True(CodexGroupModels.IsConcrete("gpt-5.5"));
        Assert.False(CodexGroupModels.IsConcrete("gpt-*"));
    }
}
