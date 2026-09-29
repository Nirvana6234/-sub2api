using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The model picker end to end: the real relay, the real config writer and the real Codex.
/// </summary>
/// <remarks>
/// Everything else about this feature is proved against our own code. What only the real
/// binary can say is whether the file we write and the answer we give are the ones Codex
/// reads — the address and switch being honoured, the catalog being accepted, a 404 sending it
/// back to its own list. Skipped where no Codex is installed. Runs it in a private
/// <c>CODEX_HOME</c>: the machine's own <c>~/.codex</c> is neither read nor written.
/// </remarks>
public sealed class CodexPickerIntegrationTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "picker-it-" + Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        try { Directory.Delete(_root, recursive: true); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    [Fact]
    public async Task CodexsPickerFollowsTheGroupTheRelayIsOn_AfterARestart()
    {
        string? codex = CodexBundledCatalogSource.LocateInstalledCodex();
        if (codex is null)
        {
            return;
        }

        await using var relay = new LocalPawRelay(
            "http://127.0.0.1:1",
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
        writer.Apply(
            relay.Token,
            relay.Origin + "/v1",
            preferredModel: "claude-sonnet-5",
            catalogUrl: relay.Origin + LocalPawRelay.CatalogPath);

        // A group with a whitelist: the picker is exactly that whitelist.
        relay.SetGroup(1, "Claude", CodexGroupModels.From(["claude-sonnet-5", "claude-opus-5"], "claude-opus-5"));
        string[] claude = await PickerAsync(codex, home);
        Assert.Equal(["claude-opus-5", "claude-sonnet-5"], claude);

        // The same relay moved to a group with none: a restarted Codex is back on its own list.
        relay.SetGroup(2, "OpenAI", null);
        string[] own = await PickerAsync(codex, home);
        Assert.DoesNotContain(own, m => m.StartsWith("claude", StringComparison.Ordinal));
        Assert.True(own.Length > 0);
    }

    private async Task<string[]> PickerAsync(string codex, string home)
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

        using Process process = Process.Start(info)!;
        _ = process.StandardError.ReadToEndAsync();
        try
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(60));
            await process.StandardInput.WriteLineAsync("""{"id":1,"method":"initialize","params":{"clientInfo":{"name":"picker-it","version":"0"}}}""");
            await ReadResponseAsync(process, 1, timeout.Token);
            await process.StandardInput.WriteLineAsync("""{"method":"initialized"}""");
            await process.StandardInput.WriteLineAsync("""{"id":2,"method":"model/list","params":{}}""");
            JsonElement result = await ReadResponseAsync(process, 2, timeout.Token);
            return [.. result.GetProperty("result").GetProperty("data").EnumerateArray().Select(m => m.GetProperty("id").GetString()!)];
        }
        finally
        {
            try { process.Kill(entireProcessTree: true); }
            catch (InvalidOperationException) { }
        }
    }

    private static async Task<JsonElement> ReadResponseAsync(Process process, int id, CancellationToken cancellationToken)
    {
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
}
