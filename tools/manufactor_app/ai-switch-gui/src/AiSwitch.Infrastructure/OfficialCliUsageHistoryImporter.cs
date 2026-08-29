using System.Collections.Concurrent;
using System.Globalization;
using System.Security;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.Workspace.Core;
using Microsoft.Data.Sqlite;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Marks a native official-CLI session as managed by this workspace.  The
/// implementation may use the raw session id only in memory to derive an
/// opaque deterministic fingerprint; it must never persist the raw id.
/// </summary>
public interface IManagedCliSessionRegistry
{
    Task RegisterManagedSessionAsync(
        CliKind cliKind,
        string nativeSessionId,
        CancellationToken cancellationToken = default);
}

/// <summary>
/// Imports exact usage counters from official Codex/Claude JSONL history and
/// Gemini JSON or JSONL history. This is an importer, not a transcript reader: it
/// never returns or stores message text, project paths, URLs, credentials, raw
/// JSON lines, or raw native session identifiers.
/// </summary>
public interface IOfficialCliUsageHistoryImporter : IManagedCliSessionRegistry, IDisposable
{
    Task<OfficialCliUsageImportResult> ImportAsync(CancellationToken cancellationToken = default);
}

/// <summary>
/// Aggregate-safe import outcome.  It contains only counts and no native file
/// names, session ids, paths, models, message data, or credentials.
/// </summary>
public sealed record OfficialCliUsageImportResult(
    int ScannedFiles,
    int ImportedEvents,
    int SkippedDuplicateEvents,
    int SkippedManagedSessionFiles,
    int SkippedMalformedLines,
    bool GeminiExactUsageUnavailable,
    int SkippedUnverifiableUsageEvents = 0,
    bool WasSkippedBecauseBusy = false)
{
    public static OfficialCliUsageImportResult Busy { get; } = new(
        ScannedFiles: 0,
        ImportedEvents: 0,
        SkippedDuplicateEvents: 0,
        SkippedManagedSessionFiles: 0,
        SkippedMalformedLines: 0,
        GeminiExactUsageUnavailable: false,
        SkippedUnverifiableUsageEvents: 0,
        WasSkippedBecauseBusy: true);
}

/// <summary>
/// Privacy-bounded official usage importer.  Its checkpoint database contains
/// only SHA-256 fingerprints and file length/write-time metadata.  It is kept
/// separate from the telemetry data database so callers can use any existing
/// <see cref="ILocalTelemetryRepository"/> implementation.
/// </summary>
public sealed class OfficialCliUsageHistoryImporter : IOfficialCliUsageHistoryImporter
{
    private const long MaximumHistoryFileBytes = 128L * 1024 * 1024;
    private const int MaximumJsonLineCharacters = 2 * 1024 * 1024;
    private const int MaximumImportedFingerprintRows = 120_000;
    private static readonly TimeSpan FingerprintRetention = TimeSpan.FromDays(120);

    private readonly AppDataPaths _paths;
    private readonly ILocalTelemetryRepository _telemetryRepository;
    private readonly string _databaseDirectory;
    private readonly string _connectionString;
    private readonly SemaphoreSlim _operationGate = new(1, 1);
    private readonly ConcurrentDictionary<string, byte> _managedSessionFingerprints = new(StringComparer.Ordinal);
    private bool _initialized;
    private bool _disposed;

    public OfficialCliUsageHistoryImporter(
        AppDataPaths paths,
        ILocalTelemetryRepository telemetryRepository)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _telemetryRepository = telemetryRepository ?? throw new ArgumentNullException(nameof(telemetryRepository));

