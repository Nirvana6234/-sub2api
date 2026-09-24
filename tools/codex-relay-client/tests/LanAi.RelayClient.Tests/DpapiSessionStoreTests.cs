using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Exercises the real DPAPI path against a temporary file.
/// </summary>
/// <remarks>
/// Uses actual encryption rather than a stub: the failure modes worth testing —
/// corrupt blobs, truncated files, wrong entropy — only exist in the real thing.
/// </remarks>
public sealed class DpapiSessionStoreTests : IDisposable
{
    private readonly string _directory =
        Path.Combine(Path.GetTempPath(), "LanAi.RelayClient.Tests", Guid.NewGuid().ToString("N"));

    private string FilePath => Path.Combine(_directory, "session.bin");

    private static StoredSession Sample => new()
    {
        ServerAddress = "https://relay.test/",
        AccessToken = "access-token-value",
        RefreshToken = "refresh-token-value",
        AccessExpiresAt = new DateTimeOffset(2026, 7, 31, 12, 0, 0, TimeSpan.Zero),
        UserEmail = "a@b.com",
        UserName = "ann",
    };

    [Fact]
    public void ASavedSessionComesBackIntact()
    {
        var store = new DpapiSessionStore(FilePath);

        store.Save(Sample);
        StoredSession? loaded = store.Load();

        Assert.NotNull(loaded);
        Assert.Equal(Sample.AccessToken, loaded!.AccessToken);
        Assert.Equal(Sample.RefreshToken, loaded.RefreshToken);
        Assert.Equal(Sample.AccessExpiresAt, loaded.AccessExpiresAt);
        Assert.Equal(Sample.UserName, loaded.UserName);
    }

    /// <remarks>
    /// <para>
    /// Pins the source-generated serializer against the failure this port already hit
    /// once. Moving to <c>ClientJsonContext</c> was required for trimming, but a
    /// source-generated context binds omitted JSON fields differently from the
    /// reflection binder it replaced: where a type is constructed through a
    /// parameterised constructor, a missing field arrives as <c>default</c> and the
    /// property initialiser never runs, so <c>string.Empty</c> becomes <c>null</c>.
    /// </para>
    /// <para>
    /// <see cref="StoredSession"/> is safe because it has no such constructor — the
    /// generator uses an object initialiser and the property defaults hold. That is a
    /// property of the type's shape, not a guarantee, and adding a primary constructor
    /// to it later would flip it silently. This test is what would notice.
    /// </para>
    /// <para>
    /// The consequence if it ever regresses is not a crash: a null
    /// <c>ServerAddress</c> compares unequal to the configured relay, and the client
    /// discards a perfectly good session and asks the user to sign in again.
    /// </para>
    /// </remarks>
    [Fact]
    public void FieldsMissingFromAnOlderSessionLoadAsEmptyRatherThanNull()
    {
        var store = new DpapiSessionStore(FilePath);
        store.Save(new StoredSession
        {
            AccessToken = "access-token-value",
            AccessExpiresAt = new DateTimeOffset(2026, 7, 31, 12, 0, 0, TimeSpan.Zero),
        });

        StoredSession? loaded = store.Load();

        Assert.NotNull(loaded);
        Assert.Equal(string.Empty, loaded!.RefreshToken);
        Assert.Equal(string.Empty, loaded.ServerAddress);
        Assert.Equal(string.Empty, loaded.UserEmail);
        Assert.Equal(string.Empty, loaded.UserName);
        Assert.False(loaded.CanRenew);
    }

