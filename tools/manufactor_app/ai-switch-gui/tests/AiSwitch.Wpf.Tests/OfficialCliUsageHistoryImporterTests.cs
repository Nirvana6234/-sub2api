using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using Microsoft.Data.Sqlite;

namespace AiSwitch.Wpf.Tests;

public sealed class OfficialCliUsageHistoryImporterTests
{
    [Fact]
    public async Task ImportAsync_ImportsExactCodexAndClaudeUsageWithoutTranscriptData()
    {
        using var scope = new ImportScope();
        string codexFile = scope.CreateCodexFile(
            "2026",
            "01",
            "01",
            "rollout-history.jsonl",
        [
            CodexMeta("codex-session-a"),
            CodexTurnContext("gpt-codex-a"),
            CodexTotal("2026-01-01T00:00:01Z", 100, 10, 5, 1),
            CodexTotal("2026-01-01T00:00:02Z", 100, 10, 5, 1),
            CodexTotal("2026-01-01T00:00:03Z", 160, 20, 7, 2),
            CodexMeta("codex-session-b"),
            CodexTurnContext("gpt-codex-b"),
            CodexTotal("2026-01-01T00:00:04Z", 30, 4, 0, 0),
            CodexMeta("codex-session-a"),
            CodexTurnContext("gpt-codex-a-later"),
            CodexTotal("2026-01-01T00:00:05Z", 120, 14, 6, 1),
            CodexTotal("2026-01-01T00:00:06Z", 180, 23, 9, 3),
            CodexLastOnly("2026-01-01T00:00:07Z", 999, 999, 999),
        ]);
        scope.CreateClaudeFile(
            "project-a",
            "claude-history.jsonl",
        [
            ClaudeAssistant("claude-session-a", "claude-event-1", "2026-01-01T00:01:00Z", 9, 3, 2, 1),
            // Repeating the same official uuid must not create a second event,
            // even if the JSONL line itself is repeated by the CLI.
            ClaudeAssistant("claude-session-a", "claude-event-1", "2026-01-01T00:01:00Z", 9, 3, 2, 1),
            ClaudeAssistant("claude-session-a", "claude-event-2", "2026-01-01T00:02:00Z", 11, 4, 3, 2),
            ClaudeMetaAssistant("claude-session-a", "claude-meta-event"),
        ]);
        scope.CreateGeminiFile("unread-gemini-history.jsonl", "this must never be imported");

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);

        OfficialCliUsageImportResult first = await importer.ImportAsync();

        Assert.False(first.GeminiExactUsageUnavailable);
        Assert.Equal(6, first.ImportedEvents);
        Assert.Equal(1, first.SkippedDuplicateEvents);
        Assert.Equal(1, first.SkippedUnverifiableUsageEvents);
        Assert.Equal(6, telemetry.UsageEvents.Count);

        LocalUsageTelemetryEvent[] codex = telemetry.UsageEvents
            .Where(item => item.CliKind == CliKind.Codex)
            .ToArray();
        Assert.Equal(4, codex.Length);
        Assert.Equal(210, codex.Sum(item => item.InputTokens));
        Assert.Equal(27, codex.Sum(item => item.OutputTokens));
        Assert.Equal(9, codex.Sum(item => item.CachedInputTokens));
        Assert.Equal(3, codex.Sum(item => item.CacheCreationTokens));
        Assert.Contains(codex, item => item.Model == "gpt-codex-a");
        Assert.Contains(codex, item => item.Model == "gpt-codex-a-later");
        Assert.DoesNotContain(codex, item => item.InputTokens == 999 || item.OutputTokens == 999);

        LocalUsageTelemetryEvent[] claude = telemetry.UsageEvents
            .Where(item => item.CliKind == CliKind.ClaudeCode)
            .ToArray();
        Assert.Equal(2, claude.Length);
        Assert.Equal(20, claude.Sum(item => item.InputTokens));
        Assert.Equal(7, claude.Sum(item => item.OutputTokens));
        Assert.Equal(5, claude.Sum(item => item.CachedInputTokens));
        Assert.Equal(3, claude.Sum(item => item.CacheCreationTokens));
        Assert.All(telemetry.UsageEvents, item =>
        {
            Assert.Null(item.SourceId);
            Assert.Null(item.SourceLabel);
        });

