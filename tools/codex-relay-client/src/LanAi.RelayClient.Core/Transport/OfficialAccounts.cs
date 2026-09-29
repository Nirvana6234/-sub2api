using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>
/// Ids of the official accounts signed in within the client (docs: 小白端客户端内登录官方账号任务计划 §3.1).
/// </summary>
/// <remarks>
/// Every local-proxy account is told apart by a <see cref="long"/> id alone: this machine's own
/// sign-ins are −1 and −2 (<see cref="LocalMachineAccounts"/>), and these start at −1000 and go
/// down, never reused. So the saved choice, the usage store and the refused-encrypted-content
/// records keep them apart without a second key.
/// </remarks>
internal static class OfficialAccountIds
{
    public const long First = -1000;

    public static bool IsOfficial(long accountId) => accountId <= First;
}

/// <summary>Reads what a ChatGPT id token says about its account. Shared by this machine's sign-in and the client's own.</summary>
internal static class ChatGptIdToken
{
    private const string AuthClaim = "https://api.openai.com/auth";

    public static string Email(string? idToken) => JwtPayload.String(JwtPayload.Read(idToken), "email");

    public static string PlanType(string? idToken) => JwtPayload.String(Auth(idToken), "chatgpt_plan_type");

    public static string AccountId(string? idToken) => JwtPayload.String(Auth(idToken), "chatgpt_account_id");

    public static bool FedRamp(string? idToken) => JwtPayload.Bool(Auth(idToken), "chatgpt_account_is_fedramp");

    /// <summary>「Plus」 for <c>plus</c>, and so on; unknown plans as they are.</summary>
    public static string PlanLabel(string plan) => plan.ToLowerInvariant() switch
    {
        "" => string.Empty,
        "plus" => "Plus",
        "pro" => "Pro",
        "team" => "Team",
        "business" => "Business",
        "enterprise" => "Enterprise",
        "edu" => "Edu",
        "free" => "Free",
        _ => plan,
    };

    private static JsonObject? Auth(string? idToken) => JwtPayload.Read(idToken)?[AuthClaim] as JsonObject;
}

/// <summary>
/// One official account the user signed in to within the client, with its credentials.
/// </summary>
/// <remarks>
/// Held encrypted on disk (<see cref="OfficialAccountStore"/>) and in memory; never logged —
/// <see cref="ToString"/> leaves the secrets out.
/// </remarks>
internal sealed record OfficialAccount
{
    public const string OpenAi = "openai";
    public const string Anthropic = "anthropic";

    [JsonPropertyName("id")]
    public long Id { get; init; }

    /// <summary><see cref="OpenAi"/> or <see cref="Anthropic"/>.</summary>
    [JsonPropertyName("platform")]
    public string Platform { get; init; } = string.Empty;

    [JsonPropertyName("email")]
    public string Email { get; init; } = string.Empty;

    [JsonPropertyName("plan_type")]
    public string PlanType { get; init; } = string.Empty;

    /// <summary>When the subscription ends, as the official side said it (ISO 8601), or empty.</summary>
    [JsonPropertyName("subscription_expires_at")]
    public string SubscriptionExpiresAt { get; init; } = string.Empty;

    /// <summary>Why the sign-in stopped working, in words; empty while it works.</summary>
    [JsonPropertyName("invalid_reason")]
    public string InvalidReason { get; init; } = string.Empty;

    [JsonPropertyName("added_at")]
    public DateTimeOffset AddedAt { get; init; }

    [JsonPropertyName("last_refresh_at")]
    public DateTimeOffset? LastRefreshAt { get; init; }

    [JsonPropertyName("access_token")]
    public string AccessToken { get; init; } = string.Empty;

    [JsonPropertyName("refresh_token")]
    public string RefreshToken { get; init; } = string.Empty;

    [JsonPropertyName("expires_at")]
    public DateTimeOffset? ExpiresAt { get; init; }

    /// <summary>OpenAI only.</summary>
    [JsonPropertyName("id_token")]
    public string IdToken { get; init; } = string.Empty;

    /// <summary>OpenAI only: sent as <c>chatgpt-account-id</c>.</summary>
    [JsonPropertyName("chatgpt_account_id")]
    public string ChatGptAccountId { get; init; } = string.Empty;

    [JsonPropertyName("fedramp")]
    public bool FedRamp { get; init; }

    /// <summary>Anthropic only.</summary>
    [JsonPropertyName("account_uuid")]
    public string AccountUuid { get; init; } = string.Empty;

    [JsonPropertyName("org_uuid")]
    public string OrgUuid { get; init; } = string.Empty;

    [JsonPropertyName("scope")]
    public string Scope { get; init; } = string.Empty;

