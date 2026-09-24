using System.Security.Cryptography;
using System.Text;

namespace LanAi.RelayClient.DesktopSync;

/// <summary>The command types a phone may send; the server holds the same list as a second line.</summary>
internal static class DesktopSyncCommands
{
    public const string ListSessions = "sessions.list";
    public const string OpenSession = "session.open";
    public const string History = "session.history";
    public const string Detail = "session.detail";
    public const string SendMessage = "message.send";
    public const string Navigate = "thread.navigate";
    public const string Subscribe = "session.subscribe";

    /// <summary>Read-only checks after a failed turn; touches nothing (see <see cref="DesktopSelfCheck"/>).</summary>
    public const string SelfCheck = "desktop.check";

    /// <summary>
    /// 修复 ChatGPT 启动, from the phone: restarts ChatGPT (keeping its key). Signed like a send,
    /// because it stops every conversation on this computer.
    /// </summary>
    public const string Repair = "desktop.repair";

    /// <summary>The mode a repair is signed with; a repair has no text, so the empty string's hash.</summary>
    public const string RepairMode = "restart";

    public static bool IsKnown(string? type) =>
        type is ListSessions or OpenSession or History or Detail or SendMessage or Navigate or SelfCheck or Repair;
}

/// <summary>
/// Checks that a message-to-send (or a repair) was signed by the phone the user approved (D-8).
/// </summary>
/// <remarks>
/// <para>
/// Why a signature and not just the pairing token: the server relays every command and
/// could forge one. A message sent into a full-access conversation runs as the user, so
/// a forged message is remote code execution. The phone's private key never leaves the
/// phone (WebCrypto, non-extractable); the public key was fixed on this computer when
/// the user approved the phone. The server cannot produce a valid signature.
/// </para>
/// <para>
/// The phone signs, as UTF-8, with ECDSA P-256 / SHA-256, in WebCrypto's raw r‖s form:
/// <code>
/// cofly-remote/1\n {command}\n {pairing_id}\n {thread_id}\n {mode}\n {sha256 hex of text}\n {ts}\n {nonce}
/// </code>
/// (no spaces; <c>\n</c> is a newline). The command binds the signature to what it
/// asks for, so a captured send cannot be replayed as a restart; the pairing id binds it
/// to this pairing, the thread id to that conversation, and the timestamp and nonce stop
/// a captured command from being replayed. A repair signs <c>desktop.repair</c>, mode
/// <c>restart</c> and empty text.
/// </para>
/// <para>
/// Reads are not signed. Everything a read returns passes through the server anyway,
/// so a forged read learns it nothing it could not already see.
/// </para>
/// </remarks>
internal sealed class SignedSendVerifier
{
    public static readonly TimeSpan MaxAge = TimeSpan.FromMinutes(5);

    private readonly Func<DateTimeOffset> _clock;
    private readonly Dictionary<string, DateTimeOffset> _seenNonces = new(StringComparer.Ordinal);
    private readonly object _gate = new();

    public SignedSendVerifier(Func<DateTimeOffset>? clock = null) => _clock = clock ?? (() => DateTimeOffset.UtcNow);

    public static string Canonical(long pairingId, string threadId, string mode, string text, long timestampMs, string nonce,
        string command = DesktopSyncCommands.SendMessage) =>
        string.Join('\n',
            "cofly-remote/1",
            command,
            pairingId.ToString(System.Globalization.CultureInfo.InvariantCulture),
            threadId,
            mode,
            Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(text))).ToLowerInvariant(),
            timestampMs.ToString(System.Globalization.CultureInfo.InvariantCulture),
            nonce);

    /// <returns>Null when valid, otherwise why not.</returns>
    public string? Verify(ApprovedPhone phone, string threadId, string mode, string text, long timestampMs, string? nonce, string? signature,
        string command = DesktopSyncCommands.SendMessage)
    {
        if (string.IsNullOrEmpty(nonce) || nonce.Length is < 16 or > 128 || string.IsNullOrEmpty(signature))
        {
            return "未签名";
        }

        DateTimeOffset now = _clock();
        DateTimeOffset signedAt = DateTimeOffset.FromUnixTimeMilliseconds(timestampMs);
        if ((now - signedAt).Duration() > MaxAge)
        {
            return "签名已过期";
        }

        byte[] sig;
        try
        {
            sig = Convert.FromBase64String(signature);
        }
        catch (FormatException)
        {
            return "签名格式错误";
        }

        byte[] message = Encoding.UTF8.GetBytes(Canonical(phone.PairingId, threadId, mode, text, timestampMs, nonce, command));
        try
        {
            using var key = ECDsa.Create();
            key.ImportSubjectPublicKeyInfo(Convert.FromBase64String(phone.PublicKey), out _);
            if (key.KeySize != 256 ||
                !key.VerifyData(message, sig, HashAlgorithmName.SHA256, DSASignatureFormat.IeeeP1363FixedFieldConcatenation))
            {
                return "签名不符";
            }
        }
        catch (Exception ex) when (ex is CryptographicException or FormatException)
        {
            return "签名不符";
        }

        // Only a verified command spends its nonce, so garbage cannot fill the cache.
        lock (_gate)
        {
            foreach (string stale in _seenNonces.Where(p => now - p.Value > MaxAge).Select(p => p.Key).ToList())
            {
                _seenNonces.Remove(stale);
            }

            string key = $"{phone.PairingId}:{nonce}";
            if (!_seenNonces.TryAdd(key, now))
            {
                return "重复的指令";
            }
        }

        return null;
    }
}
