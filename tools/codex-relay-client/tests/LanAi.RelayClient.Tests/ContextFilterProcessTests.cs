using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Drives the real bundled filter, because the properties that matter here are
/// properties of that binary.
/// </summary>
/// <remarks>
/// The switch's whole design rests on two measured facts — that the port survives a
/// restart, and that compression-off is a transparent pass-through rather than a
/// dead end. Neither can be asserted against a stub. When the optional binary is not
/// present these tests no-op rather than fail: the client is expected to work without
/// it, so its absence is not a broken build.
/// </remarks>
public sealed class ContextFilterProcessTests
{
    private static string? ExecutablePath()
    {
        string path = Path.Combine(AppContext.BaseDirectory, "context-filter.exe");
        return File.Exists(path) ? path : null;
    }

    [Fact]
    public async Task TogglingCompressionKeepsThePortCodexWasPointedAt()
    {
        // If the port moved, the switch would silently point Codex at nothing —
        // and whether a running ChatGPT re-reads config.toml has never been verified.
        if (ExecutablePath() is not { } exe) return;

        await using var upstream = await EchoUpstream.StartAsync();
        await using var filter = new ContextFilterProcess(exe);
        filter.SetUpstream(new Uri(upstream.BaseAddress + "/v1/"));

        await filter.StartAsync(filterEnabled: true);
        Uri? first = filter.BaseAddress;
        Assert.NotNull(first);
        Assert.True(filter.IsRunning);
        Assert.True(filter.FilterEnabled);

        await filter.ApplyFilterEnabledAsync(false);

        Assert.Equal(first, filter.BaseAddress);
        Assert.True(filter.IsRunning);
        Assert.False(filter.FilterEnabled);
    }