        // Unchanged files are checkpointed and have no second-pass effect.
        OfficialCliUsageImportResult unchanged = await importer.ImportAsync();
        Assert.Equal(0, unchanged.ImportedEvents);
        Assert.Equal(6, telemetry.UsageEvents.Count);

        // Appending a higher cumulative total imports only the positive delta.
        File.AppendAllText(codexFile, CodexTotal("2026-01-01T00:03:00Z", 220, 28, 11, 4) + Environment.NewLine);
        OfficialCliUsageImportResult appended = await importer.ImportAsync();
        Assert.Equal(1, appended.ImportedEvents);
        LocalUsageTelemetryEvent appendedEvent = telemetry.UsageEvents.Single(item =>
            item.CliKind == CliKind.Codex &&
            item.InputTokens == 40 &&
            item.OutputTokens == 5 &&
            item.CachedInputTokens == 2 &&
            item.CacheCreationTokens == 1);
        Assert.Equal("gpt-codex-a-later", appendedEvent.Model);

        // A different rollout file with the same logical session must compare
        // with the persisted opaque session maximum, not re-add the replayed
        // 220-token baseline.
        scope.CreateCodexFile(
            "2026",
            "01",
            "02",
            "rollout-replayed-session.jsonl",
        [
            CodexMeta("codex-session-a"),
            CodexTurnContext("gpt-codex-a-later"),
            CodexTotal("2026-01-02T00:00:00Z", 260, 31, 12, 5),
        ]);
        OfficialCliUsageImportResult replayedFile = await importer.ImportAsync();
        Assert.Equal(1, replayedFile.ImportedEvents);
        Assert.Contains(telemetry.UsageEvents, item =>
            item.CliKind == CliKind.Codex &&
            item.InputTokens == 40 &&
            item.OutputTokens == 3 &&
            item.CachedInputTokens == 1 &&
            item.CacheCreationTokens == 1);