    [JsonIgnore]
    public LocalProxyKind Kind => Platform switch
    {
        OpenAi => LocalProxyKind.Codex,
        Anthropic => LocalProxyKind.ClaudeCode,
        _ => LocalProxyKind.Unsupported,
    };

    [JsonIgnore]
    public bool IsValid => InvalidReason.Length == 0 && RefreshToken.Length > 0;

    /// <summary>
    /// Who this is on the official side: the same account signed in again replaces its record
    /// (keeping its id, so the tool's choice and usage carry over) instead of adding a second.
    /// </summary>
    [JsonIgnore]
    public string Identity => Platform switch
    {
        OpenAi when ChatGptAccountId.Length > 0 => $"{OpenAi}:{ChatGptAccountId}",
        Anthropic when AccountUuid.Length > 0 => $"{Anthropic}:{AccountUuid}",
        _ => $"{Platform}:{Email.ToLowerInvariant()}",
    };

    /// <summary>「a@b.com · Plus」, or the product name when nothing better is known.</summary>
    [JsonIgnore]
    public string DisplayName
    {
        get
        {
            string plan = Platform == OpenAi ? ChatGptIdToken.PlanLabel(PlanType) : PlanType;
            string label = string.Join(" · ", new[] { Email, plan }.Where(part => part.Length > 0));
            return label.Length > 0 ? label : Platform == Anthropic ? "Claude 账号" : "ChatGPT 账号";
        }
    }

    public override string ToString() => $"OfficialAccount {{ Id = {Id}, Platform = {Platform} }}";
}

/// <summary>
/// The official accounts signed in within the client, per 共飞 user, encrypted (§3.2).
/// </summary>
/// <remarks>
/// <para>
/// One file per 共飞 user — the directory is named by <see cref="WeChatIntent.JevApiKeyStore.ScopeFor"/> —
/// holding all of that user's accounts, protected with the platform protector (DPAPI / the
/// Keychain master key), written whole to a temporary file and swapped in so a crash never
/// leaves half a file. Signing out of the client hides the accounts (<see cref="SetScope"/> with
/// null); it does not delete them.
/// </para>
/// <para>
/// Every read and write goes through one lock over the in-memory copy, and a write replaces
/// the file from that copy: two accounts refreshing at once, or an account added while
/// another refreshes, cannot overwrite each other's changes.
/// </para>
/// </remarks>
internal sealed class OfficialAccountStore
{
    private readonly ISnapshotProtector _protector;
    private readonly string _root;
    private readonly Func<DateTimeOffset> _clock;
    private readonly object _gate = new();
    private string? _scope;
    private State _state = new();

