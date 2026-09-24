using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

/// <summary>What the phone is shown for each kind of rollout line.</summary>
public sealed class RolloutProjectorTests
{
    private const string Turn = "turn-1";

    private static List<SyncItem> Project(RolloutBuilder rollout, string? cwd = @"C:\Work\project")
    {
        var state = new ProjectionState(null, cwd);
        var items = new List<SyncItem>();
        long seq = 0;
        foreach (string line in rollout.Text.Split('\n', StringSplitOptions.RemoveEmptyEntries))
        {
            using JsonDocument document = JsonDocument.Parse(line);
            if (RolloutProjector.Project(document.RootElement, seq, state) is SyncItem item)
            {
                items.Add(item);
            }

            seq += line.Length + 1;
        }

        return items;
    }

    private static SyncItem Only(RolloutBuilder rollout) =>
        Assert.Single(Project(rollout), i => i.Kind is not (SyncItemKind.TurnStarted or SyncItemKind.TurnEnded));

    [Fact]
    public void AMessageTypedOnTheDesktopCountsItsImages()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).User(Turn, "u1", "看这张图", @"C:\tmp\a.png", @"C:\tmp\b.png"));

        Assert.Equal(SyncItemKind.User, item.Kind);
        Assert.Equal(UserMessageOrigin.Desktop, item.Origin);
        Assert.Equal("看这张图", item.Text);
        Assert.Equal(2, item.ImageCount);
        Assert.Equal(Turn, item.TurnId);
    }

    /// <summary>The phone's messages arrive this way; the model-facing copy is not shown twice.</summary>
    [Fact]
    public void ADelegatedMessageIsAUserMessageWithItsSource()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).Delegated(Turn, "fco_1", "archived-thread", "请只回复 <OK> & 结束"));

        Assert.Equal(SyncItemKind.User, item.Kind);
        Assert.Equal(UserMessageOrigin.Delegated, item.Origin);
        Assert.Equal("请只回复 <OK> & 结束", item.Text);
        Assert.Equal("archived-thread", item.SourceThreadId);
    }

    /// <summary>
    /// The agent calling send_message_to_thread itself gets a plain result back. Only the
    /// delegation wrapper marks an incoming message.
    /// </summary>
    [Fact]
    public void AToolResultNamedLikeTheSendToolIsNotAMessage()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).ToolOutput(Turn, "fco_2", "send_message_to_thread", """{"threadId":"x"}"""));

        Assert.Equal(SyncItemKind.Tool, item.Kind);
    }

    [Fact]
    public void CommentaryAndFinalAnswerAreDistinguished()
    {
        List<SyncItem> items = Project(new RolloutBuilder().TurnStarted(Turn)
            .Agent(Turn, "m1", "我先看一下文件。")
            .Agent(Turn, "m2", "改好了。", phase: "final_answer"));

        Assert.Equal([SyncItemKind.TurnStarted, SyncItemKind.Progress, SyncItemKind.Reply], items.Select(i => i.Kind));
        Assert.All(items, i => Assert.False(i.PhaseMissing));
    }

    /// <summary>
    /// A message without a phase may be the answer; only the end of the turn tells, so it
    /// is sent as progress and flagged for the phone to decide.
    /// </summary>
    [Fact]
    public void AMessageWithoutAPhaseIsFlagged()
    {
        List<SyncItem> items = Project(new RolloutBuilder().TurnStarted(Turn)
            .Agent(Turn, "m1", "改好了。", phase: null));

        SyncItem message = Assert.Single(items, i => i.ItemId == "m1");
        Assert.Equal(SyncItemKind.Progress, message.Kind);
        Assert.True(message.PhaseMissing);
    }

    /// <summary>Only the summary leaves; the encrypted reasoning never does, and empty summaries are noise.</summary>
    [Fact]
    public void ReasoningShowsOnlyItsSummary()
    {
        List<SyncItem> items = Project(new RolloutBuilder().TurnStarted(Turn)
            .Reasoning(Turn, "r1", "**Planning the edit**")
            .Reasoning(Turn, "r2"));

        SyncItem thinking = Assert.Single(items, i => i.Kind == SyncItemKind.Thinking);
        Assert.Equal("**Planning the edit**", thinking.Text);
        Assert.DoesNotContain(items, i => i.Text?.Contains("gAAAA", StringComparison.Ordinal) == true);
    }

    [Fact]
    public void ACommandShowsWhatWasRunAndTheTailOfItsOutput()
    {
        string output = string.Join('\n', Enumerable.Range(1, 30).Select(n => $"line {n}"));
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).Command(Turn, "call_1", "git status --short", output, exitCode: 1));

        Assert.Equal(SyncItemKind.Command, item.Kind);
        Assert.Equal("git status --short", item.Command);
        Assert.Equal(1, item.ExitCode);
        Assert.Equal(647, item.DurationMs);
        Assert.True(item.OutputTruncated);
        Assert.StartsWith("line 11", item.OutputPreview);
        Assert.EndsWith("line 30", item.OutputPreview);
    }

    [Fact]
    public void WithoutAParsedCommandThePowerShellWrapperIsStripped()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn)
            .CommandWithoutParse(Turn, "call_2", @"C:\pwsh.exe", "-Command", "Get-Date"));

        Assert.Equal("Get-Date", item.Command);
    }

    [Fact]
    public void AFileChangeListsFilesRelativeToTheProjectWithLineCounts()
    {
        const string diff = "@@ -89 +89,2 @@\n 4. 旧的一条\n+5. 新加的一条\n";
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn)
            .FileChange(Turn, "call_3", @"C:\Work\project\docs\评审表.md", diff));

        SyncFileChange file = Assert.Single(item.Files!);
        Assert.Equal(@"docs\评审表.md", file.Path);
        Assert.Equal((1, 0), (file.Added, file.Removed));
    }

    // ---- What is running now ---------------------------------------------------------

    /// <summary>
    /// The completed item only arrives when a command ends; the call line is written as it
    /// starts. Measured: 45 s apart for a 45 s command.
    /// </summary>
    [Fact]
    public void AStartedCommandShowsAsRunningInItsTurn()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).ExecCall("call_9", "Start-Sleep -Seconds 45"));

        Assert.Equal(SyncItemKind.Running, item.Kind);
        Assert.Equal("Start-Sleep -Seconds 45", item.Command);
        Assert.Equal(Turn, item.TurnId);
    }

    [Fact]
    public void InCodeModeTheCommandIsReadFromTheScript()
    {
        const string js = "const r = await tools.exec_command({\n  cmd: \"curl.exe -sI https://example.com\",\n  yield_time_ms: 30000\n});";
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).CodeModeCall("call_c", js));

        Assert.Equal("curl.exe -sI https://example.com", item.Command);
    }

    [Fact]
    public void AScriptWithoutARecognisableCommandStillShowsAsRunning()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).CodeModeCall("call_d", "await tools.view_image({path})"));

        Assert.Equal(SyncItemKind.Running, item.Kind);
        Assert.Null(item.Command);
    }

    /// <summary>
    /// shell_command never produces a completed item — 0 of 9,432 real calls did — so its
    /// result is assembled from the call and the output line, or it would never show.
    /// </summary>
    [Fact]
    public void AShellCommandIsShownFromItsCallAndItsOutput()
    {
        List<SyncItem> items = Project(new RolloutBuilder().TurnStarted(Turn)
            .ShellCall("call_s", "git log --oneline -1")
            .ShellOutput("call_s", "Exit code: 0\nWall time: 0.4 seconds\nOutput:\nf1053740 docs(client): design\n"));

        Assert.Equal([SyncItemKind.TurnStarted, SyncItemKind.Running, SyncItemKind.Command], items.Select(i => i.Kind));
        SyncItem command = items[^1];
        Assert.Equal("call_s", command.ItemId);
        Assert.Equal(Turn, command.TurnId);
        Assert.Equal("git log --oneline -1", command.Command);
        Assert.Equal(0, command.ExitCode);
        Assert.Equal("completed", command.Status);
        Assert.Equal("f1053740 docs(client): design", command.OutputPreview);
    }

    [Theory]
    [InlineData("exec command rejected by user", "declined")]
    [InlineData("execution error: Io(Custom { kind: Other, error: \"x\" })", "failed")]
    public void AShellCommandThatDidNotRunSaysWhy(string output, string status)
    {
        SyncItem command = Project(new RolloutBuilder().TurnStarted(Turn).ShellCall("c", "rm -rf x").ShellOutput("c", output))[^1];

        Assert.Equal(status, command.Status);
        Assert.Null(command.ExitCode);
        Assert.Equal(output, command.OutputPreview);
    }

    /// <summary>Other calls' outputs share the line type; only a pending shell call's output becomes a command.</summary>
    [Fact]
    public void AnOutputWithoutItsShellCallIsIgnored() =>
        Assert.DoesNotContain(
            Project(new RolloutBuilder().TurnStarted(Turn).ExecCall("e", "ls").ShellOutput("e", "Exit code: 0\nOutput:\nx")),
            i => i.Kind == SyncItemKind.Command);

    [Fact]
    public void InCodeModeACommandArgumentIsReadToo()
    {
        const string js = "await tools.shell_command({ command: \"npm test\", workdir: \"C:\\\\w\" })";
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).CodeModeCall("call_e", js));

        Assert.Equal("npm test", item.Command);
    }

    [Fact]
    public void ASubAgentIsAToolLine()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).SubAgent(Turn, "sa1"));

        Assert.Equal(SyncItemKind.Tool, item.Kind);
        Assert.Contains("/root/plugin_filter_tests", item.Text);
    }

    [Fact]
    public void ACallOutsideAnyTurnIsNotShownAsRunning() =>
        Assert.Empty(Project(new RolloutBuilder().ExecCall("call_x", "ls")));

    // ---- Turn boundaries -------------------------------------------------------------

    /// <summary>The upstream error arrives as a JSON string; the user wants the sentence inside.</summary>
    [Fact]
    public void AFailedTurnCarriesTheUpstreamSentence()
    {
        List<SyncItem> items = Project(new RolloutBuilder().TurnStarted(Turn)
            .TurnComplete(Turn, error: """{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}"""));

        SyncItem end = items[^1];
        Assert.Equal(TurnOutcome.Failed, end.Outcome);
        Assert.Equal("The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account.", end.Text);
    }

    [Fact]
    public void AnInterruptedTurnIsAborted()
    {
        SyncItem end = Project(new RolloutBuilder().TurnStarted(Turn).TurnAborted(Turn))[^1];

        Assert.Equal(TurnOutcome.Aborted, end.Outcome);
    }

    [Fact]
    public void CompactionIsOneDividerNotTwo()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).Compaction(Turn, "c1"));

        Assert.Equal(SyncItemKind.Notice, item.Kind);
    }

    [Fact]
    public void BookkeepingLinesProduceNothing() =>
        Assert.Empty(Project(new RolloutBuilder().SessionMeta(@"C:\w").TokenCount()));

    /// <summary>A new item type is shown as such, so an update is noticed instead of lost.</summary>
    [Fact]
    public void AnUnknownItemTypeIsShownAndNamed()
    {
        SyncItem item = Only(new RolloutBuilder().TurnStarted(Turn).Raw(Turn, """{"type":"HologramCall","id":"h1"}"""));

        Assert.Equal(SyncItemKind.Unknown, item.Kind);
        Assert.Equal("HologramCall", item.Text);
    }

    [Fact]
    public void TheWorkingDirectoryFollowsTheRolloutItself()
    {
        List<SyncItem> items = Project(
            new RolloutBuilder().SessionMeta(@"\\?\C:\Other").TurnStarted(Turn)
                .FileChange(Turn, "f1", @"C:\Other\a.txt", "+x\n"),
            cwd: null);

        Assert.Equal("a.txt", items.Single(i => i.Kind == SyncItemKind.FileChange).Files![0].Path);
    }
}
