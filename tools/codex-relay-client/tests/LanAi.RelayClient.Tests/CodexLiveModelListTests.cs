using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// A group switch reaching a running Codex without restarting it: the relay's version number for
/// the model list, the cached copy the client deletes, and — against the real Codex — the list
/// actually changing in the same process.
/// </summary>
public sealed class CodexLiveModelListTests : IDisposable
{
    private const string Bundled = """
        {"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5","description":"d","priority":1,"visibility":"list","shell_type":"unified_exec","base_instructions":"b"}]}
        """;

    private readonly string _root = Path.Combine(Path.GetTempPath(), "live-list-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        try { Directory.Delete(_root, recursive: true); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    private sealed class FixedSource(string? json) : ICodexCatalogSource
    {
        public Task<string?> GetBundledCatalogAsync(CancellationToken cancellationToken = default) => Task.FromResult(json);
    }

    private static LocalPawRelay NewRelay(string upstream, string? bundled = Bundled) =>
        new(upstream, _ => Task.FromResult("jwt"), codexCatalogSource: bundled is null ? null : new FixedSource(bundled));

    private static async Task<(string? Catalog, string? Reply)> VersionsAsync(LocalPawRelay relay)
    {
        using var client = new HttpClient();
        using var models = new HttpRequestMessage(HttpMethod.Get, new Uri(relay.BaseAddress!, "models"));
        models.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
        using HttpResponseMessage m = await client.SendAsync(models);
        m.Headers.TryGetValues("ETag", out IEnumerable<string>? etag);

        using var post = new HttpRequestMessage(HttpMethod.Post, new Uri(relay.BaseAddress!, "responses"))
        {
            Content = new StringContent("{}", Encoding.UTF8, "application/json"),
        };
        post.Headers.TryAddWithoutValidation("Authorization", "Bearer " + relay.Token);
        using HttpResponseMessage r = await client.SendAsync(post);
        r.Headers.TryGetValues("X-Models-Etag", out IEnumerable<string>? reply);
        return (etag?.SingleOrDefault(), reply?.SingleOrDefault());
    }

    // ---- The version number ---------------------------------------------------

    [Fact]
    public async Task TheCatalogAndTheRepliesToCodexNameTheSameVersion_ByteForByte()
    {
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress);
        await relay.StartAsync();
        relay.SetGroup(7, "g", CodexGroupModels.From(["claude-sonnet-5"]));

        (string? catalog, string? reply) = await VersionsAsync(relay);

        // Codex compares the two as text, quotes included; any difference is a refresh per turn.
        Assert.NotNull(catalog);
        Assert.Equal(catalog, reply);
        Assert.Equal(relay.ModelsEtag, catalog);
        Assert.StartsWith("\"", catalog, StringComparison.Ordinal);
    }

    [Fact]
    public async Task TheVersionChangesWithTheGroupsModelsAndOnlyThen()
    {
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress);
        await relay.StartAsync();

        relay.SetGroup(1, "a", CodexGroupModels.From(["claude-sonnet-5"]));
        string first = relay.ModelsEtag;
        relay.SetGroup(2, "b", CodexGroupModels.From(["claude-sonnet-5"]));
        string sameList = relay.ModelsEtag;
        relay.SetGroup(3, "c", CodexGroupModels.From(["claude-opus-5"]));
        string other = relay.ModelsEtag;
        relay.SetGroup(4, "d", null);
        string bundled = relay.ModelsEtag;
        relay.SetGroup(null, "auto");

