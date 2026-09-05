using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class AnnouncementNotifyStateStoreTests : IDisposable
{
    private readonly string _directory = Path.Combine(
        Path.GetTempPath(),
        "lanai-announce-" + Guid.NewGuid().ToString("N"));

    private string FilePath => Path.Combine(_directory, "announcements.json");

    [Fact]
    public void AnAccountWithNoRecordReadsAsNullRatherThanEmpty()
    {
        AnnouncementNotifyStateStore store = Build();

        // "No record yet" and "a record that is currently empty" are reported
        // apart so a read failure can never be mistaken for a real empty set.
        Assert.Null(store.Load("acct"));

        store.Save("acct", []);
        Assert.NotNull(store.Load("acct"));
        Assert.Empty(store.Load("acct")!);
    }

    [Fact]
    public void RecordsAreKeptApartPerAccount()
    {
        AnnouncementNotifyStateStore store = Build();

        store.Save("first", [1, 2]);
        store.Save("second", [9]);

        Assert.Equal([1L, 2L], store.Load("first"));
        Assert.Equal([9L], store.Load("second"));
    }

    [Fact]
    public void ARecordFromAnotherRelayIsDiscarded()
    {
        Build("https://one.test/").Save("acct", [1, 2]);

        // Announcement ids are allocated per relay database, so carrying them
        // across servers would suppress an unrelated announcement.
        Assert.Null(Build("https://two.test/").Load("acct"));
    }

    [Fact]
    public void TheAccountKeyDoesNotLeakTheEmailAddress()
    {
        string key = AnnouncementNotifyStateStore.AccountKey("Ann@Example.com");

        Assert.DoesNotContain("ann", key, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("@", key, StringComparison.Ordinal);

        // Case and surrounding whitespace must not split one account into two.
        Assert.Equal(key, AnnouncementNotifyStateStore.AccountKey("  ann@example.com "));
        Assert.NotEqual(key, AnnouncementNotifyStateStore.AccountKey("other@example.com"));
    }

    [Fact]
    public void OnlyTheMostRecentAccountsKeepARecord()
    {
        var clock = new TestClock();
        AnnouncementNotifyStateStore store = Build(clock: clock.Read);

        for (int i = 0; i < 10; i++)
        {
            store.Save("acct-" + i, [i]);
            clock.Advance(TimeSpan.FromMinutes(1));
        }

        Assert.Null(store.Load("acct-0"));
        Assert.Null(store.Load("acct-1"));
        Assert.Equal([9L], store.Load("acct-9"));
    }

    [Fact]
    public void ACorruptFileReadsAsNoRecordInsteadOfThrowing()
    {
        Directory.CreateDirectory(_directory);
        File.WriteAllText(FilePath, "{ not json");

        AnnouncementNotifyStateStore store = Build();

        Assert.Null(store.Load("acct"));

        // And it must be able to write over the damage rather than staying stuck.
        store.Save("acct", [3]);
        Assert.Equal([3L], store.Load("acct"));
    }

    private AnnouncementNotifyStateStore Build(
        string serverAddress = "https://relay.test/",
        Func<DateTimeOffset>? clock = null) =>
        new(serverAddress, FilePath, clock);

    public void Dispose()
    {
        try
        {
            if (Directory.Exists(_directory))
            {
                Directory.Delete(_directory, recursive: true);
            }
        }
        catch (IOException)
        {
            // A leftover temp directory is not worth failing a test run over.
        }
    }
}
