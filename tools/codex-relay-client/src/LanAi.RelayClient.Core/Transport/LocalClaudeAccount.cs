using System.IO;
using System.Text.Json;
using System.Text.Json.Nodes;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>Where Claude Code keeps its sign-in, read and written whole.</summary>
internal interface IClaudeCredentialStore
{
    /// <summary>Why this client cannot use the sign-in here at all; null when it can.</summary>
    string? UnsupportedReason { get; }

    /// <summary>
    /// The next <see cref="Read"/> may ask the user first (the macOS keychain prompt): look with
    /// <see cref="Exists"/> instead until the user switches the account on.
    /// </summary>
    bool ReadNeedsConsent { get; }

    /// <summary>A read may wait — on the user or a child process — so it is kept off the UI thread.</summary>
    bool ReadMayBlock { get; }

    /// <summary>Whether there is a sign-in, without reading it (never prompts).</summary>
    bool Exists();

    /// <summary>The text, or null when there is none. May prompt and wait (<see cref="ReadNeedsConsent"/>).</summary>
    string? Read();

    /// <summary>What the last <see cref="Read"/> returned, or the file as it is; never prompts, never waits.</summary>
    string? ReadCached();

    void Write(string json);

    /// <summary>The signed-in email from Claude Code's global config, or empty.</summary>
    string ReadEmail();
}

/// <summary>
/// <c>.credentials.json</c> in Claude Code's config directory — where it keeps the sign-in on
/// Windows and Linux, and on macOS when it fell back from the keychain (otherwise
/// <see cref="ClaudeKeychainStore"/>).
/// </summary>
internal sealed class ClaudeCredentialFile : IClaudeCredentialStore
{
    private readonly string _path;
    private readonly string _globalConfigPath;

    public ClaudeCredentialFile(string? configDirectory = null)
    {
        string? overridden = Environment.GetEnvironmentVariable("CLAUDE_CONFIG_DIR");
        string directory = configDirectory ?? ClaudeCodeSettingsWriter.DefaultConfigDirectory();
        _path = Path.Combine(directory, ".credentials.json");

        // Claude Code keeps its global config beside the directory by default (~/.claude.json),
        // and inside it when CLAUDE_CONFIG_DIR moves it.
        _globalConfigPath = configDirectory is not null || !string.IsNullOrWhiteSpace(overridden)
            ? Path.Combine(directory, ".claude.json")
            : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".claude.json");
    }

    public string? UnsupportedReason => null;

    public bool ReadNeedsConsent => false;

    public bool ReadMayBlock => false;

    public bool Exists() => File.Exists(_path);

    public string? Read() => File.Exists(_path) ? File.ReadAllText(_path) : null;

    public string? ReadCached() => Read();

    public void Write(string json)
    {
        string temporary = _path + ".tmp";
        File.WriteAllText(temporary, json);
        if (!OperatingSystem.IsWindows())
        {
            // A token file must stay readable by its owner only, as Claude Code created it.
            File.SetUnixFileMode(temporary, UnixFileMode.UserRead | UnixFileMode.UserWrite);
        }

        File.Move(temporary, _path, overwrite: true);
    }

    public string ReadEmail()
    {
        try
        {
            if (!File.Exists(_globalConfigPath))
            {
                return string.Empty;
            }

            JsonObject? config = JsonNode.Parse(File.ReadAllText(_globalConfigPath)) as JsonObject;
            return JwtPayload.String(config?["oauthAccount"] as JsonObject, "emailAddress");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
        {
            return string.Empty;
        }
    }
}

/// <summary>
/// Claude Code's own Claude sign-in on this machine, as a local-proxy account.
/// </summary>
/// <remarks>
/// <para>
/// <b>Who refreshes.</b> Unlike Codex's, this sign-in stays where Claude Code keeps it: the
/// client only points Claude Code at itself through <c>settings.json</c>. Claude Code then
/// runs on the relay's token and has no reason to touch the sign-in, but it may still — from
/// another window, another config directory. So the file is read afresh on every request,
/// and a pair Claude Code refreshed is simply used; a refresh here happens only when the
/// stored pair itself is expiring, and it is re-checked just before writing so a pair
/// Claude Code wrote meanwhile is never overwritten.
/// </para>
/// <para>
/// Only <c>accessToken</c>, <c>refreshToken</c>, <c>expiresAt</c> (and <c>scopes</c> when
/// returned) are changed; everything else in the file is kept as it was.
/// </para>
/// </remarks>
internal sealed class LocalClaudeAccount : ILocalMachineAccount
{
    private static readonly TimeSpan ExpirySkew = TimeSpan.FromMinutes(5);
    private static readonly TimeSpan ForcedRefreshQuietPeriod = TimeSpan.FromSeconds(60);