    public OfficialAccountStore(ISnapshotProtector protector, string? rootDirectory = null, Func<DateTimeOffset>? clock = null)
    {
        _protector = protector ?? throw new ArgumentNullException(nameof(protector));
        _root = rootDirectory ?? AppPaths.InData("official-accounts");
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    /// <summary>Raised after any change, on the thread that made it.</summary>
    public event Action? Changed;

    /// <summary>The 共飞 user whose accounts are open; null while signed out.</summary>
    public string? Scope
    {
        get
        {
            lock (_gate)
            {
                return _scope;
            }
        }
    }

    /// <summary>Opens another 共飞 user's accounts, or — with null — none.</summary>
    public void SetScope(string? scope)
    {
        lock (_gate)
        {
            if (scope == _scope)
            {
                return;
            }

            _scope = scope;
            _state = scope is null ? new State() : Load(scope);
        }

        Changed?.Invoke();
    }

    public IReadOnlyList<OfficialAccount> List()
    {
        lock (_gate)
        {
            return [.. _state.Accounts];
        }
    }

    public OfficialAccount? Get(long id)
    {
        lock (_gate)
        {
            return _state.Accounts.FirstOrDefault(a => a.Id == id);
        }
    }

    /// <summary>
    /// Keeps a fresh sign-in. The same official account signed in before is replaced in place —
    /// same id, same date added — and made valid again; anything else gets the next id.
    /// </summary>
    /// <returns>The account as stored.</returns>
    /// <exception cref="InvalidOperationException">Signed out of the client.</exception>
    /// <exception cref="IOException">The file could not be written (or <see cref="UnauthorizedAccessException"/>); nothing was changed.</exception>
    public OfficialAccount Add(OfficialAccount signedIn)
    {
        ArgumentNullException.ThrowIfNull(signedIn);
        OfficialAccount stored;
        lock (_gate)
        {
            string scope = _scope ?? throw new InvalidOperationException("没有登录共飞，不能保存官方账号。");
            State next = _state with { Accounts = [.. _state.Accounts] };
            int existing = next.Accounts.FindIndex(a => a.Identity == signedIn.Identity);
            if (existing >= 0)
            {
                stored = signedIn with { Id = next.Accounts[existing].Id, AddedAt = next.Accounts[existing].AddedAt, InvalidReason = string.Empty };
                next.Accounts[existing] = stored;
            }
            else
            {
                stored = signedIn with { Id = next.NextId, AddedAt = _clock(), InvalidReason = string.Empty };
                next = next with { NextId = next.NextId - 1 };
                next.Accounts.Add(stored);
            }

            Save(scope, next);
            _state = next;
        }

        ClientLog.Info($"已保存在共飞里登录的官方账号（{stored.Platform}，账号 {stored.Id}）");
        Changed?.Invoke();
        return stored;
    }

    /// <summary>
    /// Changes one account, but only while it still holds <paramref name="expectedRefreshToken"/>:
    /// a refresh that spent that token must not overwrite a sign-in made again meanwhile.
    /// </summary>
    /// <returns>False when the account is gone or its refresh token changed; nothing is written then.</returns>
    /// <exception cref="IOException">The file could not be written (or <see cref="UnauthorizedAccessException"/>); nothing was changed.</exception>
    public bool Update(long id, string expectedRefreshToken, Func<OfficialAccount, OfficialAccount> change)
    {
        ArgumentNullException.ThrowIfNull(change);
        lock (_gate)
        {
            if (_scope is not { } scope)
            {
                return false;
            }

            int index = _state.Accounts.FindIndex(a => a.Id == id);
            if (index < 0 || !string.Equals(_state.Accounts[index].RefreshToken, expectedRefreshToken, StringComparison.Ordinal))
            {
                return false;
            }

            State next = _state with { Accounts = [.. _state.Accounts] };
            next.Accounts[index] = change(next.Accounts[index]) with { Id = id };
            Save(scope, next);
            _state = next;
        }

        Changed?.Invoke();
        return true;
    }

    /// <returns>False when there was no such account.</returns>
    /// <exception cref="IOException">The file could not be written (or <see cref="UnauthorizedAccessException"/>); nothing was changed.</exception>
    public bool Remove(long id)
    {
        lock (_gate)
        {
            if (_scope is not { } scope || _state.Accounts.All(a => a.Id != id))
            {
                return false;
            }

            State next = _state with { Accounts = [.. _state.Accounts.Where(a => a.Id != id)] };
            Save(scope, next);
            _state = next;
        }

        ClientLog.Info($"已删除在共飞里登录的官方账号（账号 {id}）");
        Changed?.Invoke();
        return true;
    }

    private string DirectoryFor(string scope) => Path.Combine(_root, scope);

    private string PathFor(string scope) => Path.Combine(DirectoryFor(scope), "accounts.bin");

    private State Load(string scope)
    {
        string path = PathFor(scope);
        if (!File.Exists(path))
        {
            return new State();
        }

        try
        {
            State? state = JsonSerializer.Deserialize(_protector.Unprotect(File.ReadAllBytes(path)), ClientJsonContext.Default.OfficialAccountState);
            return state is null
                ? new State()
                : state with { Accounts = [.. state.Accounts.Where(a => OfficialAccountIds.IsOfficial(a.Id))], NextId = Math.Min(state.NextId, OfficialAccountIds.First) };
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or CryptographicException or JsonException or InvalidOperationException)
        {
            // Kept aside rather than overwritten by the next save: an unreadable file is still
            // someone's sign-ins, and the next write would otherwise destroy them for good.
            ClientLog.Warning($"读取在共飞里登录的官方账号失败：{ex.GetType().Name}，原文件已另存");
            try
            {
                File.Move(path, $"{path}.unreadable-{_clock():yyyyMMddHHmmss}", overwrite: true);
            }
            catch (Exception moveEx) when (moveEx is IOException or UnauthorizedAccessException)
            {
                ClientLog.Warning($"另存无法读取的官方账号文件失败：{moveEx.GetType().Name}");
            }

            return new State();
        }
    }

    private void Save(string scope, State state)
    {
        Directory.CreateDirectory(DirectoryFor(scope));
        string path = PathFor(scope);
        string temporary = path + ".tmp";
        File.WriteAllBytes(temporary, _protector.Protect(JsonSerializer.SerializeToUtf8Bytes(state, ClientJsonContext.Default.OfficialAccountState)));
        File.Move(temporary, path, overwrite: true);
    }

    /// <summary>What the file holds.</summary>
    internal sealed record State
    {
        [JsonPropertyName("version")]
        public int Version { get; init; } = 1;

        /// <summary>The id the next new account gets; only ever goes down, so an id is never reused.</summary>
        [JsonPropertyName("next_id")]
        public long NextId { get; init; } = OfficialAccountIds.First;

        [JsonPropertyName("accounts")]
        public List<OfficialAccount> Accounts { get; init; } = [];
    }
}
