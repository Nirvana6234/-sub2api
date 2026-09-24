using System.Text;
using LanAi.RelayClient.CodexBinding.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

/// <summary>
/// Paging, following and fetching against a rollout the desktop app is still writing.
/// </summary>
public sealed class SessionContentSyncTests : IDisposable
{
    private const string ThreadId = "thread-1";

    private readonly string _dir = Path.Combine(Path.GetTempPath(), $"rollout-{Guid.NewGuid():N}");
    private readonly string _rollout;
    private readonly SessionContentSync _sync;
    private string _rolloutPathInDatabase;

    public SessionContentSyncTests()
    {
        Directory.CreateDirectory(_dir);
        _rollout = Path.Combine(_dir, "rollout-thread-1.jsonl");
        _rolloutPathInDatabase = _rollout;
        _sync = new SessionContentSync(id => id == ThreadId
            ? new CodexThreadRecord(ThreadId, _rolloutPathInDatabase, @"C:\Work\project", "测试会话", "gpt-5.5", """{"type":"disabled"}""", "never", false)
            : null);
    }

    public void Dispose() => Directory.Delete(_dir, recursive: true);

    private void Write(RolloutBuilder rollout, string tail = "") => File.WriteAllText(_rollout, rollout.Text + tail, new UTF8Encoding(false));

    private void Append(string text) => File.AppendAllText(_rollout, text, new UTF8Encoding(false));

    private static RolloutBuilder Turns(int count)
    {
        var rollout = new RolloutBuilder().SessionMeta(@"C:\Work\project");
        for (int n = 1; n <= count; n++)
        {
            rollout.TurnStarted($"t{n}").User($"t{n}", $"u{n}", $"问题 {n}").Agent($"t{n}", $"a{n}", $"回答 {n}", "final_answer").TurnComplete($"t{n}");
        }

        return rollout;
    }

    // ---- Opening and paging ----------------------------------------------------------

    [Fact]
    public void OpeningShowsTheLatestTurnsAndSaysEarlierOnesExist()
    {
        Write(Turns(12));

        SessionSnapshot snapshot = _sync.Open(ThreadId, turns: 3)!;

        Assert.Equal(["t10", "t11", "t12"], snapshot.Page.Items.Where(i => i.Kind == SyncItemKind.TurnStarted).Select(i => i.TurnId));
        Assert.True(snapshot.Page.HasOlder);
        Assert.Equal(PermissionMode.FullAccess, snapshot.Header.Permission);
        Assert.Null(snapshot.Header.OpenTurnId);
        Assert.Equal(new FileInfo(_rollout).Length, snapshot.Cursor.Offset);
    }

    [Fact]
    public void HistoryPagesBackwardsUntilTheFirstTurn()
    {
        Write(Turns(5));

        SessionPage page = _sync.History(ThreadId, beforeTurnId: "t4", turns: 2)!;
        SessionPage first = _sync.History(ThreadId, beforeTurnId: "t2", turns: 2)!;

        Assert.Equal(["t2", "t3"], page.Items.Where(i => i.Kind == SyncItemKind.TurnStarted).Select(i => i.TurnId));
        Assert.True(page.HasOlder);
        Assert.Equal(["t1"], first.Items.Where(i => i.Kind == SyncItemKind.TurnStarted).Select(i => i.TurnId));
        Assert.False(first.HasOlder);
    }

    /// <summary>A line the desktop app is halfway through writing is neither shown nor skipped.</summary>
    [Fact]
    public void AHalfWrittenLineWaitsForItsEnd()
    {
        var rollout = Turns(1).TurnStarted("t2");
        string half = rollout.HalfLine("t2");
        Write(rollout, half);

        SessionSnapshot snapshot = _sync.Open(ThreadId)!;
        Assert.Equal(new FileInfo(_rollout).Length - Encoding.UTF8.GetByteCount(half), snapshot.Cursor.Offset);
        Assert.Equal("t2", snapshot.Header.OpenTurnId);

        Append("sage\",\"id\":\"a2\",\"content\":[{\"type\":\"Text\",\"text\":\"写完了\"}],\"phase\":\"commentary\"}}}\n");
        SessionDelta delta = _sync.ReadSince(ThreadId, snapshot.Cursor)!;

        SyncItem progress = Assert.Single(delta.Items);
        Assert.Equal("写完了", progress.Text);
        Assert.Equal(new FileInfo(_rollout).Length, delta.Cursor.Offset);
    }