    internal const string NotSignedInDetail =
        "这台电脑上的 Claude Code 还没有登录 Claude 账号。可以授权共飞AI助手本地代理使用你的 Claude 账号，授权信息只保存在这台电脑上，不会上传到共飞服务器。";

    /// <summary>Said before the first keychain read (D10): the prompt is coming, and which button keeps it from coming back.</summary>
    internal const string ConsentDetail =
        "开启时 macOS 会询问是否允许读取 Claude Code 的登录，请点「始终允许」（只问这一次）。";

    private readonly IClaudeCredentialStore _store;
    private readonly IOfficialTokenRefresher _refresher;
    private readonly Func<DateTimeOffset> _clock;
    private readonly SemaphoreSlim _gate = new(1, 1);
    private DateTimeOffset? _lastRefreshAt;

    /// <summary>A refreshed pair that could not be written yet, and the refresh token it replaced.</summary>
    private (string PreviousRefreshToken, SignIn SignIn)? _unsaved;

    public LocalClaudeAccount(IClaudeCredentialStore store, IOfficialTokenRefresher refresher, Func<DateTimeOffset>? clock = null)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _refresher = refresher ?? throw new ArgumentNullException(nameof(refresher));
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public LocalProxyKind Kind => LocalProxyKind.ClaudeCode;

    public LocalMachineAccountStatus Probe()
    {
        if (_store.UnsupportedReason is { } reason)
        {
            return new LocalMachineAccountStatus(LocalMachineAccountState.Unsupported, "本机 Claude 登录", reason);
        }

        // Before the user has switched it on, the keychain is only looked at, never read: the
        // page must not bring up a system prompt by being opened.
        if (_store.ReadNeedsConsent)
        {
            return _store.Exists()
                ? new LocalMachineAccountStatus(LocalMachineAccountState.SignedIn, DisplayName(null), ConsentDetail)
                : new LocalMachineAccountStatus(LocalMachineAccountState.NotSignedIn, "本机 Claude 登录", NotSignedInDetail);
        }

        try
        {
            return ReadSignIn(cached: true) is { } signIn && signIn.RefreshToken.Length > 0
                ? new LocalMachineAccountStatus(LocalMachineAccountState.SignedIn, DisplayName(signIn), string.Empty)
                : new LocalMachineAccountStatus(LocalMachineAccountState.NotSignedIn, "本机 Claude 登录", NotSignedInDetail);
        }
        catch (LocalProxyCredentialException ex)
        {
            return new LocalMachineAccountStatus(LocalMachineAccountState.NotSignedIn, "本机 Claude 登录", ex.UserMessage);
        }
    }

    public Task<LocalProxyCredential> GetAsync(long accountId, bool forceRefresh, CancellationToken cancellationToken) =>
        // The keychain read may sit behind a system prompt for up to a minute; the switch that
        // triggers it is awaited on the UI thread, which must not freeze meanwhile.
        _store.ReadMayBlock
            ? Task.Run(() => GetCoreAsync(forceRefresh, cancellationToken), cancellationToken)
            : GetCoreAsync(forceRefresh, cancellationToken);

