using System.Security.Cryptography;

namespace LanAi.RelayClient.CodexBinding.Tests;

internal sealed class FakeSnapshotProtector : ISnapshotProtector
{
    private const int HashLength = 32;
    private const byte Mask = 0xA5;

    public int ProtectCallCount { get; private set; }

    public int UnprotectCallCount { get; private set; }

    public bool FailProtect { get; set; }

    public bool FailUnprotect { get; set; }

    public byte[] Protect(byte[] plaintext)
    {
        ArgumentNullException.ThrowIfNull(plaintext);
        ProtectCallCount++;

        if (FailProtect)
        {
            throw new CryptographicException("Fake protection failed.");
        }

        byte[] payload = new byte[HashLength + plaintext.Length];
        SHA256.HashData(plaintext).CopyTo(payload, 0);
        plaintext.CopyTo(payload, HashLength);
        return ReverseAndMask(payload);
    }

    public byte[] Unprotect(byte[] protectedData)
    {
        ArgumentNullException.ThrowIfNull(protectedData);
        UnprotectCallCount++;

        if (FailUnprotect)
        {
            throw new CryptographicException("Fake unprotection failed.");
        }

        byte[] payload = ReverseAndMask(protectedData);
        if (payload.Length < HashLength)
        {
            throw new CryptographicException("Protected data is truncated.");
        }

        ReadOnlySpan<byte> expectedHash = payload.AsSpan(0, HashLength);
        byte[] plaintext = payload.AsSpan(HashLength).ToArray();
        byte[] actualHash = SHA256.HashData(plaintext);
        if (!CryptographicOperations.FixedTimeEquals(expectedHash, actualHash))
        {
            throw new CryptographicException("Protected data failed integrity validation.");
        }

        return plaintext;
    }

    private static byte[] ReverseAndMask(byte[] input)
    {
        byte[] output = new byte[input.Length];
        for (int index = 0; index < input.Length; index++)
        {
            output[index] = (byte)(input[input.Length - 1 - index] ^ Mask);
        }

        return output;
    }
}
