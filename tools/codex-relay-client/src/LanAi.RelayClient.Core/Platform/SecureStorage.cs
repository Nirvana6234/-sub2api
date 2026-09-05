using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform.MacOS;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform;

/// <summary>Selects the platform's encrypted session store.</summary>
/// <remarks>
/// <para>
/// The sibling of <see cref="SingleInstance"/>. Windows has DPAPI; macOS keeps a key
/// in the Keychain and encrypts with it — see <see cref="SecretEnvelope"/> for why the
/// two cannot be the same shape.
/// </para>
/// <para>
/// <b>There is deliberately no plaintext fallback.</b> The obvious way to keep a
/// macOS build running — write the JSON to a file unencrypted until the Keychain
/// lands — would put a working access token and a refresh token in cleartext under
/// the user's home directory, readable by every process running as that user. That
/// is a credential leak shipped as a placeholder, and nothing in the client would
/// ever surface it. Refusing to start is the honest failure: it is loud, it is
/// immediate, and it cannot reach a user's disk. The rule still stands for any third
/// platform, and it is why the macOS write paths throw rather than degrade.
/// </para>
/// <para>
/// <b>Both stores share one Keychain key on macOS</b>, created on first write. That is
/// deliberate: two keys would mean two chances for the Codex snapshot's key to go
/// missing, and that snapshot is the user's route back to their own ChatGPT account.
/// </para>
/// </remarks>
internal static class SecureStorage
{
    /// <exception cref="PlatformNotSupportedException">
    /// On any platform without an implementation. See the remarks.
    /// </exception>
    public static ISessionStore CreateSessionStore()
    {
        if (OperatingSystem.IsWindows())
        {
            return new DpapiSessionStore();
        }

        if (OperatingSystem.IsMacOS())
        {
            return new KeychainSessionStore(new KeychainMasterKeyStore());
        }

        throw new PlatformNotSupportedException(
            "当前平台没有安全的会话存储实现。客户端拒绝以明文保存登录凭据。");
    }

    /// <exception cref="PlatformNotSupportedException">
    /// On any platform without an implementation. See the remarks.
    /// </exception>
    public static ISnapshotProtector CreateSnapshotProtector()
    {
        if (OperatingSystem.IsWindows())
        {
            return new DpapiSnapshotProtector();
        }

        if (OperatingSystem.IsMacOS())
        {
            return new KeychainSnapshotProtector(new KeychainMasterKeyStore());
        }

        throw new PlatformNotSupportedException(
            "当前平台没有安全的快照加密实现。客户端拒绝以明文保存 Codex 配置快照。");
    }
}