    private async Task<LocalProxyCredential> GetCoreAsync(bool forceRefresh, CancellationToken cancellationToken)
    {
        if (_store.UnsupportedReason is { } reason)
        {
            throw new LocalProxyCredentialException(reason);
        }

        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            SaveUnsaved();
            SignIn signIn = ReadSignIn() ?? throw new LocalProxyCredentialException(NotSignedInDetail);
            if (_unsaved is { } pending && signIn.RefreshToken == pending.PreviousRefreshToken)
            {
                signIn = pending.SignIn;
            }

            bool expiring = signIn.AccessToken.Length == 0 ||
                            signIn.ExpiresAt is { } expires && expires - ExpirySkew <= _clock();
            bool forced = forceRefresh && !(_lastRefreshAt is { } last && _clock() - last < ForcedRefreshQuietPeriod);
            if (expiring || forced)
            {
                signIn = await RefreshAsync(signIn, cancellationToken).ConfigureAwait(false);
            }

            return new LocalProxyCredential(
                accountId: LocalMachineAccounts.ClaudeId,
                name: DisplayName(signIn),
                platform: "anthropic",
                accessToken: signIn.AccessToken,
                expiresAt: signIn.ExpiresAt);
        }
        finally
        {
            _gate.Release();
        }
    }

    public void Clear()
    {
    }

    private async Task<SignIn> RefreshAsync(SignIn signIn, CancellationToken cancellationToken)
    {
        if (signIn.RefreshToken.Length == 0)
        {
            throw new LocalProxyCredentialException(NotSignedInDetail);
        }

        ClaudeRefreshedTokens tokens = await _refresher.RefreshClaudeAsync(signIn.RefreshToken, cancellationToken).ConfigureAwait(false);
        DateTimeOffset now = _clock();
        _lastRefreshAt = now;
        SignIn refreshed = signIn with
        {
            AccessToken = tokens.AccessToken,
            RefreshToken = tokens.RefreshToken ?? signIn.RefreshToken,
            ExpiresAt = tokens.ExpiresInSeconds is { } seconds ? now.AddSeconds(seconds) : signIn.ExpiresAt,
            Scope = tokens.Scope ?? signIn.Scope,
        };

        // The pair being replaced may itself be one that never reached the disk.
        string stored = _unsaved is { } pending && pending.SignIn.RefreshToken == signIn.RefreshToken
            ? pending.PreviousRefreshToken
            : signIn.RefreshToken;
        try
        {
            if (!TryWrite(stored, refreshed))
            {
                _unsaved = null;
                ClientLog.Info("本机 Claude 登录在刷新期间已被 Claude Code 更新，改用它的");
                return ReadSignIn() ?? throw new LocalProxyCredentialException(NotSignedInDetail);
            }

            _unsaved = null;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // The old refresh token is spent; the new pair is the only one that works. Kept and
            // used, and written again on every later request until it lands.
            _unsaved = (stored, refreshed);
            ClientLog.Warning("刷新后的本机 Claude 登录暂时没能写回，下一次请求会再试", ex);
        }

        return refreshed;
    }

    private void SaveUnsaved()
    {
        if (_unsaved is not { } pending)
        {
            return;
        }

        try
        {
            TryWrite(pending.PreviousRefreshToken, pending.SignIn);
            _unsaved = null;
            ClientLog.Info("刷新后的本机 Claude 登录已补存");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("刷新后的本机 Claude 登录仍未能写回", ex);
        }
    }

    /// <summary>
    /// Writes <paramref name="signIn"/> over the stored pair when it still holds
    /// <paramref name="previousRefreshToken"/>. Read again right before writing: Claude Code
    /// may have refreshed meanwhile, and its pair must not be overwritten. Returns false then.
    /// </summary>
    private bool TryWrite(string previousRefreshToken, SignIn signIn)
    {
        JsonObject? root = Parse(_store.Read());
        if (root?["claudeAiOauth"] is not JsonObject oauth ||
            JwtPayload.String(oauth, "refreshToken") != previousRefreshToken)
        {
            return false;
        }

        oauth["accessToken"] = signIn.AccessToken;
        oauth["refreshToken"] = signIn.RefreshToken;
        if (signIn.ExpiresAt is { } at)
        {
            oauth["expiresAt"] = at.ToUnixTimeMilliseconds();
        }
        if (signIn.Scope is { } scope)
        {
            oauth["scopes"] = new JsonArray(scope.Split(' ', StringSplitOptions.RemoveEmptyEntries).Select(s => (JsonNode?)s).ToArray());
        }

        _store.Write(root.ToJsonString());
        return true;
    }

    private SignIn? ReadSignIn(bool cached = false)
    {
        string? text;
        try
        {
            text = cached ? _store.ReadCached() : _store.Read();
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            throw new LocalProxyCredentialException("读取本机 Claude 登录失败，请稍后再试。", ex);
        }

        if (Parse(text)?["claudeAiOauth"] is not JsonObject oauth)
        {
            return null;
        }

        DateTimeOffset? expiresAt = oauth["expiresAt"] is JsonValue value && value.TryGetValue(out long ms)
            ? DateTimeOffset.FromUnixTimeMilliseconds(ms)
            : null;
        return new SignIn(
            JwtPayload.String(oauth, "accessToken"),
            JwtPayload.String(oauth, "refreshToken"),
            expiresAt,
            JwtPayload.String(oauth, "subscriptionType"),
            Scope: null);
    }

    private static JsonObject? Parse(string? text)
    {
        if (string.IsNullOrWhiteSpace(text))
        {
            return null;
        }

        try
        {
            return JsonNode.Parse(text) as JsonObject;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    /// <param name="signIn">Null while the sign-in has not been read (the keychain, before consent): the email alone.</param>
    private string DisplayName(SignIn? signIn)
    {
        string plan = (signIn?.SubscriptionType ?? string.Empty).ToLowerInvariant() switch
        {
            "" => string.Empty,
            "pro" => "Pro",
            "max" => "Max",
            "team" => "Team",
            "enterprise" => "Enterprise",
            string other => other,
        };
        string label = string.Join(" · ", new[] { _store.ReadEmail(), plan }.Where(part => part.Length > 0));
        return label.Length > 0 ? $"本机 Claude 登录（{label}）" : "本机 Claude 登录";
    }

    /// <param name="Scope">Set only on a refreshed pair, when the server sent scopes to write back.</param>
    private sealed record SignIn(string AccessToken, string RefreshToken, DateTimeOffset? ExpiresAt, string SubscriptionType, string? Scope)
    {
        public override string ToString() => "SignIn { … }";
    }
}
