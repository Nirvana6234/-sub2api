using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;

namespace AiSwitch.Wpf.Tests;

public sealed class CompositeConversationIndexerTests
{
    [Fact]
    public async Task ScanAsync_IncludesActiveAndArchivedCodexButExcludesSubagents()
    {
        using var scope = new IndexScope();
        string project = scope.CreateProject("codex-project");
        scope.CreateCodexSession(
            scope.Paths.CodexSessionsDirectory,
            "active.jsonl",
            CodexMeta("active-session", project));
        scope.CreateCodexSession(
            scope.Paths.CodexArchivedSessionsDirectory,
            "archived.jsonl",
            CodexMeta("archived-session", project));
        scope.CreateCodexSession(
            scope.Paths.CodexArchivedSessionsDirectory,
            "subagent.jsonl",
            CodexMeta("subagent-session", project, isSubagent: true));

        var indexer = new CompositeConversationIndexer(scope.Paths);
        IReadOnlyList<ConversationRecord> conversations = await indexer.ScanAsync(client: CliKind.Codex);

        Assert.Equal(2, conversations.Count);
        Assert.Contains(conversations, item => item.NativeSessionId == "active-session");
        Assert.Contains(conversations, item => item.NativeSessionId == "archived-session");
        Assert.DoesNotContain(conversations, item => item.NativeSessionId == "subagent-session");
    }

    [Fact]
    public async Task ScanAsync_IndexesGeminiJsonAndJsonlSessions()
    {
        using var scope = new IndexScope();
        string project = scope.CreateProject("gemini-project");
        string chats = scope.CreateGeminiChats("project-hash", project);

        File.WriteAllText(
            Path.Combine(chats, "session-json.json"),
            JsonSerializer.Serialize(
                new
                {
                    sessionId = "gemini-json-session",
                    startTime = "2026-07-15T06:00:00Z",
                    lastUpdated = "2026-07-15T06:01:00Z",
                    kind = "main",
                    messages = new[]
                    {
                        new { id = "user-json", type = "user", content = "JSON session title" },
                    },
                },
                new JsonSerializerOptions { WriteIndented = true }));

        File.WriteAllLines(
            Path.Combine(chats, "session-jsonl.jsonl"),
        [
            // The update shape mirrors the append-only Gemini session store.
            // A dictionary supplies the literal "$set" key.
            JsonSerializer.Serialize(new Dictionary<string, object?>
            {
                ["$set"] = new
                {
                    sessionId = "gemini-jsonl-session",
                    startTime = "2026-07-15T07:00:00Z",
                    lastUpdated = "2026-07-15T07:01:00Z",
                    kind = "main",
                    messages = new[]
                    {
                        new { id = "user-jsonl", type = "user", content = "JSONL session title" },
                    },
                },
            }),
        ]);

        var indexer = new CompositeConversationIndexer(scope.Paths);
        IReadOnlyList<ConversationRecord> conversations = await indexer.ScanAsync(client: CliKind.GeminiCli);

        Assert.Equal(2, conversations.Count);
        ConversationRecord json = Assert.Single(
            conversations,
            item => item.NativeSessionId == "gemini-json-session");
        ConversationRecord jsonl = Assert.Single(
            conversations,
            item => item.NativeSessionId == "gemini-jsonl-session");
        Assert.Equal("JSON session title", json.Title);
        Assert.Equal("JSONL session title", jsonl.Title);
        Assert.All(conversations, item => Assert.Equal(PathIdentity.CreateStableId(project), item.ProjectId));
    }

    private static string CodexMeta(string id, string cwd, bool isSubagent = false)
    {
        object source = isSubagent
            ? new { subagent = new { thread_spawn = new { parent_thread_id = "parent" } } }
            : new { };
        return JsonSerializer.Serialize(new
        {
            timestamp = "2026-07-15T05:00:00Z",
            type = "session_meta",
            payload = new { id, cwd, source },
        });
    }

    private sealed class IndexScope : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(),
            "LanAi.CompositeConversationIndexer.Tests",
            Guid.NewGuid().ToString("N"));

        public IndexScope()
        {
            string profile = Path.Combine(_root, "profile");
            string appData = Path.Combine(_root, "appdata");
            Directory.CreateDirectory(profile);
            Directory.CreateDirectory(appData);
            Paths = new AppDataPaths(profile, appData);
        }

        public AppDataPaths Paths { get; }

        public string CreateProject(string name)
        {
            string path = Path.Combine(_root, name);
            Directory.CreateDirectory(path);
            return Path.GetFullPath(path);
        }

        public void CreateCodexSession(string root, string name, string metadata)
        {
            Directory.CreateDirectory(root);
            File.WriteAllText(Path.Combine(root, name), metadata + Environment.NewLine);
        }

        public string CreateGeminiChats(string hash, string projectRoot)
        {
            string project = Path.Combine(Paths.GeminiProjectsDirectory, hash);
            string chats = Path.Combine(project, "chats");
            Directory.CreateDirectory(chats);
            File.WriteAllText(Path.Combine(project, ".project_root"), projectRoot);
            return chats;
        }

        public void Dispose()
        {
            try
            {
                if (Directory.Exists(_root))
                {
                    Directory.Delete(_root, recursive: true);
                }
            }
            catch (IOException)
            {
                // Isolated temp cleanup is best effort on Windows.
            }
            catch (UnauthorizedAccessException)
            {
                // Isolated temp cleanup is best effort on Windows.
            }
        }
    }
}