        string databasePath = Path.GetFullPath(paths.OfficialUsageImportDatabasePath);
        _databaseDirectory = Path.GetDirectoryName(databasePath)
            ?? throw new ArgumentException("Official usage import database must include a directory.", nameof(paths));
        _connectionString = new SqliteConnectionStringBuilder
        {
            DataSource = databasePath,
            Mode = SqliteOpenMode.ReadWriteCreate,
            Cache = SqliteCacheMode.Shared,
            Pooling = true,
        }.ToString();
    }

    /// <summary>
    /// Stores only a hash of the native id.  Registering a session makes the
    /// scanner exclude that entire session, which is conservative by design:
    /// the workspace already records its own live turns and must not import the
    /// same official history later without a provable per-event identity.
    /// </summary>
    public async Task RegisterManagedSessionAsync(
        CliKind cliKind,
        string nativeSessionId,
        CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        if (cliKind is not (CliKind.Codex or CliKind.ClaudeCode or CliKind.GeminiCli) ||
            string.IsNullOrWhiteSpace(nativeSessionId))
        {
            return;
        }

        string fingerprint = CreateSessionFingerprint(cliKind, nativeSessionId);
        _managedSessionFingerprints.TryAdd(fingerprint, 0);

        await _operationGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);
            await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            await using SqliteCommand command = connection.CreateCommand();
            command.CommandText = """
                INSERT OR IGNORE INTO managed_cli_sessions (
                    session_fingerprint, cli_kind, registered_at_utc)
                VALUES ($session_fingerprint, $cli_kind, $registered_at_utc);
                """;
            command.Parameters.AddWithValue("$session_fingerprint", fingerprint);
            command.Parameters.AddWithValue("$cli_kind", (int)cliKind);
            command.Parameters.AddWithValue("$registered_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            _operationGate.Release();
        }
    }

    public async Task<OfficialCliUsageImportResult> ImportAsync(CancellationToken cancellationToken = default)
    {
        ThrowIfDisposed();
        if (!await _operationGate.WaitAsync(0, cancellationToken).ConfigureAwait(false))
        {
            return OfficialCliUsageImportResult.Busy;
        }

        try
        {
            await EnsureInitializedAsync(cancellationToken).ConfigureAwait(false);
            await using SqliteConnection stateConnection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
            await PruneStateAsync(stateConnection, cancellationToken).ConfigureAwait(false);
            ImportState state = await LoadImportStateAsync(stateConnection, cancellationToken).ConfigureAwait(false);

            var accumulator = new ImportAccumulator();
            foreach (string filePath in EnumerateCodexFiles())
            {
                cancellationToken.ThrowIfCancellationRequested();
                await ScanCodexFileAsync(filePath, stateConnection, state, accumulator, cancellationToken)
                    .ConfigureAwait(false);
            }

            foreach (string filePath in EnumerateClaudeFiles())
            {
                cancellationToken.ThrowIfCancellationRequested();
                await ScanClaudeFileAsync(filePath, stateConnection, state, accumulator, cancellationToken)
                    .ConfigureAwait(false);
            }

            foreach (string filePath in EnumerateGeminiFiles())
            {
                cancellationToken.ThrowIfCancellationRequested();
                await ScanGeminiFileAsync(filePath, stateConnection, state, accumulator, cancellationToken)
                    .ConfigureAwait(false);
            }

            return new OfficialCliUsageImportResult(
                accumulator.ScannedFiles,
                accumulator.ImportedEvents,
                accumulator.SkippedDuplicateEvents,
                accumulator.SkippedManagedSessionFiles,
                accumulator.SkippedMalformedLines,
                GeminiExactUsageUnavailable: false,
                SkippedUnverifiableUsageEvents: accumulator.SkippedUnverifiableUsageEvents);
        }
        finally
        {
            _operationGate.Release();
        }
    }

    private async Task ScanCodexFileAsync(
        string filePath,
        SqliteConnection stateConnection,
        ImportState state,
        ImportAccumulator accumulator,
        CancellationToken cancellationToken)
    {
        if (!TryGetUsableFile(filePath, out FileInfo? file))
        {
            return;
        }

        accumulator.ScannedFiles++;
        var sourceKey = new SourceKey(
            CliKind.Codex,
            CreateSourceFingerprint(CliKind.Codex, file!.FullName));
        if (state.SourceCheckpoints.TryGetValue(sourceKey, out SourceCheckpoint checkpoint) &&
            checkpoint.Matches(file))
        {
            return;
        }

        CodexReplayInfo replayInfo = ReadCodexReplayInfo(file!.FullName);
        CodexTimelineSegment? currentSegment = null;
        var countedManagedSegments = new HashSet<string>(StringComparer.Ordinal);
        long lineNumber = 0;
        try
        {
            await using FileStream stream = OpenSharedRead(file!.FullName);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false) is { } line)
            {
                cancellationToken.ThrowIfCancellationRequested();
                lineNumber++;
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                if (line.Length > MaximumJsonLineCharacters)
                {
                    accumulator.SkippedMalformedLines++;
                    continue;
                }

                JsonDocument? document = null;
                try
                {
                    document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    if (TryGetCodexSessionIdentity(root, out CodexSessionIdentity identity))
                    {
                        string sessionFingerprint = CreateSessionFingerprint(CliKind.Codex, identity.ThreadId);
                        bool isManaged = IsManagedSession(sessionFingerprint);
                        long? historyReplayBoundary = identity.CarriesHistorySnapshot &&
                                                      string.Equals(
                                                          identity.ThreadId,
                                                          replayInfo.Identity?.ThreadId,
                                                          StringComparison.Ordinal)
                            ? replayInfo.HistoryReplayBoundary
                            : null;
                        currentSegment = new CodexTimelineSegment(
                            sessionFingerprint,
                            isManaged,
                            historyReplayBoundary);
                        if (isManaged && countedManagedSegments.Add(sessionFingerprint))
                        {
                            accumulator.SkippedManagedSessionFiles++;
                        }

                        continue;
                    }

                    if (currentSegment is null)
                    {
                        continue;
                    }

                    if (TryReadCodexTurnContextModel(root, out string? model))
                    {
                        currentSegment.Model = model;
                        continue;
                    }

                    if (!IsCodexTokenCount(root))
                    {
                        continue;
                    }

                    if (currentSegment.IsManaged)
                    {
                        continue;
                    }

                    if (!TryReadTimestamp(root, out DateTimeOffset timestamp))
                    {
                        accumulator.SkippedMalformedLines++;
                        continue;
                    }

                    if (!TryReadCodexTotalUsage(root, out TokenCounters totalCounters))
                    {
                        // Codex does not provide a stable request/turn id for
                        // token_count. A last_token_usage-only line cannot be
                        // distinguished from its repeated snapshots, so skip it
                        // instead of overstating the user's usage.
                        accumulator.SkippedUnverifiableUsageEvents++;
                        continue;
                    }

                    TokenCounters previousMaximum = currentSegment.TimelineMaximum ??
                        (state.CodexSessionMaximums.TryGetValue(
                            currentSegment.SessionFingerprint,
                            out TokenCounters storedMaximum)
                            ? storedMaximum
                            : TokenCounters.Empty);
                    TokenCounters counters = totalCounters.PositiveDifferenceFrom(previousMaximum);
                    TokenCounters nextMaximum = previousMaximum.ComponentwiseMaximum(totalCounters);
                    currentSegment.TimelineMaximum = nextMaximum;

                    if (currentSegment.HistoryReplayBoundary is { } boundary && lineNumber < boundary)
                    {
                        // Fork/subagent files begin with a replay of their parent history.
                        // It establishes the local cumulative baseline but is not new usage.
                        continue;
                    }

                    if (!counters.HasPositiveValue)
                    {
                        continue;
                    }

                    await ImportObservationAsync(
                            new UsageObservation(
                                CliKind.Codex,
                                timestamp,
                                currentSegment.Model,
                                counters,
                                CreateCodexUsageFingerprint(sourceKey.SourceFingerprint, lineNumber)),
                            stateConnection,
                            state,
                            accumulator,
                            cancellationToken)
                        .ConfigureAwait(false);
                    await UpsertCodexSessionMaximumAsync(
                            stateConnection,
                            currentSegment.SessionFingerprint,
                            nextMaximum,
                            cancellationToken)
                        .ConfigureAwait(false);
                    state.CodexSessionMaximums[currentSegment.SessionFingerprint] = nextMaximum;
                }
                catch (JsonException)
                {
                    accumulator.SkippedMalformedLines++;
                }
                finally
                {
                    document?.Dispose();
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            // Official CLIs can append to or rotate JSONL files while the
            // workspace is open. Treat a transient read issue as no data, not
            // as a failure of local telemetry or the current chat turn.
            return;
        }

        await UpsertSourceCheckpointAsync(stateConnection, sourceKey, file!, cancellationToken)
            .ConfigureAwait(false);
        state.SourceCheckpoints[sourceKey] = SourceCheckpoint.From(file!);
    }

    private async Task ScanClaudeFileAsync(
        string filePath,
        SqliteConnection stateConnection,
        ImportState state,
        ImportAccumulator accumulator,
        CancellationToken cancellationToken)
    {
        if (!TryGetUsableFile(filePath, out FileInfo? file))
        {
            return;
        }

        accumulator.ScannedFiles++;
        var sourceKey = new SourceKey(
            CliKind.ClaudeCode,
            CreateSourceFingerprint(CliKind.ClaudeCode, file!.FullName));
        if (state.SourceCheckpoints.TryGetValue(sourceKey, out SourceCheckpoint checkpoint) &&
            checkpoint.Matches(file))
        {
            return;
        }

        var countedManagedSessions = new HashSet<string>(StringComparer.Ordinal);
        try
        {
            await using FileStream stream = OpenSharedRead(file!.FullName);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false) is { } line)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                if (line.Length > MaximumJsonLineCharacters)
                {
                    accumulator.SkippedMalformedLines++;
                    continue;
                }

                JsonDocument? document = null;
                try
                {
                    document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    if (!TryReadClaudeUsage(root, out TokenCounters counters, out string? model))
                    {
                        continue;
                    }

                    string? sessionId = GetString(root, "sessionId");
                    string? uuid = GetString(root, "uuid");
                    if (string.IsNullOrWhiteSpace(sessionId) || string.IsNullOrWhiteSpace(uuid) ||
                        !TryReadTimestamp(root, out DateTimeOffset timestamp))
                    {
                        accumulator.SkippedMalformedLines++;
                        continue;
                    }

                    string sessionFingerprint = CreateSessionFingerprint(CliKind.ClaudeCode, sessionId);
                    if (IsManagedSession(sessionFingerprint))
                    {
                        if (countedManagedSessions.Add(sessionFingerprint))
                        {
                            accumulator.SkippedManagedSessionFiles++;
                        }

                        continue;
                    }

                    await ImportObservationAsync(
                            new UsageObservation(
                                CliKind.ClaudeCode,
                                timestamp,
                                model,
                                counters,
                                CreateClaudeUsageFingerprint(sessionFingerprint, uuid)),
                            stateConnection,
                            state,
                            accumulator,
                            cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (JsonException)
                {
                    accumulator.SkippedMalformedLines++;
                }
                finally
                {
                    document?.Dispose();
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return;
        }

        await UpsertSourceCheckpointAsync(stateConnection, sourceKey, file!, cancellationToken)
            .ConfigureAwait(false);
        state.SourceCheckpoints[sourceKey] = SourceCheckpoint.From(file!);
    }

    private async Task ScanGeminiFileAsync(
        string filePath,
        SqliteConnection stateConnection,
        ImportState state,
        ImportAccumulator accumulator,
        CancellationToken cancellationToken)
    {
        if (!TryGetUsableFile(filePath, out FileInfo? file))
        {
            return;
        }

        accumulator.ScannedFiles++;
        var sourceKey = new SourceKey(
            CliKind.GeminiCli,
            CreateSourceFingerprint(CliKind.GeminiCli, file!.FullName));
        if (state.SourceCheckpoints.TryGetValue(sourceKey, out SourceCheckpoint checkpoint) &&
            checkpoint.Matches(file))
        {
            return;
        }

        if (Path.GetExtension(file.FullName).Equals(".json", StringComparison.OrdinalIgnoreCase))
        {
            try
            {
                await using FileStream documentStream = OpenSharedRead(file.FullName);
                using JsonDocument document = await JsonDocument.ParseAsync(
                        documentStream,
                        cancellationToken: cancellationToken)
                    .ConfigureAwait(false);
                JsonElement root = document.RootElement;
                if (root.ValueKind == JsonValueKind.Object &&
                    TryGetArray(root, "messages", out JsonElement messages))
                {
                    string? documentSessionId = GetString(root, "sessionId");
                    bool documentManagedSessionCounted = false;
                    foreach (JsonElement message in messages.EnumerateArray())
                    {
                        documentManagedSessionCounted |= await ImportGeminiMessageAsync(
                                message,
                                documentSessionId,
                                sourceKey,
                                stateConnection,
                                state,
                                accumulator,
                                documentManagedSessionCounted,
                                cancellationToken)
                            .ConfigureAwait(false);
                    }
                }
            }
            catch (JsonException)
            {
                accumulator.SkippedMalformedLines++;
            }
            catch (Exception exception) when (IsRecoverableFileException(exception))
            {
                return;
            }

            await UpsertSourceCheckpointAsync(stateConnection, sourceKey, file, cancellationToken)
                .ConfigureAwait(false);
            state.SourceCheckpoints[sourceKey] = SourceCheckpoint.From(file);
            return;
        }

        string? sessionId = null;
        bool managedSessionCounted = false;
        try
        {
            await using FileStream stream = OpenSharedRead(file.FullName);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);

            while (await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false) is { } line)
            {
                cancellationToken.ThrowIfCancellationRequested();
                if (string.IsNullOrWhiteSpace(line))
                {
                    continue;
                }

                if (line.Length > MaximumJsonLineCharacters)
                {
                    accumulator.SkippedMalformedLines++;
                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }

                    sessionId = GetString(root, "sessionId") ?? sessionId;
                    if (TryGetObject(root, "$set", out JsonElement update))
                    {
                        sessionId = GetString(update, "sessionId") ?? sessionId;
                        if (TryGetArray(update, "messages", out JsonElement updatedMessages))
                        {
                            foreach (JsonElement message in updatedMessages.EnumerateArray())
                            {
                                managedSessionCounted |= await ImportGeminiMessageAsync(
                                        message,
                                        sessionId,
                                        sourceKey,
                                        stateConnection,
                                        state,
                                        accumulator,
                                        managedSessionCounted,
                                        cancellationToken)
                                    .ConfigureAwait(false);
                            }
                        }
                    }

                    if (TryGetArray(root, "messages", out JsonElement messages))
                    {
                        foreach (JsonElement message in messages.EnumerateArray())
                        {
                            managedSessionCounted |= await ImportGeminiMessageAsync(
                                    message,
                                    sessionId,
                                    sourceKey,
                                    stateConnection,
                                    state,
                                    accumulator,
                                    managedSessionCounted,
                                    cancellationToken)
                                .ConfigureAwait(false);
                        }
                    }

                    managedSessionCounted |= await ImportGeminiMessageAsync(
                            root,
                            sessionId,
                            sourceKey,
                            stateConnection,
                            state,
                            accumulator,
                            managedSessionCounted,
                            cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (JsonException)
                {
                    accumulator.SkippedMalformedLines++;
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return;
        }

        await UpsertSourceCheckpointAsync(stateConnection, sourceKey, file, cancellationToken)
            .ConfigureAwait(false);
        state.SourceCheckpoints[sourceKey] = SourceCheckpoint.From(file);
    }

    private async Task<bool> ImportGeminiMessageAsync(
        JsonElement message,
        string? sessionId,
        SourceKey sourceKey,
        SqliteConnection stateConnection,
        ImportState state,
        ImportAccumulator accumulator,
        bool managedSessionAlreadyCounted,
        CancellationToken cancellationToken)
    {
        if (message.ValueKind != JsonValueKind.Object ||
            !string.Equals(GetString(message, "type"), "gemini", StringComparison.OrdinalIgnoreCase) ||
            !TryGetObject(message, "tokens", out JsonElement tokens))
        {
            return false;
        }

        string? messageId = GetString(message, "id");
        if (string.IsNullOrWhiteSpace(messageId) ||
            !TryReadTimestamp(message, out DateTimeOffset timestamp) ||
            !TryReadGeminiCounters(tokens, out TokenCounters counters))
        {
            accumulator.SkippedMalformedLines++;
            return false;
        }

        string sessionFingerprint;
        if (!string.IsNullOrWhiteSpace(sessionId))
        {
            sessionFingerprint = CreateSessionFingerprint(CliKind.GeminiCli, sessionId);
            if (IsManagedSession(sessionFingerprint))
            {
                if (!managedSessionAlreadyCounted)
                {
                    accumulator.SkippedManagedSessionFiles++;
                }

                return true;
            }
        }
        else
        {
            // Older Gemini JSONL may omit a session id. The already opaque
            // source fingerprint remains a stable per-file identity.
            sessionFingerprint = CreateSha256Fingerprint(
                $"official-gemini-source-session-v1|{sourceKey.SourceFingerprint}");
        }

        await ImportObservationAsync(
                new UsageObservation(
                    CliKind.GeminiCli,
                    timestamp,
                    GetString(message, "model"),
                    counters,
                    CreateGeminiUsageFingerprint(sessionFingerprint, messageId)),
                stateConnection,
                state,
                accumulator,
                cancellationToken)
            .ConfigureAwait(false);
        return false;
    }

    private async Task ImportObservationAsync(
        UsageObservation observation,
        SqliteConnection stateConnection,
        ImportState state,
        ImportAccumulator accumulator,
        CancellationToken cancellationToken)
    {
        string eventFingerprint = observation.EventFingerprint;
        // Claude's official uuid is a stable exact event identity. Its
        // fingerprint deliberately lives in a dedicated, non-pruned table:
        // Claude JSONL files are long-lived and an appended line causes their
        // complete file to be scanned again. The bounded generic marker table
        // remains unchanged for Codex, whose cumulative totals are separately
        // protected by persisted session maxima.
        bool usesDurableExactDeduplication = observation.CliKind is CliKind.ClaudeCode or CliKind.GeminiCli;
        if (!usesDurableExactDeduplication && !state.ImportedEventFingerprints.Add(eventFingerprint))
        {
            accumulator.SkippedDuplicateEvents++;
            return;
        }

        LocalUsageTelemetryEvent telemetryEvent = CreateTelemetryEvent(observation);
        bool stateRowWritten = false;
        try
        {
            stateRowWritten = await TryInsertImportedEventFingerprintAsync(
                    stateConnection,
                    eventFingerprint,
                    observation.CliKind,
                    cancellationToken)
                .ConfigureAwait(false);
            if (!stateRowWritten)
            {
                if (!usesDurableExactDeduplication)
                {
                    state.ImportedEventFingerprints.Remove(eventFingerprint);
                }

                accumulator.SkippedDuplicateEvents++;
                return;
            }

            await _telemetryRepository.RecordUsageAsync(telemetryEvent, cancellationToken).ConfigureAwait(false);
            accumulator.ImportedEvents++;
        }
        catch
        {
            // Write the opaque marker before telemetry so an ordinary state DB
            // write failure cannot make the next refresh double-count a usage
            // record that was already persisted by the repository. If telemetry
            // itself fails, remove that provisional marker and let a later scan
            // retry it.
            if (stateRowWritten)
            {
                await DeleteImportedEventFingerprintAsync(
                        stateConnection,
                        eventFingerprint,
                        observation.CliKind,
                        CancellationToken.None)
                    .ConfigureAwait(false);
            }
            if (!usesDurableExactDeduplication)
            {
                state.ImportedEventFingerprints.Remove(eventFingerprint);
            }

            throw;
        }
    }

    private static LocalUsageTelemetryEvent CreateTelemetryEvent(UsageObservation observation)
    {
        try
        {
            return new LocalUsageTelemetryEvent(
                observation.Timestamp,
                observation.CliKind,
                sourceId: null,
                sourceLabel: null,
                model: observation.Model,
                inputTokens: observation.Counters.InputTokens,
                outputTokens: observation.Counters.OutputTokens,
                cachedInputTokens: observation.Counters.CachedInputTokens,
                succeeded: true,
                elapsedMilliseconds: null,
                cacheCreationTokens: observation.Counters.CacheCreationTokens,
                estimatedCost: null,
                statusCategory: "success",
                pricingModel: null);
        }
        catch (ArgumentException)
        {
            // The model is display metadata from an official file.  If it is
            // malformed or looks like a secret/URL, preserve the numeric usage
            // while dropping all metadata.
            return new LocalUsageTelemetryEvent(
                observation.Timestamp,
                observation.CliKind,
                sourceId: null,
                sourceLabel: null,
                model: null,
                inputTokens: observation.Counters.InputTokens,
                outputTokens: observation.Counters.OutputTokens,
                cachedInputTokens: observation.Counters.CachedInputTokens,
                succeeded: true,
                elapsedMilliseconds: null,
                cacheCreationTokens: observation.Counters.CacheCreationTokens,
                estimatedCost: null,
                statusCategory: "success",
                pricingModel: null);
        }
    }

    private async Task EnsureInitializedAsync(CancellationToken cancellationToken)
    {
        if (_initialized)
        {
            return;
        }

        Directory.CreateDirectory(_databaseDirectory);
        await using SqliteConnection connection = await OpenConnectionAsync(cancellationToken).ConfigureAwait(false);
        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = """
                PRAGMA journal_mode = WAL;
                PRAGMA busy_timeout = 5000;

                CREATE TABLE IF NOT EXISTS imported_official_usage_events (
                    event_fingerprint TEXT PRIMARY KEY,
                    imported_at_utc INTEGER NOT NULL
                );

                CREATE INDEX IF NOT EXISTS ix_imported_official_usage_events_imported_at
                    ON imported_official_usage_events(imported_at_utc ASC);

                -- Claude usage has a stable per-assistant uuid.  Retain only
                -- its SHA-256 event fingerprint indefinitely so a long-lived
                -- append-only JSONL cannot re-add old usage after the bounded
                -- generic marker cache is pruned. No uuid, session id,
                -- transcript text, path, URL, or credential is persisted.
                CREATE TABLE IF NOT EXISTS imported_claude_usage_events_v1 (
                    event_fingerprint TEXT PRIMARY KEY,
                    imported_at_utc INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS imported_gemini_usage_events_v1 (
                    event_fingerprint TEXT PRIMARY KEY,
                    imported_at_utc INTEGER NOT NULL
                );

                -- Tracks the one-time upgrade of pre-v1 Claude markers. The
                -- numeric key contains no user-derived data.
                CREATE TABLE IF NOT EXISTS official_usage_import_migration_state (
                    migration_id INTEGER PRIMARY KEY,
                    completed_at_utc INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS managed_cli_sessions (
                    session_fingerprint TEXT PRIMARY KEY,
                    cli_kind INTEGER NOT NULL,
                    registered_at_utc INTEGER NOT NULL
                );

                -- v1 checkpoints were keyed by session fingerprint. Codex can
                -- contain several session_meta segments in one JSONL, so v2 is
                -- keyed by an opaque source-file fingerprint instead.
                CREATE TABLE IF NOT EXISTS official_usage_source_checkpoints_v2 (
                    cli_kind INTEGER NOT NULL,
                    source_fingerprint TEXT NOT NULL,
                    file_length INTEGER NOT NULL,
                    last_write_utc_ticks INTEGER NOT NULL,
                    scanned_at_utc INTEGER NOT NULL,
                    PRIMARY KEY(cli_kind, source_fingerprint)
                );

                CREATE TABLE IF NOT EXISTS official_codex_session_totals_v2 (
                    session_fingerprint TEXT PRIMARY KEY,
                    input_tokens INTEGER NOT NULL,
                    output_tokens INTEGER NOT NULL,
                    cached_input_tokens INTEGER NOT NULL,
                    cache_creation_tokens INTEGER NOT NULL,
                    updated_at_utc INTEGER NOT NULL
                );
                """;
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }

        await MigrateLegacyClaudeEventFingerprintsAsync(connection, cancellationToken).ConfigureAwait(false);

        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = "SELECT session_fingerprint FROM managed_cli_sessions;";
            await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
            while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
            {
                string fingerprint = reader.GetString(0);
                if (IsSha256Fingerprint(fingerprint))
                {
                    _managedSessionFingerprints.TryAdd(fingerprint, 0);
                }
            }
        }

        _initialized = true;
    }

    private async Task<ImportState> LoadImportStateAsync(
        SqliteConnection connection,
        CancellationToken cancellationToken)
    {
        var importedEventFingerprints = new HashSet<string>(StringComparer.Ordinal);
        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = "SELECT event_fingerprint FROM imported_official_usage_events;";
            await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
            while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
            {
                string fingerprint = reader.GetString(0);
                if (IsSha256Fingerprint(fingerprint))
                {
                    importedEventFingerprints.Add(fingerprint);
                }
            }
        }

        var sourceCheckpoints = new Dictionary<SourceKey, SourceCheckpoint>();
        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = """
                SELECT cli_kind, source_fingerprint, file_length, last_write_utc_ticks
                FROM official_usage_source_checkpoints_v2;
                """;
            await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
            while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
            {
                int rawCliKind = reader.GetInt32(0);
                string fingerprint = reader.GetString(1);
                if (!Enum.IsDefined((CliKind)rawCliKind) || !IsSha256Fingerprint(fingerprint))
                {
                    continue;
                }

                var key = new SourceKey((CliKind)rawCliKind, fingerprint);
                sourceCheckpoints[key] = new SourceCheckpoint(reader.GetInt64(2), reader.GetInt64(3));
            }
        }

        var codexSessionMaximums = new Dictionary<string, TokenCounters>(StringComparer.Ordinal);
        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = """
                SELECT session_fingerprint, input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens
                FROM official_codex_session_totals_v2;
                """;
            await using SqliteDataReader reader = await command.ExecuteReaderAsync(cancellationToken).ConfigureAwait(false);
            while (await reader.ReadAsync(cancellationToken).ConfigureAwait(false))
            {
                string fingerprint = reader.GetString(0);
                if (!IsSha256Fingerprint(fingerprint))
                {
                    continue;
                }

                long input = reader.GetInt64(1);
                long output = reader.GetInt64(2);
                long cached = reader.GetInt64(3);
                long cacheCreation = reader.GetInt64(4);
                if (input < 0 || output < 0 || cached < 0 || cacheCreation < 0)
                {
                    continue;
                }

                codexSessionMaximums[fingerprint] = new TokenCounters(input, output, cached, cacheCreation);
            }
        }

        return new ImportState(importedEventFingerprints, sourceCheckpoints, codexSessionMaximums);
    }

    private async Task PruneStateAsync(SqliteConnection connection, CancellationToken cancellationToken)
    {
        long cutoff = DateTimeOffset.UtcNow.Subtract(FingerprintRetention).ToUnixTimeSeconds();
        await using (SqliteCommand command = connection.CreateCommand())
        {
            command.CommandText = """
                DELETE FROM imported_official_usage_events
                WHERE imported_at_utc < $cutoff;

                DELETE FROM imported_official_usage_events
                WHERE event_fingerprint IN (
                    SELECT event_fingerprint
                    FROM imported_official_usage_events
                    ORDER BY imported_at_utc DESC
                    LIMIT -1 OFFSET $maximum_rows
                );
                """;
            command.Parameters.AddWithValue("$cutoff", cutoff);
            command.Parameters.AddWithValue("$maximum_rows", MaximumImportedFingerprintRows);
            await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    private static async Task MigrateLegacyClaudeEventFingerprintsAsync(
        SqliteConnection connection,
        CancellationToken cancellationToken)
    {
        // Older builds placed both Codex and Claude markers in the bounded
        // generic table. The table does not retain a CLI kind, so conservatively
        // seed the new Claude dedup table with every valid opaque fingerprint
        // exactly once. A Codex fingerprint can never collide with a Claude
        // fingerprint in practice, and this prevents a one-time upgrade from
        // re-importing existing Claude usage when a history file later grows.
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            INSERT OR IGNORE INTO imported_claude_usage_events_v1 (event_fingerprint, imported_at_utc)
            SELECT event_fingerprint, imported_at_utc
            FROM imported_official_usage_events
            WHERE length(event_fingerprint) = 64
              AND event_fingerprint NOT GLOB '*[^0-9a-f]*'
              AND NOT EXISTS (
                  SELECT 1
                  FROM official_usage_import_migration_state
                  WHERE migration_id = 1);

            INSERT OR IGNORE INTO official_usage_import_migration_state (
                migration_id, completed_at_utc)
            VALUES (1, $completed_at_utc);
            """;
        command.Parameters.AddWithValue("$completed_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private static async Task UpsertSourceCheckpointAsync(
        SqliteConnection connection,
        SourceKey key,
        FileInfo file,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            INSERT INTO official_usage_source_checkpoints_v2 (
                cli_kind, source_fingerprint, file_length, last_write_utc_ticks, scanned_at_utc)
            VALUES ($cli_kind, $source_fingerprint, $file_length, $last_write_utc_ticks, $scanned_at_utc)
            ON CONFLICT(cli_kind, source_fingerprint) DO UPDATE SET
                file_length = excluded.file_length,
                last_write_utc_ticks = excluded.last_write_utc_ticks,
                scanned_at_utc = excluded.scanned_at_utc;
        """;
        command.Parameters.AddWithValue("$cli_kind", (int)key.CliKind);
        command.Parameters.AddWithValue("$source_fingerprint", key.SourceFingerprint);
        command.Parameters.AddWithValue("$file_length", file.Length);
        command.Parameters.AddWithValue("$last_write_utc_ticks", file.LastWriteTimeUtc.Ticks);
        command.Parameters.AddWithValue("$scanned_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private static async Task UpsertCodexSessionMaximumAsync(
        SqliteConnection connection,
        string sessionFingerprint,
        TokenCounters maximum,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = """
            INSERT INTO official_codex_session_totals_v2 (
                session_fingerprint, input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens, updated_at_utc)
            VALUES ($session_fingerprint, $input_tokens, $output_tokens, $cached_input_tokens, $cache_creation_tokens, $updated_at_utc)
            ON CONFLICT(session_fingerprint) DO UPDATE SET
                input_tokens = excluded.input_tokens,
                output_tokens = excluded.output_tokens,
                cached_input_tokens = excluded.cached_input_tokens,
                cache_creation_tokens = excluded.cache_creation_tokens,
                updated_at_utc = excluded.updated_at_utc;
            """;
        command.Parameters.AddWithValue("$session_fingerprint", sessionFingerprint);
        command.Parameters.AddWithValue("$input_tokens", maximum.InputTokens);
        command.Parameters.AddWithValue("$output_tokens", maximum.OutputTokens);
        command.Parameters.AddWithValue("$cached_input_tokens", maximum.CachedInputTokens);
        command.Parameters.AddWithValue("$cache_creation_tokens", maximum.CacheCreationTokens);
        command.Parameters.AddWithValue("$updated_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private static async Task<bool> TryInsertImportedEventFingerprintAsync(
        SqliteConnection connection,
        string eventFingerprint,
        CliKind cliKind,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = cliKind switch
        {
            CliKind.ClaudeCode => """
                INSERT OR IGNORE INTO imported_claude_usage_events_v1 (event_fingerprint, imported_at_utc)
                VALUES ($event_fingerprint, $imported_at_utc);
                """,
            CliKind.GeminiCli => """
                INSERT OR IGNORE INTO imported_gemini_usage_events_v1 (event_fingerprint, imported_at_utc)
                VALUES ($event_fingerprint, $imported_at_utc);
                """,
            _ => """
                INSERT OR IGNORE INTO imported_official_usage_events (event_fingerprint, imported_at_utc)
                VALUES ($event_fingerprint, $imported_at_utc);
                """,
        };
        command.Parameters.AddWithValue("$event_fingerprint", eventFingerprint);
        command.Parameters.AddWithValue("$imported_at_utc", DateTimeOffset.UtcNow.ToUnixTimeSeconds());
        return await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false) > 0;
    }

    private static async Task DeleteImportedEventFingerprintAsync(
        SqliteConnection connection,
        string eventFingerprint,
        CliKind cliKind,
        CancellationToken cancellationToken)
    {
        await using SqliteCommand command = connection.CreateCommand();
        command.CommandText = cliKind switch
        {
            CliKind.ClaudeCode => "DELETE FROM imported_claude_usage_events_v1 WHERE event_fingerprint = $event_fingerprint;",
            CliKind.GeminiCli => "DELETE FROM imported_gemini_usage_events_v1 WHERE event_fingerprint = $event_fingerprint;",
            _ => "DELETE FROM imported_official_usage_events WHERE event_fingerprint = $event_fingerprint;",
        };
        command.Parameters.AddWithValue("$event_fingerprint", eventFingerprint);
        await command.ExecuteNonQueryAsync(cancellationToken).ConfigureAwait(false);
    }

    private IEnumerable<string> EnumerateCodexFiles()
    {
        return new[] { _paths.CodexSessionsDirectory, _paths.CodexArchivedSessionsDirectory }
            .Where(Directory.Exists)
            .SelectMany(root => EnumerateFilesSafely(root, "*.jsonl"))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    private IEnumerable<string> EnumerateClaudeFiles()
    {
        if (!Directory.Exists(_paths.ClaudeProjectsDirectory))
        {
            return Array.Empty<string>();
        }

        return EnumerateFilesSafely(_paths.ClaudeProjectsDirectory, "*.jsonl")
            .Where(path => !IsClaudeSubagentPath(path))
            .ToArray();
    }

    private IEnumerable<string> EnumerateGeminiFiles()
    {
        if (!Directory.Exists(_paths.GeminiProjectsDirectory))
        {
            return Array.Empty<string>();
        }

        return new[] { "session-*.jsonl", "session-*.json" }
            .SelectMany(pattern => EnumerateFilesSafely(_paths.GeminiProjectsDirectory, pattern))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray();
    }

    private static IEnumerable<string> EnumerateFilesSafely(string root, string searchPattern)
    {
        try
        {
            return Directory.EnumerateFiles(
                    root,
                    searchPattern,
                    new EnumerationOptions
                    {
                        RecurseSubdirectories = true,
                        IgnoreInaccessible = true,
                        AttributesToSkip = FileAttributes.ReparsePoint,
                    })
                .OrderBy(path => path, StringComparer.OrdinalIgnoreCase)
                .ToArray();
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return Array.Empty<string>();
        }
    }

    private static bool IsClaudeSubagentPath(string path)
    {
        try
        {
            return path.Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
                .Any(part => string.Equals(part, "subagents", StringComparison.OrdinalIgnoreCase));
        }
        catch (ArgumentException)
        {
            return true;
        }
    }

    private static bool TryGetUsableFile(string path, out FileInfo? file)
    {
        file = null;
        try
        {
            var candidate = new FileInfo(path);
            candidate.Refresh();
            if (!candidate.Exists || candidate.Length <= 0 || candidate.Length > MaximumHistoryFileBytes)
            {
                return false;
            }

            file = candidate;
            return true;
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return false;
        }
    }

    private static FileStream OpenSharedRead(string path) => new(
        path,
        FileMode.Open,
        FileAccess.Read,
        FileShare.ReadWrite | FileShare.Delete,
        bufferSize: 16 * 1024,
        FileOptions.Asynchronous | FileOptions.SequentialScan);

    private bool IsManagedSession(string sessionFingerprint) =>
        _managedSessionFingerprints.ContainsKey(sessionFingerprint);

    private static bool TryGetCodexSessionIdentity(JsonElement root, out CodexSessionIdentity identity)
    {
        identity = default;
        if (!string.Equals(GetString(root, "type"), "session_meta", StringComparison.OrdinalIgnoreCase) ||
            !TryGetObject(root, "payload", out JsonElement payload))
        {
            return false;
        }

        string? threadId = GetString(payload, "id") ??
                           GetString(payload, "thread_id") ??
                           GetString(payload, "threadId") ??
                           GetString(payload, "session_id") ??
                           GetString(payload, "sessionId");
        if (string.IsNullOrWhiteSpace(threadId))
        {
            return false;
        }

        string? parentSessionId = GetString(payload, "session_id") ?? GetString(payload, "sessionId");
        bool carriesHistorySnapshot = !string.IsNullOrWhiteSpace(GetString(payload, "forked_from_id")) ||
                                      (TryGetObject(payload, "source", out JsonElement source) &&
                                       source.TryGetProperty("subagent", out _)) ||
                                      (!string.IsNullOrWhiteSpace(parentSessionId) &&
                                       !string.Equals(parentSessionId, threadId, StringComparison.Ordinal));
        identity = new CodexSessionIdentity(threadId, carriesHistorySnapshot);
        return true;
    }

    private static CodexReplayInfo ReadCodexReplayInfo(string filePath)
    {
        CodexSessionIdentity? identity = null;
        long? boundary = null;
        try
        {
            using FileStream stream = OpenSharedRead(filePath);
            using var reader = new StreamReader(
                stream,
                Encoding.UTF8,
                detectEncodingFromByteOrderMarks: true,
                bufferSize: 16 * 1024,
                leaveOpen: false);
            long lineNumber = 0;
            while (reader.ReadLine() is { } line)
            {
                lineNumber++;
                if (string.IsNullOrWhiteSpace(line) || line.Length > MaximumJsonLineCharacters)
                {
                    continue;
                }

                try
                {
                    using JsonDocument document = JsonDocument.Parse(line);
                    JsonElement root = document.RootElement;
                    if (identity is null && TryGetCodexSessionIdentity(root, out CodexSessionIdentity parsed))
                    {
                        identity = parsed;
                    }

                    if (identity?.CarriesHistorySnapshot == true && IsCodexReplayBoundary(root))
                    {
                        boundary = lineNumber;
                        break;
                    }
                }
                catch (JsonException)
                {
                    // Ignore partial lines while the official CLI is appending.
                }
            }
        }
        catch (Exception exception) when (IsRecoverableFileException(exception))
        {
            return default;
        }

        return new CodexReplayInfo(identity, boundary);
    }

    private static bool IsCodexReplayBoundary(JsonElement root)
    {
        string? eventType = GetString(root, "type");
        if (!string.IsNullOrWhiteSpace(eventType) &&
            eventType.StartsWith("inter_agent_communication", StringComparison.Ordinal))
        {
            return true;
        }

        return string.Equals(eventType, "event_msg", StringComparison.Ordinal) &&
               TryGetObject(root, "payload", out JsonElement payload) &&
               string.Equals(GetString(payload, "type"), "thread_settings_applied", StringComparison.Ordinal);
    }

    private static bool TryReadCodexTurnContextModel(JsonElement root, out string? model)
    {
        model = null;
        if (!string.Equals(GetString(root, "type"), "turn_context", StringComparison.OrdinalIgnoreCase) ||
            !TryGetObject(root, "payload", out JsonElement payload))
        {
            return false;
        }

        string? candidate = GetString(payload, "model");
        if (string.IsNullOrWhiteSpace(candidate) && TryGetObject(payload, "info", out JsonElement info))
        {
            candidate = GetString(info, "model") ?? GetString(info, "model_name");
        }
        if (string.IsNullOrWhiteSpace(candidate))
        {
            return false;
        }

        model = candidate;
        return true;
    }

    private static bool IsCodexTokenCount(JsonElement root) =>
        TryGetObject(root, "payload", out JsonElement payload) &&
        string.Equals(GetString(payload, "type"), "token_count", StringComparison.OrdinalIgnoreCase);

    /// <summary>
    /// Reads only the cumulative Codex total. `last_token_usage` has no stable
    /// turn/request key and is frequently repeated beside the same total, so it
    /// is intentionally never treated as an independently billable event.
    /// </summary>
    private static bool TryReadCodexTotalUsage(JsonElement root, out TokenCounters counters)
    {
        counters = default;
        if (!TryGetObject(root, "payload", out JsonElement payload) ||
            !string.Equals(GetString(payload, "type"), "token_count", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        JsonElement info = TryGetObject(payload, "info", out JsonElement candidateInfo)
            ? candidateInfo
            : payload;
        return TryGetFirstObject(info, out JsonElement totalUsage, "total_token_usage", "totalTokenUsage") &&
               TryReadCounters(totalUsage, out counters);
    }

    private static bool TryReadClaudeUsage(JsonElement root, out TokenCounters counters, out string? model)
    {
        counters = default;
        model = null;
        if (!string.Equals(GetString(root, "type"), "assistant", StringComparison.OrdinalIgnoreCase) ||
            GetBoolean(root, "isMeta") || GetBoolean(root, "isSidechain") ||
            !TryGetObject(root, "message", out JsonElement message) ||
            !TryGetObject(message, "usage", out JsonElement usage))
        {
            return false;
        }

        model = GetString(message, "model");
        return TryReadCounters(usage, out counters);
    }

    private static bool TryReadGeminiCounters(JsonElement tokens, out TokenCounters counters)
    {
        long? input = GetNonNegativeInt64(tokens, "input");
        long? total = GetNonNegativeInt64(tokens, "total");
        long? output = GetNonNegativeInt64(tokens, "output");
        long? thoughts = GetNonNegativeInt64(tokens, "thoughts");
        long? tool = GetNonNegativeInt64(tokens, "tool");
        long? cached = GetNonNegativeInt64(tokens, "cached");
        long? combinedOutput = input is { } inputValue && total is { } totalValue && totalValue >= inputValue
            ? totalValue - inputValue
            : SumNullable(output, thoughts, tool);
        counters = new TokenCounters(input, combinedOutput, cached, 0);
        return counters.HasPositiveValue;
    }

    private static long? SumNullable(params long?[] values)
        => values.Any(value => value is not null)
            ? values.Sum(value => value ?? 0)
            : null;

    private static bool TryReadCounters(JsonElement usage, out TokenCounters counters)
    {
        // Keep the same official total-token accounting as the live clients:
        // output_tokens is the output dimension. Codex also exposes reasoning
        // and total convenience fields, but adding either would double-count
        // the verified input + output total and this UI has no separate
        // reasoning metric.
        long? input = GetNonNegativeInt64(usage, "input_tokens", "inputTokens");
        long? output = GetNonNegativeInt64(usage, "output_tokens", "outputTokens");
        long? cached = GetNonNegativeInt64(
            usage,
            "cache_read_input_tokens",
            "cached_input_tokens",
            "cacheReadInputTokens",
            "cachedInputTokens");
        long? cacheCreation = GetNonNegativeInt64(
            usage,
            "cache_creation_input_tokens",
            "cache_creation_tokens",
            "cacheCreationInputTokens",
            "cacheCreationTokens");
        counters = new TokenCounters(input, output, cached, cacheCreation);
        return counters.HasAny;
    }

    private static bool TryReadTimestamp(JsonElement root, out DateTimeOffset timestamp)
    {
        timestamp = default;
        if (!root.TryGetProperty("timestamp", out JsonElement raw))
        {
            return false;
        }

        if (raw.ValueKind == JsonValueKind.String)
        {
            string? value = raw.GetString();
            if (DateTimeOffset.TryParse(
                    value,
                    CultureInfo.InvariantCulture,
                    DateTimeStyles.AssumeUniversal | DateTimeStyles.AdjustToUniversal,
                    out DateTimeOffset parsed))
            {
                timestamp = parsed;
                return true;
            }

            if (long.TryParse(value, NumberStyles.Integer, CultureInfo.InvariantCulture, out long numeric))
            {
                return TryReadUnixTimestamp(numeric, out timestamp);
            }

            return false;
        }

        if (raw.ValueKind == JsonValueKind.Number && raw.TryGetInt64(out long number))
        {
            return TryReadUnixTimestamp(number, out timestamp);
        }

        return false;
    }

    private static bool TryReadUnixTimestamp(long value, out DateTimeOffset timestamp)
    {
        timestamp = default;
        try
        {
            timestamp = Math.Abs(value) >= 100_000_000_000
                ? DateTimeOffset.FromUnixTimeMilliseconds(value)
                : DateTimeOffset.FromUnixTimeSeconds(value);
            return true;
        }
        catch (ArgumentOutOfRangeException)
        {
            return false;
        }
    }

    private static string? GetString(JsonElement root, string propertyName)
    {
        return root.ValueKind == JsonValueKind.Object &&
               root.TryGetProperty(propertyName, out JsonElement value) &&
               value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;
    }

    private static bool GetBoolean(JsonElement root, string propertyName) =>
        root.ValueKind == JsonValueKind.Object &&
        root.TryGetProperty(propertyName, out JsonElement value) &&
        value.ValueKind == JsonValueKind.True;

    private static bool TryGetObject(JsonElement root, string propertyName, out JsonElement value)
    {
        value = default;
        return root.ValueKind == JsonValueKind.Object &&
               root.TryGetProperty(propertyName, out JsonElement candidate) &&
               candidate.ValueKind == JsonValueKind.Object &&
               (value = candidate).ValueKind == JsonValueKind.Object;
    }

    private static bool TryGetArray(JsonElement root, string propertyName, out JsonElement value)
    {
        value = default;
        return root.ValueKind == JsonValueKind.Object &&
               root.TryGetProperty(propertyName, out JsonElement candidate) &&
               candidate.ValueKind == JsonValueKind.Array &&
               (value = candidate).ValueKind == JsonValueKind.Array;
    }

    private static bool TryGetFirstObject(JsonElement root, out JsonElement value, params string[] names)
    {
        foreach (string name in names)
        {
            if (TryGetObject(root, name, out value))
            {
                return true;
            }
        }

        value = default;
        return false;
    }

    private static long? GetNonNegativeInt64(JsonElement root, params string[] propertyNames)
    {
        foreach (string propertyName in propertyNames)
        {
            if (!root.TryGetProperty(propertyName, out JsonElement value))
            {
                continue;
            }

            if (value.ValueKind == JsonValueKind.Number && value.TryGetInt64(out long numeric) && numeric >= 0)
            {
                return numeric;
            }

            if (value.ValueKind == JsonValueKind.String &&
                long.TryParse(value.GetString(), NumberStyles.Integer, CultureInfo.InvariantCulture, out numeric) &&
                numeric >= 0)
            {
                return numeric;
            }
        }

        return null;
    }

    private static string CreateSessionFingerprint(CliKind cliKind, string nativeSessionId) =>
        CreateSha256Fingerprint($"official-cli-session-v1|{(int)cliKind}|{nativeSessionId.Trim()}");

    private static string CreateSourceFingerprint(CliKind cliKind, string fullPath)
    {
        string normalized = Path.GetFullPath(fullPath);
        if (OperatingSystem.IsWindows())
        {
            normalized = normalized.ToUpperInvariant();
        }

        return CreateSha256Fingerprint($"official-cli-source-v2|{(int)cliKind}|{normalized}");
    }

    private static string CreateCodexUsageFingerprint(string sourceFingerprint, long lineNumber) =>
        CreateSha256Fingerprint(
            $"official-codex-usage-v2|{sourceFingerprint}|{lineNumber.ToString(CultureInfo.InvariantCulture)}");

    private static string CreateClaudeUsageFingerprint(string sessionFingerprint, string uuid) =>
        CreateSha256Fingerprint($"official-claude-usage-v2|{sessionFingerprint}|{uuid.Trim()}");

    private static string CreateGeminiUsageFingerprint(string sessionFingerprint, string messageId) =>
        CreateSha256Fingerprint($"official-gemini-usage-v1|{sessionFingerprint}|{messageId.Trim()}");

    private static string CreateSha256Fingerprint(string value)
    {
        byte[] bytes = Encoding.UTF8.GetBytes(value);
        try
        {
            return Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant();
        }
        finally
        {
            CryptographicOperations.ZeroMemory(bytes);
        }
    }

    private static bool IsSha256Fingerprint(string value) =>
        value.Length == 64 && value.All(character =>
            character is >= '0' and <= '9' or >= 'a' and <= 'f');

    private static bool IsRecoverableFileException(Exception exception) => exception is
        IOException or UnauthorizedAccessException or SecurityException or NotSupportedException or PathTooLongException;

    private async Task<SqliteConnection> OpenConnectionAsync(CancellationToken cancellationToken)
    {
        var connection = new SqliteConnection(_connectionString);
        await connection.OpenAsync(cancellationToken).ConfigureAwait(false);
        return connection;
    }

    private void ThrowIfDisposed()
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        // Do not dispose the semaphore here. A best-effort scan may already
        // be unwinding after shutdown and still needs to release it safely.
        // This importer owns no long-lived connection or native handle, so
        // leaving this tiny managed synchronization object for process teardown
        // is safer than turning shutdown into an ObjectDisposedException race.
    }

    private sealed class CodexTimelineSegment(
        string sessionFingerprint,
        bool isManaged,
        long? historyReplayBoundary)
    {
        public string SessionFingerprint { get; } = sessionFingerprint;

        public bool IsManaged { get; } = isManaged;

        public long? HistoryReplayBoundary { get; } = historyReplayBoundary;

        public TokenCounters? TimelineMaximum { get; set; }

        /// <summary>
        /// Codex token_count does not carry a model. The most recent
        /// turn_context for the active meta segment is the only safe display
        /// attribution we use.
        /// </summary>
        public string? Model { get; set; }
    }

    private readonly record struct CodexSessionIdentity(string ThreadId, bool CarriesHistorySnapshot);

    private readonly record struct CodexReplayInfo(
        CodexSessionIdentity? Identity,
        long? HistoryReplayBoundary);

    private sealed class ImportAccumulator
    {
        public int ScannedFiles { get; set; }

        public int ImportedEvents { get; set; }

        public int SkippedDuplicateEvents { get; set; }

        public int SkippedManagedSessionFiles { get; set; }

        public int SkippedMalformedLines { get; set; }

        public int SkippedUnverifiableUsageEvents { get; set; }
    }

    private sealed class ImportState(
        HashSet<string> importedEventFingerprints,
        Dictionary<SourceKey, SourceCheckpoint> sourceCheckpoints,
        Dictionary<string, TokenCounters> codexSessionMaximums)
    {
        public HashSet<string> ImportedEventFingerprints { get; } = importedEventFingerprints;

        public Dictionary<SourceKey, SourceCheckpoint> SourceCheckpoints { get; } = sourceCheckpoints;

        public Dictionary<string, TokenCounters> CodexSessionMaximums { get; } = codexSessionMaximums;
    }

    private readonly record struct SourceKey(CliKind CliKind, string SourceFingerprint);

    private readonly record struct SourceCheckpoint(long FileLength, long LastWriteUtcTicks)
    {
        public static SourceCheckpoint From(FileInfo file) => new(file.Length, file.LastWriteTimeUtc.Ticks);

        public bool Matches(FileInfo file) =>
            FileLength == file.Length && LastWriteUtcTicks == file.LastWriteTimeUtc.Ticks;
    }

    private readonly record struct TokenCounters(
        long? Input,
        long? Output,
        long? CachedInput,
        long? CacheCreation)
    {
        public TokenCounters(long inputTokens, long outputTokens, long cachedInputTokens, long cacheCreationTokens)
            : this((long?)inputTokens, outputTokens, cachedInputTokens, cacheCreationTokens)
        {
        }

        public static TokenCounters Empty { get; } = new(0, 0, 0, 0);

        public bool HasAny => Input is not null || Output is not null || CachedInput is not null || CacheCreation is not null;

        public bool HasPositiveValue =>
            InputTokens > 0 ||
            OutputTokens > 0 ||
            CachedInputTokens > 0 ||
            CacheCreationTokens > 0;

        public TokenCounters PositiveDifferenceFrom(TokenCounters previous) => new(
            PositiveDifference(Input, previous.Input),
            PositiveDifference(Output, previous.Output),
            PositiveDifference(CachedInput, previous.CachedInput),
            PositiveDifference(CacheCreation, previous.CacheCreation));

        public TokenCounters ComponentwiseMaximum(TokenCounters candidate) => new(
            Maximum(Input, candidate.Input),
            Maximum(Output, candidate.Output),
            Maximum(CachedInput, candidate.CachedInput),
            Maximum(CacheCreation, candidate.CacheCreation));

        private static long PositiveDifference(long? current, long? previous) =>
            current is { } value
                ? Math.Max(0, value - (previous ?? 0))
                : 0;

        private static long Maximum(long? existing, long? candidate) => Math.Max(existing ?? 0, candidate ?? 0);

        public long InputTokens => Input ?? 0;

        public long OutputTokens => Output ?? 0;

        public long CachedInputTokens => CachedInput ?? 0;

        public long CacheCreationTokens => CacheCreation ?? 0;
    }

    private readonly record struct UsageObservation(
        CliKind CliKind,
        DateTimeOffset Timestamp,
        string? Model,
        TokenCounters Counters,
        string EventFingerprint);
}
