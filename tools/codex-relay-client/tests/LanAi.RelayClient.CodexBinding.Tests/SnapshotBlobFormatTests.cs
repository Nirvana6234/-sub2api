using System.Text;
using Xunit;

namespace LanAi.RelayClient.CodexBinding.Tests;

public sealed class SnapshotBlobFormatTests
{
    [Fact]
    public void ProtectedBlobRoundTripsWithoutContainingPlaintext()
    {
        byte[] plaintext = Encoding.UTF8.GetBytes("oauth-refresh-token");
        var protector = new FakeSnapshotProtector();

        byte[] stored = SnapshotBlobFormat.Protect(plaintext, protector);
        SnapshotBlob result = SnapshotBlobFormat.Read(stored, protector, requireProtection: true);

        Assert.Equal(
            new byte[] { 0x89, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50, 0x01 },
            stored[..8]);
        Assert.False(ContainsSequence(stored, plaintext));
        Assert.Equal(plaintext, result.Plaintext);
        Assert.True(result.WasProtected);
        Assert.Equal(1, protector.ProtectCallCount);
        Assert.Equal(1, protector.UnprotectCallCount);
    }

    [Fact]
    public void LegacyPlaintextIsReturnedOnlyWhenExplicitlyAllowed()
    {
        byte[] stored = Encoding.UTF8.GetBytes("legacy-snapshot");
        var protector = new FakeSnapshotProtector();

        SnapshotBlob result = SnapshotBlobFormat.Read(stored, protector, requireProtection: false);

        Assert.Equal(stored, result.Plaintext);
        Assert.NotSame(stored, result.Plaintext);
        Assert.False(result.WasProtected);
        Assert.Equal(0, protector.UnprotectCallCount);
        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, protector, requireProtection: true));
    }

    [Fact]
    public void UnknownHeaderVersionIsNeverTreatedAsPlaintext()
    {
        byte[] stored = [0x89, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50, 0x02, 0x01];

        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new FakeSnapshotProtector(), requireProtection: false));
    }

    [Fact]
    public void TruncatedMagicIsNeverTreatedAsLegacyPlaintext()
    {
        byte[] stored = [0x89, 0x4C, 0x41, 0x52];

        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new FakeSnapshotProtector(), requireProtection: false));
    }

    [Fact]
    public void CorruptedFirstMagicByteIsNeverTreatedAsLegacyPlaintext()
    {
        byte[] stored = [0x88, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50, 0x01, 0x02];

        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new FakeSnapshotProtector(), requireProtection: false));
    }

    [Fact]
    public void HeaderWithoutVersionIsRejected()
    {
        byte[] stored = [0x89, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50];

        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new FakeSnapshotProtector(), requireProtection: false));
    }

    [Fact]
    public void TamperedCiphertextIsReportedAsInvalidData()
    {
        var protector = new FakeSnapshotProtector();
        byte[] stored = SnapshotBlobFormat.Protect(
            Encoding.UTF8.GetBytes("oauth-refresh-token"),
            protector);
        stored[^1] ^= 0x01;

        InvalidDataException exception = Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, protector, requireProtection: true));

        Assert.IsType<System.Security.Cryptography.CryptographicException>(exception.InnerException);
    }

    [Fact]
    public void EmptyCiphertextIsRejectedDuringProtect()
    {
        Assert.Throws<System.Security.Cryptography.CryptographicException>(() =>
            SnapshotBlobFormat.Protect(
                Encoding.UTF8.GetBytes("secret"),
                new EmptyCiphertextProtector()));
    }

    [Fact]
    public void HeaderWithoutCiphertextIsRejectedDuringRead()
    {
        byte[] stored = [0x89, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50, 0x01];

        Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new EmptyPlaintextProtector(), requireProtection: true));
    }

    [Fact]
    public void NullPlaintextFromProtectorIsReportedAsInvalidData()
    {
        byte[] stored = SnapshotBlobFormat.Protect(
            Encoding.UTF8.GetBytes("secret"),
            new FakeSnapshotProtector());

        InvalidDataException exception = Assert.Throws<InvalidDataException>(() =>
            SnapshotBlobFormat.Read(stored, new NullPlaintextProtector(), requireProtection: true));

        Assert.IsType<System.Security.Cryptography.CryptographicException>(exception.InnerException);
    }

    private static bool ContainsSequence(byte[] haystack, byte[] needle)
    {
        if (needle.Length == 0)
        {
            return true;
        }

        for (int index = 0; index <= haystack.Length - needle.Length; index++)
        {
            if (haystack.AsSpan(index, needle.Length).SequenceEqual(needle))
            {
                return true;
            }
        }

        return false;
    }

    private sealed class EmptyCiphertextProtector : ISnapshotProtector
    {
        public byte[] Protect(byte[] plaintext) => [];

        public byte[] Unprotect(byte[] protectedData) => throw new NotSupportedException();
    }

    private sealed class NullPlaintextProtector : ISnapshotProtector
    {
        public byte[] Protect(byte[] plaintext) => throw new NotSupportedException();

        public byte[] Unprotect(byte[] protectedData) => null!;
    }

    private sealed class EmptyPlaintextProtector : ISnapshotProtector
    {
        public byte[] Protect(byte[] plaintext) => throw new NotSupportedException();

        public byte[] Unprotect(byte[] protectedData) => [];
    }
}
