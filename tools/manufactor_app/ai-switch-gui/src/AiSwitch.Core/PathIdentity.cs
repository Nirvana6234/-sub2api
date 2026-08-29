using System.Security.Cryptography;
using System.Text;

namespace LanAi.Workspace.Core;

/// <summary>
/// Produces normalized paths and deterministic SHA-256 identifiers for project roots.
/// </summary>
public static class PathIdentity
{
    public static string Normalize(string path, string? basePath = null)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);

        string fullPath = basePath is null
            ? Path.GetFullPath(path)
            : Path.GetFullPath(path, basePath);

        string normalized = fullPath
            .Replace(Path.AltDirectorySeparatorChar, Path.DirectorySeparatorChar)
            .Normalize(NormalizationForm.FormC);

        return Path.TrimEndingDirectorySeparator(normalized);
    }

    public static string CreateStableId(string path, string? basePath = null)
    {
        string normalizedPath = Normalize(path, basePath);
        string identityPath = OperatingSystem.IsWindows()
            ? normalizedPath.ToUpperInvariant()
            : normalizedPath;

        byte[] hash = SHA256.HashData(Encoding.UTF8.GetBytes(identityPath));
        return Convert.ToHexString(hash).ToLowerInvariant();
    }
}
