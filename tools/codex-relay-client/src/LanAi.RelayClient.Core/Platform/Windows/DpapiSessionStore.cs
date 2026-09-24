using System.Runtime.Versioning;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Stores the session encrypted with DPAPI under the current user's profile.
/// </summary>
/// <remarks>
/// <para>
/// <see cref="DataProtectionScope.CurrentUser"/> ties the ciphertext to this
/// Windows account, so copying the file to another machine or user yields
/// nothing usable.
/// </para>
/// <para>
/// Every read failure — missing file, truncated file, tampered bytes, a profile
/// restored from another machine — resolves to "no session" rather than an
/// exception. A client that crashes on startup because a cache went bad is worse
/// than one that asks the user to sign in again.
/// </para>
/// <para>
/// Windows only, and marked as such rather than probed at runtime. DPAPI has no
/// counterpart on macOS; the Keychain is a different API with different semantics,
/// so that platform gets its own implementation reached through
/// <see cref="SessionStore.Create"/> — not a branch inside this class.
/// </para>
/// </remarks>
[SupportedOSPlatform("windows")]
internal sealed class DpapiSessionStore : ISessionStore
{
    /// <summary>
    /// Extra entropy mixed into the DPAPI operation.
    /// </summary>
    /// <remarks>
    /// Not a secret — it is compiled in and provides no protection on its own. It
    /// scopes the ciphertext to this application, so another program running as
    /// the same user cannot decrypt the blob simply by handing it to DPAPI.
    /// </remarks>
    private static readonly byte[] Entropy = Encoding.UTF8.GetBytes("LanAi.RelayClient.Session.v1");

    private readonly string _filePath;

    public DpapiSessionStore(string? filePath = null) =>
        _filePath = filePath ?? DefaultFilePath();

    internal static string DefaultFilePath() => AppPaths.InData("Auth", "session.bin");

    public StoredSession? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        byte[] plain;
        try
        {
            byte[] protectedBytes = File.ReadAllBytes(_filePath);
            plain = ProtectedData.Unprotect(protectedBytes, Entropy, DataProtectionScope.CurrentUser);
        }
        catch (Exception ex) when (ex is CryptographicException or IOException or UnauthorizedAccessException)
        {
            return null;
        }

        try
        {
            StoredSession? session = JsonSerializer.Deserialize(plain, ClientJsonContext.Default.StoredSession);

            // A session without an access token cannot authenticate anything, so
            // it is indistinguishable from having none.
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

        byte[] plain = JsonSerializer.SerializeToUtf8Bytes(session, ClientJsonContext.Default.StoredSession);
        try
        {
            byte[] protectedBytes = ProtectedData.Protect(plain, Entropy, DataProtectionScope.CurrentUser);

            // Written whole then moved into place: a half-written session file
            // would be indistinguishable from a corrupt one on next launch.
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, protectedBytes);
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        finally
        {
            Array.Clear(plain, 0, plain.Length);
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
            // Sign-out must complete even if the file is locked. The in-memory
            // session is dropped regardless, and a stale file on disk is refused
            // on the next load anyway once the server rejects its tokens.
        }
    }
}
