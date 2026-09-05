using System.IO;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexAccountStoreTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"lanai-codex-account-{Guid.NewGuid():N}.json");

    public void Dispose()
    {
        if (File.Exists(_path))
        {
            File.Delete(_path);
        }
    }

    [Fact]
    public void MissingFileMeansNoAccount()
    {
        Assert.Null(new CodexAccountStore(_path).Load());
    }

    [Fact]
    public void SavedAccountSurvivesARoundTrip()
    {
        new CodexAccountStore(_path).Save("ann@example.com");

        Assert.Equal("ann@example.com", new CodexAccountStore(_path).Load());
    }

    [Fact]
    public void AccountIsTrimmedAndComparedCaseInsensitively()
    {
        new CodexAccountStore(_path).Save("  Ann@Example.COM  ");

        Assert.Equal("ann@example.com", new CodexAccountStore(_path).Load());
    }

    [Fact]
    public void CorruptFileMeansNoAccount()
    {
        File.WriteAllText(_path, "{not json");

        Assert.Null(new CodexAccountStore(_path).Load());
    }
}
