using LanAi.RelayClient.CodexBinding;

namespace LanAi.RelayClient.Tests;

internal sealed class TestSnapshotProtector : ISnapshotProtector
{
    private const byte Mask = 0x5A;

    public byte[] Protect(byte[] plaintext) => Transform(plaintext);

    public byte[] Unprotect(byte[] protectedData) => Transform(protectedData);

    private static byte[] Transform(byte[] source)
    {
        byte[] result = (byte[])source.Clone();
        for (int index = 0; index < result.Length; index++)
        {
            result[index] ^= Mask;
        }

        return result;
    }
}
