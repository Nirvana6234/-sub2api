using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The preference is scoped to a relay, on the same reasoning as the session store.
/// </summary>
public sealed class GroupPreferenceStoreTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"lanai-prefs-{Guid.NewGuid():N}.json");

    public void Dispose()
    {
        if (File.Exists(_path))
        {
            File.Delete(_path);
        }
    }

    [Fact]
    public void APreferenceSurvivesARoundTripOnTheSameServer()
    {
        new GroupPreferenceStore("https://relay.test/", _path).Save(7);

        Assert.Equal(7, new GroupPreferenceStore("https://relay.test/", _path).Load());
    }

    [Fact]
    public void APreferenceFromAnotherRelayIsNotApplied()
    {
        // Group ids are allocated per relay database, so id 7 on a development
        // server and id 7 in production are different groups. Carrying the value
        // across would silently bill the user on something they never chose.
        new GroupPreferenceStore("http://127.0.0.1:8080/", _path).Save(7);

        Assert.Null(new GroupPreferenceStore("https://relay.example.com/", _path).Load());
    }

    [Fact]
    public void AddressComparisonIgnoresCase()
    {
        new GroupPreferenceStore("https://Relay.Test/", _path).Save(7);

        Assert.Equal(7, new GroupPreferenceStore("https://relay.test/", _path).Load());
    }

    [Fact]
    public void APreferenceWrittenBeforeScopingExistedIsDiscarded()
    {
        // Such a file cannot be attributed to any relay, so it is dropped rather
        // than guessed at — the user re-picks once, instead of being silently put
        // on whatever group happens to hold that id here.
        File.WriteAllText(_path, """{"groupId":3}""");

        Assert.Null(new GroupPreferenceStore("https://relay.test/", _path).Load());
    }

    [Fact]
    public void AMissingFileMeansNoPreference()
    {
        Assert.Null(new GroupPreferenceStore("https://relay.test/", _path).Load());
    }

    [Fact]
    public void ACorruptFileDegradesToNoPreference()
    {
        File.WriteAllText(_path, "{not json");

        Assert.Null(new GroupPreferenceStore("https://relay.test/", _path).Load());
    }
}
