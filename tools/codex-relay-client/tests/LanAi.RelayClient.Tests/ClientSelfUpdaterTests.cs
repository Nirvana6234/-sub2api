using System.IO;
using System.IO.Compression;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The download-and-decide half of applying an update: what actually lands on disk, and what
/// gets asked of <see cref="IClientRelaunchHost"/> for each channel. Never exercises a real
/// process spawn — <see cref="FakeRelaunchHost"/> stands in for that, the same way
/// <c>FakeCodexStartup</c> stands in for actually launching Codex.
/// </summary>
public sealed class ClientSelfUpdaterTests : IAsyncDisposable
{
    private readonly string _installDirectory = Path.Combine(Path.GetTempPath(), $"relay-update-{Guid.NewGuid():N}");
    private readonly HttpClient _http = new();

    /// <summary>
    /// A path under a throwaway directory this test owns — never the real test runner's own
    /// executable, which ApplyWindowsAsync would otherwise derive an "update" folder from.
    /// </summary>
    private string ExePath => Path.Combine(_installDirectory, "app.exe");

    private string UpdateRoot => Path.Combine(_installDirectory, "update");

    public async ValueTask DisposeAsync()
    {
        _http.Dispose();
        try
        {
            if (Directory.Exists(_installDirectory)) Directory.Delete(_installDirectory, recursive: true);
        }
        catch (IOException) { }
        await Task.CompletedTask;
    }

    private ClientSelfUpdater Updater(FakeRelaunchHost host) =>
        new(_http, host, () => ExePath);

