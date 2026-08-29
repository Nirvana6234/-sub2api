using System.IO;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>Recognising the client's own key by name, never by value (F3.2.1).</summary>
public sealed class ManagedKeyNamingTests
{
    private const string Mine = "install1";
    private const string Earlier = "install0";

    private static ManagedKeyNaming Naming(string installId = Mine) => new(new FixedInstallId(installId));

    private static string NameFor(string installId) => ManagedKeyNaming.MachinePrefix() + installId;

    private static RelayApiKey Key(long id, string name, DateTimeOffset? expiresAt = null) =>
        new() { Id = id, Name = name, ExpiresAt = expiresAt };

    [Fact]
    public void TheKeyNameCarriesBothTheMachineAndTheInstallation()
    {
        string name = Naming().KeyName();

        Assert.StartsWith("共飞直连客户端-", name, StringComparison.Ordinal);
        Assert.EndsWith("-" + Mine, name, StringComparison.Ordinal);
    }

    [Fact]
    public void AKeyTheUserMadeByHandIsNeverAdopted()
    {
        // Adopting it would let a group switch silently change how the user's own
        // key bills, and later let the lease machinery revoke it.
        Assert.Null(Naming().FindCurrent([Key(1, "我自己建的"), Key(2, "another app key")]));
    }

    [Fact]
    public void AKeyNamedForAnotherMachineIsNotOurs()
    {
        // One account may run the client on several machines; each needs its own
        // lease, or signing out on one revokes authorization on the other.
        Assert.Null(Naming().FindCurrent([Key(1, "共飞直连客户端-别的机器-install1")]));
    }

    [Fact]
    public void ThisInstallationsOwnKeyBeatsAnEarlierInstallationsLease()
    {
        // Even when the older lease runs longer. The client issues and renews under
        // its own name, so preferring the other one would leave it renewing a key
        // it will never write into auth.json.
        DateTimeOffset now = DateTimeOffset.UtcNow;

        RelayApiKey? found = Naming().FindCurrent(
        [
            Key(1, NameFor(Earlier), now.AddDays(5)),
            Key(2, NameFor(Mine), now.AddHours(1)),
        ]);

        Assert.Equal(2, found!.Id);
    }

    [Fact]
    public void AnEarlierInstallationsLeaseIsAdoptedWhenThereIsNoneOfOurOwn()
    {
        // It authorises the same account against the same relay, so using it beats
        // issuing a second key and leaving the first to expire unnoticed.
        DateTimeOffset now = DateTimeOffset.UtcNow;

        RelayApiKey? found = Naming().FindCurrent([Key(1, NameFor(Earlier), now.AddHours(3))]);

        Assert.Equal(1, found!.Id);
    }

    [Fact]
    public void TheLeaseRunningLongestWinsAmongEqualCandidates()
    {
        DateTimeOffset now = DateTimeOffset.UtcNow;

        RelayApiKey? found = Naming().FindCurrent(
        [
            Key(1, NameFor("older1"), now.AddHours(1)),
            Key(2, NameFor("older2"), now.AddHours(20)),
        ]);

        Assert.Equal(2, found!.Id);
    }

    [Fact]
    public void AKeyWithNoExpiryLosesToAProperlyLeasedOne()
    {
        // Under F3.2 a managed key without an expiry is a defect — an authorization
        // that outlives the client, most likely left by an update that cleared
        // expires_at. Preferring it would make the client adopt precisely the key
        // the lease model exists to prevent, and renew it indefinitely.
        DateTimeOffset now = DateTimeOffset.UtcNow;

        RelayApiKey? found = Naming().FindCurrent(
        [
            Key(1, NameFor("older1"), expiresAt: null),
            Key(2, NameFor("older2"), now.AddHours(6)),
        ]);

        Assert.Equal(2, found!.Id);
    }

    [Fact]
    public void AnUnexpiringKeyIsStillReturnedWhenItIsTheOnlyOne()
    {
        // Reporting "no key" would make the client issue a second one and leave the
        // defective lease in place, unnoticed.
        RelayApiKey? found = Naming().FindCurrent([Key(1, NameFor(Mine))]);

        Assert.Equal(1, found!.Id);
    }

    [Fact]
    public void OrphansAreEveryLeaseOnThisMachineThatIsNotOurs()
    {
        // F3.2.1 wants these found so a crashed run's leftovers can be cleaned up
        // rather than accumulating in the user's key list.
        RelayApiKey[] orphans = Naming().FindOrphans(
        [
            Key(1, NameFor(Mine)),
            Key(2, NameFor(Earlier)),
            Key(3, "共飞直连客户端-别的机器-x"),
            Key(4, "用户自己的 key"),
        ]).ToArray();

        Assert.Equal([2L], orphans.Select(k => k.Id));
    }

    [Fact]
    public void AGeneratedInstallIdIsWellFormedAndStable()
    {
        string path = Path.Combine(Path.GetTempPath(), $"lanai-install-{Guid.NewGuid():N}", "install-id");
        try
        {
            string first = new InstallId(path).Get();
            string second = new InstallId(path).Get();

            Assert.True(InstallId.IsWellFormed(first));
            Assert.Equal(first, second);
        }
        finally
        {
            string? directory = Path.GetDirectoryName(path);
            if (directory is not null && Directory.Exists(directory))
            {
                Directory.Delete(directory, recursive: true);
            }
        }
    }

    [Fact]
    public void ACorruptInstallIdFileYieldsAFreshOneRatherThanBadKeyNames()
    {
        // Whitespace or punctuation from a damaged file would end up inside a key
        // name and stop the naming rule matching — at which point the client
        // silently loses track of its own lease.
        string directory = Path.Combine(Path.GetTempPath(), $"lanai-install-{Guid.NewGuid():N}");
        string path = Path.Combine(directory, "install-id");
        Directory.CreateDirectory(directory);
        File.WriteAllText(path, "not a valid id!!");

        try
        {
            Assert.True(InstallId.IsWellFormed(new InstallId(path).Get()));
        }
        finally
        {
            Directory.Delete(directory, recursive: true);
        }
    }
}

/// <summary>An install id the test dictates.</summary>
internal sealed class FixedInstallId(string value) : IInstallIdProvider
{
    public string Get() => value;
}
