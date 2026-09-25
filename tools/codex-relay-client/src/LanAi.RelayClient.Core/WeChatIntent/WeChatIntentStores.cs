using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using LanAi.RelayClient.CodexBinding;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.WeChatIntent;

/// <summary>
/// The user's own TypeSafe API key, encrypted with the platform protector (DPAPI / the
/// Keychain) — the same mechanism as the Codex snapshot, with the same refusal to fall back to
/// plaintext (docs §3.4).
/// </summary>
/// <remarks>
/// One file per signed-in account: its name carries a hash of the server address and the
/// account's email (the session has no user id), so another account signing in on this
/// computer does not find, and spend, this one's key. Signing out leaves the file; 「清除」
/// deletes it.
/// </remarks>
internal sealed class JevApiKeyStore(ISnapshotProtector protector, string? directory = null)
{
    private readonly ISnapshotProtector _protector = protector ?? throw new ArgumentNullException(nameof(protector));
    private readonly string _directory = directory ?? AppPaths.InData("wechat-intent");

    public static string ScopeFor(string serverAddress, string email)
    {
        byte[] hash = SHA256.HashData(Encoding.UTF8.GetBytes($"{serverAddress.Trim().ToLowerInvariant()}\n{email.Trim().ToLowerInvariant()}"));
        return Convert.ToHexString(hash, 0, 8).ToLowerInvariant();
    }

    public string? Load(string scope)
    {
        string path = PathFor(scope);
        if (!File.Exists(path))
        {
            return null;
        }

        try
        {
            return Encoding.UTF8.GetString(_protector.Unprotect(File.ReadAllBytes(path)));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or CryptographicException or InvalidOperationException)
        {
            // Not the key, only that it could not be read.
            ClientLog.Warning($"读取 TypeSafe API Key 失败：{ex.GetType().Name}");
            return null;
        }
    }

    public void Save(string scope, string key)
    {
        Directory.CreateDirectory(_directory);
        string path = PathFor(scope);
        string temporary = path + ".tmp";
        File.WriteAllBytes(temporary, _protector.Protect(Encoding.UTF8.GetBytes(key.Trim())));
        File.Move(temporary, path, overwrite: true);
    }

    public void Clear(string scope)
    {
        try
        {
            File.Delete(PathFor(scope));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning($"删除 TypeSafe API Key 失败：{ex.GetType().Name}");
        }
    }

    /// <summary>「apik…7f3a」: enough to recognise, not enough to use.</summary>
    public static string Mask(string key)
    {
        key = key.Trim();
        return key.Length <= 8 ? new string('•', key.Length) : $"{key[..4]}…{key[^4..]}";
    }

    private string PathFor(string scope) => Path.Combine(_directory, $"typesafe-key-{scope}.bin");
}

/// <summary>The feature's settings. No conversation content is ever stored here (§3.4).</summary>
internal sealed class WeChatIntentPreferenceStore(string? filePath = null)
{
    private readonly string _filePath = filePath ?? AppPaths.InData("wechat-intent", "preferences.json");

    internal sealed record Preferences
    {
        /// <summary>The user's intent. Whether it is actually running also depends on WeChat, the key and the reader.</summary>
        public bool Enabled { get; init; }

        /// <summary>The consent text version the user accepted; 0 when never.</summary>
        public int ConsentVersion { get; init; }

        public bool Automatic { get; init; } = true;

        /// <summary>Conversation titles not to analyse.</summary>
        public List<string> Muted { get; init; } = [];

        /// <summary>
        /// Judge through the 共飞 relay's Jev group (the default, and the only route in a production
        /// build) rather than the user's own key, which only test builds offer.
        /// </summary>
        public bool UseRelayGroup { get; init; } = true;

        /// <summary>The Jev group last chosen; the first available one when it is gone.</summary>
        public long? RelayGroupId { get; init; }