    [Fact]
    public async Task ItStillForwardsWithCompressionOff()
    {
        // "Off" must mean "do not compress", never "do not relay".
        if (ExecutablePath() is not { } exe) return;

        await using var upstream = await EchoUpstream.StartAsync();
        await using var filter = new ContextFilterProcess(exe);
        filter.SetUpstream(new Uri(upstream.BaseAddress + "/v1/"));
        await filter.StartAsync(filterEnabled: false);

        using var client = new HttpClient();
        using var request = new HttpRequestMessage(
            HttpMethod.Post,
            new Uri(filter.BaseAddress!, "responses"))
        {
            Content = new StringContent("""{"model":"gpt-5","input":[]}""", Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer local-token");
        HttpResponseMessage response = await client.SendAsync(request);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        (string path, string auth) = Assert.Single(upstream.Requests);
        Assert.Equal("/v1/responses", path);
        // The local token has to survive the hop, or the relay behind it would 401.
        Assert.Equal("Bearer local-token", auth);
    }

    [Fact]
    public async Task TheLogSaysWhetherCompressionActuallyRanOnThisTurn()
    {
        // The whole chain, as it ships: filter in front, relay behind. Asserted through
        // what the relay really logs because "can I tell from the log afterwards" is
        // the question this is here to answer, and a unit test of the formatter alone
        // would not notice the header names drifting. Captured rather than read back
        // from the file: other tests' relays log 「本轮未经过上下文压缩」 into the same
        // file at the same time. Opened before the relay starts, so its request loop
        // inherits the capture.
        if (ExecutablePath() is not { } exe) return;

        var log = new StringBuilder();
        using IDisposable capture = ClientLog.Capture(log);
        await using var upstream = await EchoUpstream.StartAsync();
        await using var relay = new LocalPawRelay(upstream.BaseAddress, _ => Task.FromResult("jwt"));
        await relay.StartAsync();
        relay.SetGroup(7);

        await using var filter = new ContextFilterProcess(exe);
        filter.SetUpstream(relay.BaseAddress!);
        await filter.StartAsync(filterEnabled: true);

        int before = log.Length;
        using (var client = new HttpClient())
        using (var request = new HttpRequestMessage(
            HttpMethod.Post,
            new Uri(filter.BaseAddress!, "responses"))
        {
            Content = new StringContent("""{"model":"gpt-5","input":[]}""", Encoding.UTF8, "application/json"),
        })
        {
            request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
            using HttpResponseMessage response = await client.SendAsync(request);
            Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        }

        string written = log.ToString(before, log.Length - before);
        Assert.Contains("上下文压缩", written, StringComparison.Ordinal);
        // Reached the relay through the filter, so it must not read as "not filtered".
        Assert.DoesNotContain("本轮未经过上下文压缩", written, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ARealCompressibleTurnAdvancesTheUsageStore()
    {
        // Every other usage-store test drives the callback with synthetic headers
        // (LocalPawRelayTests) or synthetic byte counts (ContextFilterUsageStoreTests).
        // This is the one place the real filter, a real payload shaped like an actual
        // Codex turn, the real relay, and the store all run together — the question
        // being answered is "does the estimate on screen actually move", which none of
        // the unit-level tests can see on their own.
        if (ExecutablePath() is not { } exe) return;

        // Three turns of old, repetitive tool output ahead of a final question — the
        // shape the filter is documented to compress (old tool output and images), not
        // the running conversation text itself. A short {"input":[]} body, as the other
        // tests here use, has nothing in it for the filter to touch.
        string toolOutput = string.Join('\n', Enumerable.Range(0, 400).Select(i => $"line {i}: build output token here"));
        var input = new List<object>();
        for (int turn = 0; turn < 3; turn++)
        {
            input.Add(new { type = "message", role = "user", content = new[] { new { type = "input_text", text = $"step {turn}" } } });
            input.Add(new { type = "function_call", name = "shell", arguments = "{}", call_id = $"c{turn}" });
            input.Add(new { type = "function_call_output", call_id = $"c{turn}", output = toolOutput });
        }
        input.Add(new { type = "message", role = "user", content = new[] { new { type = "input_text", text = "summarize" } } });
        string body = JsonSerializer.Serialize(new { model = "gpt-5", input });

        await using var upstream = await EchoUpstream.StartAsync();
        var usageStore = new ContextFilterUsageStore(Path.Combine(_scratchDir, "usage.json"));
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => Task.FromResult("jwt"),
            (before, saved) => usageStore.Add(before, saved));
        await relay.StartAsync();
        relay.SetGroup(7);

        await using var filter = new ContextFilterProcess(exe);
        filter.SetUpstream(relay.BaseAddress!);
        await filter.StartAsync(filterEnabled: true);

        using var client = new HttpClient();
        using var request = new HttpRequestMessage(HttpMethod.Post, new Uri(filter.BaseAddress!, "responses"))
        {
            Content = new StringContent(body, Encoding.UTF8, "application/json"),
        };
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
        using HttpResponseMessage response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        ContextFilterUsage usage = usageStore.Load();
        Assert.True(usage.TotalBytesBefore > 0, "过滤器应当报告处理过的字节数");
        Assert.True(usage.TotalBytesSaved > 0, $"这份载荷设计为可压缩，但没有省下任何字节（{usage})");
        Assert.Contains("累计处理约", ContextFilterUsageStore.Describe(usage), StringComparison.Ordinal);
    }

    private readonly string _scratchDir = Path.Combine(Path.GetTempPath(), $"cf-usage-e2e-{Guid.NewGuid():N}");

    [Fact]
    public async Task AppliedBeforeAnythingIsRunningItOnlyRecordsTheChoice()
    {
        if (ExecutablePath() is not { } exe) return;

        await using var filter = new ContextFilterProcess(exe);

        await filter.ApplyFilterEnabledAsync(false);

        Assert.False(filter.IsRunning);
        Assert.False(filter.FilterEnabled);
        Assert.Null(filter.BaseAddress);
    }

    /// <summary>Answers anything, recording the path and credential it was given.</summary>
    private sealed class EchoUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener;
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;

        public List<(string Path, string? Authorization)> Requests { get; } = [];

        public string BaseAddress { get; }

        private EchoUpstream()
        {
            _listener = LoopbackHttpListener.Start(null, out int port);
            BaseAddress = $"http://127.0.0.1:{port}";
            _serve = ServeAsync(_stop.Token);
        }

        public static Task<EchoUpstream> StartAsync()
        {
            return Task.FromResult(new EchoUpstream());
        }

        private async Task ServeAsync(CancellationToken cancellationToken)
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                HttpListenerContext context;
                try { context = await _listener.GetContextAsync().WaitAsync(cancellationToken); }
                catch (Exception) { break; }

                lock (Requests)
                {
                    Requests.Add((
                        context.Request.Url?.AbsolutePath ?? string.Empty,
                        context.Request.Headers["Authorization"]));
                }

                byte[] payload = Encoding.UTF8.GetBytes("data: {\"type\":\"response.completed\"}\n\n");
                context.Response.StatusCode = 200;
                context.Response.ContentType = "text/event-stream";
                await context.Response.OutputStream.WriteAsync(payload, cancellationToken);
                context.Response.Close();
            }
        }

        public async ValueTask DisposeAsync()
        {
            await _stop.CancelAsync();
            _listener.Close();
            try { await _serve; } catch (Exception) { }
            _stop.Dispose();
        }
    }
}
