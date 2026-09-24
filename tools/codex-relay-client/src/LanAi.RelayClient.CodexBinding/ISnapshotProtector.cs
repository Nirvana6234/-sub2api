namespace LanAi.RelayClient.CodexBinding;

public interface ISnapshotProtector
{
    byte[] Protect(byte[] plaintext);

    byte[] Unprotect(byte[] protectedData);
}
