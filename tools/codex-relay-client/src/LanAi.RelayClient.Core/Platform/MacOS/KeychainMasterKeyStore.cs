using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using System.Security.Cryptography;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Keeps the encryption key in the login Keychain.
/// </summary>
/// <remarks>
/// <para>
/// <b>Written without a Mac to run it on — the only genuinely blind part of macOS
/// secure storage.</b> Everything it guards (envelope layout, nonce handling, tamper
/// rejection, the session and snapshot round trips) is tested on Windows against a
/// fake key store. What is unverified here is narrow and specific: that these four
/// entry points exist and behave as documented on macOS 12+ Apple Silicon.
/// </para>
/// <para>
/// <b>Which Keychain API — decided for a cheap reversal, not decided for good.</b>
/// This uses <c>SecKeychainAddGenericPassword</c> / <c>SecKeychainFindGenericPassword</c>:
/// flat C arguments, no <c>CFDictionary</c>, no <c>CFString</c>, no CFRelease
/// discipline — roughly a third of the blind surface of the modern
/// <c>SecItemAdd</c> / <c>SecItemCopyMatching</c> pair. They are deprecated, and
/// whether the deprecation has become removal on current macOS is exactly the kind of
/// fact that cannot be settled from Windows. The call site is
/// <see cref="IMasterKeyStore"/>, so swapping to <c>SecItem*</c> is a new file and one
/// line in the factory, not a rewrite.
/// </para>
/// <para>
/// <c>/usr/bin/security add-generic-password</c> was considered as a third route, in
/// keeping with the <c>osascript</c> and <c>launchctl</c> precedent, and rejected for
/// the write path: it takes the secret as <c>-w &lt;value&gt;</c>, which puts the key
/// in <c>argv</c> where every process running as this user can read it out of
/// <c>ps</c>. That is the same exposure <see cref="SecureStorage"/> refuses a
/// plaintext file for.
/// </para>
/// </remarks>
[SupportedOSPlatform("macos")]
internal sealed class KeychainMasterKeyStore : IMasterKeyStore
{
    private const string Security = "/System/Library/Frameworks/Security.framework/Security";

    /// <remarks>
    /// Service and account together name the item. The service string is versioned so
    /// that a future change of key derivation can coexist rather than silently reading
    /// a key it cannot use.
    /// </remarks>
    private const string ServiceName = "com.gongfeiai.chatgpt-assistant.secrets.v1";

    private const string AccountName = "master-key";

    private const int ErrSecSuccess = 0;
    private const int ErrSecItemNotFound = -25300;
    private const int ErrSecDuplicateItem = -25299;

    public byte[]? TryRead()
    {
        IntPtr data = IntPtr.Zero;
        try
        {
            int status = SecKeychainFindGenericPassword(
                IntPtr.Zero,
                (uint)ServiceName.Length, ServiceName,
                (uint)AccountName.Length, AccountName,
                out uint length, out data, IntPtr.Zero);

            if (status == ErrSecItemNotFound)
            {
                return null;
            }

            if (status != ErrSecSuccess || data == IntPtr.Zero)
            {
                ClientLog.Warning($"读取钥匙串密钥失败，状态码 {status}");
                return null;
            }

            if (length != SecretEnvelope.KeySize)
            {
                // Something else wrote under this name, or the item was truncated.
                // Treated as absent rather than repaired: overwriting it here would
                // destroy whatever it actually is.
                ClientLog.Warning($"钥匙串中的密钥长度异常（{length} 字节），已忽略");
                return null;
            }

            byte[] key = new byte[length];
            Marshal.Copy(data, key, 0, (int)length);
            return key;
        }
        catch (Exception ex) when (ex is EntryPointNotFoundException or DllNotFoundException)
        {
            ClientLog.Warning("钥匙串接口不可用", ex);
            return null;
        }
        finally
        {
            if (data != IntPtr.Zero)
            {
                SecKeychainItemFreeContent(IntPtr.Zero, data);
            }
        }
    }

    public byte[] ReadOrCreate()
    {
        if (TryRead() is { } existing)
        {
            return existing;
        }

        byte[] key = RandomNumberGenerator.GetBytes(SecretEnvelope.KeySize);

        int status;
        try
        {
            status = SecKeychainAddGenericPassword(
                IntPtr.Zero,
                (uint)ServiceName.Length, ServiceName,
                (uint)AccountName.Length, AccountName,
                (uint)key.Length, key,
                IntPtr.Zero);
        }
        catch (Exception ex) when (ex is EntryPointNotFoundException or DllNotFoundException)
        {
            throw new InvalidOperationException("钥匙串接口不可用，无法安全保存凭据。", ex);
        }

        if (status == ErrSecDuplicateItem)
        {
            // Another process won the race between the read above and this write.
            // Its key is the real one; ours is discarded rather than forced in, or the
            // two processes would each be unable to read the other's blobs.
            Array.Clear(key, 0, key.Length);
            return TryRead()
                ?? throw new InvalidOperationException("钥匙串报告密钥已存在，但读不回来。");
        }

        if (status != ErrSecSuccess)
        {
            Array.Clear(key, 0, key.Length);

            // Thrown, never swallowed. A caller that treated this as "no encryption
            // available" would have to choose between failing and writing the token in
            // the clear, and the second is what this whole layer exists to prevent.
            throw new InvalidOperationException($"无法在钥匙串中保存密钥，状态码 {status}。");
        }

        return key;
    }

    [DllImport(Security, CharSet = CharSet.Ansi)]
    private static extern int SecKeychainFindGenericPassword(
        IntPtr keychainOrArray,
        uint serviceNameLength,
        string serviceName,
        uint accountNameLength,
        string accountName,
        out uint passwordLength,
        out IntPtr passwordData,
        IntPtr itemRef);

    [DllImport(Security, CharSet = CharSet.Ansi)]
    private static extern int SecKeychainAddGenericPassword(
        IntPtr keychain,
        uint serviceNameLength,
        string serviceName,
        uint accountNameLength,
        string accountName,
        uint passwordLength,
        byte[] passwordData,
        IntPtr itemRef);

    [DllImport(Security)]
    private static extern int SecKeychainItemFreeContent(IntPtr attrList, IntPtr data);
}
