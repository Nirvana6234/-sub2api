using System.Runtime.Versioning;
using System.Security.Cryptography;
using System.Text;
using LanAi.RelayClient.CodexBinding;

namespace LanAi.RelayClient.Services;

/// <summary>Encrypts the Codex snapshot with DPAPI, scoped to this Windows user.</summary>
/// <remarks>
/// Windows only. The blobs this protects are the user's own <c>auth.json</c> and
/// <c>config.toml</c> — see <see cref="Platform.AppPaths.CodexSnapshotRoot"/> for why
/// losing them cannot be undone. macOS needs a Keychain-backed counterpart before the
/// client can take over Codex there at all, which is why
/// <see cref="SnapshotProtector.Create"/> refuses rather than falling back to
/// plaintext.
/// </remarks>
[SupportedOSPlatform("windows")]
internal sealed class DpapiSnapshotProtector : ISnapshotProtector
{
    private static readonly byte[] Entropy =
        Encoding.UTF8.GetBytes("LanAi.RelayClient.CodexSnapshot.v1");

    public byte[] Protect(byte[] plaintext)
    {
        ArgumentNullException.ThrowIfNull(plaintext);
        return ProtectedData.Protect(plaintext, Entropy, DataProtectionScope.CurrentUser);
    }

    public byte[] Unprotect(byte[] protectedData)
    {
        ArgumentNullException.ThrowIfNull(protectedData);
        return ProtectedData.Unprotect(protectedData, Entropy, DataProtectionScope.CurrentUser);
    }
}
