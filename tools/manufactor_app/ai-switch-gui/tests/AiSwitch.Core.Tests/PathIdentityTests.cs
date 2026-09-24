using System.Text.RegularExpressions;

namespace LanAi.Workspace.Core.Tests;

public sealed class PathIdentityTests
{
    [Fact]
    public void Normalize_ProducesAbsolutePathWithoutTrailingSeparator()
    {
        string basePath = Path.Combine(Path.GetTempPath(), "lan-ai-tests");

        string result = PathIdentity.Normalize($"project{Path.DirectorySeparatorChar}", basePath);

        Assert.Equal(Path.Combine(basePath, "project"), result);
        Assert.True(Path.IsPathFullyQualified(result));
    }

    [Fact]
    public void Normalize_PreservesFileSystemRoot()
    {
        string root = Path.GetPathRoot(Path.GetFullPath("."))!;

        Assert.Equal(Path.TrimEndingDirectorySeparator(root), PathIdentity.Normalize(root));
    }

    [Fact]
    public void CreateStableId_IsDeterministicSha256Hex()
    {
        string path = Path.Combine(Path.GetTempPath(), "lan-ai-tests", "project");

        string first = PathIdentity.CreateStableId(path);
        string second = PathIdentity.CreateStableId(path + Path.DirectorySeparatorChar);

        Assert.Equal(first, second);
        Assert.Matches(new Regex("^[0-9a-f]{64}$", RegexOptions.CultureInvariant), first);
    }

    [Fact]
    public void CreateStableId_DiffersForDifferentPaths()
    {
        string basePath = Path.Combine(Path.GetTempPath(), "lan-ai-tests");

        string first = PathIdentity.CreateStableId("project-a", basePath);
        string second = PathIdentity.CreateStableId("project-b", basePath);

        Assert.NotEqual(first, second);
    }

    [Fact]
    public void CreateStableId_IsCaseInsensitiveOnWindows()
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }

        string path = Path.Combine(Path.GetTempPath(), "LanAi-Tests", "Project");

        Assert.Equal(
            PathIdentity.CreateStableId(path),
            PathIdentity.CreateStableId(path.ToLowerInvariant()));
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void Normalize_RejectsBlankPaths(string path)
    {
        Assert.Throws<ArgumentException>(() => PathIdentity.Normalize(path));
    }
}
