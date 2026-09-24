using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Holds the minimum durable state needed to renew one locally authenticated
/// Sub2API account session.  Passwords and short-lived access tokens are
/// intentionally never accepted or persisted here.
/// </summary>
internal interface ILocalSub2ApiAccountSessionStore
{
    /// <summary>
    /// Reads the session saved for the current Windows user.  Invalid,
    /// malformed, unavailable, or non-local records are treated as absent.
    /// </summary>
    LocalSub2ApiAccountSession? Load(Uri apiBaseUri);

    LocalSub2ApiAccountSession? LoadMostRecent();

    /// <summary>
    /// Persists a replacement rotating refresh-token session for the current
    /// Windows user.
    /// </summary>
    LocalSub2ApiAccountSessionSaveResult Save(LocalSub2ApiAccountSession session);

    /// <summary>
    /// Removes the locally persisted session.  Revocation at the server is a
    /// separate authenticated network operation owned by the session manager.
    /// </summary>
    bool Clear(Uri apiBaseUri);
}

internal enum LocalSub2ApiAccountSessionSaveResult
{
    Saved,
    Invalid,
    Unavailable,
}

/// <summary>
/// A validated local-account session.  The refresh token is intentionally
/// assembly-internal so it cannot be bound into a WPF view or displayed by
/// consumers.  <see cref="ToString"/> is deliberately non-sensitive.
/// </summary>
internal sealed class LocalSub2ApiAccountSession
{
    internal const int CurrentVersion = 1;

    private LocalSub2ApiAccountSession(
        string refreshToken,
        Uri apiBaseUri,
        long userId,
        string role,
        DateTimeOffset savedAtUtc)
    {
        RefreshToken = refreshToken;
        ApiBaseUri = apiBaseUri;
        UserId = userId;
        Role = role;
        SavedAtUtc = savedAtUtc;
    }

    /// <summary>
    /// Only session/network services inside this assembly can use the token.
    /// It must never be copied to a bindable property, a status value, or a
    /// diagnostic message.
    /// </summary>
    internal string RefreshToken { get; }

    public int Version => CurrentVersion;

    public Uri ApiBaseUri { get; }

    public long UserId { get; }

    public string Role { get; }

    public DateTimeOffset SavedAtUtc { get; }

    public bool IsAdministrator => string.Equals(Role, "admin", StringComparison.Ordinal);

    public override string ToString() => "Local Sub2API account session";

    /// <summary>
    /// Validates data received from the login/refresh flow before it can be
    /// written to disk. The endpoint is normalized using the same rules as an
    /// explicitly selected Sub2API source. Public HTTP refresh sessions are
    /// deliberately excluded: they would be replayed automatically at startup.
    /// </summary>
    internal static bool TryCreate(
        string? refreshToken,
        string? apiBaseUrl,
        long userId,
        string? role,
        DateTimeOffset savedAtUtc,
        out LocalSub2ApiAccountSession? session)
    {
        session = null;
        if (!TryNormalizeRefreshToken(refreshToken, out string? normalizedToken) ||
            !Sub2ApiEndpointNormalizer.TryNormalizeApiBaseUri(
                apiBaseUrl,
                allowPublicHttp: false,
                out Uri? normalizedBaseUri) ||
            userId <= 0 ||
            !TryNormalizeRole(role, out string? normalizedRole) ||
            savedAtUtc == default)
        {
            return false;
        }

        session = new LocalSub2ApiAccountSession(
            normalizedToken!,
            normalizedBaseUri!,
            userId,
            normalizedRole!,
            savedAtUtc.ToUniversalTime());
        return true;
    }

    internal static bool TryNormalizeRefreshToken(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        string candidate = value.Trim();
        if (candidate.Length is 0 or > 4096 || candidate.Any(char.IsControl))
        {
            return false;
        }

        normalized = candidate;
        return true;
    }

    internal static bool TryNormalizeRole(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        string candidate = value.Trim();
        if (string.Equals(candidate, "admin", StringComparison.OrdinalIgnoreCase))
        {
            normalized = "admin";
            return true;
        }

        if (string.Equals(candidate, "user", StringComparison.OrdinalIgnoreCase))
        {
            normalized = "user";
            return true;
        }

        return false;
    }
}

/// <summary>
/// Stores a rotating Sub2API account refresh token using Windows DPAPI scoped
/// to the current Windows user.  The protected payload additionally binds the
    /// token to a normalized gateway address and the server-issued account
