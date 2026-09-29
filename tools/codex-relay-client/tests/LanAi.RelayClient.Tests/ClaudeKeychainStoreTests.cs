using System.Text;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Claude Code's sign-in in the macOS keychain (任务计划 D10, §3.9, A8), with <c>security</c> and
/// the Security framework faked: nothing here runs on a Mac.
/// </summary>
public sealed class ClaudeKeychainStoreTests : IDisposable
{
    private const string Secret = """{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}""";

    private readonly string _directory = Path.Combine(Path.GetTempPath(), "claude-keychain-" + Guid.NewGuid().ToString("N"));
    private DateTimeOffset _now = new(2026, 9, 28, 12, 0, 0, TimeSpan.Zero);

    public ClaudeKeychainStoreTests() => Directory.CreateDirectory(_directory);

    public void Dispose() => Directory.Delete(_directory, recursive: true);

    private sealed class FakeSecurity : ISecurityCommand
    {
        public List<IReadOnlyList<string>> Calls { get; } = [];

        public string Attributes { get; set; } = "\"mdat\"<timedate>=\"20260928120000Z\"";

        public bool ItemExists { get; set; } = true;

        /// <summary>What <c>-w</c> answers: null for a timeout, else the exit code and output.</summary>
        public Func<SecurityResult?> OnRead { get; set; } = () => new SecurityResult(0, Secret + "\n");

        public SecurityResult? Run(IReadOnlyList<string> arguments, TimeSpan timeout)
        {
            Calls.Add(arguments);
            if (!ItemExists)
            {
                return new SecurityResult(44, string.Empty);
            }

            return arguments.Contains("-w") ? OnRead() : new SecurityResult(0, Attributes);
        }

        public int SecretReads => Calls.Count(c => c.Contains("-w"));
    }

    private sealed class FakeWriter : IKeychainItemWriter
    {
        public List<(string Service, string Account, string Data)> Writes { get; } = [];

        public int Status { get; set; }

        public int Replace(string service, string account, byte[] data)
        {
            Writes.Add((service, account, Encoding.UTF8.GetString(data)));
            return Status;
        }
    }

    private (ClaudeKeychainStore Store, FakeSecurity Security, FakeWriter Writer) Create()
    {
        var security = new FakeSecurity();
        var writer = new FakeWriter();
        return (new ClaudeKeychainStore(new ClaudeCredentialFile(_directory), security, writer, "me", () => _now), security, writer);
    }

    [Fact]
    public void LookingNeverReadsTheSecretAndIsNotRepeatedEveryCycle()
    {
        var (store, security, _) = Create();

        Assert.True(store.ReadNeedsConsent);
        Assert.True(store.Exists());
        Assert.True(store.Exists());
        _now = _now.AddMinutes(5).AddSeconds(1);
        Assert.True(store.Exists());

        Assert.Equal(0, security.SecretReads);
        Assert.Equal(2, security.Calls.Count);
        Assert.Equal(["find-generic-password", "-s", "Claude Code-credentials", "-a", "me"], security.Calls[0]);
        Assert.Null(store.ReadCached());
    }

    [Fact]
    public void TheSecretIsReadOnceAndAgainOnlyWhenClaudeCodeChangedIt()
    {
        var (store, security, _) = Create();

        Assert.Equal(Secret, store.Read());
        Assert.False(store.ReadNeedsConsent);
        Assert.Equal(Secret, store.Read());
        Assert.Equal(1, security.SecretReads);

        security.Attributes = "\"mdat\"<timedate>=\"20260928130000Z\"";
        store.Read();
        Assert.Equal(2, security.SecretReads);
        Assert.Equal(Secret, store.ReadCached());
    }

    [Theory]
    [InlineData(null, "一分钟")]
    [InlineData(128, "拒绝")]
    public void APromptNotAnsweredOrRefusedIsSaidInWords(int? exitCode, string words)
    {
        var (store, security, _) = Create();
        security.OnRead = () => exitCode is int code ? new SecurityResult(code, string.Empty) : null;

        var failure = Assert.Throws<LocalProxyCredentialException>(() => store.Read());

        Assert.Contains(words, failure.UserMessage, StringComparison.Ordinal);
        Assert.True(store.ReadNeedsConsent);
    }

    [Fact]
    public void NoItemMeansNoSignIn()
    {
        var (store, security, _) = Create();
        security.ItemExists = false;

        Assert.False(store.Exists());
        Assert.Null(store.Read());
        Assert.Equal(0, security.SecretReads);
    }

    [Fact]
    public void AWriteGoesThroughTheFrameworkNeverAnArgument()
    {
        var (store, security, writer) = Create();
        store.Read();

        store.Write("""{"claudeAiOauth":{"accessToken":"a2","refreshToken":"r2"}}""");

        (string service, string account, string data) = Assert.Single(writer.Writes);
        Assert.Equal("Claude Code-credentials", service);
        Assert.Equal("me", account);
        Assert.Contains("r2", data, StringComparison.Ordinal);
        Assert.DoesNotContain(security.Calls, call => call.Any(argument => argument.Contains("r2", StringComparison.Ordinal)));
        Assert.Contains("r2", store.ReadCached(), StringComparison.Ordinal);
    }

    [Fact]
    public void AFailedWriteIsAnIoErrorSoTheNewPairIsKeptAndRetried()
    {
        var (store, _, writer) = Create();
        writer.Status = -25293;

        Assert.Throws<IOException>(() => store.Write(Secret));
    }

    [Fact]
    public void WhenClaudeCodeFellBackToTheFileTheKeychainIsNotTouched()
    {
        var (store, security, writer) = Create();
        File.WriteAllText(Path.Combine(_directory, ".credentials.json"), Secret);

        Assert.False(store.ReadNeedsConsent);
        Assert.False(store.ReadMayBlock);
        Assert.True(store.Exists());
        Assert.Equal(Secret, store.Read());
        store.Write(Secret.Replace("\"r\"", "\"r3\""));

        Assert.Empty(security.Calls);
        Assert.Empty(writer.Writes);
        Assert.Contains("r3", File.ReadAllText(Path.Combine(_directory, ".credentials.json")), StringComparison.Ordinal);
    }

    [Fact]
    public async Task ThroughTheAccountTheFirstReadIsWhenItIsSwitchedOn()
    {
        var (store, security, _) = Create();
        var account = new LocalClaudeAccount(store, new FakeTokenRefresher(), () => _now);

        Assert.True(account.Probe().IsUsable);
        Assert.Equal(0, security.SecretReads);

        LanAi.RelayClient.Server.LocalProxyCredential credential =
            await account.GetAsync(LocalMachineAccounts.ClaudeId, forceRefresh: false, CancellationToken.None);

        Assert.Equal("a", credential.AccessToken);
        Assert.Equal(1, security.SecretReads);
    }
}