        // The state DB has only opaque identifiers and numeric counters. It
        // must never retain arbitrary transcript text encountered in the JSONL.
        IReadOnlyList<string> opaqueStateValues = await ReadOpaqueStateValuesAsync(scope.Paths.OfficialUsageImportDatabasePath);
        Assert.NotEmpty(opaqueStateValues);
        Assert.All(opaqueStateValues, value => Assert.Matches("^[0-9a-f]{64}$", value));
    }

    [Fact]
    public async Task ImportAsync_ImportsGeminiMessageTokensWithStableIdAndCacheSemantics()
    {
        using var scope = new ImportScope();
        object firstMessage = GeminiAssistant(
            "gemini-message-1",
            "2026-07-15T05:00:00Z",
            "gemini-3.5-flash-extra-low",
            input: 100,
            output: 20,
            cached: 40,
            thoughts: 5,
            tool: 3,
            total: 128);
        object secondMessage = GeminiAssistant(
            "gemini-message-2",
            "2026-07-15T05:01:00Z",
            "gemini-3.5-flash-extra-low",
            input: 50,
            output: 10,
            cached: 0,
            thoughts: 0,
            tool: 0,
            total: 60);
        scope.CreateGeminiFile(
            "session-gemini-history.jsonl",
            string.Join(Environment.NewLine,
            [
                Serialize(new Dictionary<string, object?>
                {
                    ["$set"] = new
                    {
                        sessionId = "gemini-session-a",
                        kind = "main",
                        messages = new[] { firstMessage },
                    },
                }),
                Serialize(firstMessage),
                Serialize(secondMessage),
            ]));

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);

        OfficialCliUsageImportResult first = await importer.ImportAsync();
        OfficialCliUsageImportResult second = await importer.ImportAsync();

        Assert.False(first.GeminiExactUsageUnavailable);
        Assert.Equal(2, first.ImportedEvents);
        Assert.Equal(1, first.SkippedDuplicateEvents);
        Assert.Equal(0, second.ImportedEvents);
        LocalUsageTelemetryEvent[] gemini = telemetry.UsageEvents
            .Where(item => item.CliKind == CliKind.GeminiCli)
            .OrderBy(item => item.Timestamp)
            .ToArray();
        Assert.Equal(2, gemini.Length);
        Assert.Equal(100, gemini[0].InputTokens);
        Assert.Equal(28, gemini[0].OutputTokens);
        Assert.Equal(40, gemini[0].CachedInputTokens);
        Assert.Equal(50, gemini[1].InputTokens);
        Assert.Equal(10, gemini[1].OutputTokens);
        Assert.All(gemini, item => Assert.Equal("gemini-3.5-flash-extra-low", item.Model));
        Assert.All(gemini, item => Assert.Null(item.EstimatedCost));
    }

    [Fact]
    public async Task ImportAsync_ArchivedCodexSubagentUsesReplayOnlyAsCumulativeBaseline()
    {
        using var scope = new ImportScope();
        scope.CreateArchivedCodexFile(
            "rollout-child.jsonl",
        [
            CodexSubagentMeta("child-thread", "parent-thread"),
            CodexTurnContext("gpt-5.6"),
            CodexTotal("2026-07-10T03:00:01Z", 1_000, 100, 900, 0),
            CodexTotal("2026-07-10T03:00:02Z", 1_200, 120, 1_000, 0),
            CodexReplayBoundary(),
            CodexTotal("2026-07-10T03:00:04Z", 1_300, 150, 1_050, 0),
        ]);

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);

        OfficialCliUsageImportResult result = await importer.ImportAsync();

        LocalUsageTelemetryEvent usage = Assert.Single(
            telemetry.UsageEvents,
            item => item.CliKind == CliKind.Codex);
        Assert.Equal(1, result.ImportedEvents);
        Assert.Equal(100, usage.InputTokens);
        Assert.Equal(30, usage.OutputTokens);
        Assert.Equal(50, usage.CachedInputTokens);
        Assert.Equal("gpt-5.6", usage.Model);

        IReadOnlyList<string> stateValues = await ReadOpaqueStateValuesAsync(
            scope.Paths.OfficialUsageImportDatabasePath);
        Assert.DoesNotContain(stateValues, value => value.Contains("child-thread", StringComparison.Ordinal));
        Assert.DoesNotContain(stateValues, value => value.Contains("parent-thread", StringComparison.Ordinal));
        Assert.DoesNotContain(stateValues, value => value.Contains(scope.Paths.UserProfile, StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task ImportAsync_ImportsGeminiWholeJsonDocument()
    {
        using var scope = new ImportScope();
        object message = GeminiAssistant(
            "gemini-json-message",
            "2026-07-15T06:00:00Z",
            "gemini-3.5-pro",
            input: 120,
            output: 11,
            cached: 50,
            thoughts: 8,
            tool: 0,
            total: 139);
        scope.CreateGeminiFile(
            "session-whole-document.json",
            JsonSerializer.Serialize(
                new
                {
                    sessionId = "gemini-json-session",
                    startTime = "2026-07-15T05:59:00Z",
                    lastUpdated = "2026-07-15T06:00:00Z",
                    kind = "main",
                    messages = new[] { message },
                },
                new JsonSerializerOptions { WriteIndented = true }));

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);

        OfficialCliUsageImportResult first = await importer.ImportAsync();
        OfficialCliUsageImportResult second = await importer.ImportAsync();

        LocalUsageTelemetryEvent usage = Assert.Single(
            telemetry.UsageEvents,
            item => item.CliKind == CliKind.GeminiCli);
        Assert.Equal(1, first.ImportedEvents);
        Assert.Equal(0, second.ImportedEvents);
        Assert.Equal(120, usage.InputTokens);
        Assert.Equal(19, usage.OutputTokens);
        Assert.Equal(50, usage.CachedInputTokens);
        Assert.Equal("gemini-3.5-pro", usage.Model);
    }

    [Fact]
    public async Task RegisterManagedSessionAsync_ExcludesOnlyWorkspaceManagedCodexAndClaudeSessions()
    {
        using var scope = new ImportScope();
        scope.CreateCodexFile(
            "2026",
            "02",
            "01",
            "rollout-managed.jsonl",
        [
            CodexMeta("managed-codex-session"),
            CodexTurnContext("gpt-codex"),
            CodexTotal("2026-02-01T00:00:00Z", 100, 10, 0, 0),
            CodexMeta("external-codex-session"),
            CodexTurnContext("gpt-codex"),
            CodexTotal("2026-02-01T00:01:00Z", 30, 4, 0, 0),
        ]);
        scope.CreateClaudeFile(
            "project-b",
            "claude-managed.jsonl",
        [
            ClaudeAssistant("managed-claude-session", "managed-event", "2026-02-01T00:02:00Z", 20, 2, 0, 0),
            ClaudeAssistant("external-claude-session", "external-event", "2026-02-01T00:03:00Z", 8, 1, 0, 0),
        ]);

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);
        await importer.RegisterManagedSessionAsync(CliKind.Codex, "managed-codex-session");
        await importer.RegisterManagedSessionAsync(CliKind.ClaudeCode, "managed-claude-session");

        OfficialCliUsageImportResult result = await importer.ImportAsync();

        Assert.Equal(2, result.ImportedEvents);
        Assert.Equal(2, result.SkippedManagedSessionFiles);
        Assert.Collection(
            telemetry.UsageEvents.OrderBy(item => item.CliKind),
            item =>
            {
                Assert.Equal(CliKind.Codex, item.CliKind);
                Assert.Equal(30, item.InputTokens);
            },
            item =>
            {
                Assert.Equal(CliKind.ClaudeCode, item.CliKind);
                Assert.Equal(8, item.InputTokens);
            });
    }

    [Fact]
    public async Task ImportAsync_ClaudeAppendDoesNotReimportOldUsageAfterEphemeralFingerprintsArePruned()
    {
        using var scope = new ImportScope();
        string claudeFile = scope.CreateClaudeFile(
            "project-long-lived",
            "claude-history.jsonl",
        [
            ClaudeAssistant("claude-long-lived-session", "claude-event-old", "2026-01-01T00:01:00Z", 11, 2, 3, 1),
        ]);

        var telemetry = new RecordingTelemetryRepository();
        using var importer = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);

        OfficialCliUsageImportResult first = await importer.ImportAsync();

        Assert.Equal(1, first.ImportedEvents);
        Assert.Single(telemetry.UsageEvents);
        Assert.Equal(1, await CountDurableClaudeFingerprintRowsAsync(scope.Paths.OfficialUsageImportDatabasePath));

        // Reproduce the old long-running failure mode: the bounded generic
        // event-marker cache is cleared before the same JSONL grows. Claude's
        // persistent opaque uuid fingerprint must still suppress the old line.
        await ClearEphemeralEventFingerprintsAsync(scope.Paths.OfficialUsageImportDatabasePath);
        Assert.Equal(1, await CountDurableClaudeFingerprintRowsAsync(scope.Paths.OfficialUsageImportDatabasePath));
        File.AppendAllText(
            claudeFile,
            ClaudeAssistant("claude-long-lived-session", "claude-event-new", "2026-06-01T00:01:00Z", 17, 4, 5, 2) +
            Environment.NewLine);

        OfficialCliUsageImportResult second = await importer.ImportAsync();

        Assert.Equal(1, second.ImportedEvents);
        Assert.Equal(1, second.SkippedDuplicateEvents);
        Assert.Equal(2, await CountDurableClaudeFingerprintRowsAsync(scope.Paths.OfficialUsageImportDatabasePath));
        LocalUsageTelemetryEvent[] claude = telemetry.UsageEvents
            .Where(item => item.CliKind == CliKind.ClaudeCode)
            .ToArray();
        Assert.Equal(2, claude.Length);
        Assert.Equal(1, claude.Count(item => item.InputTokens == 11 && item.OutputTokens == 2));
        Assert.Equal(1, claude.Count(item => item.InputTokens == 17 && item.OutputTokens == 4));
    }

    [Fact]
    public async Task ImportAsync_UpgradesLegacyClaudeFingerprintBeforeItIsPrunedAndTheFileGrows()
    {
        using var scope = new ImportScope();
        const string sessionId = "claude-legacy-session";
        const string oldUuid = "claude-legacy-event";
        string claudeFile = scope.CreateClaudeFile(
            "project-legacy",
            "claude-history.jsonl",
        [
            ClaudeAssistant(sessionId, oldUuid, "2026-01-01T00:01:00Z", 13, 3, 2, 1),
        ]);

        // This is the schema left by the previous importer: it knew only the
        // bounded generic marker table. The old event was already recorded by
        // that build, so an upgrade must seed the durable Claude marker before
        // it ever re-scans this long-lived JSONL.
        await SeedLegacyGenericClaudeFingerprintAsync(
            scope.Paths.OfficialUsageImportDatabasePath,
            CreateLegacyClaudeUsageFingerprint(sessionId, oldUuid));

        var telemetry = new RecordingTelemetryRepository();
        using (var upgradedImporter = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry))
        {
            OfficialCliUsageImportResult upgradeScan = await upgradedImporter.ImportAsync();
            Assert.Equal(0, upgradeScan.ImportedEvents);
            Assert.Equal(1, upgradeScan.SkippedDuplicateEvents);
        }

        Assert.Equal(1, await CountDurableClaudeFingerprintRowsAsync(scope.Paths.OfficialUsageImportDatabasePath));

        // The normal generic retention sweep can now remove the legacy marker
        // without making the old assistant event eligible for re-import.
        await ClearEphemeralEventFingerprintsAsync(scope.Paths.OfficialUsageImportDatabasePath);
        File.AppendAllText(
            claudeFile,
            ClaudeAssistant(sessionId, "claude-legacy-event-new", "2026-06-01T00:01:00Z", 19, 5, 4, 2) +
            Environment.NewLine);

        using var restartedImporter = new OfficialCliUsageHistoryImporter(scope.Paths, telemetry);
        OfficialCliUsageImportResult appended = await restartedImporter.ImportAsync();

        Assert.Equal(1, appended.ImportedEvents);
        Assert.Equal(1, appended.SkippedDuplicateEvents);
        LocalUsageTelemetryEvent[] claude = telemetry.UsageEvents
            .Where(item => item.CliKind == CliKind.ClaudeCode)
            .ToArray();
        Assert.Single(claude);
        Assert.Equal(19, claude[0].InputTokens);
        Assert.Equal(5, claude[0].OutputTokens);
    }

    private static string CodexMeta(string sessionId) => Serialize(new
    {
        timestamp = "2026-01-01T00:00:00Z",
        type = "session_meta",
        payload = new { id = sessionId },
    });

    private static string CodexSubagentMeta(string threadId, string parentSessionId) => Serialize(new
    {
        timestamp = "2026-07-10T03:00:00Z",
        type = "session_meta",
        payload = new
        {
            id = threadId,
            session_id = parentSessionId,
            source = new
            {
                subagent = new
                {
                    thread_spawn = new
                    {
                        parent_thread_id = parentSessionId,
                        depth = 1,
                        agent_role = "explorer",
                    },
                },
            },
        },
    });

    private static string CodexReplayBoundary() => Serialize(new
    {
        timestamp = "2026-07-10T03:00:03Z",
        type = "event_msg",
        payload = new { type = "thread_settings_applied" },
    });

    private static string CodexTurnContext(string model) => Serialize(new
    {
        timestamp = "2026-01-01T00:00:00Z",
        type = "turn_context",
        payload = new { model },
    });

    private static string CodexTotal(
        string timestamp,
        long input,
        long output,
        long cached,
        long cacheCreation) => Serialize(new
    {
        timestamp,
        type = "event_msg",
        payload = new
        {
            type = "token_count",
            // This field deliberately resembles a transcript field. The
            // importer must never retain it in the state database.
            private_transcript = "private-transcript-value",
            info = new
            {
                total_token_usage = new
                {
                    input_tokens = input,
                    output_tokens = output,
                    cached_input_tokens = cached,
                    cache_creation_input_tokens = cacheCreation,
                },
                last_token_usage = new
                {
                    input_tokens = input,
                    output_tokens = output,
                },
            },
        },
    });

    private static string CodexLastOnly(string timestamp, long input, long output, long cached) => Serialize(new
    {
        timestamp,
        type = "event_msg",
        payload = new
        {
            type = "token_count",
            info = new
            {
                last_token_usage = new
                {
                    input_tokens = input,
                    output_tokens = output,
                    cached_input_tokens = cached,
                },
            },
        },
    });

    private static string ClaudeAssistant(
        string sessionId,
        string uuid,
        string timestamp,
        long input,
        long output,
        long cached,
        long cacheCreation) => Serialize(new
    {
        type = "assistant",
        sessionId,
        uuid,
        timestamp,
        message = new
        {
            model = "claude-test",
            content = "private-transcript-value",
            usage = new
            {
                input_tokens = input,
                output_tokens = output,
                cache_read_input_tokens = cached,
                cache_creation_input_tokens = cacheCreation,
            },
        },
        // Claude may include detailed iteration data. Only root message.usage
        // is an exact per-assistant-event usage source.
        iterations = new[]
        {
            new
            {
                usage = new
                {
                    input_tokens = 999L,
                    output_tokens = 999L,
                },
            },
        },
    });

    private static string ClaudeMetaAssistant(string sessionId, string uuid) => Serialize(new
    {
        type = "assistant",
        sessionId,
        uuid,
        timestamp = "2026-01-01T00:03:00Z",
        isMeta = true,
        message = new
        {
            model = "claude-test",
            usage = new { input_tokens = 999L, output_tokens = 999L },
        },
    });

    private static object GeminiAssistant(
        string id,
        string timestamp,
        string model,
        long input,
        long output,
        long cached,
        long thoughts,
        long tool,
        long total) => new
        {
            id,
            type = "gemini",
            timestamp,
            model,
            content = "private-transcript-value",
            tokens = new
            {
                input,
                output,
                cached,
                thoughts,
                tool,
                total,
            },
        };

    private static string Serialize<T>(T value) => JsonSerializer.Serialize(value);

    private static async Task<IReadOnlyList<string>> ReadOpaqueStateValuesAsync(string databasePath)
    {
        var values = new List<string>();
        await using var connection = new SqliteConnection($"Data Source={databasePath};Cache=Shared");
        await connection.OpenAsync();
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            SELECT event_fingerprint FROM imported_official_usage_events
            UNION ALL
            SELECT event_fingerprint FROM imported_claude_usage_events_v1
            UNION ALL
            SELECT event_fingerprint FROM imported_gemini_usage_events_v1
            UNION ALL
            SELECT session_fingerprint FROM managed_cli_sessions
            UNION ALL
            SELECT source_fingerprint FROM official_usage_source_checkpoints_v2
            UNION ALL
            SELECT session_fingerprint FROM official_codex_session_totals_v2;
            """;
        await using SqliteDataReader reader = await command.ExecuteReaderAsync();
        while (await reader.ReadAsync())
        {
            values.Add(reader.GetString(0));
        }

        return values;
    }

    private static async Task ClearEphemeralEventFingerprintsAsync(string databasePath)
    {
        await using var connection = new SqliteConnection($"Data Source={databasePath};Cache=Shared");
        await connection.OpenAsync();
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = "DELETE FROM imported_official_usage_events;";
        await command.ExecuteNonQueryAsync();
    }

    private static async Task<long> CountDurableClaudeFingerprintRowsAsync(string databasePath)
    {
        await using var connection = new SqliteConnection($"Data Source={databasePath};Cache=Shared");
        await connection.OpenAsync();
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = "SELECT COUNT(*) FROM imported_claude_usage_events_v1;";
        object? value = await command.ExecuteScalarAsync();
        return Convert.ToInt64(value, System.Globalization.CultureInfo.InvariantCulture);
    }

    private static async Task SeedLegacyGenericClaudeFingerprintAsync(string databasePath, string eventFingerprint)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(databasePath)!);
        await using var connection = new SqliteConnection($"Data Source={databasePath};Cache=Shared");
        await connection.OpenAsync();
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            CREATE TABLE IF NOT EXISTS imported_official_usage_events (
                event_fingerprint TEXT PRIMARY KEY,
                imported_at_utc INTEGER NOT NULL
            );

            INSERT INTO imported_official_usage_events (event_fingerprint, imported_at_utc)
            VALUES ($event_fingerprint, $imported_at_utc);
            """;
        command.Parameters.AddWithValue("$event_fingerprint", eventFingerprint);
        command.Parameters.AddWithValue("$imported_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
        await command.ExecuteNonQueryAsync();
    }

    private static string CreateLegacyClaudeUsageFingerprint(string sessionId, string uuid)
    {
        string sessionFingerprint = CreateSha256Fingerprint(
            $"official-cli-session-v1|{(int)CliKind.ClaudeCode}|{sessionId.Trim()}");
        return CreateSha256Fingerprint($"official-claude-usage-v2|{sessionFingerprint}|{uuid.Trim()}");
    }

    private static string CreateSha256Fingerprint(string value) =>
        Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(value))).ToLowerInvariant();

    private sealed class RecordingTelemetryRepository : ILocalTelemetryRepository
    {
        private readonly object _gate = new();
        private readonly List<LocalUsageTelemetryEvent> _usageEvents = [];

        public IReadOnlyList<LocalUsageTelemetryEvent> UsageEvents
        {
            get
            {
                lock (_gate)
                {
                    return _usageEvents.ToArray();
                }
            }
        }

        public Task RecordUsageAsync(
            LocalUsageTelemetryEvent telemetryEvent,
            CancellationToken cancellationToken = default)
        {
            lock (_gate)
            {
                _usageEvents.Add(telemetryEvent);
            }

            return Task.CompletedTask;
        }

        public Task RecordNetworkProbeAsync(
            LocalNetworkHealthProbe probe,
            CancellationToken cancellationToken = default) => Task.CompletedTask;

        public Task<LocalTelemetrySnapshot> GetSnapshotAsync(
            TimeZoneInfo? timeZone = null,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new LocalTelemetrySnapshot(
                DateTimeOffset.UtcNow,
                LocalTelemetryUsageSummary.Empty,
                LocalTelemetryUsageSummary.Empty,
                Array.Empty<LocalTelemetryDailyUsage>(),
                LatestNetworkStatus: null));
    }

    private sealed class ImportScope : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(),
            "LanAi.OfficialUsageHistoryImporter.Tests",
            Guid.NewGuid().ToString("N"));

        public ImportScope()
        {
            string userProfile = Path.Combine(_root, "profile");
            string localAppData = Path.Combine(_root, "appdata");
            Directory.CreateDirectory(userProfile);
            Directory.CreateDirectory(localAppData);
            Paths = new AppDataPaths(userProfile, localAppData);
        }

        public AppDataPaths Paths { get; }

        public string CreateCodexFile(string year, string month, string day, string name, IReadOnlyList<string> lines)
        {
            string directory = Path.Combine(Paths.CodexSessionsDirectory, year, month, day);
            Directory.CreateDirectory(directory);
            string path = Path.Combine(directory, name);
            File.WriteAllLines(path, lines);
            return path;
        }

        public string CreateArchivedCodexFile(string name, IReadOnlyList<string> lines)
        {
            Directory.CreateDirectory(Paths.CodexArchivedSessionsDirectory);
            string path = Path.Combine(Paths.CodexArchivedSessionsDirectory, name);
            File.WriteAllLines(path, lines);
            return path;
        }

        public string CreateClaudeFile(string project, string name, IReadOnlyList<string> lines)
        {
            string directory = Path.Combine(Paths.ClaudeProjectsDirectory, project);
            Directory.CreateDirectory(directory);
            string path = Path.Combine(directory, name);
            File.WriteAllLines(path, lines);
            return path;
        }

        public void CreateGeminiFile(string name, string content)
        {
            Directory.CreateDirectory(Paths.GeminiProjectsDirectory);
            File.WriteAllText(Path.Combine(Paths.GeminiProjectsDirectory, name), content);
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
                // SQLite connection pooling can release a temporary test file
                // just after the test ends. Leaving an isolated temp directory
                // is safer than making a passing test flaky.
            }
            catch (UnauthorizedAccessException)
            {
                // Same cleanup rationale as above.
            }
        }
    }
}