        Assert.Equal(first, sameList);
        Assert.NotEqual(first, other);
        Assert.Equal("\"bundled\"", bundled);
        Assert.Equal(bundled, relay.ModelsEtag);
    }

    [Fact]
    public async Task ADefaultChangeIsAVersionChange_BecauseThePickerOrderChanged()
    {
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress);
        await relay.StartAsync();

        relay.SetGroup(1, "a", CodexGroupModels.From(["x", "y"], "x"));
        string first = relay.ModelsEtag;
        relay.SetGroup(1, "a", CodexGroupModels.From(["x", "y"], "y"));

        Assert.NotEqual(first, relay.ModelsEtag);
    }

    [Fact]
    public async Task StampsNothingWhenTheRelayCannotAnswerACatalogRequestAtAll()
    {
        // A version Codex could never match would only make it ask again every turn.
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress, bundled: null);
        await relay.StartAsync();
        relay.SetGroup(1, "a", CodexGroupModels.From(["x"]));

        (_, string? reply) = await VersionsAsync(relay);

        Assert.Equal(string.Empty, relay.ModelsEtag);
        Assert.Null(reply);
    }

    // ---- The cached copy ---------------------------------------------------------

    private (CodexStartup Startup, string Cache) NewStartup(LocalPawRelay relay)
    {
        var relayClient = new FakeRelayClient();
        var session = new RelaySessionManager(relayClient, new FakeSessionStore(), "https://relay.test/");
        var paths = new CodexPaths(Path.Combine(_root, "codex"));
        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, Path.Combine(_root, "snapshot"), protector));
        Directory.CreateDirectory(paths.Home);
        var startup = new CodexStartup(
            relayClient, session, new ManagedKeyNaming(new FixedInstallId("testinst")), writer, new FakeCodexAppLauncher(), localRelay: relay);
        return (startup, Path.Combine(paths.Home, "models_cache.json"));
    }

    [Fact]
    public async Task SwitchingToADifferentListDeletesCodexsCachedCopy_ButTheSameListDoesNot()
    {
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress);
        (CodexStartup startup, string cache) = NewStartup(relay);
        startup.SetActiveGroup(1, "a", CodexGroupModels.From(["claude-sonnet-5"]));
        File.WriteAllText(cache, "{}");

        startup.SetActiveGroup(2, "b", CodexGroupModels.From(["claude-sonnet-5"]));
        Assert.True(File.Exists(cache)); // a different group, the same list: nothing for Codex to learn

        startup.SetActiveGroup(3, "c", CodexGroupModels.From(["claude-opus-5"]));
        Assert.False(File.Exists(cache));

        File.WriteAllText(cache, "{}");
        startup.SetActiveGroup(4, "d", null);
        Assert.False(File.Exists(cache)); // back to Codex's own list
    }

    [Fact]
    public async Task TurningACodexLocalProxyOnOrOffAlsoChangesTheVersion()
    {
        await using var upstream = new SinkUpstream();
        await using LocalPawRelay relay = NewRelay(upstream.BaseAddress);
        (CodexStartup startup, string cache) = NewStartup(relay);
        startup.SetActiveGroup(1, "a", CodexGroupModels.From(["claude-sonnet-5"]));
        File.WriteAllText(cache, "{}");

        // Codex would then be going to the official API, whose list is not the group's.
        startup.SetLocalProxy(
            LanAi.RelayClient.Server.LocalProxyKind.Codex,
            new LocalProxyTarget(-1, "acct"));

        Assert.Equal("\"bundled\"", relay.ModelsEtag);
        Assert.False(File.Exists(cache));
    }

    // ---- The real Codex, no restart ---------------------------------------------------

    [Fact]
    public async Task ARunningCodexShowsTheNewGroupsListAfterOneTurn_WithoutBeingRestarted()
    {
        string? codex = CodexBundledCatalogSource.LocateInstalledCodex();
        if (codex is null)
        {
            return;
        }

        await using var upstream = new SinkUpstream(replySse: true);
        await using var relay = new LocalPawRelay(
            upstream.BaseAddress,
            _ => Task.FromResult("jwt"),
            codexCatalogSource: new CodexBundledCatalogSource(() => codex));
        await relay.StartAsync();

        string home = Path.Combine(_root, "home");
        var paths = new CodexPaths(home);
        var protector = new TestSnapshotProtector();
        var writer = new CodexConfigWriter(
            paths,
            new CodexAuthSnapshot(protector, Path.Combine(_root, "legacy-auth.json")),
            new CodexFileSnapshot(paths, Path.Combine(_root, "snapshot"), protector));
        writer.Apply(relay.Token, relay.Origin + "/v1", "claude-sonnet-5", relay.Origin + LocalPawRelay.CatalogPath);
        relay.SetGroup(1, "Claude", CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"]));

        using Process process = StartAppServer(codex, home);
        try
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(90));
            await Rpc(process, 1, "initialize", """{"clientInfo":{"name":"live","version":"0"}}""", timeout.Token);
            await process.StandardInput.WriteLineAsync("""{"method":"initialized"}""");
            string[] before = await ListAsync(process, 2, timeout.Token);
            Assert.Equal(["claude-opus-5", "claude-sonnet-5"], before.OrderBy(m => m, StringComparer.Ordinal));

            // The switch, exactly as the client performs it: the relay is repointed and Codex's
            // cached copy is dropped. Codex itself is not touched.
            relay.SetGroup(2, "OpenAI", null);
            writer.ForgetCachedModelList();

            // Opening the picker asks Codex for the list again.
            string[] afterPicker = await ListAsync(process, 3, timeout.Token);
            Assert.Contains("gpt-5.5", afterPicker);
            Assert.DoesNotContain(afterPicker, m => m.StartsWith("claude", StringComparison.Ordinal));

            // And the other route: a group with a list again, then only a turn is sent.
            relay.SetGroup(3, "Claude 2", CodexGroupModels.From(["claude-haiku-4"]));
            JsonElement thread = await Rpc(process, 4, "thread/start",
                JsonSerializer.Serialize(new { cwd = Path.Combine(_root, "work"), model = "claude-haiku-4", approvalPolicy = "never", sandbox = "read-only" }),
                timeout.Token);
            string threadId = thread.GetProperty("result").GetProperty("thread").GetProperty("id").GetString()!;
            await Rpc(process, 5, "turn/start",
                JsonSerializer.Serialize(new { threadId, input = new[] { new { type = "text", text = "hi", text_elements = Array.Empty<object>() } } }),
                timeout.Token);

            string[] afterTurn = [];
            for (int i = 0; i < 40 && !afterTurn.SequenceEqual(["claude-haiku-4"]); i++)
            {
                await Task.Delay(250, timeout.Token);
                afterTurn = await ListAsync(process, 100 + i, timeout.Token);
            }

            Assert.Equal(["claude-haiku-4"], afterTurn);
        }
        finally
        {
            try { process.Kill(entireProcessTree: true); }
            catch (InvalidOperationException) { }
        }
    }

    private static Process StartAppServer(string codex, string home)
    {
        var info = new ProcessStartInfo(codex)
        {
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
            StandardOutputEncoding = Encoding.UTF8,
        };
        info.ArgumentList.Add("app-server");
        info.Environment["CODEX_HOME"] = home;
        Directory.CreateDirectory(Path.Combine(Path.GetDirectoryName(home)!, "work"));
        Process process = Process.Start(info)!;
        _ = process.StandardError.ReadToEndAsync();
        return process;
    }

    private static async Task<string[]> ListAsync(Process process, int id, CancellationToken cancellationToken)
    {
        JsonElement result = await Rpc(process, id, "model/list", "{}", cancellationToken);
        return [.. result.GetProperty("result").GetProperty("data").EnumerateArray().Select(m => m.GetProperty("id").GetString()!)];
    }

    private static async Task<JsonElement> Rpc(Process process, int id, string method, string parameters, CancellationToken cancellationToken)
    {
        await process.StandardInput.WriteLineAsync($$"""{"id":{{id}},"method":"{{method}}","params":{{parameters}}}""");
        while (true)
        {
            string? line = await process.StandardOutput.ReadLineAsync(cancellationToken);
            Assert.NotNull(line);
            JsonDocument doc;
            try { doc = JsonDocument.Parse(line); }
            catch (JsonException) { continue; }
            if (doc.RootElement.TryGetProperty("id", out JsonElement value) && value.ValueKind == JsonValueKind.Number && value.GetInt32() == id)
            {
                return doc.RootElement.Clone();
            }
        }
    }

    /// <summary>A stand-in for the server that swallows requests, or answers a turn with a minimal event stream.</summary>
    private sealed class SinkUpstream : IAsyncDisposable
    {
        private readonly HttpListener _listener;
        private readonly CancellationTokenSource _stop = new();
        private readonly Task _serve;
        private readonly bool _replySse;

        public SinkUpstream(bool replySse = false)
        {
            _replySse = replySse;
            _listener = LoopbackHttpListener.Start(null, out int port);
            BaseAddress = $"http://127.0.0.1:{port}";
            _serve = ServeAsync(_stop.Token);
        }

        public string BaseAddress { get; }

        private async Task ServeAsync(CancellationToken cancellationToken)
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                HttpListenerContext context;
                try { context = await _listener.GetContextAsync().WaitAsync(cancellationToken); }
                catch (Exception) { break; }

                using var reader = new StreamReader(context.Request.InputStream, Encoding.UTF8);
                await reader.ReadToEndAsync(cancellationToken);
                if (_replySse)
                {
                    const string body =
                        "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
                        "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":1,\"input_tokens_details\":null,\"output_tokens\":1,\"output_tokens_details\":null,\"total_tokens\":2}}}\n\n";
                    byte[] payload = Encoding.UTF8.GetBytes(body);
                    context.Response.StatusCode = 200;
                    context.Response.ContentType = "text/event-stream";
                    context.Response.ContentLength64 = payload.Length;
                    await context.Response.OutputStream.WriteAsync(payload, cancellationToken);
                }
                else
                {
                    context.Response.StatusCode = 200;
                    context.Response.ContentLength64 = 0;
                }

                context.Response.Close();
            }
        }

        public async ValueTask DisposeAsync()
        {
            await _stop.CancelAsync();
            _listener.Close();
            try { await _serve; }
            catch (Exception) { }
        }
    }
}