        /// <summary>The relay route's consent text version the user accepted; 0 when never.</summary>
        public int RelayConsentVersion { get; init; }
    }

    public Preferences Load()
    {
        try
        {
            return File.Exists(_filePath)
                ? JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), WeChatIntentJsonContext.Default.Preferences) ?? new Preferences()
                : new Preferences();
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return new Preferences();
        }
    }

    public void Save(Preferences preferences)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporary = _filePath + ".tmp";
            File.WriteAllBytes(temporary, JsonSerializer.SerializeToUtf8Bytes(preferences, WeChatIntentJsonContext.Default.Preferences));
            File.Move(temporary, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("保存微信意图判断设置失败", ex);
        }
    }
}

/// <summary>Today's count and input tokens: numbers only, reset on the local date (§3.4).</summary>
internal sealed class WeChatIntentUsageStore(string? filePath = null, Func<DateTime>? today = null)
{
    /// <summary>TypeSafe's list price: input only, US$0.042 per million tokens; output is free.</summary>
    public const double UsdPerInputToken = 0.042 / 1_000_000;

    private readonly string _filePath = filePath ?? AppPaths.InData("wechat-intent", "usage.json");
    private readonly Func<DateTime> _today = today ?? (() => DateTime.Now.Date);
    private readonly object _gate = new();

    internal sealed record Totals
    {
        public string Date { get; init; } = string.Empty;

        public int Count { get; init; }

        public long InputTokens { get; init; }
    }

    public Totals Today()
    {
        lock (_gate)
        {
            Totals stored = Read();
            string date = _today().ToString("yyyy-MM-dd", System.Globalization.CultureInfo.InvariantCulture);
            return stored.Date == date ? stored : new Totals { Date = date };
        }
    }

    public Totals Add(long inputTokens)
    {
        lock (_gate)
        {
            Totals today = Today();
            Totals next = today with { Count = today.Count + 1, InputTokens = today.InputTokens + Math.Max(0, inputTokens) };
            try
            {
                Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
                File.WriteAllBytes(_filePath, JsonSerializer.SerializeToUtf8Bytes(next, WeChatIntentJsonContext.Default.Totals));
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                ClientLog.Warning("保存微信意图判断用量失败", ex);
            }

            return next;
        }
    }

    private Totals Read()
    {
        try
        {
            return File.Exists(_filePath)
                ? JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), WeChatIntentJsonContext.Default.Totals) ?? new Totals()
                : new Totals();
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return new Totals();
        }
    }
}

/// <summary>
/// 「判断得对 / 不对」 from self-testing (docs §10.5): the judgement's labels and the verdict,
/// one JSON line each. The conversation is not in it — only what Jev answered.
/// </summary>
internal sealed class WeChatIntentFeedbackStore(string? filePath = null)
{
    private readonly string _filePath = filePath ?? AppPaths.InData("wechat-intent", "feedback.jsonl");

    internal sealed record Entry
    {
        public DateTimeOffset At { get; init; }

        public string Model { get; init; } = string.Empty;

        public string Intent { get; init; } = string.Empty;

        public string Emotion { get; init; } = string.Empty;

        public int Risk { get; init; }

        public string Action { get; init; } = string.Empty;

        public bool Correct { get; init; }
    }

    public void Add(Entry entry)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            File.AppendAllText(_filePath, JsonSerializer.Serialize(entry, WeChatIntentJsonContext.Default.Entry) + "\n");
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("保存判断反馈失败", ex);
        }
    }
}

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase, PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(WeChatIntentFeedbackStore.Entry), TypeInfoPropertyName = "Entry")]
[JsonSerializable(typeof(WeChatIntentPreferenceStore.Preferences), TypeInfoPropertyName = "Preferences")]
[JsonSerializable(typeof(WeChatIntentUsageStore.Totals), TypeInfoPropertyName = "Totals")]
internal sealed partial class WeChatIntentJsonContext : JsonSerializerContext;
