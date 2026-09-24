using System.Security.Cryptography;
using System.Text;

namespace LanAi.Workspace.Wpf.Services;

internal static class LocalControlTokenStore
{
    private const string RelativeTokenPath = ".local/local-control-token.bin";

    public static string? Load(string? nativeRoot)
    {
        if (string.IsNullOrWhiteSpace(nativeRoot))
        {
            return null;
        }

        byte[]? protectedBytes = null;
        byte[]? plainBytes = null;
        try
        {
            string root = Path.GetFullPath(nativeRoot);
            string path = Path.GetFullPath(Path.Combine(root, RelativeTokenPath));
            if (!path.StartsWith(root.TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase) ||
                !File.Exists(path))
            {
                return null;
            }

            protectedBytes = File.ReadAllBytes(path);
            plainBytes = ProtectedData.Unprotect(protectedBytes, null, DataProtectionScope.CurrentUser);
            string token = Encoding.UTF8.GetString(plainBytes).Trim();
            return token.Length is >= 32 and <= 512 && !token.Any(char.IsControl) ? token : null;
        }
        catch (Exception exception) when (
            exception is IOException or UnauthorizedAccessException or CryptographicException or ArgumentException)
        {
            return null;
        }
        finally
        {
            if (plainBytes is not null)
            {
                CryptographicOperations.ZeroMemory(plainBytes);
            }
            if (protectedBytes is not null)
            {
                CryptographicOperations.ZeroMemory(protectedBytes);
            }
        }
    }
}
