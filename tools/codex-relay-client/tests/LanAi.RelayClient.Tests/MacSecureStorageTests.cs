using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>A key store whose behaviour the test dictates.</summary>
/// <remarks>
/// Stands in for the Keychain so that everything built on top of it is exercised on
/// Windows. Only <c>KeychainMasterKeyStore</c> itself stays unverified until a Mac is
/// available — which is the whole reason the key and the encryption were separated.
/// </remarks>
internal sealed class FakeMasterKeyStore : IMasterKeyStore
{
    private byte[]? _key;

    public int CreateCount { get; private set; }

    /// <summary>Set to simulate a Keychain that refuses to store anything.</summary>
    public bool WritesFail { get; set; }

    public static FakeMasterKeyStore WithKey()
    {
        var store = new FakeMasterKeyStore();
        _ = store.ReadOrCreate();
        return store;
    }

    public byte[]? TryRead() => _key?.ToArray();

    public byte[] ReadOrCreate()
    {
        if (_key is not null)
        {
            return _key.ToArray();
        }

        if (WritesFail)
        {
            throw new InvalidOperationException("钥匙串写入失败。");
        }

        CreateCount++;
        _key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);
        return _key.ToArray();
    }

    /// <summary>Simulates the key disappearing — a wiped or replaced Keychain item.</summary>
    public void Forget() => _key = null;
}

public sealed class SecretEnvelopeTests
{
    [Fact]
    public void SealedBytesRoundTrip()
    {
        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);
        byte[] plaintext = "共飞 access token"u8.ToArray();

        byte[] opened = SecretEnvelope.Open(key, SecretEnvelope.Seal(key, plaintext));

        Assert.Equal(plaintext, opened);
    }

    /// <remarks>
    /// The one mistake that breaks GCM outright is a repeated nonce under the same
    /// key, and the session file is rewritten on every token refresh — so this is not
    /// a theoretical concern that "rarely written" would excuse.
    /// </remarks>
    [Fact]
    public void EachSealUsesAFreshNonce()
    {
        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);
        byte[] plaintext = "same input every time"u8.ToArray();

        byte[] first = SecretEnvelope.Seal(key, plaintext);
        byte[] second = SecretEnvelope.Seal(key, plaintext);

        Assert.NotEqual(first.AsSpan(0, 12).ToArray(), second.AsSpan(0, 12).ToArray());
        Assert.NotEqual(first, second);
    }

    [Fact]
    public void TamperedBytesAreRejectedRatherThanDecrypted()
    {
        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);
        byte[] sealedBytes = SecretEnvelope.Seal(key, "token"u8.ToArray());

        // Flip a bit in the ciphertext, past the nonce and tag.
        sealedBytes[^1] ^= 0x01;

        Assert.Throws<AuthenticationTagMismatchException>(() => SecretEnvelope.Open(key, sealedBytes));
    }

    [Fact]
    public void ADifferentKeyCannotOpenIt()
    {
        byte[] sealedBytes = SecretEnvelope.Seal(RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize), "t"u8.ToArray());
        byte[] otherKey = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);

        Assert.Throws<AuthenticationTagMismatchException>(() => SecretEnvelope.Open(otherKey, sealedBytes));
    }

    [Fact]
    public void TruncatedBytesFailCleanlyRatherThanIndexingOutOfRange()
    {
        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);

        Assert.Throws<CryptographicException>(() => SecretEnvelope.Open(key, new byte[8]));
    }

    [Fact]
    public void AnEmptyPayloadIsStillAuthenticated()
    {
        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);

        byte[] sealedBytes = SecretEnvelope.Seal(key, []);

        Assert.Empty(SecretEnvelope.Open(key, sealedBytes));
        Assert.Equal(12 + 16, sealedBytes.Length);
    }
}

public sealed class KeychainSessionStoreTests : IDisposable
{
    private readonly string _directory = Path.Combine(
        Path.GetTempPath(), "lanai-keychain-tests-" + Guid.NewGuid().ToString("N"));

    private string FilePath => Path.Combine(_directory, "session.bin");

    private static StoredSession Sample() => new()
    {
        ServerAddress = "https://relay.test/",
        AccessToken = "access-token",
        RefreshToken = "refresh-token",
        UserEmail = "user@example.com",
    };

    [Fact]
    public void ASavedSessionLoadsBack()
    {
        var keys = new FakeMasterKeyStore();
        var store = new KeychainSessionStore(keys, FilePath);

        store.Save(Sample());
        StoredSession? loaded = store.Load();

        Assert.Equal("access-token", loaded!.AccessToken);
        Assert.Equal("user@example.com", loaded.UserEmail);
    }

    /// <remarks>
    /// The point of the whole exercise: the token must not be readable from the file
    /// by anything that does not hold the key.
    /// </remarks>
    [Fact]
    public void TheFileOnDiskDoesNotContainTheToken()
    {
        new KeychainSessionStore(new FakeMasterKeyStore(), FilePath).Save(Sample());

        byte[] onDisk = File.ReadAllBytes(FilePath);

        Assert.DoesNotContain("access-token"u8.ToArray(), Windows(onDisk));
        Assert.DoesNotContain("user@example.com"u8.ToArray(), Windows(onDisk));

        static IEnumerable<byte[]> Windows(byte[] bytes)
        {
            for (int i = 0; i + 12 <= bytes.Length; i++)
            {
                yield return bytes.AsSpan(i, 12).ToArray();
            }
        }
    }