    // ---- Following -------------------------------------------------------------------

    [Fact]
    public void FollowingReturnsOnlyWhatWasAppended()
    {
        Write(Turns(2));
        SessionSnapshot snapshot = _sync.Open(ThreadId)!;

        Append(new RolloutBuilder().TurnStarted("t3").Delegated("t3", "fco_3", "archived", "改一下").Text);
        SessionDelta delta = _sync.ReadSince(ThreadId, snapshot.Cursor)!;
        SessionDelta nothing = _sync.ReadSince(ThreadId, delta.Cursor)!;

        Assert.Equal([SyncItemKind.TurnStarted, SyncItemKind.User], delta.Items.Select(i => i.Kind));
        Assert.Empty(nothing.Items);
        Assert.False(delta.Resync);
    }

    /// <summary>
    /// Measured: a background command's completed item was written after its turn's
    /// task_complete. It must still be filed under its own turn.
    /// </summary>
    [Fact]
    public void AnItemWrittenAfterItsTurnEndedKeepsItsTurn()
    {
        Write(Turns(1).TurnStarted("t2").TurnComplete("t2").Command("t2", "late", "Start-Sleep 45", "step1-done"));

        SessionSnapshot snapshot = _sync.Open(ThreadId)!;

        SyncItem late = Assert.Single(snapshot.Page.Items, i => i.ItemId == "late");
        Assert.Equal("t2", late.TurnId);
    }

    /// <summary>
    /// A shell command's call and its output usually land in different reads. The result
    /// must still be assembled — by the follower that saw the call, and by a phone that
    /// connects in between and never saw it.
    /// </summary>
    [Fact]
    public void AShellCommandSpanningTwoReadsStillShowsItsResult()
    {
        Write(Turns(1).TurnStarted("t2").ShellCall("call_s", "npm test"));
        SessionSnapshot snapshot = _sync.Open(ThreadId)!;
        Assert.Contains(snapshot.Page.Items, i => i.Kind == SyncItemKind.Running && i.Command == "npm test");

        Append(new RolloutBuilder().ShellOutput("call_s", "Exit code: 1\nWall time: 3 seconds\nOutput:\n1 failing\n").Text);

        SessionDelta follower = _sync.ReadSince(ThreadId, snapshot.Cursor)!;
        SessionDelta latecomer = new SessionContentSync(_ => Record()).ReadSince(ThreadId, snapshot.Cursor)!;

        foreach (SessionDelta delta in new[] { follower, latecomer })
        {
            SyncItem command = Assert.Single(delta.Items);
            Assert.Equal(SyncItemKind.Command, command.Kind);
            Assert.Equal("npm test", command.Command);
            Assert.Equal(1, command.ExitCode);
            Assert.Equal("t2", command.TurnId);
        }
    }

    [Fact]
    public void AShellCommandsOutputIsFetchedByItsCallId()
    {
        Write(new RolloutBuilder().TurnStarted("t1").ShellCall("call_s", "dir")
            .ShellOutput("call_s", "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nfull listing\n"));

        SessionDetail detail = _sync.ReadDetail(ThreadId, "t1", "call_s", DetailPart.Output);

        Assert.Equal(DetailOutcome.Found, detail.Outcome);
        Assert.Equal("full listing\n", detail.Text);
    }

    private CodexThreadRecord Record() =>
        new(ThreadId, _rolloutPathInDatabase, @"C:\Work\project", "测试会话", "gpt-5.5", """{"type":"disabled"}""", "never", false);

    [Fact]
    public void ACursorForAnotherFileMeansStartOver()
    {
        Write(Turns(1));

        SessionDelta delta = _sync.ReadSince(ThreadId, new SyncCursor(Path.Combine(_dir, "other.jsonl"), 10))!;

        Assert.True(delta.Resync);
    }

