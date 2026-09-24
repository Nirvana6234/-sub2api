using System.Security.Cryptography;

namespace LanAi.RelayClient.CodexBinding;

internal sealed record SnapshotBlob(byte[] Plaintext, bool WasProtected);

internal static class SnapshotBlobFormat
{
    private const int HeaderLength = 8;
    private const byte CurrentFormatVersion = 0x01;
    private static readonly byte[] Magic = [0x89, 0x4C, 0x41, 0x52, 0x43, 0x53, 0x50];

    public const int CurrentProtectionVersion = 1;

    public static byte[] Protect(byte[] plaintext, ISnapshotProtector protector)
    {
        ArgumentNullException.ThrowIfNull(plaintext);
        ArgumentNullException.ThrowIfNull(protector);

        byte[] ciphertext = protector.Protect(plaintext)
            ?? throw new CryptographicException("Snapshot protector returned null.");
        if (ciphertext.Length == 0)
        {
            throw new CryptographicException("Snapshot protector returned empty data.");
        }

        byte[] stored = new byte[HeaderLength + ciphertext.Length];
        Magic.CopyTo(stored, 0);
        stored[Magic.Length] = CurrentFormatVersion;
        ciphertext.CopyTo(stored, HeaderLength);
        return stored;
    }

    public static SnapshotBlob Read(
        byte[] stored,
        ISnapshotProtector protector,
        bool requireProtection)
    {
        ArgumentNullException.ThrowIfNull(stored);
        ArgumentNullException.ThrowIfNull(protector);

        if (stored.Length == 0 ||
            (stored[0] != Magic[0] && !HasMagicTail(stored)))
        {
            if (requireProtection)
            {
                throw new InvalidDataException("Snapshot protection header is required.");
            }

            return new SnapshotBlob(stored.ToArray(), WasProtected: false);
        }

        if (stored.Length < Magic.Length || !stored.AsSpan(0, Magic.Length).SequenceEqual(Magic))
        {
            throw new InvalidDataException("Snapshot protection magic is invalid or truncated.");
        }

        if (stored.Length < HeaderLength)
        {
            throw new InvalidDataException("Snapshot protection header is truncated.");
        }

        if (stored[Magic.Length] != CurrentFormatVersion)
        {
            throw new InvalidDataException("Snapshot protection version is unsupported.");
        }

        if (stored.Length == HeaderLength)
        {
            throw new InvalidDataException("Snapshot protected data is missing.");
        }

        try
        {
            byte[] plaintext = protector.Unprotect(stored.AsSpan(HeaderLength).ToArray())
                ?? throw new CryptographicException("Snapshot protector returned null.");
            return new SnapshotBlob(plaintext, WasProtected: true);
        }
        catch (CryptographicException ex)
        {
            throw new InvalidDataException("Snapshot protected data is invalid.", ex);
        }
    }

    private static bool HasMagicTail(byte[] stored) =>
        stored.Length >= Magic.Length &&
        stored.AsSpan(1, Magic.Length - 1).SequenceEqual(Magic.AsSpan(1));
}
