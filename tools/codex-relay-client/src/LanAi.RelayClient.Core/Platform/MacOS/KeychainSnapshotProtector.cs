using System.Security.Cryptography;
using LanAi.RelayClient.CodexBinding;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Encrypts the Codex snapshot under a Keychain-held key — the macOS counterpart of
/// <c>DpapiSnapshotProtector</c>.
/// </summary>
/// <remarks>
/// <para>
/// The blobs this protects are the user's own <c>auth.json</c> and <c>config.toml</c>:
/// the copy that gives them their personal ChatGPT account back when they stop using
/// 共飞. Losing it cannot be undone, and it fails quietly — the user finds out on the
/// day they try to go back, not on the day it breaks.
/// </para>
/// <para>
/// That is why <see cref="Unprotect"/> throws on a missing key instead of treating it
/// the way the session store does. Both call the same <see cref="IMasterKeyStore"/>,
/// but they want opposite things from a miss: for a session, "sign in again"; for the
/// snapshot, there is no again.
/// </para>
/// </remarks>
internal sealed class KeychainSnapshotProtector : ISnapshotProtector
{
    private readonly IMasterKeyStore _keys;

    public KeychainSnapshotProtector(IMasterKeyStore keys) =>
        _keys = keys ?? throw new ArgumentNullException(nameof(keys));

    public byte[] Protect(byte[] plaintext)
    {
        ArgumentNullException.ThrowIfNull(plaintext);

        byte[] key = _keys.ReadOrCreate();
        try
        {
            return SecretEnvelope.Seal(key, plaintext);
        }
        finally
        {
            Array.Clear(key, 0, key.Length);
        }
    }

    /// <exception cref="CryptographicException">
    /// The key is gone, or the bytes do not authenticate. Deliberately not softened to
    /// a null or an empty array: the caller restores the user's Codex configuration
    /// from this, and an empty restore would overwrite their real configuration with
    /// nothing.
    /// </exception>
    public byte[] Unprotect(byte[] protectedData)
    {
        ArgumentNullException.ThrowIfNull(protectedData);

        byte[] key = _keys.TryRead()
            ?? throw new CryptographicException(
                "钥匙串中没有快照密钥，无法还原原始 Codex 配置。");
        try
        {
            return SecretEnvelope.Open(key, protectedData);
        }
        finally
        {
            Array.Clear(key, 0, key.Length);
        }
    }
}