    [Fact]
    public void ACursorPastTheEndMeansStartOver()
    {
        Write(Turns(1));

        SessionDelta delta = _sync.ReadSince(ThreadId, new SyncCursor(_rollout, 10_000_000))!;

        Assert.True(delta.Resync);
    }

    [Fact]
    public void AnUnknownConversationYieldsNothing()
    {
        Assert.Null(_sync.Open("nope"));
        Assert.Equal(DetailOutcome.NotFound, _sync.ReadDetail("nope", "t1", "x", DetailPart.Output).Outcome);
    }

    // ---- Running cards in a page -----------------------------------------------------

    /// <summary>
    /// A page is history: a command shown as running must still be running, i.e. be the
    /// last thing in its open turn.
    /// </summary>
    [Fact]
    public void OnlyTheLastStepOfAnOpenTurnShowsAsRunning()
    {
        Write(new RolloutBuilder()
            .TurnStarted("t1").ExecCall("c1", "old").Command("t1", "c1", "old", "ok").TurnComplete("t1")
            .TurnStarted("t2").ExecCall("c2", "done-already").Command("t2", "c2", "done-already", "ok").ExecCall("c3", "Start-Sleep 60"));

        SessionSnapshot snapshot = _sync.Open(ThreadId)!;

        SyncItem running = Assert.Single(snapshot.Page.Items, i => i.Kind == SyncItemKind.Running);
        Assert.Equal("Start-Sleep 60", running.Command);
        Assert.Equal("t2", snapshot.Header.OpenTurnId);
    }

    // ---- Page limits -----------------------------------------------------------------

    [Fact]
    public void AnOversizedPageDropsWholeEarlyTurnsFirst()
    {
        var rollout = new RolloutBuilder().TurnStarted("t1");
        for (int i = 0; i < 150; i++)
        {
            rollout.Agent("t1", $"a1-{i}", "x");
        }

        rollout.TurnComplete("t1").TurnStarted("t2");
        for (int i = 0; i < 100; i++)
        {
            rollout.Agent("t2", $"a2-{i}", "y");
        }

        Write(rollout.TurnComplete("t2"));

        SessionSnapshot snapshot = _sync.Open(ThreadId)!;

        Assert.All(snapshot.Page.Items, i => Assert.Equal("t2", i.TurnId));
        Assert.True(snapshot.Page.HasOlder);
        Assert.Null(snapshot.Page.TruncatedTurnId);
    }

    [Fact]
    public void ASingleTurnLongerThanAPageKeepsItsLatestItemsAndSaysSo()
    {
        var rollout = new RolloutBuilder().TurnStarted("t1");
        for (int i = 0; i < 250; i++)
        {
            rollout.Agent("t1", $"a{i}", "x");
        }

        Write(rollout.TurnComplete("t1"));

        SessionPage page = _sync.Open(ThreadId)!.Page;

        Assert.Equal(SessionContentSync.MaxItemsPerPage, page.Items.Count);
        Assert.Equal("t1", page.TruncatedTurnId);
        Assert.Equal(SyncItemKind.TurnEnded, page.Items[^1].Kind);
    }

    // ---- Details ---------------------------------------------------------------------

    [Fact]
    public void AnOutputIsSentFromItsEnd()
    {
        string output = new string('a', SessionContentSync.MaxOutputChars) + "THE END";
        Write(new RolloutBuilder().TurnStarted("t1").Command("t1", "c1", "big", output));

        SessionDetail detail = _sync.ReadDetail(ThreadId, "t1", "c1", DetailPart.Output);

        Assert.Equal(DetailOutcome.Found, detail.Outcome);
        Assert.True(detail.Truncated);
        Assert.EndsWith("THE END", detail.Text);
        Assert.Equal(SessionContentSync.MaxOutputChars, detail.Text!.Length);
    }

