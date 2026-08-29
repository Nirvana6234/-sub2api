using System.Security.Cryptography;
using System.Text.Json;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Platform.MacOS;

/// <summary>
/// Stores the session encrypted under a Keychain-held key — the macOS counterpart of
/// <c>DpapiSessionStore</c>.
/// </summary>
/// <remarks>
/// <para>
/// Same file, same path, same JSON as the Windows store: <c>Auth/session.bin</c> under
/// <see cref="AppPaths"/>, serialised through <c>ClientJsonContext</c>. Only the
/// encryption differs, because only the encryption has to.
/// </para>
/// <para>
/// Read failures resolve to "no session" exactly as on Windows — missing file,
/// truncated file, tampered bytes, a home directory copied from another Mac. A client
/// that refuses to start because a cache went bad is worse than one that asks for a
/// password. <b>Write failures do not get the same treatment</b>: if the key cannot be
/// obtained, <see cref="Save"/> throws and nothing is written, because the only other
/// option is a plaintext token on disk.
/// </para>
/// </remarks>
internal sealed class KeychainSessionStore : ISessionStore
{
    private readonly IMasterKeyStore _keys;
    private readonly string _filePath;

    public KeychainSessionStore(IMasterKeyStore keys, string? filePath = null)
    {
        _keys = keys ?? throw new ArgumentNullException(nameof(keys));
        _filePath = filePath ?? AppPaths.InData("Auth", "session.bin");
    }

    public StoredSession? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        byte[] plain;
        try
        {
            byte[] key = _keys.TryRead() ?? throw new CryptographicException("钥匙串中没有会话密钥。");
            try
            {
                plain = SecretEnvelope.Open(key, File.ReadAllBytes(_filePath));
            }
            finally
            {
                Array.Clear(key, 0, key.Length);
            }
        }
        catch (Exception ex) when (ex is CryptographicException
            or IOException
            or UnauthorizedAccessException
            or InvalidOperationException)
        {
            return null;
        }

        try
        {
            StoredSession? session = JsonSerializer.Deserialize(plain, ClientJsonContext.Default.StoredSession);

            // A session without an access token cannot authenticate anything, so it is
            // indistinguishable from having none.
            return string.IsNullOrWhiteSpace(session?.AccessToken) ? null : session;
        }
        catch (JsonException)
        {
            return null;
        }
        finally
        {
            Array.Clear(plain, 0, plain.Length);
        }
    }

    public void Save(StoredSession session)
    {
        ArgumentNullException.ThrowIfNull(session);

        Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

        byte[] key = _keys.ReadOrCreate();
        byte[] plain = JsonSerializer.SerializeToUtf8Bytes(session, ClientJsonContext.Default.StoredSession);
        try
        {
            byte[] sealedBytes = SecretEnvelope.Seal(key, plain);

            // Written whole then moved into place: a half-written session file would be
            // indistinguishable from a corrupt one on next launch.
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, sealedBytes);
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        finally
        {
            Array.Clear(plain, 0, plain.Length);
            Array.Clear(key, 0, key.Length);
        }
    }

    public void Clear()
    {
        try
        {
            if (File.Exists(_filePath))
            {
                File.Delete(_filePath);
            }
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Sign-out must complete even if the file is locked. The in-memory session
            // is dropped regardless, and a stale file is refused on the next load once
            // the server rejects its tokens.
        }
    }
}