/// identity/role.  It never stores a password or an access token.
/// </summary>
internal sealed class LocalSub2ApiAccountSessionStore : ILocalSub2ApiAccountSessionStore
{
    private const int MaximumProtectedBytes = 16 * 1024;
    private const int CurrentStoreVersion = 2;
    private static readonly byte[] Entropy = SHA256.HashData(
        Encoding.UTF8.GetBytes("LanAi.Workspace/Sub2ApiLocalAccountSession/v1"));

    private readonly string _path;
    private readonly object _sync = new();

    public LocalSub2ApiAccountSessionStore()
        : this(Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "LanAi.Workspace",
            "Auth",
            "sub2api-local-account-session.bin"))
    {
    }

    internal LocalSub2ApiAccountSessionStore(string path)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(path);
        _path = Path.GetFullPath(path);
    }

    public LocalSub2ApiAccountSession? Load(Uri apiBaseUri)
    {
        ArgumentNullException.ThrowIfNull(apiBaseUri);
        lock (_sync)
        {
            return ReadSessions().FirstOrDefault(session => SameEndpoint(session.ApiBaseUri, apiBaseUri));
        }
    }

    public LocalSub2ApiAccountSession? LoadMostRecent()
    {
        lock (_sync)
        {
            return ReadSessions().MaxBy(session => session.SavedAtUtc);
        }
    }

    private List<LocalSub2ApiAccountSession> ReadSessions()
    {
        if (!OperatingSystem.IsWindows() || !File.Exists(_path))
        {
            return [];
        }

        byte[]? protectedBytes = null;
        byte[]? plainBytes = null;
        try
        {
            protectedBytes = File.ReadAllBytes(_path);
            if (protectedBytes.Length is 0 or > MaximumProtectedBytes)
            {
                return [];
            }

            plainBytes = ProtectedData.Unprotect(
                protectedBytes,
                Entropy,
                DataProtectionScope.CurrentUser);
            using JsonDocument document = JsonDocument.Parse(plainBytes);
            if (document.RootElement.TryGetProperty(nameof(PersistedSessionCollection.Sessions), out _))
            {
                PersistedSessionCollection? collection = JsonSerializer.Deserialize<PersistedSessionCollection>(plainBytes);
                return collection?.Version == CurrentStoreVersion
                    ? RestoreSessions(collection.Sessions)
                    : [];
            }

            // Version 1 stored a single endpoint. Keep it readable so existing
            // installations retain their current login after upgrading.
            PersistedSession? legacy = JsonSerializer.Deserialize<PersistedSession>(plainBytes);
            return legacy is not null &&
                   legacy.Version == LocalSub2ApiAccountSession.CurrentVersion &&
                   TryRestoreSession(legacy, out LocalSub2ApiAccountSession? session)
                ? [session!]
                : [];
        }
        catch (Exception exception) when (exception is IOException or
                                           UnauthorizedAccessException or
                                           CryptographicException or
                                           JsonException or
                                           ArgumentException or
                                           PlatformNotSupportedException or
                                           NotSupportedException)
        {
            return [];
        }
        finally
        {
            if (protectedBytes is not null)
            {
                CryptographicOperations.ZeroMemory(protectedBytes);
            }

            if (plainBytes is not null)
            {
                CryptographicOperations.ZeroMemory(plainBytes);
            }
        }
    }

    public LocalSub2ApiAccountSessionSaveResult Save(LocalSub2ApiAccountSession session)
    {
        ArgumentNullException.ThrowIfNull(session);
        if (!OperatingSystem.IsWindows())
        {
            return LocalSub2ApiAccountSessionSaveResult.Unavailable;
        }

        // Recreate the session from its own values before writing.  This keeps
        // the persistence boundary defensive even if a future caller receives
        // a session from a different source.
        if (!LocalSub2ApiAccountSession.TryCreate(
                session.RefreshToken,
                session.ApiBaseUri.AbsoluteUri,
                session.UserId,
                session.Role,
                session.SavedAtUtc,
                out LocalSub2ApiAccountSession? normalizedSession))
        {
            return LocalSub2ApiAccountSessionSaveResult.Invalid;
        }

        lock (_sync)
        {
            List<LocalSub2ApiAccountSession> sessions = ReadSessions();
            sessions.RemoveAll(existing => SameEndpoint(existing.ApiBaseUri, normalizedSession!.ApiBaseUri));
            sessions.Add(normalizedSession!);
            return WriteSessions(sessions);
        }
    }

    public bool Clear(Uri apiBaseUri)
    {
        ArgumentNullException.ThrowIfNull(apiBaseUri);
        lock (_sync)
        {
            List<LocalSub2ApiAccountSession> sessions = ReadSessions();
            int removed = sessions.RemoveAll(session => SameEndpoint(session.ApiBaseUri, apiBaseUri));
            if (removed == 0)
            {
                return false;
            }

            if (sessions.Count > 0)
            {
                return WriteSessions(sessions) == LocalSub2ApiAccountSessionSaveResult.Saved;
            }

            try
            {
                File.Delete(_path);
                return true;
            }
            catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
            {
                return false;
            }
        }
    }

    private LocalSub2ApiAccountSessionSaveResult WriteSessions(IReadOnlyCollection<LocalSub2ApiAccountSession> sessions)
    {
        byte[]? plainBytes = null;
        byte[]? protectedBytes = null;
        string? temporaryPath = null;
        try
        {
            plainBytes = JsonSerializer.SerializeToUtf8Bytes(new PersistedSessionCollection
            {
                Version = CurrentStoreVersion,
                Sessions = sessions.Select(ToPersistedSession).ToList(),
            });
            protectedBytes = ProtectedData.Protect(
                plainBytes,
                Entropy,
                DataProtectionScope.CurrentUser);

            string? directory = Path.GetDirectoryName(_path);
            if (string.IsNullOrWhiteSpace(directory))
            {
                return LocalSub2ApiAccountSessionSaveResult.Unavailable;
            }

            Directory.CreateDirectory(directory);
            temporaryPath = _path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            File.WriteAllBytes(temporaryPath, protectedBytes);
            File.Move(temporaryPath, _path, overwrite: true);
            return LocalSub2ApiAccountSessionSaveResult.Saved;
        }
        catch (Exception exception) when (exception is IOException or
                                           UnauthorizedAccessException or
                                           CryptographicException or
                                           JsonException or
                                           ArgumentException or
                                           PlatformNotSupportedException or
                                           NotSupportedException)
        {
            return LocalSub2ApiAccountSessionSaveResult.Unavailable;
        }
        finally
        {
            if (plainBytes is not null)
            {
                CryptographicOperations.ZeroMemory(plainBytes);
            }

            if (protectedBytes is not null)
            {
                CryptographicOperations.ZeroMemory(protectedBytes);
            }

            if (!string.IsNullOrWhiteSpace(temporaryPath))
            {
                TryDeleteTemporaryFile(temporaryPath);
            }
        }
    }

    private static List<LocalSub2ApiAccountSession> RestoreSessions(IEnumerable<PersistedSession>? persistedSessions)
    {
        var sessions = new List<LocalSub2ApiAccountSession>();
        if (persistedSessions is null)
        {
            return sessions;
        }

        foreach (PersistedSession persisted in persistedSessions)
        {
            if (TryRestoreSession(persisted, out LocalSub2ApiAccountSession? session) &&
                !sessions.Any(existing => SameEndpoint(existing.ApiBaseUri, session!.ApiBaseUri)))
            {
                sessions.Add(session!);
            }
        }

        return sessions;
    }

    private static bool TryRestoreSession(PersistedSession persisted, out LocalSub2ApiAccountSession? session)
        => LocalSub2ApiAccountSession.TryCreate(
            persisted.RefreshToken,
            persisted.ApiBaseUrl,
            persisted.UserId,
            persisted.Role,
            persisted.SavedAtUtc,
            out session);

    private static PersistedSession ToPersistedSession(LocalSub2ApiAccountSession session) => new()
    {
        Version = LocalSub2ApiAccountSession.CurrentVersion,
        RefreshToken = session.RefreshToken,
        ApiBaseUrl = session.ApiBaseUri.AbsoluteUri,
        UserId = session.UserId,
        Role = session.Role,
        SavedAtUtc = session.SavedAtUtc,
    };

    private static bool SameEndpoint(Uri left, Uri right)
        => Uri.Compare(
            left,
            right,
            UriComponents.SchemeAndServer | UriComponents.Path,
            UriFormat.SafeUnescaped,
            StringComparison.OrdinalIgnoreCase) == 0;

    private static void TryDeleteTemporaryFile(string temporaryPath)
    {
        try
        {
            if (File.Exists(temporaryPath))
            {
                File.Delete(temporaryPath);
            }
        }
        catch (IOException)
        {
            // The temporary content is DPAPI-protected, so a cleanup failure
            // does not expose plaintext session material.
        }
        catch (UnauthorizedAccessException)
        {
            // Same confidentiality boundary as above.
        }
    }

    private sealed class PersistedSession
    {
        public int Version { get; set; }

        public string? RefreshToken { get; set; }

        public string? ApiBaseUrl { get; set; }

        public long UserId { get; set; }

        public string? Role { get; set; }

        public DateTimeOffset SavedAtUtc { get; set; }
    }

    private sealed class PersistedSessionCollection
    {
        public int Version { get; set; }

        public List<PersistedSession>? Sessions { get; set; }
    }
}
