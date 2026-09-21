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
    private readonly List<string> _tempPaths = [];
    private readonly HttpClient _http = new();

    public async ValueTask DisposeAsync()
    {
        _http.Dispose();
        foreach (string path in _tempPaths)
        {
            try
            {
                if (Directory.Exists(path)) Directory.Delete(path, recursive: true);
                else if (File.Exists(path)) File.Delete(path);
            }
            catch (IOException) { }
        }
    }

    [Fact]
    public async Task ASelfReplaceDownloadsAndExtractsTheZipForTheHostToCopy()
    {
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("hello.txt", "hi")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = true };
        var updater = new ClientSelfUpdater(_http, host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Restarting, result.Outcome);
        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        _tempPaths.Add(call.StagingDirectory);
        Assert.True(File.Exists(Path.Combine(call.StagingDirectory, "hello.txt")));
        Assert.Equal("hi", await File.ReadAllTextAsync(Path.Combine(call.StagingDirectory, "hello.txt")));
        Assert.Equal(Environment.ProcessPath, call.ExePath);
        Assert.Equal(Path.GetDirectoryName(Environment.ProcessPath), call.InstallDirectory);
        Assert.Equal(Environment.ProcessId, call.CurrentProcessId);
    }

    [Fact]
    public async Task AFailedDownloadIsAProblemAndNothingIsStaged()
    {
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("a.txt", "a")), status: 404);
        var host = new FakeRelaunchHost();
        var updater = new ClientSelfUpdater(_http, host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.Empty(host.RelaunchCalls);
    }

    [Fact]
    public async Task GarbageBytesAreAProblemNotACrash()
    {
        await using var upstream = await ZipUpstream.StartAsync([1, 2, 3, 4, 5]);
        var host = new FakeRelaunchHost();
        var updater = new ClientSelfUpdater(_http, host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        Assert.Empty(host.RelaunchCalls);
    }

    /// <summary>
    /// The extracted staging directory is only worth keeping when the helper was actually
    /// started — otherwise nothing will ever clean it up.
    /// </summary>
    [Fact]
    public async Task WhenTheHelperCannotBeStartedTheStagedFilesAreCleanedUp()
    {
        await using var upstream = await ZipUpstream.StartAsync(BuildZip(("a.txt", "a")));
        var host = new FakeRelaunchHost { RelaunchHelperResult = false };
        var updater = new ClientSelfUpdater(_http, host);
        var update = new ClientUpdateInfo(
            new Version(0, 9), new Uri("https://example.test/download"), ClientUpdateChannel.SelfReplace,
            PackageUrl: new Uri(upstream.BaseAddress));

        ClientSelfUpdateResult result = await updater.ApplyAsync(update);

        Assert.Equal(ClientSelfUpdateOutcome.Problem, result.Outcome);
        FakeRelaunchHost.RelaunchCall call = Assert.Single(host.RelaunchCalls);
        Assert.False(Directory.Exists(call.StagingDirectory));
    }

    [Fact]
    public async Task RunInTerminalNeverTouchesTheNetwork()
    {
        var host = new FakeRelaunchHost { TerminalResult = true };
        var updater = new ClientSelfUpdater(_http, host);
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
        var updater = new ClientSelfUpdater(_http, host);
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
        var updater = new ClientSelfUpdater(_http, host);
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

    /// <summary>Serves fixed bytes once per request, standing in for the download proxy.</summary>
    private sealed class ZipUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener = new();
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;
        private readonly byte[] _body;
        private readonly int _status;

        public string BaseAddress { get; }

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
