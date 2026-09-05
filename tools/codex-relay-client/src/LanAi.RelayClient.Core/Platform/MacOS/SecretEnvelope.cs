using System.Security.Cryptography;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Authenticated encryption of a blob under a key held elsewhere.
/// </summary>
/// <remarks>
/// <para>
/// <b>Why this exists at all: the Keychain is not a DPAPI replacement.</b> DPAPI
/// conflates "encrypt these bytes" with "using a key I never handle"; macOS splits
/// the two. <c>SecItem</c> stores a <i>secret</i> — it does not encrypt arbitrary
/// byte arrays, and the Codex snapshot is several KB, which is the wrong shape for a
/// generic-password item. So the Keychain holds one 32-byte key and this does the
/// encrypting, for both the session file and the snapshot.
/// </para>
/// <para>
/// The split is also what makes the work verifiable. Everything here — the envelope
/// layout, the nonce, tamper rejection, the round trip — runs and is tested on
/// Windows. Only fetching the key is Mac-only. Letting the Keychain encrypt would
/// have made 100% of this blind instead of the ~30 lines that actually are.
/// </para>
/// <para>
/// AES-GCM rather than AES-CBC + HMAC: it is authenticated in one primitive, it is
/// in-box on net8.0, and it needs no reflection, so it survives trimming.
/// </para>
/// </remarks>
internal static class SecretEnvelope
{
    /// <summary>Bytes of key material this envelope expects.</summary>
    public const int KeySize = 32;

    /// <remarks>96 bits, the size AES-GCM is defined for and the only one worth using.</remarks>
    private const int NonceSize = 12;

    private const int TagSize = 16;

    /// <summary>Encrypts <paramref name="plaintext"/> as nonce ‖ tag ‖ ciphertext.</summary>
    /// <remarks>
    /// A fresh random nonce every time. Reusing one under the same key is the single
    /// mistake that breaks GCM outright, and the session file is rewritten on every
    /// token refresh — so "rarely written" is not a defence available here.
    /// </remarks>
    public static byte[] Seal(byte[] key, byte[] plaintext)
    {
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(plaintext);
        RequireKeySize(key);

        byte[] sealedBytes = new byte[NonceSize + TagSize + plaintext.Length];
        Span<byte> nonce = sealedBytes.AsSpan(0, NonceSize);
        Span<byte> tag = sealedBytes.AsSpan(NonceSize, TagSize);
        Span<byte> ciphertext = sealedBytes.AsSpan(NonceSize + TagSize);

        RandomNumberGenerator.Fill(nonce);

        using var aes = new AesGcm(key, TagSize);
        aes.Encrypt(nonce, plaintext, ciphertext, tag);

        return sealedBytes;
    }

    /// <summary>Decrypts what <see cref="Seal"/> produced.</summary>
    /// <exception cref="CryptographicException">
    /// The bytes were truncated, tampered with, or encrypted under a different key.
    /// Thrown rather than returned as null so that callers have to decide: the
    /// session store treats it as "no session", but the snapshot protector must not,
    /// because the snapshot is the user's own Codex configuration and losing it
    /// silently is how someone ends up unable to get their account back.
    /// </exception>
    public static byte[] Open(byte[] key, byte[] sealedBytes)
    {
        ArgumentNullException.ThrowIfNull(key);
        ArgumentNullException.ThrowIfNull(sealedBytes);
        RequireKeySize(key);

        if (sealedBytes.Length < NonceSize + TagSize)
        {
            throw new CryptographicException("密文长度不足，无法解密。");
        }

        ReadOnlySpan<byte> nonce = sealedBytes.AsSpan(0, NonceSize);
        ReadOnlySpan<byte> tag = sealedBytes.AsSpan(NonceSize, TagSize);
        ReadOnlySpan<byte> ciphertext = sealedBytes.AsSpan(NonceSize + TagSize);

        byte[] plaintext = new byte[ciphertext.Length];
        using var aes = new AesGcm(key, TagSize);

        // Throws AuthenticationTagMismatchException (a CryptographicException) if the
        // bytes do not authenticate. Nothing is written to plaintext in that case.
        aes.Decrypt(nonce, ciphertext, tag, plaintext);

        return plaintext;
    }

    private static void RequireKeySize(byte[] key)
    {
        if (key.Length != KeySize)
        {
            throw new ArgumentException($"密钥长度必须是 {KeySize} 字节。", nameof(key));
        }
    }
}
