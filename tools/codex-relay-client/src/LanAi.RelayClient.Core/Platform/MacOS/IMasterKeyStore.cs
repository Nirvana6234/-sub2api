namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Supplies the key <see cref="SecretEnvelope"/> encrypts under.
/// </summary>
/// <remarks>
/// <para>
/// An interface for two reasons. It keeps the Mac-only Keychain calls to one small
/// seam that the rest of the platform layer can be tested without; and it leaves the
/// choice of Keychain API swappable, which matters because that choice cannot be
/// settled from Windows — see <c>KeychainMasterKeyStore</c>.
/// </para>
/// <para>
/// <b>The two read paths are deliberately different methods.</b> The obvious design
/// is one <c>GetOrCreateKey()</c>, and on the decrypt path it is destructive: a key
/// that regenerates when the lookup misses makes every blob written under the old
/// key permanently unreadable, silently. For the session that only costs a sign-in.
/// For the Codex snapshot it costs the user their own ChatGPT account — that file is
/// the backup of their original configuration, and they would find out on the day
/// they try to go back to it. So creation happens on the write path only, and the
/// read path fails rather than inventing a key.
/// </para>
/// </remarks>
internal interface IMasterKeyStore
{
    /// <summary>Returns the stored key, or null when there is none.</summary>
    /// <remarks>Never creates. A miss means "nothing was ever encrypted", or trouble.</remarks>
    byte[]? TryRead();

    /// <summary>Returns the stored key, creating and persisting one if absent.</summary>
    /// <exception cref="InvalidOperationException">
    /// The key could not be stored. It must not be swallowed: the caller's fallback
    /// would otherwise be to write credentials unencrypted, which is precisely what
    /// <see cref="SecureStorage"/> refuses to allow.
    /// </exception>
    byte[] ReadOrCreate();
}