    [Fact]
    public async Task ASelfReplaceDownloadsAndExtractsTheZipForTheHostToCopy()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("hello.txt", "hi")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Restarting, result.Outcome);
        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        Assert.Equal(Path.Combine(UpdateRoot, "extracted"), call.StagingDirectory);
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "hello.txt")));
        Assert.Equal("hi", await File.ReadAllTextAsync(Path.Combine(call.StagingDirectory, "hello.txt")));
        Assert.Equal(ExePath, call.ExePath);
        Assert.Equal(_installDirectory, call.InstallDirectory);
        Assert.Equal(Environment.ProcessId, call.CurrentProcessId);
        // No leftover staging artefact beside the real one.
        Assert.False(Directory.Exists(Path.Combine(UpdateRoot, "extracted.tmp")));
    }

    /// <summary>
    /// The package's own exe is never named after the process running it — packaging renames it
    /// to 共飞-ChatGPT助手.exe, and a user may rename it again after installing. robocopy matches
    /// by filename alone, so without this the new exe would land as an unrelated extra file and
    /// the real, running one would never be touched at all.
    /// </summary>
    [Fact]
    public async Task TheShippedExeIsRenamedToMatchWhateverThisProcessIsRunningAs()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("共飞-ChatGPT助手.exe", "new build"), ("readme.txt", "hi")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Restarting, result.Outcome);
        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        // Renamed to match ExePath's own basename ("app.exe"), not left under the shipped name.
        Assert.False(File.Exists(Path.Combine(call.StagingDirectory, "共飞-ChatGPT助手.exe")));
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "app.exe")));
        Assert.Equal("new build", await File.ReadAllTextAsync(Path.Combine(call.StagingDirectory, "app.exe")));
        // Untouched: renaming the exe must not disturb anything else in the package.
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "readme.txt")));
    }

    /// <summary>
    /// An ambiguous package (no top-level exe found, or more than one) is left exactly as
    /// published rather than guessing which file to rename — a wrong guess would rename the
    /// wrong file over the real app.
    /// </summary>
    [Fact]
    public async Task AnAmbiguousPackageIsLeftAsPublished()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("one.exe", "a"), ("two.exe", "b")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        await updater.ApplyAsync(update);

        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "one.exe")));
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "two.exe")));
        Assert.False(File.Exists(Path.Combine(call.StagingDirectory, "app.exe")));
    }

    [Fact]
    public async Task ASecondCallReusesTheDownloadedZipInsteadOfFetchingItAgain()
    {
        Directory.CreateDirectory(_installDirectory);
        byte[] zip = BuildZip(("a.txt", "first"));
        await using var upstream = await ZipUpstream.StartAsync(zip);
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        await updater.ApplyAsync(update);
        string zipPath = Path.Combine(UpdateRoot, "package.zip");
        DateTime writtenAt = File.GetLastWriteTimeUtc(zipPath);

        // A fresh attempt, as if the relaunch step failed and the (real) helper removed the
        // extraction on its way out — only the zip survives for this to find.
        Directory.Delete(Path.Combine(UpdateRoot, "extracted"), recursive: true);
        await Task.Delay(50); // past filesystem timestamp resolution, so a rewrite would show
        await updater.ApplyAsync(update);

        // Untouched: a re-download would have rewritten it just now.
        Assert.Equal(writtenAt, File.GetLastWriteTimeUtc(zipPath));
        Assert.Equal(2, host.RelaunchCalls.Count);
        Assert.Equal(2, upstream.RequestCount); // still asked for headers each time, just not the body
    }

    /// <summary>
    /// The point of downloading into the install directory rather than %TEMP%: an extraction
    /// that finished but was never applied (the helper never ran, or it did and the relaunch
    /// step failed) is reused on the next click rather than fetched and unzipped from nothing.
    /// </summary>
    [Fact]
    public async Task AnAlreadyExtractedPackageIsAppliedWithoutTouchingTheNetworkAtAll()
    {
        Directory.CreateDirectory(Path.Combine(UpdateRoot, "extracted"));
        await File.WriteAllTextAsync(Path.Combine(UpdateRoot, "extracted", "marker.txt"), "from last time");
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        // No upstream started at all — a request here would refuse to connect and fail the test.
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri("http://127.0.0.1:1/unreachable"));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Restarting, result.Outcome);
        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        Assert.Equal("from last time", await File.ReadAllTextAsync(Path.Combine(call.StagingDirectory, "marker.txt")));
    }

    /// <summary>
    /// Left in place rather than deleted: the extraction is still good, so the point of keeping
    /// it is exactly to let the next click retry without downloading or unzipping again.
    /// </summary>
    [Fact]
    public async Task WhenTheHelperCannotBeStartedTheExtractedFilesSurviveForARetry()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("a.txt", "a")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = false };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.True(Directory.Exists(Path.Combine(UpdateRoot, "extracted")));
        Assert.True(File.Exists(Path.Combine(UpdateRoot, "package.zip")));
    }

    [Fact]
    public async Task AFailedDownloadIsAProblemAndNothingIsStaged()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("a.txt", "a")), status: 404);
        var host = new FakeRelaunchHost();
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.Empty(host.RelaunchCalls);
        Assert.False(Directory.Exists(Path.Combine(UpdateRoot, "extracted")));
    }

    /// <summary>
    /// A corrupt zip must never leave <c>extracted\</c> looking usable — that directory's mere
    /// existence is what the reuse path above trusts. The zip is dropped too, since retrying
    /// with the same bytes can never succeed; only a fresh download can.
    /// </summary>
    [Fact]
    public async Task GarbageBytesAreAProblemAndLeaveNothingAReplayWouldTrust()
    {
        Directory.CreateDirectory(_installDirectory);
        await using var upstream = await ZipUpstream.StartAsync([1, 2, 3, 4, 5]);
        var host = new FakeRelaunchHost();
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.Empty(host.RelaunchCalls);
        Assert.False(Directory.Exists(Path.Combine(UpdateRoot, "extracted")));
        Assert.False(Directory.Exists(Path.Combine(UpdateRoot, "extracted.tmp")));
        Assert.False(File.Exists(Path.Combine(UpdateRoot, "package.zip")));
    }

    [Fact]
    public async Task DownloadProgressIsReportedAsBytesArrive()
    {
        Directory.CreateDirectory(_installDirectory);
        // Large enough to cross the copy loop's buffer more than once, so progress is seen
        // moving rather than jumping straight from nothing to done in one read.
        string big = new('x', 250_000);
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("big.txt", big)));
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));
        var reports = new List<double>();
        var progress = new Progress<double>(reports.Add);

        await updater.ApplyAsync(update, progress);
        // Progress<T> marshals through the captured SynchronizationContext; give it a moment.
        for (int i = 0; i < 50 && reports.Count == 0; i++)
        {
            await Task.Delay(10);
        }

        Assert.NotEmpty(reports);
        Assert.True(reports[^1] is > 0.99 and <= 1.0);
        Assert.True(reports.SequenceEqual(reports.OrderBy(v => v)), "progress must never move backwards");
    }

    [Fact]
    public async Task RunInTerminalNeverTouchesTheNetwork()
    {
        var host = new FakeRelaunchHost { TerminalResult = true };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.RunInTerminal,
            TerminalCommand: "curl -fsSL https://example.test/install-mac.sh | bash");

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.OpenedTerminal, result.Outcome);
        Assert.Equal(["curl -fsSL https://example.test/install-mac.sh | bash"], host.TerminalCommands);
        Assert.Empty(host.RelaunchCalls);
    }

    [Fact]
    public async Task ATerminalThatWillNotOpenIsAProblem()
    {
        var host = new FakeRelaunchHost { TerminalResult = false };
        ClientSelfUpdater updater = Updater(host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.RunInTerminal,
            TerminalCommand: "curl -fsSL https://example.test/install-mac.sh | bash");

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.NotNull(result.Note);
    }

    [Fact]
    public async Task OpenDownloadPageAsksTheHostForThatExactPage()
    {
        var host = new FakeRelaunchHost { UrlResult = true };
        ClientSelfUpdater updater = Updater(host);
        var downloadPage = new Uri("https://example.test/download");
        var update = new ClientUpdateInfo(new Version(0, 9), downloadPage, ClientUpdateChannel.OpenDownloadPage);

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.OpenedDownloadPage, result.Outcome);
        Assert.Equal([downloadPage], host.OpenedUrls);
    }

    private static byte[] BuildZip(params (string Name, string Content)[] entries)
    {
        using var stream = new MemoryStream();
        using (var archive = new ZipArchive(stream, ZipArchiveMode.Create, leaveOpen: true))
        {
            foreach ((string name, string content) in entries)
            {
                ZipArchiveEntry entry = archive.CreateEntry(name);
                using Stream entryStream = entry.Open();
                using var writer = new StreamWriter(entryStream);
                writer.Write(content);
            }
        }

        return stream.ToArray();
    }

    /// <summary>Serves fixed bytes, standing in for the download proxy; counts requests received.</summary>
    private sealed class ZipUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener = new();
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;
        private readonly byte[] _body;
        private readonly int _status;
        private int _requestCount;

        public string BaseAddress { get; }

        public int RequestCount => _requestCount;

        private ZipUpstream(int port, byte[] body, int status)
        {
            BaseAddress = $"http://127.0.0.1:{port}/package.zip";
            _body = body;
            _status = status;
            _listener.Prefixes.Add($"http://127.0.0.1:{port}/");
            _listener.Start();
            _serve = ServeAsync(_stop.Token);
        }

        public static Task<ZipUpstream> StartAsync(byte[] body, int status = 200)
        {
            using var probe = new TcpListener(IPAddress.Loopback, 0);
            probe.Start();
            int port = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            return Task.FromResult(new ZipUpstream(port, body, status));
        }

        private async Task ServeAsync(CancellationToken cancellationToken)
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                HttpListenerContext context;
                try { context = await _listener.GetContextAsync().WaitAsync(cancellationToken); }
                catch (Exception) { break; }

                Interlocked.Increment(ref _requestCount);
                context.Response.StatusCode = _status;
                if (_status == 200)
                {
                    context.Response.ContentType = "application/zip";
                    context.Response.ContentLength64 = _body.Length;
                    await context.Response.OutputStream.WriteAsync(_body, cancellationToken);
                }

                context.Response.Close();
            }
        }

        public async ValueTask DisposeAsync()
        {
            _stop.Cancel();
            if (_listener.IsListening) _listener.Stop();
            try { await _serve.ConfigureAwait(false); } catch (OperationCanceledException) { }
            _stop.Dispose();
            _listener.Close();
        }
    }

    private sealed class FakeRelaunchHost : IClientRelaunchHost
    {
        public sealed record RelaunchCall(string StagingDirectory, string InstallDirectory, string ExePath, int CurrentProcessId);

        public List<RelaunchCall> RelaunchCalls { get; } = [];

        public List<string> TerminalCommands { get; } = [];

        public List<Uri> OpenedUrls { get; } = [];

        public bool RelaunchHelperResult { get; set; }

        public bool TerminalResult { get; set; }

        public bool UrlResult { get; set; }

        public bool StartRelaunchHelper(string stagingDirectory, string installDirectory, string exePath, int currentProcessId)
        {
            RelaunchCalls.Add(new RelaunchCall(stagingDirectory, installDirectory, exePath, currentProcessId));
            return RelaunchHelperResult;
        }

        public bool OpenTerminalWithCommand(string command)
        {
            TerminalCommands.Add(command);
            return TerminalResult;
        }

        public bool OpenUrl(Uri url)
        {
            OpenedUrls.Add(url);
            return UrlResult;
        }
    }
}
