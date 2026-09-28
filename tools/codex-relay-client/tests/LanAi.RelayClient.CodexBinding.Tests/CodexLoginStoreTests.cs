using System.Text.Json.Nodes;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

/// <summary>
/// The user's own ChatGPT sign-in while the client holds it: found wherever it lives, a
/// newer one replacing the old, refreshed tokens written back to every copy — because an
/// OpenAI refresh token works once, and handing back a spent one signs the user out.
/// </summary>
public sealed class CodexLoginStoreTests : IDisposable
{
    private readonly string _home = Path.Combine(Path.GetTempPath(), $"codex-home-{Guid.NewGuid():N}");
    private readonly string _snapshotRoot = Path.Combine(Path.GetTempPath(), $"codex-snapshot-{Guid.NewGuid():N}");
    private readonly CodexPaths _paths;
    private readonly CodexConfigWriter _writer;
    private static readonly DateTimeOffset RefreshedAt = new(2026, 9, 28, 1, 2, 3, TimeSpan.Zero);

    public CodexLoginStoreTests()
    {
        _paths = new CodexPaths(_home);
        var protector = new FakeSnapshotProtector();
        _writer = new CodexConfigWriter(
            _paths,
            new CodexAuthSnapshot(protector, Path.Combine(_snapshotRoot, "legacy-auth.json")),
            new CodexFileSnapshot(_paths, _snapshotRoot, protector));
    }

    public void Dispose()
    {
        foreach (string dir in new[] { _home, _snapshotRoot })
        {
            if (Directory.Exists(dir))
            {
                Directory.Delete(dir, recursive: true);
            }
        }
    }

    private static string SignIn(string name) =>
        $$"""{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":"id-{{name}}","access_token":"access-{{name}}","refresh_token":"refresh-{{name}}","account_id":"acct-{{name}}"},"last_refresh":"2026-09-01T00:00:00Z"}""";

    private void GivenAuth(string json)
    {
        Directory.CreateDirectory(_home);
        File.WriteAllText(_paths.AuthPath, json);
    }

    private JsonObject Auth() => (JsonObject)JsonNode.Parse(File.ReadAllText(_paths.AuthPath))!;

    private static string Token(JsonObject auth, string name) => auth["tokens"]![name]!.GetValue<string>();

    [Fact]
    public void ASignInMadeWhileOnTheRelayIsWhatGoesBack()
    {
        GivenAuth(SignIn("old"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        // codex login rewrites the file; the route guard then puts the relay back.
        GivenAuth(SignIn("new"));
        _writer.Apply("sk-relay", "https://relay.test/v1");
        Assert.Equal("sk-relay", Auth()["OPENAI_API_KEY"]!.GetValue<string>());

        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal("refresh-new", Token(Auth(), "refresh_token"));
    }

    [Fact]
    public void ASignInMadeAfterStartingWithoutOneIsKept()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth(SignIn("new"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.RestoreOriginalFiles());
        Assert.Equal("access-new", Token(Auth(), "access_token"));
    }

    [Fact]
    public void TheLegacySnapshotFollowsANewerSignInToo()
    {
        GivenAuth(SignIn("old"));
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth(SignIn("new"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.RestoreOriginalAuth());
        Assert.Equal("refresh-new", Token(Auth(), "refresh_token"));
    }

    [Fact]
    public void TheSignInIsFoundInTheSnapshotWhileCodexIsOnTheRelay()
    {
        GivenAuth(SignIn("mine"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        CodexLogin? login = _writer.ReadLogin();

        Assert.NotNull(login);
        Assert.Equal("access-mine", login.AccessToken);
        Assert.Equal("refresh-mine", login.RefreshToken);
        Assert.Equal("id-mine", login.IdToken);
        Assert.Equal("acct-mine", login.AccountId);
    }

    [Fact]
    public void TheSignInIsFoundInTheFileWhenCodexIsNotOnTheRelay()
    {
        GivenAuth(SignIn("mine"));

        Assert.Equal("refresh-mine", _writer.ReadLogin()?.RefreshToken);
    }

    [Fact]
    public void ANewerSignInInTheFileWinsOverTheSnapshot()
    {
        GivenAuth(SignIn("old"));
        _writer.Apply("sk-relay", "https://relay.test/v1");
        GivenAuth(SignIn("new"));

        Assert.Equal("refresh-new", _writer.ReadLogin()?.RefreshToken);
    }

    [Fact]
    public void NoSignInAnywhereIsNull()
    {
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.Null(_writer.ReadLogin());
    }

    [Fact]
    public void RefreshedTokensAreWhatTheRestoreHandsBack()
    {
        GivenAuth(SignIn("one"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.True(_writer.UpdateLoginTokens("refresh-one", new CodexRefreshedTokens("access-two", "refresh-two", "id-two"), RefreshedAt));
        Assert.Equal("refresh-two", _writer.ReadLogin()?.RefreshToken);
        Assert.Equal("sk-relay", Auth()["OPENAI_API_KEY"]!.GetValue<string>());

        Assert.True(_writer.RestoreOriginalFiles());
        JsonObject auth = Auth();
        Assert.Equal("access-two", Token(auth, "access_token"));
        Assert.Equal("refresh-two", Token(auth, "refresh_token"));
        Assert.Equal("id-two", Token(auth, "id_token"));
        Assert.Equal("acct-one", Token(auth, "account_id"));
        Assert.Equal("chatgpt", auth["auth_mode"]!.GetValue<string>());
        Assert.Equal("2026-09-28T01:02:03.0000000Z", auth["last_refresh"]!.GetValue<string>());
    }

    [Fact]
    public void RefreshedTokensReachTheLegacySnapshot()
    {
        GivenAuth(SignIn("one"));
        _writer.Apply("sk-relay", "https://relay.test/v1");
        _writer.UpdateLoginTokens("refresh-one", new CodexRefreshedTokens("access-two", "refresh-two", null), RefreshedAt);

        Assert.True(_writer.RestoreOriginalAuth());
        Assert.Equal("refresh-two", Token(Auth(), "refresh_token"));
        Assert.Equal("id-one", Token(Auth(), "id_token"));
    }

    [Fact]
    public void ARefreshFinishingAfterTheRestoreLandsInTheFile()
    {
        GivenAuth(SignIn("one"));
        _writer.Apply("sk-relay", "https://relay.test/v1");
        Assert.True(_writer.RestoreOriginalFiles());

        Assert.True(_writer.UpdateLoginTokens("refresh-one", new CodexRefreshedTokens("access-two", "refresh-two", null), RefreshedAt));
        Assert.Equal("refresh-two", Token(Auth(), "refresh_token"));
    }

    [Fact]
    public void AnOmittedRefreshTokenKeepsTheCurrentOne()
    {
        GivenAuth(SignIn("one"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        _writer.UpdateLoginTokens("refresh-one", new CodexRefreshedTokens("access-two", null, null), RefreshedAt);

        CodexLogin? login = _writer.ReadLogin();
        Assert.Equal("access-two", login?.AccessToken);
        Assert.Equal("refresh-one", login?.RefreshToken);
    }

    [Fact]
    public void AStaleRefreshTokenChangesNothing()
    {
        GivenAuth(SignIn("current"));
        _writer.Apply("sk-relay", "https://relay.test/v1");

        Assert.False(_writer.UpdateLoginTokens("refresh-gone", new CodexRefreshedTokens("access-x", "refresh-x", null), RefreshedAt));
        Assert.Equal("refresh-current", _writer.ReadLogin()?.RefreshToken);
    }
}