    /// <remarks>
    /// The names on disk are a compatibility surface: existing installations hold a
    /// session written by the reflection binder, and the context that replaced it
    /// applies a CamelCase policy to every other type it carries. If that policy ever
    /// reaches this type, every installed client silently signs its user out on
    /// upgrade. The explicit [JsonPropertyName] attributes are what prevent it; this
    /// asserts on the bytes rather than trusting them.
    /// </remarks>
    [Fact]
    public void ThePersistedFieldNamesAreTheHistoricalSnakeCaseOnes()
    {
        byte[] json = System.Text.Json.JsonSerializer.SerializeToUtf8Bytes(
            Sample,
            ClientJsonContext.Default.StoredSession);

        string text = System.Text.Encoding.UTF8.GetString(json);

        Assert.Contains("\"access_token\"", text, StringComparison.Ordinal);
        Assert.Contains("\"refresh_token\"", text, StringComparison.Ordinal);
        Assert.Contains("\"access_expires_at\"", text, StringComparison.Ordinal);
        Assert.Contains("\"user_email\"", text, StringComparison.Ordinal);
        Assert.DoesNotContain("accessToken", text, StringComparison.Ordinal);
    }

    [Fact]
    public void TokensAreNotRecoverableFromTheFileBytes()
    {
        // The whole point of the store. If this fails, anything that can read the
        // user's profile can lift a working relay credential straight off disk.
        var store = new DpapiSessionStore(FilePath);
        store.Save(Sample);

        string raw = Convert.ToBase64String(File.ReadAllBytes(FilePath));
        string rawText = System.Text.Encoding.UTF8.GetString(File.ReadAllBytes(FilePath));

        Assert.DoesNotContain("access-token-value", rawText, StringComparison.Ordinal);
        Assert.DoesNotContain("refresh-token-value", rawText, StringComparison.Ordinal);
        Assert.DoesNotContain(Convert.ToBase64String(System.Text.Encoding.UTF8.GetBytes("access-token-value")), raw, StringComparison.Ordinal);
    }

    [Fact]
    public void AMissingFileReadsAsNoSession()
    {
        Assert.Null(new DpapiSessionStore(FilePath).Load());
    }

    [Fact]
    public void ACorruptFileReadsAsNoSessionInsteadOfThrowing()
    {
        // A bad cache must never stop the app from starting.
        Directory.CreateDirectory(_directory);
        File.WriteAllBytes(FilePath, [1, 2, 3, 4, 5, 6, 7, 8]);

        Assert.Null(new DpapiSessionStore(FilePath).Load());
    }

    [Fact]
    public void ATruncatedFileReadsAsNoSession()
    {
        var store = new DpapiSessionStore(FilePath);
        store.Save(Sample);

        byte[] full = File.ReadAllBytes(FilePath);
        File.WriteAllBytes(FilePath, full[..(full.Length / 2)]);

        Assert.Null(store.Load());
    }

    [Fact]
    public void ASessionWithoutAnAccessTokenIsTreatedAsAbsent()
    {
        var store = new DpapiSessionStore(FilePath);
        store.Save(Sample with { AccessToken = string.Empty });

        Assert.Null(store.Load());
    }

    [Fact]
    public void ClearingRemovesTheFile()
    {
        var store = new DpapiSessionStore(FilePath);
        store.Save(Sample);

        store.Clear();

        Assert.False(File.Exists(FilePath));
        Assert.Null(store.Load());
    }

    [Fact]
    public void ClearingWhenNothingWasStoredIsHarmless()
    {
        new DpapiSessionStore(FilePath).Clear();
    }

    [Fact]
    public void SavingTwiceLeavesNoTemporaryFileBehind()
    {
        var store = new DpapiSessionStore(FilePath);

        store.Save(Sample);
        store.Save(Sample with { AccessToken = "second" });

        Assert.False(File.Exists(FilePath + ".tmp"));
        Assert.Equal("second", store.Load()!.AccessToken);
    }

    [Fact]
    public void TheDefaultLocationSitsUnderTheUsersLocalAppData()
    {
        string path = DpapiSessionStore.DefaultFilePath();

        Assert.StartsWith(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            path,
            StringComparison.OrdinalIgnoreCase);
        Assert.EndsWith("session.bin", path, StringComparison.OrdinalIgnoreCase);
    }

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
        }
    }
}