    [Fact]
    public void ADiffNamesEachFile()
    {
        Write(new RolloutBuilder().TurnStarted("t1").FileChange("t1", "f1", @"C:\Work\project\a.md", "+new\n"));

        SessionDetail detail = _sync.ReadDetail(ThreadId, "t1", "f1", DetailPart.Diff);

        Assert.Contains(@"--- C:\Work\project\a.md", detail.Text);
        Assert.Contains("+new", detail.Text);
    }

    [Fact]
    public void AnItemAskedForUnderTheWrongTurnIsNotFound()
    {
        Write(new RolloutBuilder().TurnStarted("t1").Command("t1", "c1", "x", "y").TurnComplete("t1").TurnStarted("t2"));

        Assert.Equal(DetailOutcome.NotFound, _sync.ReadDetail(ThreadId, "t2", "c1", DetailPart.Output).Outcome);
    }

    [Fact]
    public void AnImageIsReadFromThePathItsOwnRecordNames()
    {
        string image = Path.Combine(_dir, "shot.png");
        File.WriteAllBytes(image, [0x89, 0x50, 0x4E, 0x47]);
        Write(new RolloutBuilder().TurnStarted("t1").User("t1", "u1", "看图", image).ImageView("t1", "v1", new Uri(image).AbsoluteUri));

        SessionDetail attached = _sync.ReadDetail(ThreadId, "t1", "u1", DetailPart.Image);
        SessionDetail viewed = _sync.ReadDetail(ThreadId, "t1", "v1", DetailPart.Image);

        Assert.Equal(DetailOutcome.Found, attached.Outcome);
        Assert.Equal("image/png", attached.MediaType);
        Assert.Equal(4, attached.Bytes!.Length);
        Assert.Equal(DetailOutcome.Found, viewed.Outcome);
        Assert.Equal(DetailOutcome.NotFound, _sync.ReadDetail(ThreadId, "t1", "u1", DetailPart.Image, index: 1).Outcome);
    }

    /// <summary>
    /// The path comes from the rollout, but a rollout can name anything. Only picture
    /// files leave, and never over a network share.
    /// </summary>
    [Theory]
    [InlineData(@"C:\Users\tester\.ssh\id_ed25519")]
    [InlineData(@"C:\Users\tester\.codex\auth.json")]
    [InlineData(@"\\attacker\share\x.png")]
    [InlineData("file://attacker/share/x.png")]
    public void OnlyLocalPicturesAreSent(string path)
    {
        Write(new RolloutBuilder().TurnStarted("t1").ImageView("t1", "v1", path));

        SessionDetail detail = _sync.ReadDetail(ThreadId, "t1", "v1", DetailPart.Image);

        Assert.True(detail.Outcome is DetailOutcome.Refused or DetailOutcome.NotFound, detail.Outcome.ToString());
        Assert.Null(detail.Bytes);
    }

    [Fact]
    public void AScreenshotClearedFromTempIsMissingNotAnError()
    {
        Write(new RolloutBuilder().TurnStarted("t1").User("t1", "u1", "图", Path.Combine(_dir, "gone.png")));

        Assert.Equal(DetailOutcome.Missing, _sync.ReadDetail(ThreadId, "t1", "u1", DetailPart.Image).Outcome);
    }

    [Fact]
    public void AnImageTooLargeToShrinkIsRefused()
    {
        string image = Path.Combine(_dir, "big.png");
        File.WriteAllBytes(image, new byte[SessionContentSync.MaxImageBytes + 1]);
        Write(new RolloutBuilder().TurnStarted("t1").ImageView("t1", "v1", image));

        Assert.Equal(DetailOutcome.Refused, _sync.ReadDetail(ThreadId, "t1", "v1", DetailPart.Image).Outcome);
    }

    // ---- The follower ----------------------------------------------------------------

    [Fact]
    public async Task TheFollowerSignalsWhenTheRolloutGrows()
    {
        Write(Turns(1));
        var signalled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        using var follower = new RolloutFollower(_rollout, () => signalled.TrySetResult(), TimeSpan.FromMilliseconds(100));

        Append(new RolloutBuilder().TurnStarted("t2").Text);

        Assert.Same(signalled.Task, await Task.WhenAny(signalled.Task, Task.Delay(TimeSpan.FromSeconds(5))));
    }
}