    [Fact]
    public void NoFileMeansNoSession()
    {
        Assert.Null(new KeychainSessionStore(new FakeMasterKeyStore(), FilePath).Load());
    }

    /// <remarks>
    /// A home directory copied from another Mac, or a wiped Keychain. Costs a sign-in;
    /// must not cost a crash on startup.
    /// </remarks>
    [Fact]
    public void AMissingKeyReadsAsNoSessionRatherThanThrowing()
    {
        var keys = new FakeMasterKeyStore();
        var store = new KeychainSessionStore(keys, FilePath);
        store.Save(Sample());

        keys.Forget();

        Assert.Null(store.Load());
    }

    [Fact]
    public void CorruptBytesReadAsNoSession()
    {
        var keys = FakeMasterKeyStore.WithKey();
        Directory.CreateDirectory(_directory);
        File.WriteAllBytes(FilePath, "not an envelope"u8.ToArray());

        Assert.Null(new KeychainSessionStore(keys, FilePath).Load());
    }

    /// <remarks>
    /// The invariant SecureStorage exists for: when the key cannot be obtained there is
    /// no acceptable fallback, so the write fails and leaves nothing behind. A file
    /// written here would be a plaintext credential on disk.
    /// </remarks>
    [Fact]
    public void AFailedKeyWriteThrowsAndLeavesNoFile()
    {
        var keys = new FakeMasterKeyStore { WritesFail = true };
        var store = new KeychainSessionStore(keys, FilePath);

        Assert.Throws<InvalidOperationException>(() => store.Save(Sample()));
        Assert.False(File.Exists(FilePath));
    }

    [Fact]
    public void ClearRemovesTheFile()
    {
        var store = new KeychainSessionStore(new FakeMasterKeyStore(), FilePath);
        store.Save(Sample());

        store.Clear();

        Assert.False(File.Exists(FilePath));
        Assert.Null(store.Load());
    }

    [Fact]
    public void ClearOnAnAbsentFileIsNotAnError()
    {
        new KeychainSessionStore(new FakeMasterKeyStore(), FilePath).Clear();
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
            // A leftover temp directory is not worth failing a test run over.
        }
    }
}

public sealed class KeychainSnapshotProtectorTests
{
    private static readonly byte[] Snapshot = "[model]\nname = \"gpt-5\"\n"u8.ToArray();

    [Fact]
    public void ProtectedSnapshotRoundTrips()
    {
        var protector = new KeychainSnapshotProtector(new FakeMasterKeyStore());

        Assert.Equal(Snapshot, protector.Unprotect(protector.Protect(Snapshot)));
    }

    [Fact]
    public void ProtectedBytesDoNotContainThePlaintext()
    {
        byte[] sealedBytes = new KeychainSnapshotProtector(new FakeMasterKeyStore()).Protect(Snapshot);

        Assert.DoesNotContain("gpt-5"u8.ToArray()[0], sealedBytes.AsSpan(0, 12).ToArray());
        Assert.NotEqual(Snapshot, sealedBytes);
    }

    /// <remarks>
    /// <b>The difference from the session store, and the reason both exist.</b> A lost
    /// session costs a sign-in; a lost snapshot is the user's original Codex
    /// configuration — their route back to their own ChatGPT account. Returning null or
    /// an empty array here would let the caller overwrite that configuration with
    /// nothing, and the user would find out weeks later.
    /// </remarks>
    [Fact]
    public void AMissingKeyThrowsRatherThanReturningNothing()
    {
        var keys = new FakeMasterKeyStore();
        var protector = new KeychainSnapshotProtector(keys);
        byte[] sealedBytes = protector.Protect(Snapshot);

        keys.Forget();

        Assert.Throws<CryptographicException>(() => protector.Unprotect(sealedBytes));
    }

    /// <remarks>
    /// Pins the split in <see cref="IMasterKeyStore"/>: reading never creates. A
    /// <c>GetOrCreateKey</c> here would mint a fresh key on the miss above and make
    /// every existing snapshot permanently undecryptable, silently.
    /// </remarks>
    [Fact]
    public void UnprotectNeverCreatesAKey()
    {
        var keys = new FakeMasterKeyStore();
        var protector = new KeychainSnapshotProtector(keys);
        byte[] sealedBytes = protector.Protect(Snapshot);
        Assert.Equal(1, keys.CreateCount);

        keys.Forget();
        Assert.Throws<CryptographicException>(() => protector.Unprotect(sealedBytes));

        Assert.Equal(1, keys.CreateCount);
    }

    [Fact]
    public void TamperedSnapshotBytesAreRejected()
    {
        var protector = new KeychainSnapshotProtector(FakeMasterKeyStore.WithKey());

        Assert.Throws<CryptographicException>(() => protector.Unprotect("garbage"u8.ToArray()));
    }
}
