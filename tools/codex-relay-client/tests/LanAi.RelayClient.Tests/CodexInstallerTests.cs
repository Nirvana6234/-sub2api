using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class CodexInstallerTests
{
    [Fact]
    public void MissingDirectoryIsReportedAndCanBeCreated()
    {
        string directory = Path.Combine(Path.GetTempPath(), "codex-installer-" + Guid.NewGuid());
        var launcher = new RecordingProcessLauncher();
        var installer = new CodexInstaller(directory, launcher);

        CodexInstallerInspection inspection = installer.Inspect();
        CodexInstallerResult result = installer.Launch();

        Assert.False(inspection.DirectoryExists);
        Assert.True(Directory.Exists(directory));
        Assert.False(result.Started);
        Assert.Equal(directory, launcher.LastStart!.FileName);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void UnsupportedFilesAreIgnored()
    {
        string directory = CreateDirectory();
        File.WriteAllText(Path.Combine(directory, "notes.txt"), "not an installer");

        var installer = new CodexInstaller(directory, new RecordingProcessLauncher());

        Assert.False(installer.Inspect().PackageAvailable);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void NewestSupportedPackageWinsWhenSeveralExist()
    {
        string directory = CreateDirectory();
        string oldPackage = Path.Combine(directory, "Codex-old.msix");
        string newPackage = Path.Combine(directory, "Codex-new.msix");
        File.WriteAllText(oldPackage, "old");
        File.WriteAllText(newPackage, "new");
        File.SetLastWriteTimeUtc(oldPackage, DateTime.UtcNow.AddMinutes(-2));
        File.SetLastWriteTimeUtc(newPackage, DateTime.UtcNow);

        var installer = new CodexInstaller(directory, new RecordingProcessLauncher());

        Assert.Equal(newPackage, installer.Inspect().PackagePath);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void MsiUsesMsiexecInstallArgument()
    {
        string directory = CreateDirectory();
        string package = Path.Combine(directory, "Codex.msi");
        File.WriteAllText(package, "msi");
        var launcher = new RecordingProcessLauncher();
        var installer = new CodexInstaller(directory, launcher);

        CodexInstallerResult result = installer.Launch();

        Assert.True(result.Started);
        Assert.NotNull(result.InstallerProcess);
        Assert.Equal("msiexec.exe", launcher.LastStart!.FileName);
        Assert.Contains("/i", launcher.LastStart.Arguments);
        Assert.Contains(package, launcher.LastStart.Arguments);
        Assert.Contains("/qn", launcher.LastStart.Arguments);
        Assert.Contains("/norestart", launcher.LastStart.Arguments);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public void EmptyDirectoryReturnsCopyInstruction()
    {
        string directory = CreateDirectory();
        var launcher = new RecordingProcessLauncher();
        var installer = new CodexInstaller(directory, launcher);

        CodexInstallerResult result = installer.Launch();

        Assert.False(result.Started);
        Assert.Contains(directory, result.Message);
        Assert.Equal(directory, launcher.LastStart!.FileName);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task MissingPackageIsDownloadedWithProgressThenLaunched()
    {
        string directory = CreateDirectory();
        var launcher = new RecordingProcessLauncher();
        using var http = new HttpClient(new InstallerResponseHandler(HttpStatusCode.OK, [1, 2, 3, 4]));
        var installer = new CodexInstaller(directory, launcher, http, new Uri("https://mirror.test/latest/win-x64"));
        var progress = new RecordingProgress<CodexDownloadProgress>();

        CodexInstallerResult result = await installer.EnsureAndLaunchAsync(progress);

        string packagePath = Path.Combine(directory, "ChatGPT-Windows-x64.msix");
        Assert.True(result.Started);
        Assert.Equal([1, 2, 3, 4], File.ReadAllBytes(packagePath));
        Assert.False(File.Exists(packagePath + ".part"));
        Assert.Equal(packagePath, launcher.LastStart!.FileName);
        Assert.Equal(100, progress.Values[^1].Percent);
        Directory.Delete(directory, recursive: true);
    }

    [Fact]
    public async Task FailedDownloadLeavesNoPartialPackage()
    {
        string directory = CreateDirectory();
        var launcher = new RecordingProcessLauncher();
        using var http = new HttpClient(new InstallerResponseHandler(HttpStatusCode.BadGateway, []));
        var installer = new CodexInstaller(directory, launcher, http, new Uri("https://mirror.test/latest/win-x64"));

        CodexInstallerResult result = await installer.EnsureAndLaunchAsync();

        Assert.False(result.Started);
        Assert.Null(launcher.LastStart);
        Assert.False(File.Exists(Path.Combine(directory, "ChatGPT-Windows-x64.msix.part")));
        Directory.Delete(directory, recursive: true);
    }

    private static string CreateDirectory()
    {
        string directory = Path.Combine(Path.GetTempPath(), "codex-installer-" + Guid.NewGuid());
        Directory.CreateDirectory(directory);
        return directory;
    }

    private sealed class RecordingProcessLauncher : IProcessLauncher
    {
        public ProcessStartInfo? LastStart { get; private set; }

        public Process? ProcessToReturn { get; set; } = Process.GetCurrentProcess();

        public Process? Start(ProcessStartInfo startInfo)
        {
            LastStart = startInfo;
            return ProcessToReturn;
        }
    }

    private sealed class RecordingProgress<T> : IProgress<T>
    {
        public List<T> Values { get; } = [];

        public void Report(T value) => Values.Add(value);
    }

    private sealed class InstallerResponseHandler(HttpStatusCode statusCode, byte[] content) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(new HttpResponseMessage(statusCode)
            {
                Content = new ByteArrayContent(content),
            });
    }
}
